// The scheduling "brain": predicate checks that mirror the kube-scheduler
// filtering + scoring phases. Everything here is pure so it can be unit tested
// and reused by both the manual UI and the auto-scheduler.

import { sumRequests } from "./types.js";

/**
 * Does a set of tolerations tolerate a given taint?
 * A toleration matches when the key matches (or operator Exists with no key),
 * the value matches (or toleration has no value), and the effect matches
 * (or toleration has no effect).
 */
export function tolerationMatches(toleration, taint) {
  if (toleration.key && toleration.key !== taint.key) return false;
  if (toleration.effect && toleration.effect !== taint.effect) return false;
  if (toleration.operator === "Exists") return true;
  if (toleration.value === undefined) return true; // treat missing value as wildcard
  return toleration.value === taint.value;
}

export function tolerates(tolerations, taint) {
  return (tolerations || []).some((t) => tolerationMatches(t, taint));
}

/** Every key/value in selector must be present in labels. */
export function selectorMatches(selector, labels) {
  for (const [k, v] of Object.entries(selector || {})) {
    if (labels[k] !== v) return false;
  }
  return true;
}

/** Resources currently requested by the pods on a node. */
export function nodeAllocation(nodePods) {
  return sumRequests(nodePods);
}

/** Free capacity on a node given the pods running on it. */
export function freeResources(node, nodePods) {
  const used = nodeAllocation(nodePods);
  return {
    cpu: node.cpu - used.cpu,
    mem: node.mem - used.mem,
    gpu: node.gpu - used.gpu,
  };
}

/**
 * Run all scheduling predicates for placing `pod` on `node`, where `nodePods`
 * are the pods already running on that node.
 *
 * Returns { ok, reasons } where reasons is an array of short, k8s-flavored
 * strings describing every failing predicate (empty when ok === true).
 *
 * Set opts.ignoreState to evaluate as if the node were Ready (used by the
 * autoscaler when sizing brand-new nodes).
 */
export function evaluateFit(pod, node, nodePods, opts = {}) {
  const reasons = [];

  if (!opts.ignoreState && node.status !== "Ready") {
    if (node.status === "Provisioning") reasons.push("node is still provisioning");
    else if (node.status === "Cordoned") reasons.push("node is cordoned (unschedulable)");
    else if (node.status === "Draining") reasons.push("node is draining");
    else reasons.push(`node is ${node.status.toLowerCase()}`);
  }

  // Taints / tolerations (NoSchedule effect filters the node out).
  for (const taint of node.taints || []) {
    if (taint.effect === "NoSchedule" && !tolerates(pod.tolerations, taint)) {
      reasons.push(`untolerated taint {${taint.key}=${taint.value}}`);
    }
  }

  // Required node affinity / nodeSelector.
  for (const [k, v] of Object.entries(pod.nodeSelector || {})) {
    if (node.labels[k] !== v) {
      reasons.push(`didn't match selector {${k}=${v}}`);
    }
  }

  // Pod anti-affinity (hostname topology): no two same-app pods per node.
  if (pod.antiAffinity && nodePods.some((p) => p.app === pod.app)) {
    reasons.push(`anti-affinity: ${pod.app} already on node`);
  }

  // Resource fit (the filtering phase's NodeResourcesFit).
  const free = freeResources(node, nodePods);
  if (free.cpu < pod.cpu) {
    reasons.push(`insufficient cpu (free ${free.cpu}m, need ${pod.cpu}m)`);
  }
  if (free.mem < pod.mem) {
    reasons.push(`insufficient memory (free ${free.mem}Mi, need ${pod.mem}Mi)`);
  }
  if (pod.gpu > 0 && free.gpu < pod.gpu) {
    reasons.push(`insufficient nvidia.com/gpu (free ${free.gpu}, need ${pod.gpu})`);
  }

  return { ok: reasons.length === 0, reasons };
}

/**
 * Score a feasible node for a pod (the scoring phase). Higher is better.
 * Uses a MostAllocated/bin-packing strategy so the cluster stays tightly
 * packed, with a small bonus for honoring the pod's preferred zone.
 */
export function scoreNode(pod, node, nodePods) {
  const used = nodeAllocation(nodePods);
  const cpuFrac = (used.cpu + pod.cpu) / node.cpu;
  const memFrac = (used.mem + pod.mem) / node.mem;
  let score = ((cpuFrac + memFrac) / 2) * 100;

  if (pod.preferredZone && node.labels["topology.kubernetes.io/zone"] === pod.preferredZone) {
    score += 8;
  }
  // Soft (preferred) pod anti-affinity: discourage co-locating same-app replicas.
  if (pod.softAntiAffinity && nodePods.some((p) => p.app === pod.app)) {
    score -= 18;
  }
  // Gently prefer cheaper nodes when packing is otherwise equal.
  score -= node.cost * 2;
  // GPU nodes are scarce: avoid burning them on non-GPU pods.
  if (pod.gpu === 0 && node.gpu > 0) score -= 25;
  return score;
}

/**
 * Pick the best feasible node for a pod.
 * @param pod the pod to place
 * @param nodes array of node objects
 * @param podsByNode function(nodeId) -> pod[] running on that node
 * @returns { node, score } or null when nothing fits.
 */
export function bestNodeFor(pod, nodes, podsByNode) {
  let best = null;
  for (const node of nodes) {
    const nodePods = podsByNode(node.id);
    const fit = evaluateFit(pod, node, nodePods);
    if (!fit.ok) continue;
    const score = scoreNode(pod, node, nodePods);
    if (!best || score > best.score) best = { node, score };
  }
  return best;
}

/**
 * Aggregate the reasons across all nodes into the single most common blocker,
 * so the UI/event log can explain why a pod is stuck Pending.
 */
export function summarizePendingReason(pod, nodes, podsByNode) {
  if (nodes.length === 0) return "no nodes in cluster";
  const counts = new Map();
  for (const node of nodes) {
    const fit = evaluateFit(pod, node, podsByNode(node.id));
    for (const r of fit.reasons) {
      // Normalize the dynamic numbers out of resource messages for tallying.
      const key = r.replace(/\d+/g, "N");
      counts.set(key, (counts.get(key) || 0) + 1);
    }
  }
  let top = null;
  for (const [k, c] of counts) {
    if (!top || c > top.c) top = { k, c };
  }
  return top ? top.k : "unschedulable";
}
