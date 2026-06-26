// Pure-logic tests for the scheduler predicates and the engine. Run with:
//   node --test k8s-scheduler-game/test/scheduler.test.mjs
import test from "node:test";
import assert from "node:assert/strict";

import { evaluateFit, tolerates, selectorMatches, freeResources, bestNodeFor } from "../src/scheduler.js";
import { Game } from "../src/engine.js";
import { INSTANCE_TYPES } from "../src/types.js";
import { createPod, makeRng, SCENARIOS, spawnForTick } from "../src/workload.js";

function nodeFrom(typeKey, zone = "us-east-1a") {
  const s = INSTANCE_TYPES[typeKey];
  return {
    id: "n1",
    status: "Ready",
    cpu: s.cpu,
    mem: s.mem,
    gpu: s.gpu,
    cost: s.cost,
    labels: { ...s.labels, "topology.kubernetes.io/zone": zone },
    taints: s.taints.map((t) => ({ ...t })),
    podIds: [],
  };
}

test("tolerations match taints correctly", () => {
  const taint = { key: "spot", value: "true", effect: "NoSchedule" };
  assert.equal(tolerates([{ key: "spot", value: "true", effect: "NoSchedule" }], taint), true);
  assert.equal(tolerates([{ key: "spot", operator: "Exists" }], taint), true);
  assert.equal(tolerates([{ key: "nvidia.com/gpu" }], taint), false);
  assert.equal(tolerates([], taint), false);
});

test("selectorMatches enforces every label", () => {
  assert.equal(selectorMatches({ disktype: "ssd" }, { disktype: "ssd", zone: "a" }), true);
  assert.equal(selectorMatches({ disktype: "ssd" }, { disktype: "hdd" }), false);
  assert.equal(selectorMatches({}, { anything: "x" }), true);
});

test("resource fit blocks oversized pods", () => {
  const node = nodeFrom("c5.xlarge"); // 4000m / 8192Mi
  const big = { cpu: 5000, mem: 1024, gpu: 0, nodeSelector: {}, tolerations: [], antiAffinity: false, app: "x" };
  const fit = evaluateFit(big, node, []);
  assert.equal(fit.ok, false);
  assert.ok(fit.reasons.some((r) => r.includes("insufficient cpu")));
});

test("free resources subtract running pods", () => {
  const node = nodeFrom("c5.2xlarge"); // 8000m / 16384Mi
  const pods = [
    { cpu: 1000, mem: 2048, gpu: 0 },
    { cpu: 500, mem: 512, gpu: 0 },
  ];
  const free = freeResources(node, pods);
  assert.equal(free.cpu, 8000 - 1500);
  assert.equal(free.mem, 16384 - 2560);
});

test("nodeSelector for ssd is enforced", () => {
  const hdd = nodeFrom("c5.2xlarge"); // disktype network
  const ssd = nodeFrom("c5d.2xlarge"); // disktype ssd
  const pod = { cpu: 500, mem: 512, gpu: 0, nodeSelector: { disktype: "ssd" }, tolerations: [], antiAffinity: false, app: "cache" };
  assert.equal(evaluateFit(pod, hdd, []).ok, false);
  assert.equal(evaluateFit(pod, ssd, []).ok, true);
});

test("gpu taint requires toleration and gpu capacity", () => {
  const gpuNode = nodeFrom("g4dn.xlarge");
  const noTol = { cpu: 500, mem: 512, gpu: 0, nodeSelector: {}, tolerations: [], antiAffinity: false, app: "frontend" };
  // frontend has no toleration -> blocked by taint
  assert.equal(evaluateFit(noTol, gpuNode, []).ok, false);

  const mlPod = {
    cpu: 2000, mem: 8192, gpu: 1,
    nodeSelector: { accelerator: "nvidia-t4" },
    tolerations: [{ key: "nvidia.com/gpu", value: "present", effect: "NoSchedule" }],
    antiAffinity: false, app: "ml-train",
  };
  assert.equal(evaluateFit(mlPod, gpuNode, []).ok, true);
});

test("anti-affinity prevents two same-app pods on one node", () => {
  const node = nodeFrom("c5.2xlarge");
  const a = { cpu: 250, mem: 256, gpu: 0, nodeSelector: {}, tolerations: [], antiAffinity: true, app: "frontend" };
  const b = { cpu: 250, mem: 256, gpu: 0, nodeSelector: {}, tolerations: [], antiAffinity: true, app: "frontend" };
  assert.equal(evaluateFit(b, node, [a]).ok, false);
  assert.ok(evaluateFit(b, node, [a]).reasons.some((r) => r.includes("anti-affinity")));
});

test("cordoned nodes are unschedulable", () => {
  const node = nodeFrom("c5.2xlarge");
  node.status = "Cordoned";
  const pod = { cpu: 250, mem: 256, gpu: 0, nodeSelector: {}, tolerations: [], antiAffinity: false, app: "frontend" };
  assert.equal(evaluateFit(pod, node, []).ok, false);
});

test("bestNodeFor packs onto the fuller feasible node", () => {
  const n1 = { ...nodeFrom("c5.2xlarge"), id: "n1" };
  const n2 = { ...nodeFrom("c5.2xlarge"), id: "n2" };
  const existing = { cpu: 4000, mem: 4096, gpu: 0, app: "api" };
  const podsByNode = (id) => (id === "n1" ? [existing] : []);
  const pod = { cpu: 500, mem: 512, gpu: 0, nodeSelector: {}, tolerations: [], antiAffinity: false, app: "frontend" };
  const best = bestNodeFor(pod, [n1, n2], podsByNode);
  assert.equal(best.node.id, "n1"); // MostAllocated -> fill n1 first
});

test("engine schedules a pod and records latency", () => {
  const game = new Game("steady");
  game.state.tick = 5;
  const pod = createPod("frontend", makeRng(1), 2);
  game.state.pods.set(pod.id, pod);
  game.state.pendingIds.push(pod.id);
  const node = game.schedulableNodes()[0];
  const res = game.schedulePod(pod.id, node.id);
  assert.equal(res.ok, true);
  assert.equal(pod.status, "Running");
  assert.equal(game.state.metrics.latencySum, 3); // 5 - 2
});

test("draining a node evicts its pods back to pending with a penalty", () => {
  const game = new Game("steady");
  const pod = createPod("api", makeRng(2), 0);
  game.state.pods.set(pod.id, pod);
  game.state.pendingIds.push(pod.id);
  const node = game.schedulableNodes()[0];
  game.schedulePod(pod.id, node.id);
  const before = game.state.score;
  game.drain(node.id);
  assert.equal(pod.status, "Pending");
  assert.equal(node.status, "Cordoned");
  assert.ok(game.state.score < before);
  assert.ok(game.state.pendingIds.includes(pod.id));
});

test("jobs complete and free resources after their lifetime", () => {
  const game = new Game("steady");
  const pod = createPod("batch", makeRng(3), 0);
  pod.remainingTicks = 2;
  game.state.pods.set(pod.id, pod);
  game.state.pendingIds.push(pod.id);
  const node = game.schedulableNodes()[0];
  game.schedulePod(pod.id, node.id);
  assert.ok(node.podIds.includes(pod.id));
  game.tick();
  game.tick();
  game.tick();
  assert.equal(game.state.pods.has(pod.id), false);
  assert.equal(node.podIds.includes(pod.id), false);
  assert.ok(game.state.metrics.completedTotal >= 1);
});

test("autoscaler provisions a GPU node when ml-train is stuck pending", () => {
  const game = new Game("steady");
  game.state.autoScale = true;
  game.state.autoSchedule = true;
  const pod = createPod("ml-train", makeRng(4), 0);
  game.state.pods.set(pod.id, pod);
  game.state.pendingIds.push(pod.id);
  // No GPU node exists initially; autoscaler should add one within a few ticks.
  let addedGpu = false;
  for (let i = 0; i < 40 && !addedGpu; i++) {
    game.tick();
    addedGpu = game.state.nodes.some((n) => n.type === "g4dn.xlarge");
  }
  assert.equal(addedGpu, true);
});

test("workload generator is deterministic for a seed", () => {
  const a = spawnForTick(SCENARIOS.chaos, makeRng(42), 10).map((p) => p.app);
  const b = spawnForTick(SCENARIOS.chaos, makeRng(42), 10).map((p) => p.app);
  assert.deepEqual(a, b);
});

function bootNode(game, node) {
  for (let i = 0; i < INSTANCE_TYPES[node.type].bootTicks + 1; i++) game.tick();
}

test("daemonsets run one pod per node, match selectors, and never queue", () => {
  const game = new Game("steady");
  // Start nodes are non-GPU: node-exporter + fluent-bit + kube-proxy = 3 each.
  const node = game.schedulableNodes()[0];
  const daemons = game.podsOnNode(node).filter((p) => p.kind === "daemon");
  assert.equal(daemons.length, 3);
  assert.ok(daemons.every((p) => p.daemonOf));
  assert.equal(game.state.pendingIds.length, 0); // daemonsets are not scheduled via the queue

  const gpu = game.addNode("g4dn.xlarge");
  bootNode(game, gpu);
  const gpuReady = game.nodeById(gpu.id);
  const names = game.podsOnNode(gpuReady).filter((p) => p.kind === "daemon").map((p) => p.daemonOf);
  assert.ok(names.includes("nvidia-device-plugin"), "GPU node gets the device plugin daemonset");
  assert.equal(names.length, 4);
});

test("daemonsets are overhead: they reduce schedulable capacity", () => {
  const game = new Game("steady");
  const node = game.schedulableNodes()[0];
  const free = freeResources(node, game.podsOnNode(node));
  assert.ok(free.cpu < node.cpu, "daemonsets consume cpu");
  assert.ok(free.mem < node.mem, "daemonsets consume memory");
});

test("drain keeps daemonset pods; delete purges them with no pod leak", () => {
  const game = new Game("steady");
  const node = game.schedulableNodes()[0];
  const pod = createPod("frontend", makeRng(7), 0);
  game.state.pods.set(pod.id, pod);
  game.state.pendingIds.push(pod.id);
  game.schedulePod(pod.id, node.id);

  const daemonsBefore = game.podsOnNode(node).filter((p) => p.kind === "daemon").length;
  assert.ok(daemonsBefore >= 3);
  game.drain(node.id);
  assert.equal(pod.status, "Pending"); // workload evicted
  assert.equal(game.podsOnNode(node).filter((p) => p.kind === "daemon").length, daemonsBefore); // daemons stay

  const daemonIds = game.podsOnNode(node).filter((p) => p.kind === "daemon").map((p) => p.id);
  game.deleteNode(node.id);
  for (const id of daemonIds) assert.equal(game.state.pods.has(id), false);
});

test("spot reclamation evicts workload, drops the node, and is counted", () => {
  const game = new Game("steady");
  const spot = game.addNode("c5.xlarge-spot");
  bootNode(game, spot);
  const node = game.nodeById(spot.id);
  assert.equal(node.status, "Ready");

  const pod = createPod("batch", makeRng(9), game.state.tick); // batch tolerates spot
  game.state.pods.set(pod.id, pod);
  game.state.pendingIds.push(pod.id);
  assert.equal(game.schedulePod(pod.id, node.id).ok, true);

  const daemonIds = game.podsOnNode(node).filter((p) => p.kind === "daemon").map((p) => p.id);
  game.reclaimSpotNode(node);
  assert.ok(!game.nodeById(spot.id)); // node is gone
  assert.equal(pod.status, "Pending");
  assert.ok(game.state.pendingIds.includes(pod.id));
  assert.equal(game.state.metrics.spotReclaims, 1);
  for (const id of daemonIds) assert.equal(game.state.pods.has(id), false);
});

test("spot price fluctuates over time but stays in bounds", () => {
  const game = new Game("steady");
  const seen = new Set();
  for (let i = 0; i < 300; i++) {
    game.tick();
    assert.ok(game.state.spotPrice >= 0.2 && game.state.spotPrice <= 0.95);
    seen.add(game.state.spotPrice.toFixed(3));
  }
  assert.ok(seen.size > 5, "spot price should vary");
});

test("upgradeNode drains workload and reboots onto the new version", () => {
  const game = new Game("steady");
  const node = game.schedulableNodes()[0];
  const pod = createPod("api", makeRng(11), 0);
  game.state.pods.set(pod.id, pod);
  game.state.pendingIds.push(pod.id);
  game.schedulePod(pod.id, node.id);

  game.triggerUpgrade();
  const target = game.state.clusterMinor;
  assert.equal(game.state.upgradePending, true);
  assert.ok(node.minor < target);

  const r = game.upgradeNode(node.id);
  assert.equal(r.ok, true);
  assert.equal(node.status, "Upgrading");
  assert.equal(pod.status, "Pending"); // workload gracefully drained
  assert.equal(game.podsOnNode(node).filter((p) => p.kind === "daemon").length, 0);

  bootNode(game, node);
  assert.equal(node.status, "Ready");
  assert.equal(node.minor, target);
  assert.ok(game.podsOnNode(node).filter((p) => p.kind === "daemon").length >= 3); // daemons recreated
  assert.ok(game.state.metrics.nodesUpgraded >= 1);
});

test("completing a version rollout awards the bonus exactly once", () => {
  const game = new Game("steady");
  game.triggerUpgrade();
  const m = game.state.clusterMinor;
  const before = game.state.breakdown.upgrade;
  for (const n of game.state.nodes) n.minor = m; // pretend every node was restarted
  game.checkUpgradeComplete();
  assert.equal(game.state.upgradePending, false);
  assert.equal(game.state.breakdown.upgrade - before, 35);
  game.checkUpgradeComplete(); // idempotent
  assert.equal(game.state.breakdown.upgrade - before, 35);
});

test("auto rolling upgrade brings the whole fleet current under automation", () => {
  const game = new Game("steady");
  game.state.autoSchedule = true;
  game.state.autoScale = true;
  for (let i = 0; i < 60; i++) game.tick(); // warm up
  game.triggerUpgrade();
  const target = game.state.clusterMinor;
  let done = false;
  for (let i = 0; i < 250 && !done; i++) {
    game.tick();
    done = !game.state.upgradePending;
  }
  assert.equal(done, true, "rollout should finish");
  assert.ok(game.state.nodes.every((n) => n.minor >= target));
  assert.ok(game.state.metrics.nodesUpgraded >= 1);
});

test("soak: every scenario runs 1200 ticks under full automation without overcommit", () => {
  for (const id of Object.keys(SCENARIOS)) {
    const game = new Game(id);
    game.state.autoSchedule = true;
    game.state.autoScale = true;
    for (let i = 0; i < 1200; i++) game.tick();
    assert.ok(Number.isFinite(game.state.score), `${id} score is finite`);
    for (const node of game.state.nodes) {
      const used = game.podsOnNode(node).reduce(
        (a, p) => ({ cpu: a.cpu + p.cpu, mem: a.mem + p.mem, gpu: a.gpu + p.gpu }),
        { cpu: 0, mem: 0, gpu: 0 }
      );
      assert.ok(used.cpu <= node.cpu, `${id}: cpu overcommit on ${node.id}`);
      assert.ok(used.mem <= node.mem, `${id}: mem overcommit on ${node.id}`);
      assert.ok(used.gpu <= node.gpu, `${id}: gpu overcommit on ${node.id}`);
    }
  }
});

test("full automation keeps every scenario healthy (bounded queue, good utilization)", () => {
  for (const id of Object.keys(SCENARIOS)) {
    const game = new Game(id);
    game.state.autoSchedule = true;
    game.state.autoScale = true;
    for (let i = 0; i < 1000; i++) game.tick();
    const pending = game.state.pendingIds.length;
    const util = game.clusterUtilization();
    // Generous bounds: the auto policy should clearly keep up, not melt down.
    assert.ok(pending < 35, `${id}: pending=${pending}`);
    assert.ok(util > 0.35, `${id}: utilization=${util.toFixed(2)}`);
    assert.ok(game.state.metrics.slaBreaches < 40, `${id}: sla=${game.state.metrics.slaBreaches}`);
    assert.ok(game.state.score > 0, `${id}: score=${Math.round(game.state.score)}`);
  }
});

test("finite pod lifetimes keep the cluster population bounded", () => {
  const game = new Game("steady");
  game.state.autoSchedule = true;
  game.state.autoScale = true;
  for (let i = 0; i < 1200; i++) game.tick();
  // Services retire, so the total live pod count reaches a steady state instead
  // of growing without bound (this also keeps the tick loop cheap).
  assert.ok(game.state.pods.size < 400, `live pods=${game.state.pods.size}`);
  assert.ok(game.state.metrics.retiredTotal > 0, "services should retire over time");
  assert.ok(game.state.pendingIds.length < 60, `pending=${game.state.pendingIds.length}`);
});
