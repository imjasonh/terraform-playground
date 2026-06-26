// The simulation engine. Holds all game state, advances time one tick at a
// time, exposes the operator actions (add/cordon/drain/delete node, schedule
// pod), and computes the score. It is intentionally UI-agnostic: the UI reads
// `game.state` and calls these methods, then re-renders.

import { INSTANCE_TYPES, ZONES, TICKS_PER_SECOND, SLA_PENDING_TICKS } from "./types.js";
import { bestNodeFor, evaluateFit, summarizePendingReason } from "./scheduler.js";
import { SCENARIOS, makeRng, spawnForTick } from "./workload.js";

// --- Scoring weights -------------------------------------------------------
const UTIL_W = 30; // reward per tick at 100% cluster utilization
const PENDING_PEN = 1.5; // penalty per pending pod per tick (latency pressure)
const COST_PEN = 0.6; // multiplier applied to summed node $/hr each tick
const COMPLETE_BONUS = 8; // reward for finishing a job
const DRAIN_EVICT_PEN = 2; // graceful eviction (drain) penalty per pod
const FORCE_EVICT_PEN = 10; // hard eviction (delete live node) penalty per pod
const SLA_BREACH_PEN = 40; // one-time penalty when a pod blows its scheduling SLA
const MAX_EVENTS = 240;

export class Game {
  constructor(scenarioId = "steady") {
    this.reset(scenarioId);
  }

  reset(scenarioId = this.state?.scenarioId || "steady") {
    const scenario = SCENARIOS[scenarioId];
    this.scenario = scenario;
    this.state = {
      scenarioId,
      tick: 0,
      nodes: [],
      pods: new Map(),
      pendingIds: [],
      nodeSeq: 0,
      paused: true,
      speed: 1,
      autoSchedule: false,
      autoScale: false,
      events: [],
      score: 0,
      // running tallies for the score breakdown panel
      breakdown: { util: 0, latency: 0, cost: 0, jobs: 0, sla: 0, disruption: 0 },
      metrics: {
        latencySum: 0,
        latencyCount: 0,
        scheduledTotal: 0,
        completedTotal: 0,
        retiredTotal: 0,
        slaBreaches: 0,
        evictions: 0,
        spawnedTotal: 0,
      },
      lastUtil: 0,
      scaleCooldown: 0,
    };
    this.rng = makeRng(scenario.seed);
    for (const typeKey of scenario.startNodes) {
      this.addNode(typeKey, undefined, /*instant*/ true);
    }
    this.log("info", `Scenario "${scenario.name}" loaded. Cluster ready.`);
    return this.state;
  }

  // --- helpers -------------------------------------------------------------
  nodeById(id) {
    return this.state.nodes.find((n) => n.id === id) || null;
  }
  podById(id) {
    return this.state.pods.get(id) || null;
  }
  podsOnNode(node) {
    return node.podIds.map((id) => this.state.pods.get(id)).filter(Boolean);
  }
  pendingPods() {
    return this.state.pendingIds.map((id) => this.state.pods.get(id)).filter(Boolean);
  }
  log(level, msg) {
    this.state.events.push({ tick: this.state.tick, level, msg });
    if (this.state.events.length > MAX_EVENTS) this.state.events.shift();
  }

  // --- node lifecycle ------------------------------------------------------
  addNode(typeKey, zone, instant = false) {
    const spec = INSTANCE_TYPES[typeKey];
    if (!spec) throw new Error(`unknown instance type ${typeKey}`);
    this.state.nodeSeq += 1;
    const z = zone || ZONES[this.state.nodeSeq % ZONES.length];
    const node = {
      id: `node-${this.state.nodeSeq}`,
      name: `node-${this.state.nodeSeq}`,
      type: typeKey,
      cpu: spec.cpu,
      mem: spec.mem,
      gpu: spec.gpu,
      cost: spec.cost,
      labels: { ...spec.labels, "topology.kubernetes.io/zone": z },
      taints: spec.taints.map((t) => ({ ...t })),
      status: instant ? "Ready" : "Provisioning",
      provisioningTicksLeft: instant ? 0 : spec.bootTicks,
      podIds: [],
      idleTicks: 0,
      createdTick: this.state.tick,
    };
    this.state.nodes.push(node);
    if (!instant) this.log("info", `Provisioning ${node.name} (${typeKey}, ${z})…`);
    return node;
  }

  cordon(nodeId) {
    const node = this.nodeById(nodeId);
    if (!node || node.status === "Provisioning") return;
    if (node.status === "Cordoned") {
      node.status = "Ready";
      this.log("info", `Uncordoned ${node.name}.`);
    } else {
      node.status = "Cordoned";
      this.log("warn", `Cordoned ${node.name} — marked unschedulable.`);
    }
  }

  /** Graceful drain: cordon, then evict every pod back to the queue. */
  drain(nodeId) {
    const node = this.nodeById(nodeId);
    if (!node) return;
    const pods = this.podsOnNode(node);
    for (const pod of pods) this.evictPod(pod, node, DRAIN_EVICT_PEN, "drain");
    node.status = "Cordoned";
    this.log("warn", `Drained ${node.name} (${pods.length} pod(s) rescheduled).`);
  }

  /** Terminate a node. Any still-running pods are forcibly evicted. */
  deleteNode(nodeId) {
    const node = this.nodeById(nodeId);
    if (!node) return;
    const pods = this.podsOnNode(node);
    for (const pod of pods) this.evictPod(pod, node, FORCE_EVICT_PEN, "force-delete");
    this.state.nodes = this.state.nodes.filter((n) => n.id !== nodeId);
    const lvl = pods.length ? "error" : "info";
    this.log(lvl, `Terminated ${node.name}${pods.length ? ` (force-killed ${pods.length} pod(s)!)` : ""}.`);
  }

  evictPod(pod, node, penalty, reason) {
    node.podIds = node.podIds.filter((id) => id !== pod.id);
    pod.status = "Pending";
    pod.nodeId = null;
    pod.scheduledTick = null;
    pod.pendingTicks = 0;
    pod.arrivalTick = this.state.tick; // fresh latency clock after disruption
    pod.slaBreached = false;
    this.state.pendingIds.push(pod.id);
    this.state.score -= penalty;
    this.state.breakdown.disruption -= penalty;
    this.state.metrics.evictions += 1;
    void reason;
  }

  // --- scheduling actions --------------------------------------------------
  /**
   * Attempt to bind a pod to a node. Returns { ok, reasons }.
   */
  schedulePod(podId, nodeId) {
    const pod = this.podById(podId);
    const node = this.nodeById(nodeId);
    if (!pod || !node) return { ok: false, reasons: ["pod or node not found"] };
    if (pod.status !== "Pending") return { ok: false, reasons: ["pod is not pending"] };
    const fit = evaluateFit(pod, node, this.podsOnNode(node));
    if (!fit.ok) return fit;

    pod.status = "Running";
    pod.nodeId = node.id;
    pod.scheduledTick = this.state.tick;
    node.podIds.push(pod.id);
    node.idleTicks = 0;
    this.state.pendingIds = this.state.pendingIds.filter((id) => id !== podId);

    const latency = pod.scheduledTick - pod.arrivalTick;
    this.state.metrics.latencySum += latency;
    this.state.metrics.latencyCount += 1;
    this.state.metrics.scheduledTotal += 1;
    return { ok: true, reasons: [] };
  }

  /** Place a single pending pod on its best feasible node, if any. */
  autoPlaceOne(podId) {
    const pod = this.podById(podId);
    if (!pod || pod.status !== "Pending") return { ok: false, reasons: ["not pending"] };
    const best = bestNodeFor(pod, this.schedulableNodes(), (nid) =>
      this.podsOnNode(this.nodeById(nid))
    );
    if (!best) {
      return { ok: false, reasons: [summarizePendingReason(pod, this.state.nodes, (nid) => this.podsOnNode(this.nodeById(nid)))] };
    }
    return this.schedulePod(podId, best.node.id);
  }

  schedulableNodes() {
    return this.state.nodes.filter((n) => n.status === "Ready");
  }

  // --- automation ----------------------------------------------------------
  runAutoScheduler() {
    // Highest priority first, then oldest (FIFO) — like the real scheduler.
    const queue = [...this.pendingPods()].sort(
      (a, b) => b.priority - a.priority || a.arrivalTick - b.arrivalTick
    );
    for (const pod of queue) {
      const best = bestNodeFor(pod, this.schedulableNodes(), (nid) =>
        this.podsOnNode(this.nodeById(nid))
      );
      if (best) this.schedulePod(pod.id, best.node.id);
    }
  }

  /** The instance type the autoscaler would provision to host this pod. */
  scaleUpTypeForPod(pod) {
    if (pod.gpu > 0) return "gpu-xlarge";
    if (pod.nodeSelector && pod.nodeSelector.disktype === "ssd") {
      return pod.mem > 8192 ? "mem-xlarge" : "ssd-large";
    }
    // batch jobs tolerate spot — grab cheap, interruptible capacity for them
    if (pod.kind === "job" && (pod.tolerations || []).some((t) => t.key === "spot")) {
      return "spot-medium";
    }
    return "general-large";
  }

  runAutoScaler() {
    const s = this.state;
    if (s.scaleCooldown > 0) s.scaleCooldown -= 1;

    if (s.scaleCooldown === 0) {
      const ready = this.schedulableNodes();
      const unfittable = this.pendingPods().filter(
        (pod) => !ready.some((n) => evaluateFit(pod, n, this.podsOnNode(n)).ok)
      );
      const provisioning = s.nodes.filter((n) => n.status === "Provisioning").length;
      // ~8 generic pods fit on a fresh node; discount capacity already booting.
      let budget = Math.min(3, Math.ceil(unfittable.length / 8) - provisioning);

      if (unfittable.length > 0 && budget > 0) {
        const added = [];
        const usedSpecial = new Set();
        // Highest priority first so scarce gpu/ssd workloads aren't starved.
        const queue = [...unfittable].sort((a, b) => b.priority - a.priority);
        for (const pod of queue) {
          if (budget <= 0) break;
          const type = this.scaleUpTypeForPod(pod);
          if (type !== "general-large") {
            // one specialized node hosts several such pods — don't over-add
            if (usedSpecial.has(type)) continue;
            usedSpecial.add(type);
          }
          this.addNode(type);
          added.push(type);
          budget -= 1;
        }
        if (added.length) {
          this.log("info", `Autoscaler: scaling up (+${added.length}: ${[...new Set(added)].join(", ")}).`);
          s.scaleCooldown = 3;
        }
      }
    }

    // Scale down: terminate a node that has been idle a while (keep at least one).
    if (s.scaleCooldown === 0 && this.schedulableNodes().length > 1) {
      const idle = this.state.nodes.find(
        (n) => n.status === "Ready" && n.podIds.length === 0 && n.idleTicks > 20
      );
      if (idle) {
        this.deleteNode(idle.id);
        this.log("info", `Autoscaler: scaling down idle ${idle.name}.`);
        s.scaleCooldown = 3;
      }
    }
  }

  // --- the main tick -------------------------------------------------------
  tick() {
    const s = this.state;
    s.tick += 1;

    // 1. node provisioning
    for (const node of s.nodes) {
      if (node.status === "Provisioning") {
        node.provisioningTicksLeft -= 1;
        if (node.provisioningTicksLeft <= 0) {
          node.status = "Ready";
          this.log("good", `${node.name} is now Ready.`);
        }
      }
    }

    // 2. spawn workload (respecting each app's replica cap)
    const aliveByApp = {};
    for (const p of s.pods.values()) {
      if (p.status === "Pending" || p.status === "Running") {
        aliveByApp[p.app] = (aliveByApp[p.app] || 0) + 1;
      }
    }
    const spawned = spawnForTick(this.scenario, this.rng, s.tick, aliveByApp);
    for (const pod of spawned) {
      s.pods.set(pod.id, pod);
      s.pendingIds.push(pod.id);
    }
    s.metrics.spawnedTotal += spawned.length;

    // 3. automation
    if (s.autoSchedule) this.runAutoScheduler();
    if (s.autoScale) this.runAutoScaler();

    // 4. advance running jobs, track idleness
    for (const node of s.nodes) {
      if (node.podIds.length === 0 && node.status === "Ready") node.idleTicks += 1;
      else node.idleTicks = 0;
    }
    for (const pod of [...s.pods.values()]) {
      if (pod.status === "Running" && pod.remainingTicks != null) {
        pod.remainingTicks -= 1;
        if (pod.remainingTicks <= 0) this.finishPod(pod);
      }
    }

    // 5. pending accounting + SLA
    let pendingCount = 0;
    for (const id of s.pendingIds) {
      const pod = s.pods.get(id);
      if (!pod) continue;
      pendingCount += 1;
      pod.pendingTicks += 1;
      if (!pod.slaBreached && pod.pendingTicks > SLA_PENDING_TICKS) {
        pod.slaBreached = true;
        s.metrics.slaBreaches += 1;
        s.score -= SLA_BREACH_PEN;
        s.breakdown.sla -= SLA_BREACH_PEN;
        this.log("error", `SLA breach: ${pod.name} pending >${(SLA_PENDING_TICKS / TICKS_PER_SECOND).toFixed(0)}s.`);
      }
    }

    // 6. scoring (utilization reward + latency/cost pressure)
    const util = this.clusterUtilization();
    s.lastUtil = util;
    const utilDelta = util * UTIL_W;
    const latDelta = pendingCount * PENDING_PEN;
    const hourlyCost = this.hourlyCost();
    const costDelta = hourlyCost * COST_PEN;
    s.score += utilDelta - latDelta - costDelta;
    s.breakdown.util += utilDelta;
    s.breakdown.latency -= latDelta;
    s.breakdown.cost -= costDelta;

    return s;
  }

  /** A pod reached the end of its lifetime: free its resources. */
  finishPod(pod) {
    const node = this.nodeById(pod.nodeId);
    if (node) node.podIds = node.podIds.filter((id) => id !== pod.id);
    pod.status = "Completed";
    this.state.pods.delete(pod.id);
    if (pod.kind === "job") {
      this.state.metrics.completedTotal += 1;
      this.state.score += COMPLETE_BONUS;
      this.state.breakdown.jobs += COMPLETE_BONUS;
      this.log("good", `Job ${pod.name} completed and freed its resources.`);
    } else {
      // a service replica was rolled / scaled down — quietly frees capacity
      this.state.metrics.retiredTotal += 1;
    }
  }

  // --- metrics -------------------------------------------------------------
  /** Average utilization (cpu+mem)/2 across nodes that are up (paying). */
  clusterUtilization() {
    const up = this.state.nodes.filter((n) =>
      ["Ready", "Cordoned", "Draining"].includes(n.status)
    );
    if (up.length === 0) return 0;
    let sum = 0;
    for (const node of up) {
      const used = this.podsOnNode(node).reduce(
        (a, p) => ({ cpu: a.cpu + p.cpu, mem: a.mem + p.mem }),
        { cpu: 0, mem: 0 }
      );
      const cpuFrac = node.cpu ? used.cpu / node.cpu : 0;
      const memFrac = node.mem ? used.mem / node.mem : 0;
      sum += (cpuFrac + memFrac) / 2;
    }
    return sum / up.length;
  }

  /** Sum of $/hr for every node currently powered on. */
  hourlyCost() {
    return this.state.nodes
      .filter((n) => n.status !== "Terminating")
      .reduce((s, n) => s + n.cost, 0);
  }

  avgLatencySeconds() {
    const m = this.state.metrics;
    if (m.latencyCount === 0) return 0;
    return m.latencySum / m.latencyCount / TICKS_PER_SECOND;
  }

  runningCount() {
    let n = 0;
    for (const p of this.state.pods.values()) if (p.status === "Running") n += 1;
    return n;
  }

  uptimeSeconds() {
    return this.state.tick / TICKS_PER_SECOND;
  }
}
