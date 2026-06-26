// DOM rendering and interaction. Reads from a Game instance and re-renders the
// node grid, pending queue, KPIs and log. Handles click-to-schedule, drag and
// drop, and the operator controls. No framework — just small render functions.

import { INSTANCE_TYPES, fmtCpu, fmtMem } from "./types.js";
import { SCENARIOS } from "./workload.js";
import { evaluateFit } from "./scheduler.js";

const $ = (sel) => document.querySelector(sel);

export class UI {
  constructor(game) {
    this.game = game;
    this.selectedPodId = null;
    this.dragPodId = null;
    this.dragging = false;
    this.dirty = true;
    this.els = {
      kpis: $("#kpis"),
      nodegrid: $("#nodegrid"),
      queuelist: $("#queuelist"),
      eventlog: $("#eventlog"),
      clock: $("#clock"),
      nodeSummary: $("#node-summary"),
      queueSummary: $("#queue-summary"),
      playpause: $("#playpause"),
      toast: $("#toast"),
    };
    this.populateSelects();
    this.wire();
  }

  markDirty() {
    this.dirty = true;
  }

  // --- one-time DOM setup --------------------------------------------------
  populateSelects() {
    const scenarioSel = $("#scenario");
    scenarioSel.innerHTML = Object.values(SCENARIOS)
      .map((s) => `<option value="${s.id}">${s.name}</option>`)
      .join("");
    scenarioSel.value = this.game.state.scenarioId;

    const nodeSel = $("#nodetype");
    nodeSel.innerHTML = Object.values(INSTANCE_TYPES)
      .map((t) => {
        const gpu = t.gpu ? `, ${t.gpu} GPU` : "";
        const price =
          t.family === "spot" ? `~$${(t.cost * 0.45).toFixed(2)}/hr spot` : `$${t.cost.toFixed(2)}/hr`;
        return `<option value="${t.key}">${t.key} — ${fmtCpu(t.cpu)} vCPU / ${fmtMem(
          t.mem
        )}${gpu} · ${price}</option>`;
      })
      .join("");
    nodeSel.value = "c5.2xlarge";
  }

  wire() {
    const g = this.game;

    $("#playpause").addEventListener("click", () => {
      g.state.paused = !g.state.paused;
      this.markDirty();
    });

    document.querySelectorAll(".speed-btn").forEach((b) => {
      b.addEventListener("click", () => {
        g.state.speed = Number(b.dataset.speed);
        this.markDirty();
      });
    });

    $("#scenario").addEventListener("change", (e) => {
      g.reset(e.target.value);
      this.selectedPodId = null;
      this.markDirty();
    });
    $("#reset").addEventListener("click", () => {
      g.reset();
      this.selectedPodId = null;
      this.markDirty();
    });

    $("#addnode").addEventListener("click", () => {
      const type = $("#nodetype").value;
      g.addNode(type);
      this.toast(`Provisioning a ${type}…`, "good");
      this.markDirty();
    });

    $("#autoschedule").addEventListener("change", (e) => {
      g.state.autoSchedule = e.target.checked;
      this.markDirty();
    });
    $("#autoscale").addEventListener("change", (e) => {
      g.state.autoScale = e.target.checked;
      this.markDirty();
    });

    $("#help").addEventListener("click", () => ($("#help-modal").hidden = false));
    $("#help-close").addEventListener("click", () => ($("#help-modal").hidden = true));
    $("#help-modal").addEventListener("click", (e) => {
      if (e.target.id === "help-modal") $("#help-modal").hidden = true;
    });

    // Node grid: schedule selected pod, or run node actions.
    this.els.nodegrid.addEventListener("click", (e) => this.onNodeClick(e));

    // Queue: select pod / auto-place.
    this.els.queuelist.addEventListener("click", (e) => this.onQueueClick(e));

    // Drag and drop.
    this.els.queuelist.addEventListener("dragstart", (e) => {
      const pod = e.target.closest(".pod");
      if (!pod) return;
      this.dragging = true;
      this.dragPodId = pod.dataset.podId;
      e.dataTransfer.effectAllowed = "move";
      e.dataTransfer.setData("text/plain", this.dragPodId);
    });
    this.els.queuelist.addEventListener("dragend", () => {
      this.dragging = false;
      this.dragPodId = null;
      this.clearDropClasses();
      this.markDirty();
    });
    this.els.nodegrid.addEventListener("dragover", (e) => this.onDragOver(e));
    this.els.nodegrid.addEventListener("dragleave", (e) => {
      const node = e.target.closest(".node");
      if (node) node.classList.remove("drop-ok", "drop-bad");
    });
    this.els.nodegrid.addEventListener("drop", (e) => this.onDrop(e));

    // keyboard: space = pause, esc = deselect/close help
    window.addEventListener("keydown", (e) => {
      if (e.code === "Space" && e.target.tagName !== "SELECT") {
        e.preventDefault();
        g.state.paused = !g.state.paused;
        this.markDirty();
      } else if (e.code === "Escape") {
        this.selectedPodId = null;
        $("#help-modal").hidden = true;
        this.markDirty();
      }
    });
  }

  // --- interaction handlers ------------------------------------------------
  onNodeClick(e) {
    const actionBtn = e.target.closest("[data-action]");
    if (actionBtn) {
      const id = actionBtn.dataset.node;
      const act = actionBtn.dataset.action;
      if (act === "cordon") this.game.cordon(id);
      else if (act === "drain") this.game.drain(id);
      else if (act === "delete") this.game.deleteNode(id);
      else if (act === "upgrade") {
        const res = this.game.upgradeNode(id);
        if (!res.ok) this.toast(`Can't upgrade: ${res.reason}`, "bad");
      }
      this.markDirty();
      return;
    }
    if (!this.selectedPodId) return;
    const card = e.target.closest(".node");
    if (!card) return;
    const res = this.game.schedulePod(this.selectedPodId, card.dataset.node);
    if (res.ok) {
      this.game.log("info", `Bound pod to ${card.dataset.node}.`);
      this.selectedPodId = null;
    } else {
      this.toast(res.reasons[0], "bad");
    }
    this.markDirty();
  }

  onQueueClick(e) {
    const auto = e.target.closest(".auto");
    if (auto) {
      const id = auto.closest(".pod").dataset.podId;
      const res = this.game.autoPlaceOne(id);
      this.toast(res.ok ? "Auto-placed on best node." : `Can't place: ${res.reasons[0]}`, res.ok ? "good" : "bad");
      this.markDirty();
      return;
    }
    const pod = e.target.closest(".pod");
    if (!pod) return;
    this.selectedPodId = this.selectedPodId === pod.dataset.podId ? null : pod.dataset.podId;
    this.markDirty();
  }

  onDragOver(e) {
    const card = e.target.closest(".node");
    if (!card || !this.dragPodId) return;
    e.preventDefault();
    const ok = this.canSchedule(this.dragPodId, card.dataset.node);
    card.classList.toggle("drop-ok", ok);
    card.classList.toggle("drop-bad", !ok);
  }

  onDrop(e) {
    const card = e.target.closest(".node");
    if (!card) return;
    e.preventDefault();
    const podId = this.dragPodId || e.dataTransfer.getData("text/plain");
    const res = this.game.schedulePod(podId, card.dataset.node);
    if (res.ok) {
      this.game.log("info", `Bound pod to ${card.dataset.node}.`);
      if (this.selectedPodId === podId) this.selectedPodId = null;
    } else {
      this.toast(res.reasons[0], "bad");
    }
    this.clearDropClasses();
    this.markDirty();
  }

  clearDropClasses() {
    this.els.nodegrid
      .querySelectorAll(".drop-ok, .drop-bad")
      .forEach((n) => n.classList.remove("drop-ok", "drop-bad"));
  }

  canSchedule(podId, nodeId) {
    const pod = this.game.podById(podId);
    const node = this.game.nodeById(nodeId);
    if (!pod || !node) return false;
    return evaluateFit(pod, node, this.game.podsOnNode(node)).ok;
  }

  toast(msg, level = "") {
    const el = this.els.toast;
    el.textContent = msg;
    el.className = `toast show ${level}`;
    clearTimeout(this._toastT);
    this._toastT = setTimeout(() => (el.className = "toast"), 2600);
  }

  // --- rendering -----------------------------------------------------------
  render() {
    if (this.dragging) return; // don't yank the DOM out from under a drag
    // Surface upgrade events even between dirty renders so they never get missed.
    const minor = this.game.state.clusterMinor;
    if (this._lastMinor != null && minor > this._lastMinor) {
      this.toast(`Control plane upgraded to v1.${minor} — responsibly restart every node!`, "warn");
      this.markDirty();
    }
    this._lastMinor = minor;
    if (!this.dirty) return;
    this.dirty = false;
    this.renderKpis();
    this.renderControls();
    this.renderNodes();
    this.renderQueue();
    this.renderLog();
  }

  renderControls() {
    const s = this.game.state;
    this.els.playpause.textContent = s.paused ? "▶ Start" : "⏸ Pause";
    this.els.playpause.classList.toggle("primary", s.paused);
    document.querySelectorAll(".speed-btn").forEach((b) => {
      b.classList.toggle("active", Number(b.dataset.speed) === s.speed);
    });
    $("#autoschedule").checked = s.autoSchedule;
    $("#autoscale").checked = s.autoScale;
    const secs = Math.floor(this.game.uptimeSeconds());
    this.els.clock.textContent = `${String(Math.floor(secs / 60)).padStart(2, "0")}:${String(
      secs % 60
    ).padStart(2, "0")}`;
  }

  renderKpis() {
    const g = this.game;
    const s = g.state;
    const util = g.clusterUtilization();
    const pending = s.pendingIds.length;
    const ready = g.schedulableNodes().length;
    const lat = g.avgLatencySeconds();
    const outdated = g.outdatedNodeCount();
    const spot = s.spotPrice;
    const tiles = [
      { l: "Score", v: Math.round(s.score), cls: "score" },
      { l: "Utilization", v: `${Math.round(util * 100)}%`, cls: util >= 0.5 ? "good" : util < 0.3 ? "warn" : "" },
      { l: "Avg latency", v: `${lat.toFixed(1)}s`, cls: lat > 6 ? "bad" : lat > 3 ? "warn" : "good" },
      { l: "Pending", v: pending, cls: pending > 8 ? "bad" : pending > 3 ? "warn" : "" },
      { l: "Running", v: g.runningCount(), cls: "" },
      { l: "Nodes", v: `${ready}/${s.nodes.length}`, cls: "" },
      { l: "Cost", v: `$${g.hourlyCost().toFixed(2)}/hr`, cls: "" },
      { l: "spot vs on-dmd", v: `${spot.toFixed(2)}×`, cls: spot <= 0.5 ? "good" : spot >= 0.75 ? "bad" : "" },
      {
        l: s.upgradePending ? `upgrade: ${outdated} left` : "k8s version",
        v: `v1.${s.clusterMinor}`,
        cls: s.upgradePending ? "warn" : "good",
      },
      { l: "SLA breaches", v: s.metrics.slaBreaches, cls: s.metrics.slaBreaches > 0 ? "bad" : "good" },
    ];
    this.els.kpis.innerHTML = tiles
      .map((t) => `<div class="kpi ${t.cls}"><div class="v">${t.v}</div><div class="l">${t.l}</div></div>`)
      .join("");
  }

  renderNodes() {
    const g = this.game;
    const selectedPod = this.selectedPodId ? g.podById(this.selectedPodId) : null;
    this.els.nodegrid.parentElement.classList.toggle("selecting", !!selectedPod);

    if (g.state.nodes.length === 0) {
      this.els.nodegrid.innerHTML = `<div class="empty-state">No nodes. Provision one with “+ Add node”.</div>`;
    } else {
      this.els.nodegrid.innerHTML = g.state.nodes
        .map((node) => this.nodeCard(node, selectedPod))
        .join("");
    }
    const ready = g.schedulableNodes().length;
    this.els.nodeSummary.textContent = `${ready} schedulable · ${g.state.nodes.length} total`;
  }

  nodeCard(node, selectedPod) {
    const g = this.game;
    const s = g.state;
    const pods = g.podsOnNode(node);
    const workload = pods.filter((p) => p.kind !== "daemon");
    const daemons = pods.filter((p) => p.kind === "daemon");
    // Draw daemonset overhead first so it sits at the base of each meter.
    const meterPods = [...daemons, ...workload];
    const used = pods.reduce(
      (a, p) => ({ cpu: a.cpu + p.cpu, mem: a.mem + p.mem, gpu: a.gpu + p.gpu }),
      { cpu: 0, mem: 0, gpu: 0 }
    );

    let feasClass = "";
    let why = "";
    if (selectedPod) {
      const fit = evaluateFit(selectedPod, node, pods);
      feasClass = fit.ok ? "feasible" : "infeasible";
      why = fit.ok ? "" : fit.reasons[0];
    }

    const outdated = node.minor < s.clusterMinor;
    const busy = ["Provisioning", "Upgrading", "Reclaiming"].includes(node.status);

    const labels = Object.entries(node.labels)
      .filter(([k]) => k !== "node.kubernetes.io/instance-type")
      .map(([k, v]) => `<span class="tag">${shortLabel(k)}=${v}</span>`)
      .join("");
    const taints = (node.taints || [])
      .map((t) => `<span class="tag taint">⛔ ${t.key}=${t.value}:${t.effect}</span>`)
      .join("");
    const spotTag = node.spot
      ? `<span class="tag spot">⚡ spot $${(node.cost * s.spotPrice).toFixed(2)}/hr · −${Math.round(
          (1 - s.spotPrice) * 100
        )}%</span>`
      : "";

    const cpuBar = this.meter("CPU", used.cpu, node.cpu, meterPods, "cpu");
    const memBar = this.meter("Mem", used.mem, node.mem, meterPods, "mem");
    const gpuLine = node.gpu
      ? `<div class="gpu-line">GPU <b>${used.gpu}/${node.gpu}</b> nvidia.com/gpu</div>`
      : "";

    const workChips = workload
      .map(
        (p) =>
          `<span class="podchip" style="background:${p.color}" title="${p.name} · ${fmtCpu(
            p.cpu
          )} vCPU / ${fmtMem(p.mem)}${p.gpu ? ` / ${p.gpu} GPU` : ""}">${p.app}</span>`
      )
      .join("");
    const daemonChips = daemons
      .map(
        (p) =>
          `<span class="podchip daemon" title="DaemonSet ${p.daemonOf} · ${fmtCpu(p.cpu)} vCPU / ${fmtMem(
            p.mem
          )}">⚙ ${p.daemonOf}</span>`
      )
      .join("");
    const podchips = workChips || daemonChips ? workChips + daemonChips : `<span class="podchip empty">empty</span>`;

    const booting = busy && node.status !== "Reclaiming"
      ? `<div class="muted">${node.status === "Upgrading" ? "upgrading" : "booting"}… ${
          node.provisioningTicksLeft
        } ticks left</div>`
      : "";
    const reclaim =
      node.status === "Reclaiming"
        ? `<div class="reclaim">⚠ spot reclaim in ${(node.spotWarnTicksLeft / 4).toFixed(1)}s — drain to save pods!</div>`
        : "";

    const cordonLabel = node.status === "Cordoned" ? "Uncordon" : "Cordon";
    const ver = `v1.${node.minor}`;
    const upgradeBtn =
      outdated && !busy
        ? `<button class="btn upgrade" data-action="upgrade" data-node="${node.id}" title="Drain & restart this node onto v1.${s.clusterMinor}">⤴ Upgrade to v1.${s.clusterMinor}</button>`
        : "";

    return `
    <div class="node ${feasClass} ${outdated ? "outdated" : ""}" data-node="${node.id}" data-status="${node.status}">
      <div class="node-top">
        <div>
          <div class="nname">${node.name}</div>
          <div class="ntype">${node.type} · <span class="ver ${outdated ? "stale" : ""}">${ver}</span></div>
        </div>
        <span class="badge ${node.status}">${node.status}</span>
      </div>
      <div class="chips">${labels}${spotTag}${taints}</div>
      ${reclaim}
      ${booting}
      ${cpuBar}
      ${memBar}
      ${gpuLine}
      <div class="podchips">${podchips}</div>
      <div class="why">✕ ${why}</div>
      ${upgradeBtn}
      <div class="node-actions">
        <button class="btn" data-action="cordon" data-node="${node.id}" ${
      node.status === "Provisioning" || node.status === "Upgrading" || node.status === "Reclaiming"
        ? "disabled"
        : ""
    }>${cordonLabel}</button>
        <button class="btn" data-action="drain" data-node="${node.id}" ${
      node.status === "Provisioning" || node.status === "Upgrading" ? "disabled" : ""
    }>Drain</button>
        <button class="btn danger" data-action="delete" data-node="${node.id}">Delete</button>
      </div>
    </div>`;
  }

  meter(label, used, cap, pods, key) {
    const pct = cap ? Math.min(100, (used / cap) * 100) : 0;
    const over = used > cap;
    const segs = pods
      .map((p) => {
        const w = cap ? (p[key] / cap) * 100 : 0;
        if (w <= 0) return "";
        return `<div class="seg" style="width:${w}%;background:${p.color}" title="${p.app}"></div>`;
      })
      .join("");
    const usedTxt = key === "cpu" ? `${fmtCpu(used)}/${fmtCpu(cap)} vCPU` : `${fmtMem(used)}/${fmtMem(cap)}`;
    return `
      <div class="meter ${over ? "over" : ""}">
        <div class="meter-top"><span>${label}</span><span>${usedTxt} · ${Math.round(pct)}%</span></div>
        <div class="bar">${segs}</div>
      </div>`;
  }

  renderQueue() {
    const g = this.game;
    const pods = g.pendingPods();
    // priority desc, then oldest first — the order the scheduler would consider.
    pods.sort((a, b) => b.priority - a.priority || a.arrivalTick - b.arrivalTick);
    this.els.queueSummary.textContent = `${pods.length} waiting`;
    if (pods.length === 0) {
      this.els.queuelist.innerHTML = `<div class="empty-state">Queue empty — every pod is scheduled. Nice.</div>`;
      return;
    }
    this.els.queuelist.innerHTML = pods.map((p) => this.podCard(p)).join("");
  }

  podCard(p) {
    const selected = p.id === this.selectedPodId ? "selected" : "";
    const breached = p.slaBreached ? "breached" : "";
    const constraints = [];
    for (const [k, v] of Object.entries(p.nodeSelector)) {
      constraints.push(`<span class="pill">🔖 ${shortLabel(k)}=${v}</span>`);
    }
    for (const t of p.tolerations) {
      constraints.push(`<span class="pill">tol ${t.key}</span>`);
    }
    if (p.gpu) constraints.push(`<span class="pill">🖥 ${p.gpu} GPU</span>`);
    if (p.antiAffinity) constraints.push(`<span class="pill">⇄ anti-affinity</span>`);
    else if (p.softAntiAffinity) constraints.push(`<span class="pill">⇄ spread</span>`);
    constraints.push(
      `<span class="pill ${p.kind === "job" ? "kind-job" : ""}">${p.kind}</span>`
    );

    const waitS = (p.pendingTicks / 4).toFixed(1);
    return `
    <div class="pod ${selected} ${breached}" data-pod-id="${p.id}" draggable="true">
      <div class="stripe" style="background:${p.color}"></div>
      <div class="pmain">
        <div class="pname">${p.name}</div>
        <div class="preq">${fmtCpu(p.cpu)} vCPU · ${fmtMem(p.mem)}</div>
        <div class="pconstraints">${constraints.join("")}</div>
      </div>
      <div class="pside">
        <span class="wait">${waitS}s</span>
        <button class="auto" title="Auto-place on best node">⚡</button>
      </div>
    </div>`;
  }

  renderLog() {
    const ev = this.game.state.events.slice(-60);
    this.els.eventlog.innerHTML = ev
      .map((e) => {
        const secs = Math.floor(e.tick / 4);
        const t = `${String(Math.floor(secs / 60)).padStart(2, "0")}:${String(secs % 60).padStart(2, "0")}`;
        return `<div class="logline ${e.level}"><span class="t">${t}</span>${escapeHtml(e.msg)}</div>`;
      })
      .join("");
  }
}

function shortLabel(k) {
  // Trim long k8s label keys for display, keep the last path segment.
  if (k.includes("/")) return k.split("/").pop();
  if (k.startsWith("topology.kubernetes.io/")) return k.split("/").pop();
  return k;
}

function escapeHtml(s) {
  return String(s).replace(/[&<>]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;" }[c]));
}
