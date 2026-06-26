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
  const node = nodeFrom("general-medium"); // 4000m / 8192Mi
  const big = { cpu: 5000, mem: 1024, gpu: 0, nodeSelector: {}, tolerations: [], antiAffinity: false, app: "x" };
  const fit = evaluateFit(big, node, []);
  assert.equal(fit.ok, false);
  assert.ok(fit.reasons.some((r) => r.includes("insufficient cpu")));
});

test("free resources subtract running pods", () => {
  const node = nodeFrom("general-large"); // 8000m / 16384
  const pods = [
    { cpu: 1000, mem: 2048, gpu: 0 },
    { cpu: 500, mem: 512, gpu: 0 },
  ];
  const free = freeResources(node, pods);
  assert.equal(free.cpu, 8000 - 1500);
  assert.equal(free.mem, 16384 - 2560);
});

test("nodeSelector for ssd is enforced", () => {
  const hdd = nodeFrom("general-large"); // disktype hdd
  const ssd = nodeFrom("ssd-large"); // disktype ssd
  const pod = { cpu: 500, mem: 512, gpu: 0, nodeSelector: { disktype: "ssd" }, tolerations: [], antiAffinity: false, app: "cache" };
  assert.equal(evaluateFit(pod, hdd, []).ok, false);
  assert.equal(evaluateFit(pod, ssd, []).ok, true);
});

test("gpu taint requires toleration and gpu capacity", () => {
  const gpuNode = nodeFrom("gpu-xlarge");
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
  const node = nodeFrom("general-large");
  const a = { cpu: 250, mem: 256, gpu: 0, nodeSelector: {}, tolerations: [], antiAffinity: true, app: "frontend" };
  const b = { cpu: 250, mem: 256, gpu: 0, nodeSelector: {}, tolerations: [], antiAffinity: true, app: "frontend" };
  assert.equal(evaluateFit(b, node, [a]).ok, false);
  assert.ok(evaluateFit(b, node, [a]).reasons.some((r) => r.includes("anti-affinity")));
});

test("cordoned nodes are unschedulable", () => {
  const node = nodeFrom("general-large");
  node.status = "Cordoned";
  const pod = { cpu: 250, mem: 256, gpu: 0, nodeSelector: {}, tolerations: [], antiAffinity: false, app: "frontend" };
  assert.equal(evaluateFit(pod, node, []).ok, false);
});

test("bestNodeFor packs onto the fuller feasible node", () => {
  const n1 = { ...nodeFrom("general-large"), id: "n1" };
  const n2 = { ...nodeFrom("general-large"), id: "n2" };
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
    addedGpu = game.state.nodes.some((n) => n.type === "gpu-xlarge");
  }
  assert.equal(addedGpu, true);
});

test("workload generator is deterministic for a seed", () => {
  const a = spawnForTick(SCENARIOS.chaos, makeRng(42), 10).map((p) => p.app);
  const b = spawnForTick(SCENARIOS.chaos, makeRng(42), 10).map((p) => p.app);
  assert.deepEqual(a, b);
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
