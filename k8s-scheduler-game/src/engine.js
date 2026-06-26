// The simulation engine. Holds all game state, advances time one tick at a
// time, exposes the operator actions (add/cordon/drain/delete node, schedule
// pod), and computes the score. It is intentionally UI-agnostic: the UI reads
// `game.state` and calls these methods, then re-renders.

import {
  INSTANCE_TYPES,
  ZONES,
  TICKS_PER_SECOND,
  SLA_PENDING_TICKS,
  DAEMONSETS,
  effectiveCost,
  randHash,
} from "./types.js";
import { bestNodeFor, evaluateFit, selectorMatches, summarizePendingReason } from "./scheduler.js";
import { SCENARIOS, makeRng, spawnForTick, createDaemonPod } from "./workload.js";

// --- Scoring weights -------------------------------------------------------
const UTIL_W = 30; // reward per tick at 100% cluster utilization
const PENDING_PEN = 1.5; // penalty per pending pod per tick (latency pressure)
const COST_PEN = 0.6; // multiplier applied to summed node $/hr each tick
const COMPLETE_BONUS = 8; // reward for finishing a job
const DRAIN_EVICT_PEN = 2; // graceful eviction (drain) penalty per pod
const FORCE_EVICT_PEN = 10; // hard eviction (delete live node) penalty per pod
const SLA_BREACH_PEN = 40; // one-time penalty when a pod blows its scheduling SLA
const SPOT_RECLAIM_PEN = 1.5; // per workload pod lost to a (somewhat expected) spot interruption
const SPOT_WARNING_TICKS = 12; // ~3s interruption notice at 4x before a spot node is reclaimed
const SPOT_RECLAIM_BASE = 0.0035; // base per-tick probability a spot node is interrupted
const OUTDATED_PEN = 0.3; // per outdated, still-up node per tick while an upgrade is pending
const UPGRADE_DONE_BONUS = 35; // reward for completing a cluster-wide rollout
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
      // cluster version: nodes carry a minor version; an upgrade bumps the target.
      clusterMinor: 30,
      upgradePending: false,
      // spot market: the live spot price as a fraction of on-demand (<1 means
      // savings; ~0.45 ≈ 55% off). Drives both billing and interruption risk.
      spotPrice: 0.4,
      nextUpgradeTick: scenario.upgradeEvery || 0,
      // running tallies for the score breakdown panel
      breakdown: { util: 0, latency: 0, cost: 0, jobs: 0, sla: 0, disruption: 0, upgrade: 0 },
      metrics: {
        latencySum: 0,
        latencyCount: 0,
        scheduledTotal: 0,
        completedTotal: 0,
        retiredTotal: 0,
        slaBreaches: 0,
        evictions: 0,
        spawnedTotal: 0,
        spotReclaims: 0,
        nodesUpgraded: 0,
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
    // Random k8s-style name (e.g. node-xugjs); seq still backs the stable id.
    const name = `node-${randHash(this.rng, 5)}`;
    const node = {
      id: `node-${this.state.nodeSeq}`,
      name,
      type: typeKey,
      cpu: spec.cpu,
      mem: spec.mem,
      gpu: spec.gpu,
      cost: spec.cost,
      spot: spec.family === "spot",
      minor: this.state.clusterMinor, // new nodes join on the current version
      labels: { ...spec.labels, "topology.kubernetes.io/zone": z },
      taints: spec.taints.map((t) => ({ ...t })),
      status: instant ? "Ready" : "Provisioning",
      provisioningTicksLeft: instant ? 0 : spec.bootTicks,
      spotWarnTicksLeft: 0,
      podIds: [],
      idleTicks: 0,
      createdTick: this.state.tick,
    };
    this.state.nodes.push(node);
    if (instant) this.reconcileDaemonSets(node);
    else this.log("info", `Provisioning ${node.name} (${typeKey}, ${z})…`);
    return node;
  }

  /** Ensure every applicable DaemonSet has a pod on this (Ready) node. */
  reconcileDaemonSets(node) {
    if (node.status !== "Ready") return;
    for (const ds of DAEMONSETS) {
      if (ds.nodeSelector && !selectorMatches(ds.nodeSelector, node.labels)) continue;
      const present = node.podIds.some((id) => this.state.pods.get(id)?.daemonOf === ds.name);
      if (!present) {
        const pod = createDaemonPod(ds, node, this.state.tick);
        this.state.pods.set(pod.id, pod);
        node.podIds.push(pod.id);
      }
    }
  }

  /** Pods on a node that are real workload (excludes DaemonSet/infra pods). */
  workloadPodsOnNode(node) {
    return this.podsOnNode(node).filter((p) => p.kind !== "daemon");
  }

  /** Delete a node's DaemonSet pods from the pod map (used when a node leaves). */
  purgeDaemonPods(node) {
    for (const id of node.podIds) {
      const p = this.state.pods.get(id);
      if (p && p.kind === "daemon") this.state.pods.delete(id);
    }
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

  /** Graceful drain: cordon, then evict workload pods back to the queue.
   *  DaemonSet pods are left running, exactly like `kubectl drain`. */
  drain(nodeId) {
    const node = this.nodeById(nodeId);
    if (!node) return;
    const pods = this.workloadPodsOnNode(node);
    for (const pod of pods) this.evictPod(pod, node, DRAIN_EVICT_PEN, "drain");
    if (node.status !== "Reclaiming" && node.status !== "Upgrading") node.status = "Cordoned";
    this.log("warn", `Drained ${node.name} (${pods.length} pod(s) rescheduled; daemonsets kept).`);
  }

  /** Terminate a node. Workload pods still on it are forcibly evicted. */
  deleteNode(nodeId) {
    const node = this.nodeById(nodeId);
    if (!node) return;
    const pods = this.workloadPodsOnNode(node);
    for (const pod of pods) this.evictPod(pod, node, FORCE_EVICT_PEN, "force-delete");
    this.purgeDaemonPods(node);
    this.state.nodes = this.state.nodes.filter((n) => n.id !== nodeId);
    const lvl = pods.length ? "error" : "info";
    this.log(lvl, `Terminated ${node.name}${pods.length ? ` (force-killed ${pods.length} pod(s)!)` : ""}.`);
    this.checkUpgradeComplete();
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

  // --- spot interruptions --------------------------------------------------
  /** The cloud reclaims a spot node: its workload is evicted and it's gone. */
  reclaimSpotNode(node) {
    const pods = this.workloadPodsOnNode(node);
    for (const pod of pods) this.evictPod(pod, node, SPOT_RECLAIM_PEN, "spot-reclaim");
    this.purgeDaemonPods(node);
    this.state.nodes = this.state.nodes.filter((n) => n.id !== node.id);
    this.state.metrics.spotReclaims += 1;
    this.log("error", `Spot interruption: ${node.name} reclaimed by the cloud (${pods.length} pod(s) evicted).`);
    this.checkUpgradeComplete();
  }

  // --- cluster upgrades ----------------------------------------------------
  /** Control-plane upgrade event: bump the target version; nodes now lag. */
  triggerUpgrade() {
    this.state.clusterMinor += 1;
    this.state.upgradePending = true;
    this.log(
      "error",
      `Control plane upgraded to v1.${this.state.clusterMinor}. Drain & restart every node to match!`
    );
  }

  /**
   * Responsibly restart a node onto the current version: gracefully drain its
   * workload, drop its daemonset pods, then reboot (it returns Ready on the new
   * version after its boot time). Force-restarting a node full of pods is the
   * irresponsible way and is discouraged by the drain penalty.
   */
  upgradeNode(nodeId) {
    const node = this.nodeById(nodeId);
    if (!node) return { ok: false, reason: "node not found" };
    if (node.minor >= this.state.clusterMinor) return { ok: false, reason: "already up to date" };
    if (["Provisioning", "Upgrading", "Reclaiming"].includes(node.status)) {
      return { ok: false, reason: `node is ${node.status.toLowerCase()}` };
    }
    const pods = this.workloadPodsOnNode(node);
    for (const pod of pods) this.evictPod(pod, node, DRAIN_EVICT_PEN, "upgrade");
    this.purgeDaemonPods(node);
    node.status = "Upgrading";
    node.provisioningTicksLeft = INSTANCE_TYPES[node.type].bootTicks;
    this.log("warn", `Upgrading ${node.name} → v1.${this.state.clusterMinor} (${pods.length} pod(s) drained).`);
    return { ok: true };
  }

  /** When every node is on the current version, the rollout is complete. */
  checkUpgradeComplete() {
    if (!this.state.upgradePending) return;
    const outdated = this.state.nodes.some((n) => n.minor < this.state.clusterMinor);
    if (!outdated) {
      this.state.upgradePending = false;
      this.state.score += UPGRADE_DONE_BONUS;
      this.state.breakdown.upgrade += UPGRADE_DONE_BONUS;
      this.log("good", `Cluster fully upgraded to v1.${this.state.clusterMinor}. Clean rollout! +${UPGRADE_DONE_BONUS}`);
    }
  }

  /**
   * Advance the spot market one tick. spotPrice is the fraction of on-demand
   * that spot currently costs: a mean-reverting random walk around ~0.45
   * (~55% savings), clamped below 1 (AWS never charges spot above on-demand),
   * with the odd demand spike. Then roll interruption dice for each spot node —
   * a pricier (hotter) market means more reclaims. Warned nodes count down a
   * short notice (during which you can drain them) before being reclaimed.
   */
  updateSpotMarket() {
    const s = this.state;
    const r = this.rng;
    let sp = s.spotPrice + (0.45 - s.spotPrice) * 0.03 + (r() - 0.5) * 0.05;
    if (r() < 0.004) sp += 0.25 + r() * 0.2; // occasional demand spike toward on-demand
    s.spotPrice = Math.max(0.2, Math.min(0.95, sp));

    for (const node of [...s.nodes]) {
      if (!node.spot) continue;
      if (node.status === "Reclaiming") {
        node.spotWarnTicksLeft -= 1;
        if (node.spotWarnTicksLeft <= 0) this.reclaimSpotNode(node);
        continue;
      }
      if (node.status === "Ready" || node.status === "Cordoned") {
        const p = SPOT_RECLAIM_BASE * (0.5 + s.spotPrice);
        if (r() < p) {
          node.status = "Reclaiming";
          node.spotWarnTicksLeft = SPOT_WARNING_TICKS;
          this.log(
            "warn",
            `Spot notice: ${node.name} will be reclaimed in ${(SPOT_WARNING_TICKS / TICKS_PER_SECOND).toFixed(0)}s.`
          );
        }
      }
    }
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
    if (pod.gpu > 0) return "g4dn.xlarge";
    if (pod.nodeSelector && pod.nodeSelector.disktype === "ssd") {
      return pod.mem > 8192 ? "r5.2xlarge" : "c5d.2xlarge";
    }
    // batch jobs tolerate spot — grab cheap, interruptible capacity for them
    if (pod.kind === "job" && (pod.tolerations || []).some((t) => t.key === "spot")) {
      return "c5.2xlarge-spot";
    }
    return "c5.2xlarge";
  }

  runAutoScaler() {
    const s = this.state;
    if (s.scaleCooldown > 0) s.scaleCooldown -= 1;

    // Automated rolling upgrade: bring outdated nodes up one at a time. We only
    // start a new one when nothing is already booting/upgrading, which keeps the
    // rollout safe (never tears down the whole cluster at once).
    if (s.upgradePending) {
      // One upgrade at a time keeps the rollout safe, but don't wait on routine
      // provisioning (spot replacements etc.) or the rollout can stall.
      const upgrading = s.nodes.some((n) => n.status === "Upgrading");
      if (!upgrading) {
        const outdated = s.nodes
          .filter((n) => n.status === "Ready" && n.minor < s.clusterMinor)
          .sort((a, b) => this.workloadPodsOnNode(a).length - this.workloadPodsOnNode(b).length)[0];
        if (outdated) this.upgradeNode(outdated.id);
      }
    }

    if (s.scaleCooldown === 0) {
      const ready = this.schedulableNodes();
      const unfittable = this.pendingPods().filter(
        (pod) => !ready.some((n) => evaluateFit(pod, n, this.podsOnNode(n)).ok)
      );

      if (unfittable.length > 0) {
        const added = [];
        const provisioning = s.nodes.filter((n) => n.status === "Provisioning");

        // GPU pods need a dedicated single-GPU node each: provision one per
        // uncovered GPU pod (minus GPU nodes already booting), capped per burst.
        const gpuPending = unfittable.filter((p) => p.gpu > 0).length;
        const gpuBooting = provisioning.filter((n) => n.gpu > 0).length;
        const gpuToAdd = Math.min(3, Math.max(0, gpuPending - gpuBooting));
        for (let i = 0; i < gpuToAdd; i++) {
          this.addNode("g4dn.xlarge");
          added.push("g4dn.xlarge");
        }

        // Everything else bin-packs several pods per node. Add general capacity
        // sized to the backlog, plus one node per distinct specialized type.
        const other = unfittable.filter((p) => p.gpu === 0);
        const otherBooting = provisioning.filter((n) => n.gpu === 0).length;
        let budget = Math.min(3, Math.ceil(other.length / 8) - otherBooting);
        const usedSpecial = new Set();
        // Highest priority first so scarce ssd workloads aren't starved.
        const queue = [...other].sort((a, b) => b.priority - a.priority);
        for (const pod of queue) {
          if (budget <= 0) break;
          const type = this.scaleUpTypeForPod(pod);
          if (type !== "c5.2xlarge") {
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

    // Scale down: terminate a node idle of *workload* a while (keep at least one).
    if (s.scaleCooldown === 0 && this.schedulableNodes().length > 1) {
      const idle = this.state.nodes.find(
        (n) =>
          n.status === "Ready" &&
          n.minor >= s.clusterMinor &&
          this.workloadPodsOnNode(n).length === 0 &&
          n.idleTicks > 20
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

    // 1. node provisioning + upgrade completion
    for (const node of s.nodes) {
      if (node.status === "Provisioning" || node.status === "Upgrading") {
        node.provisioningTicksLeft -= 1;
        if (node.provisioningTicksLeft <= 0) {
          const wasUpgrade = node.status === "Upgrading";
          node.status = "Ready";
          if (wasUpgrade) {
            node.minor = s.clusterMinor;
            s.metrics.nodesUpgraded += 1;
            this.log("good", `${node.name} back online on v1.${node.minor}.`);
          } else {
            this.log("good", `${node.name} is now Ready.`);
          }
          this.reconcileDaemonSets(node);
          this.checkUpgradeComplete();
        }
      }
    }

    // 1b. cluster upgrade events
    if (s.nextUpgradeTick && s.tick >= s.nextUpgradeTick) {
      this.triggerUpgrade();
      s.nextUpgradeTick = s.tick + (this.scenario.upgradeEvery || 700);
    }

    // 1c. spot market + interruptions
    this.updateSpotMarket();

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

    // 4. advance running jobs, track idleness (daemonset-only nodes count idle)
    for (const node of s.nodes) {
      const hasWorkload = node.podIds.some((id) => {
        const p = s.pods.get(id);
        return p && p.kind !== "daemon";
      });
      if (!hasWorkload && node.status === "Ready") node.idleTicks += 1;
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

    // 6. scoring (utilization reward + latency/cost pressure + upgrade tech-debt)
    const util = this.clusterUtilization();
    s.lastUtil = util;
    const utilDelta = util * UTIL_W;
    const latDelta = pendingCount * PENDING_PEN;
    const hourlyCost = this.hourlyCost();
    const costDelta = hourlyCost * COST_PEN;
    let outdatedDelta = 0;
    if (s.upgradePending) {
      const outdated = s.nodes.filter(
        (n) => n.minor < s.clusterMinor && ["Ready", "Cordoned"].includes(n.status)
      ).length;
      outdatedDelta = outdated * OUTDATED_PEN;
    }
    s.score += utilDelta - latDelta - costDelta - outdatedDelta;
    s.breakdown.util += utilDelta;
    s.breakdown.latency -= latDelta;
    s.breakdown.cost -= costDelta;
    s.breakdown.upgrade -= outdatedDelta;

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
      ["Ready", "Cordoned", "Draining", "Reclaiming"].includes(n.status)
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

  /** Sum of $/hr for every node currently powered on (spot at live price). */
  hourlyCost() {
    return this.state.nodes
      .filter((n) => n.status !== "Terminating")
      .reduce((s, n) => s + effectiveCost(n, this.state.spotPrice), 0);
  }

  avgLatencySeconds() {
    const m = this.state.metrics;
    if (m.latencyCount === 0) return 0;
    return m.latencySum / m.latencyCount / TICKS_PER_SECOND;
  }

  runningCount() {
    let n = 0;
    for (const p of this.state.pods.values()) {
      if (p.status === "Running" && p.kind !== "daemon") n += 1;
    }
    return n;
  }

  /** Count of nodes not yet on the current cluster version. */
  outdatedNodeCount() {
    return this.state.nodes.filter((n) => n.minor < this.state.clusterMinor).length;
  }

  uptimeSeconds() {
    return this.state.tick / TICKS_PER_SECOND;
  }
}
