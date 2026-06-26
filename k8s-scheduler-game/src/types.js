// Domain model for the Kubescheduler game.
//
// All CPU values are in millicores (m); 1000m == 1 vCPU.
// All memory values are in MiB; 1024 MiB == 1 GiB.
// These are the same units real Kubernetes resource requests use.

export const TICKS_PER_SECOND = 4; // simulation ticks per real second at 1x speed
export const SLA_PENDING_TICKS = 40; // a pod pending longer than this breaches SLA

/** Availability zones a node can be placed in. */
export const ZONES = ["us-east-1a", "us-east-1b", "us-east-1c"];

/**
 * Node instance types ("node pools"), modeled on real AWS EC2 families. Each
 * carries baked-in labels and taints, exactly like a managed node group in
 * EKS/GKE. Specs and `cost` ($/hour) mirror AWS on-demand pricing in us-east-1
 * (Linux): c5 compute-optimized general purpose, c5d (compute + local NVMe SSD)
 * for storage-bound apps, r5 memory-optimized, and g4dn (NVIDIA T4) GPU — plus
 * spot variants of the c5s. CPU is in millicores, memory in MiB. Spot `cost` is
 * the on-demand reference price; spot nodes are billed at the live spot price
 * (a fraction of it — see engine).
 */
export const INSTANCE_TYPES = {
  "c5.xlarge": {
    key: "c5.xlarge",
    family: "general",
    cpu: 4000,
    mem: 8192,
    gpu: 0,
    cost: 0.17,
    bootTicks: 8,
    labels: { "node.kubernetes.io/instance-type": "c5.xlarge", disktype: "network" },
    taints: [],
  },
  "c5.2xlarge": {
    key: "c5.2xlarge",
    family: "general",
    cpu: 8000,
    mem: 16384,
    gpu: 0,
    cost: 0.34,
    bootTicks: 10,
    labels: { "node.kubernetes.io/instance-type": "c5.2xlarge", disktype: "network" },
    taints: [],
  },
  "c5d.2xlarge": {
    key: "c5d.2xlarge",
    family: "ssd",
    cpu: 8000,
    mem: 16384,
    gpu: 0,
    cost: 0.384,
    bootTicks: 10,
    labels: { "node.kubernetes.io/instance-type": "c5d.2xlarge", disktype: "ssd" },
    taints: [],
  },
  "r5.2xlarge": {
    key: "r5.2xlarge",
    family: "mem",
    cpu: 8000,
    mem: 65536, // 64 GiB
    gpu: 0,
    cost: 0.504,
    bootTicks: 12,
    labels: { "node.kubernetes.io/instance-type": "r5.2xlarge", disktype: "ssd" },
    taints: [],
  },
  "g4dn.xlarge": {
    key: "g4dn.xlarge",
    family: "gpu",
    cpu: 4000,
    mem: 16384,
    gpu: 1, // 1× NVIDIA T4
    cost: 0.526,
    bootTicks: 16,
    labels: {
      "node.kubernetes.io/instance-type": "g4dn.xlarge",
      disktype: "ssd",
      accelerator: "nvidia-t4",
    },
    taints: [{ key: "nvidia.com/gpu", value: "present", effect: "NoSchedule" }],
  },
  "c5.xlarge-spot": {
    key: "c5.xlarge-spot",
    family: "spot",
    cpu: 4000,
    mem: 8192,
    gpu: 0,
    cost: 0.17, // on-demand reference; billed at the live spot fraction
    bootTicks: 6,
    labels: {
      "node.kubernetes.io/instance-type": "c5.xlarge",
      "karpenter.sh/capacity-type": "spot",
      disktype: "network",
    },
    taints: [{ key: "spot", value: "true", effect: "NoSchedule" }],
  },
  "c5.2xlarge-spot": {
    key: "c5.2xlarge-spot",
    family: "spot",
    cpu: 8000,
    mem: 16384,
    gpu: 0,
    cost: 0.34,
    bootTicks: 6,
    labels: {
      "node.kubernetes.io/instance-type": "c5.2xlarge",
      "karpenter.sh/capacity-type": "spot",
      disktype: "network",
    },
    taints: [{ key: "spot", value: "true", effect: "NoSchedule" }],
  },
};

/**
 * DaemonSets: infrastructure pods the daemonset controller runs on EVERY node
 * (or every node matching nodeSelector). They tolerate all taints, can't be
 * hand-scheduled or moved, and consume capacity on every node — so they're pure
 * per-node overhead that rewards running fewer, larger nodes. A node-local agent
 * is recreated automatically whenever a node joins or finishes an upgrade.
 */
export const DAEMONSETS = [
  { name: "node-exporter", cpu: 75, mem: 96, color: "#64748b" },
  { name: "fluent-bit", cpu: 150, mem: 256, color: "#475569" },
  { name: "kube-proxy", cpu: 75, mem: 96, color: "#52525b" },
  {
    name: "nvidia-device-plugin",
    cpu: 50,
    mem: 128,
    color: "#3f6212",
    nodeSelector: { accelerator: "nvidia-t4" },
  },
];

/** Per-app color used throughout the UI. */
export const APP_COLORS = {
  frontend: "#38bdf8",
  api: "#34d399",
  cache: "#f472b6",
  postgres: "#a78bfa",
  batch: "#fbbf24",
  "ml-train": "#fb7185",
};

/**
 * Deployment / workload templates. Pods are minted from these.
 * - kind "service": long-running, never completes on its own.
 * - kind "job": runs for a bounded lifetime then completes and frees resources.
 * - nodeSelector: hard label match (required node affinity).
 * - tolerations: taints this pod can land on.
 * - antiAffinity: HARD pod anti-affinity — two pods of the same app may never
 *   share a node (hostname topology). Only used on low-volume apps, since it
 *   caps the app at one replica per node.
 * - softAntiAffinity: PREFERRED spread — influences scoring (the scheduler
 *   tries to spread replicas across nodes) but never blocks placement.
 * - maxReplicas: like a Deployment's replica count — the workload generator
 *   won't mint a new pod for an app already at this many live replicas. This
 *   bounds the cluster and keeps hard anti-affinity satisfiable.
 * - preferredZone: soft affinity used only for scoring/tie-breaks.
 * - lifetime: [min, max] ticks the pod runs before it finishes. Services get
 *   long, variable lifetimes (think rollouts / scaling churn) so the cluster
 *   reaches a bounded steady state; jobs are short.
 */
export const APP_TEMPLATES = {
  frontend: {
    app: "frontend",
    kind: "service",
    cpu: 250,
    mem: 256,
    gpu: 0,
    nodeSelector: {},
    tolerations: [],
    antiAffinity: false,
    softAntiAffinity: true,
    maxReplicas: 26,
    priority: 100,
    lifetime: [120, 300],
    weight: 7,
  },
  api: {
    app: "api",
    kind: "service",
    cpu: 500,
    mem: 512,
    gpu: 0,
    nodeSelector: {},
    tolerations: [],
    antiAffinity: false,
    softAntiAffinity: true,
    maxReplicas: 18,
    priority: 200,
    lifetime: [140, 340],
    weight: 6,
  },
  cache: {
    app: "cache",
    kind: "service",
    cpu: 500,
    mem: 2048,
    gpu: 0,
    nodeSelector: { disktype: "ssd" },
    tolerations: [],
    antiAffinity: false,
    softAntiAffinity: false,
    maxReplicas: 10,
    priority: 150,
    lifetime: [160, 360],
    weight: 3,
  },
  postgres: {
    app: "postgres",
    kind: "service",
    cpu: 1000,
    mem: 6144,
    gpu: 0,
    nodeSelector: { disktype: "ssd" },
    tolerations: [],
    antiAffinity: true,
    softAntiAffinity: false,
    maxReplicas: 4,
    priority: 400,
    lifetime: [160, 300],
    weight: 1,
  },
  batch: {
    app: "batch",
    kind: "job",
    cpu: 1000,
    mem: 1024,
    gpu: 0,
    nodeSelector: {},
    tolerations: [{ key: "spot", value: "true", effect: "NoSchedule" }],
    antiAffinity: false,
    softAntiAffinity: false,
    maxReplicas: 28,
    priority: 50,
    lifetime: [30, 90],
    weight: 5,
  },
  "ml-train": {
    app: "ml-train",
    kind: "job",
    cpu: 2000,
    mem: 8192,
    gpu: 1,
    nodeSelector: { accelerator: "nvidia-t4" },
    tolerations: [{ key: "nvidia.com/gpu", value: "present", effect: "NoSchedule" }],
    antiAffinity: false,
    softAntiAffinity: false,
    maxReplicas: 6,
    priority: 300,
    lifetime: [40, 120],
    weight: 2,
  },
};

// ---------------------------------------------------------------------------
// Small pure helpers shared across modules.
// ---------------------------------------------------------------------------

/** Sum the resource requests of a list of pod objects. */
export function sumRequests(pods) {
  return pods.reduce(
    (acc, p) => {
      acc.cpu += p.cpu;
      acc.mem += p.mem;
      acc.gpu += p.gpu;
      return acc;
    },
    { cpu: 0, mem: 0, gpu: 0 }
  );
}

/** Capacity object for a node from its instance type. */
export function nodeCapacity(node) {
  return { cpu: node.cpu, mem: node.mem, gpu: node.gpu };
}

/**
 * A node's current $/hr. Spot nodes are billed at the live, fluctuating spot
 * price (a multiplier on their base cost); on-demand nodes are flat.
 */
export function effectiveCost(node, spotPrice = 1) {
  return node.cost * (node.spot ? spotPrice : 1);
}

/** True if a node is currently able to accept newly scheduled pods. */
export function isSchedulable(node) {
  return node.status === "Ready";
}

/** Format millicores as a friendly core string. */
export function fmtCpu(m) {
  if (m >= 1000) return `${(m / 1000).toFixed(m % 1000 === 0 ? 0 : 1)}`;
  return `${(m / 1000).toFixed(2)}`;
}

/** Format MiB as Gi/Mi. */
export function fmtMem(mib) {
  if (mib >= 1024) return `${(mib / 1024).toFixed(mib % 1024 === 0 ? 0 : 1)}Gi`;
  return `${mib}Mi`;
}

let _seq = 0;
/** Monotonic id generator. */
export function nextId(prefix) {
  _seq += 1;
  return `${prefix}-${_seq.toString(36)}`;
}

/** Short random suffix that looks like a real replica hash. */
export function randHash(rng, n = 5) {
  const chars = "0123456789abcdefghijklmnopqrstuvwxyz";
  let s = "";
  for (let i = 0; i < n; i++) s += chars[Math.floor(rng() * chars.length)];
  return s;
}
