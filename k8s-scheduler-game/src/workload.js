// Workload generation. Each scenario describes a starting cluster and an
// arrival process that mints pods from the APP_TEMPLATES over time.

import { APP_TEMPLATES, APP_COLORS, ZONES, nextId, randHash } from "./types.js";

/** Deterministic, seedable PRNG (mulberry32) so runs are reproducible. */
export function makeRng(seed) {
  let a = seed >>> 0;
  return function () {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

function pick(rng, arr) {
  return arr[Math.floor(rng() * arr.length)];
}

function randInt(rng, [lo, hi]) {
  return lo + Math.floor(rng() * (hi - lo + 1));
}

/** Weighted choice from a {key: weight} map. */
function weightedPick(rng, weights) {
  const entries = Object.entries(weights).filter(([, w]) => w > 0);
  const total = entries.reduce((s, [, w]) => s + w, 0);
  let r = rng() * total;
  for (const [k, w] of entries) {
    r -= w;
    if (r <= 0) return k;
  }
  return entries[entries.length - 1][0];
}

/** Mint a concrete pod from a template. */
export function createPod(appName, rng, tick) {
  const t = APP_TEMPLATES[appName];
  const pod = {
    id: nextId("pod"),
    name: `${t.app}-${randHash(rng, 4)}-${randHash(rng, 4)}`,
    app: t.app,
    color: APP_COLORS[t.app] || "#94a3b8",
    kind: t.kind,
    cpu: t.cpu,
    mem: t.mem,
    gpu: t.gpu,
    nodeSelector: { ...t.nodeSelector },
    tolerations: t.tolerations.map((x) => ({ ...x })),
    antiAffinity: !!t.antiAffinity,
    softAntiAffinity: !!t.softAntiAffinity,
    priority: t.priority,
    preferredZone: pick(rng, ZONES),
    status: "Pending",
    nodeId: null,
    arrivalTick: tick,
    scheduledTick: null,
    pendingTicks: 0,
    slaBreached: false,
    // every pod runs for a bounded time; remaining ticks only tick down while Running.
    remainingTicks: randInt(rng, t.lifetime),
  };
  return pod;
}

/**
 * Mint a DaemonSet pod bound to a specific node. These are placed directly by
 * the "daemonset controller" (bypassing the scheduler), tolerate every taint,
 * and never appear in the pending queue.
 */
export function createDaemonPod(ds, node, tick) {
  return {
    id: nextId("ds"),
    name: `${ds.name}-${node.name.replace("node-", "")}`,
    app: ds.name,
    color: ds.color || "#64748b",
    kind: "daemon",
    daemonOf: ds.name,
    cpu: ds.cpu,
    mem: ds.mem,
    gpu: 0,
    nodeSelector: {},
    tolerations: [],
    antiAffinity: false,
    softAntiAffinity: false,
    priority: 2000,
    preferredZone: null,
    status: "Running",
    nodeId: node.id,
    arrivalTick: tick,
    scheduledTick: tick,
    pendingTicks: 0,
    slaBreached: false,
    remainingTicks: null,
  };
}

/**
 * Scenario catalogue. `arrival(tick, rng)` returns the expected number of pods
 * to spawn this tick (fractional values spawn probabilistically). `weights`
 * may be a function of tick to create waves.
 */
export const SCENARIOS = {
  steady: {
    id: "steady",
    name: "Steady State",
    blurb: "A balanced, predictable workload. Great for learning the ropes.",
    startNodes: ["c5.2xlarge", "c5.2xlarge", "c5d.2xlarge"],
    seed: 1337,
    upgradeEvery: 900,
    arrival: () => 0.55,
    weights: () => ({ frontend: 7, api: 6, cache: 3, postgres: 1, batch: 4 }),
  },
  spike: {
    id: "spike",
    name: "Traffic Spike",
    blurb: "Calm baseline punctuated by big frontend/api surges. Scale up fast, scale down after.",
    startNodes: ["c5.2xlarge", "c5d.2xlarge"],
    seed: 7,
    upgradeEvery: 700,
    arrival: (tick) => {
      const phase = tick % 240;
      return phase < 60 ? 1.8 : 0.25; // ~15s storm every ~60s
    },
    weights: (tick) => {
      const storm = tick % 240 < 60;
      return storm
        ? { frontend: 12, api: 9, cache: 2, postgres: 0, batch: 1 }
        : { frontend: 4, api: 3, cache: 2, postgres: 1, batch: 3 };
    },
  },
  gpu: {
    id: "gpu",
    name: "GPU Crunch",
    blurb: "Steady services plus periodic ML training jobs that demand GPU nodes you must provision.",
    startNodes: ["c5.2xlarge", "c5d.2xlarge"],
    seed: 99,
    upgradeEvery: 750,
    arrival: (tick) => (tick % 200 < 30 ? 1.4 : 0.5),
    weights: (tick) => {
      const burst = tick % 200 < 30;
      return burst
        ? { frontend: 3, api: 3, cache: 1, postgres: 0, batch: 2, "ml-train": 6 }
        : { frontend: 6, api: 5, cache: 2, postgres: 1, batch: 3, "ml-train": 0 };
    },
  },
  chaos: {
    id: "chaos",
    name: "Production Chaos",
    blurb: "Everything, everywhere, all at once. High churn across every workload type. Hard mode.",
    startNodes: ["c5.2xlarge", "c5d.2xlarge"],
    seed: 42,
    upgradeEvery: 550,
    arrival: (tick) => 0.9 + (tick % 150 < 40 ? 1.2 : 0),
    weights: () => ({ frontend: 8, api: 7, cache: 4, postgres: 2, batch: 6, "ml-train": 3 }),
  },
};

/**
 * Spawn pods for a single tick. Returns an array of new pod objects.
 *
 * `aliveByApp` maps app -> current live replica count; apps already at their
 * maxReplicas are excluded so each app behaves like a bounded Deployment.
 */
export function spawnForTick(scenario, rng, tick, aliveByApp = {}) {
  const rate = scenario.arrival(tick, rng);
  const weights = { ...scenario.weights(tick) };
  // Zero out apps that have hit their replica cap.
  for (const app of Object.keys(weights)) {
    const cap = APP_TEMPLATES[app]?.maxReplicas ?? Infinity;
    if ((aliveByApp[app] || 0) >= cap) weights[app] = 0;
  }
  if (Object.values(weights).every((w) => w <= 0)) return [];

  let count = Math.floor(rate);
  if (rng() < rate - count) count += 1; // fractional remainder -> probabilistic
  const pods = [];
  for (let i = 0; i < count; i++) {
    pods.push(createPod(weightedPick(rng, weights), rng, tick));
  }
  return pods;
}
