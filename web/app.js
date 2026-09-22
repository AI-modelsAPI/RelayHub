const $ = (s, r = document) => r.querySelector(s);
const $$ = (s, r = document) => [...r.querySelectorAll(s)];

const state = {
  view: "pulse",
  channels: [],
  models: [],
  usage: [],
  checkin: [],
  overview: null,
  selected: {},
  summary: null,
  scores: [],
  identity: [],
  lab: [],
};

async function api(path) {
  const r = await fetch("/api/v1/" + path);
  if (!r.ok) throw new Error(path + " " + r.status);
  return r.json();
}

function setView(name) {
  state.view = name;
  $$(".view").forEach((v) => v.classList.toggle("on", v.id === "view-" + name));
  $$(".rail-btn").forEach((b) => b.classList.toggle("on", b.dataset.view === name));
  render();
}

$$(".rail-btn").forEach((b) => b.addEventListener("click", () => setView(b.dataset.view)));

function fmtUptime(s) {
  s = Number(s) || 0;
  if (s < 60) return s + "s";
  if (s < 3600) return Math.floor(s / 60) + "m";
  return Math.floor(s / 3600) + "h";
}

function rowHTML(id, title, meta, tag, extraClass = "") {
  // Every interpolated field is server-controlled data: id/tag/meta can carry
  // quotes and markup, so all of them must be escaped like title is (AUDIT RH-01).
  return `<li class="row ${extraClass}" data-id="${esc(id)}">
    <span class="n">${esc(title)}</span>
    <span class="t">${esc(tag || "")}</span>
    <span class="m">${esc(meta || "")}</span>
  </li>`;
}
function esc(s) {
  return String(s ?? "").replace(/[&<>"]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" }[c]));
}

function renderPulse() {
  const o = state.overview || {};
  const counts = o.counts || {};
  const sum = state.summary || {};
  const ratio = sum.cache_hit_ratio;
  $("#pulse-cache").textContent =
    ratio == null ? "—" : Math.round(Number(ratio) * 100) + "% cache";
  $("#pulse-saved").textContent = counts.channels
    ? `${counts.channels} 渠道 · ${fmtUptime(o.uptime_seconds)} 运行`
    : "等待网关心跳";
  $("#pulse-metrics").innerHTML = [
    ["渠道", counts.channels ?? "—"],
    ["模型", counts.models ?? "—"],
    ["路由", counts.routes ?? "—"],
    ["运行", fmtUptime(o.uptime_seconds)],
  ]
    .map(([k, v]) => `<div><span>${esc(k)}</span><b>${esc(v)}</b></div>`)
    .join("");
  const feed = (state.usage || []).slice(0, 12);
  $("#pulse-feed").innerHTML = feed.length
    ? feed
        .map((u) => {
          const t = (u.created_at || u.time || "").toString().slice(11, 19) || "--:--:--";
          return `<li><time>${esc(t)}</time><span>${esc(u.model || u.request_model || "request")}</span><span>${esc(
            u.channel_id || u.channel || ""
          )}</span></li>`;
        })
        .join("")
    : `<li><time>—</time><span>尚无请求流过网关</span><span></span></li>`;
  const trust = (state.scores || []).slice().sort((a, b) => (a.score || 0) - (b.score || 0)).slice(0, 5);
  $("#pulse-trust").innerHTML = trust.length
    ? trust.map((c) => `<li><span>${esc(c.channel_id)}</span><span class="badge">${esc(c.score)}</span></li>`).join("")
    : "<li><span>尚无信任分</span><span></span></li>";
  $("#topo-up").textContent = (counts.channels || 0) + " 上游";
}

function renderChannels() {
  const q = ($("#ch-q")?.value || "").toLowerCase();
  const list = state.channels.filter((c) => JSON.stringify(c).toLowerCase().includes(q));
  $("#ch-rows").innerHTML = list.length
    ? list.map((c) => rowHTML(c.id, c.name || c.id, c.base_url || c.provider_id || "", c.status || "")).join("")
    : `<li class="row"><span class="n">暂无渠道</span><span class="m">点右上角新建</span></li>`;
  $$("#ch-rows .row").forEach((el) =>
    el.addEventListener("click", () => {
      $$("#ch-rows .row").forEach((x) => x.classList.remove("on"));
      el.classList.add("on");
      const ch = state.channels.find((c) => c.id === el.dataset.id);
      if (ch) inspectChannel(ch);
    })
  );
}

function inspectChannel(ch) {
  $("#ch-ins").innerHTML = `
    <header class="col-h"><h2>Inspector</h2><span class="badge">${esc(ch.status || "")}</span></header>
    <div class="ins-block">
      <h3>渠道</h3>
      <dl class="kv">
        <dt>名称</dt><dd>${esc(ch.name || ch.id)}</dd>
        <dt>Provider</dt><dd>${esc(ch.provider_id || "—")}</dd>
        <dt>Base URL</dt><dd>${esc(ch.base_url || "—")}</dd>
        <dt>Proxy</dt><dd>${esc(ch.proxy_url || "未接线")}</dd>
        <dt>Tags</dt><dd>${esc((ch.tags || []).join(", ") || "—")}</dd>
      </dl>
      <h3>身份束</h3>
      <p class="lede">${esc((state.identity || []).find((b) => b.channel_id === ch.id)?.user_agent || "UA 未设")} · ${esc(ch.proxy_url || "直连")}</p>
      <h3>信任</h3>
      <p class="lede">${esc(JSON.stringify((state.scores || []).find((s) => s.channel_id === ch.id) || { score: 100 }))}</p>
    </div>`;
}

function renderModels() {
  $("#md-rows").innerHTML = state.models.length
    ? state.models.map((m) => rowHTML(m.id, m.id || m.name, `ctx ${m.context_window || "—"}`, m.protocol || "")).join("")
    : `<li class="row"><span class="n">暂无模型</span></li>`;
  $$("#md-rows .row").forEach((el) =>
    el.addEventListener("click", () => {
      const m = state.models.find((x) => x.id === el.dataset.id);
      if (!m) return;
      $("#md-ins").innerHTML = `<header class="col-h"><h2>${esc(m.id)}</h2></header>
        <div class="ins-block"><dl class="kv">
          <dt>工具</dt><dd>${esc(m.tool_call_support ?? "—")}</dd>
          <dt>视觉</dt><dd>${esc(m.vision_support ?? "—")}</dd>
          <dt>推理</dt><dd>${esc(m.reasoning_support ?? "—")}</dd>
        </dl></div>`;
    })
  );
}

function renderIdentity() {
  const bundles = state.identity || [];
  $("#id-rows").innerHTML = (bundles.length ? bundles : state.channels)
    .map((c) => rowHTML(c.channel_id || c.id, c.channel_id || c.name || c.id, c.proxy_url || "默认出口", c.egress_ip || "bundle"))
    .join("");
}

function renderCheckin() {
  const rows = state.checkin || [];
  $("#ck-rows").innerHTML = rows.length
    ? rows.map((j, i) => rowHTML(String(i), j.channel_id || j.provider_id || "job", j.message || j.status || "", j.status || "")).join("")
    : `<li class="row"><span class="n">暂无签到记录</span></li>`;
}

function renderLab() {
  const items = state.lab || [];
  $("#lab-rows").innerHTML = items.length
    ? items.map((c) => rowHTML(c.id, c.model || c.id, c.protocol || "", String(c.size || 0))).join("")
    : `<li class="row"><span class="n">实验室空闲</span><span class="m">默认关闭捕获；POST /api/v1/lab/capture 开启</span></li>`;
}

function renderUsage() {
  const rec = state.usage || [];
  $("#usage-metrics").innerHTML = [
    ["请求", rec.length],
    ["渠道", state.channels.length],
  ]
    .map(([k, v]) => `<div><span>${esc(k)}</span><b>${esc(v)}</b></div>`)
    .join("");
  $("#usage-rows").innerHTML = rec.length
    ? rec.map((u, i) => rowHTML(String(i), u.model || "req", u.channel_id || "", (u.latency_ms || "") + "ms")).join("")
    : `<li class="row"><span class="n">暂无用量</span></li>`;
}

function renderAgents() {
  $("#ag-rows").innerHTML = ["Claude Code", "Codex", "Hermes"]
    .map((n) => rowHTML(n, n, "clisync · MCP", "detect"))
    .join("");
}

function render() {
  const map = {
    pulse: renderPulse,
    channels: renderChannels,
    models: renderModels,
    identity: renderIdentity,
    checkin: renderCheckin,
    lab: renderLab,
    usage: renderUsage,
    agents: renderAgents,
  };
  map[state.view]?.();
}

async function boot() {
  try {
    const [ov, ch, md, us, ck, sm, sc, idn, lb] = await Promise.allSettled([
      api("overview"),
      api("channels"),
      api("models"),
      api("usage"),
      api("checkin"),
      api("usage/summary"),
      api("verify/scores"),
      api("identity"),
      api("lab"),
    ]);
    if (ov.status === "fulfilled") state.overview = ov.value;
    if (ch.status === "fulfilled") state.channels = ch.value.channels || ch.value.items || [];
    if (md.status === "fulfilled") state.models = md.value.models || md.value.catalog || [];
    if (us.status === "fulfilled") state.usage = us.value.records || us.value.items || us.value.usage || [];
    if (ck.status === "fulfilled") state.checkin = ck.value.jobs || ck.value.items || ck.value.records || [];
    if (sm.status === "fulfilled") state.summary = sm.value;
    if (sc.status === "fulfilled") state.scores = sc.value.scores || [];
    if (idn.status === "fulfilled") state.identity = idn.value.bundles || [];
    if (lb.status === "fulfilled") state.lab = lb.value.captures || [];
    $("#core-live").classList.remove("off");
  } catch {
    $("#core-live").classList.add("off");
  }
  $("#sb-gw").textContent = "gw :8789";
  $("#sb-cache").textContent = "cache —";
  $("#sb-trust").textContent = "trust —";
  render();
}

$("#ch-q")?.addEventListener("input", () => renderChannels());
$("#ck-now")?.addEventListener("click", async () => {
  await fetch("/api/v1/checkin", { method: "POST", headers: { "Content-Type": "application/json" }, body: "{}" }).catch(() => {});
  boot();
});

const palette = $("#palette");
const palQ = $("#palette-q");
const views = [
  ["pulse", "脉搏"],
  ["channels", "渠道"],
  ["models", "模型"],
  ["identity", "身份束"],
  ["checkin", "签到"],
  ["lab", "实验室"],
  ["usage", "用量"],
  ["agents", "Agent"],
  ["settings", "设置"],
];
function openPalette() {
  palette.hidden = false;
  palQ.value = "";
  drawPal("");
  palQ.focus();
}
function drawPal(q) {
  const items = views.filter(([, n]) => n.includes(q) || q === "");
  $("#palette-list").innerHTML = items.map(([id, n]) => `<li data-view="${id}">${n}</li>`).join("");
  $$("#palette-list li").forEach((li) =>
    li.addEventListener("click", () => {
      setView(li.dataset.view);
      palette.hidden = true;
    })
  );
}
$("#cmd-open").addEventListener("click", openPalette);
palQ.addEventListener("input", () => drawPal(palQ.value.trim()));
document.addEventListener("keydown", (e) => {
  if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "k") {
    e.preventDefault();
    openPalette();
  }
  if (e.key === "Escape") palette.hidden = true;
});

boot();
setInterval(boot, 15000);
