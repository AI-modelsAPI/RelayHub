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
  labEnabled: false,
  agents: [],
  agentRecords: [],
  settings: null,
  online: false,
};

// Management authentication (AUDIT 2026-09-24 F4). The API requires a token
// by default. The console gets it by redeeming a one-time pairing code — from
// the startup log, `relayhub pair` or the desktop app — that arrives in the
// URL fragment (#pair=…, never sent to a server), or through the auth gate
// below. The token is kept in this origin's localStorage only.
const TOKEN_KEY = "relayhub.managementToken";
function withAuth(init) {
  const token = localStorage.getItem(TOKEN_KEY);
  if (!token) return init || {};
  const next = Object.assign({}, init || {});
  next.headers = Object.assign({}, (init && init.headers) || {}, { Authorization: "Bearer " + token });
  return next;
}

function pairingCodeFrom(text) {
  const m = /#pair=([A-Za-z0-9_-]{8,128})\s*$/.exec(text || "");
  return m ? m[1] : null;
}

async function redeemPairingCode(code) {
  const r = await fetch("/api/v1/auth/pair", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ code }),
  });
  if (!r.ok) return false;
  const body = await r.json().catch(() => null);
  if (!body || typeof body.token !== "string" || !body.token) return false;
  localStorage.setItem(TOKEN_KEY, body.token);
  return true;
}

// Drop the code from the address bar and history first, then redeem it once.
const pairingReady = (async () => {
  const code = pairingCodeFrom(location.hash);
  if (!code) return;
  history.replaceState(null, "", location.pathname + location.search);
  try {
    await redeemPairingCode(code);
  } catch (_) {
    /* the auth gate takes over on the first 401 */
  }
})();

// One gate for every request that hits 401, however many are in flight. It
// is an in-page form rather than window.prompt, which WKWebView (the desktop
// shell) does not implement.
let authGate = null;
function askForCredential() {
  if (authGate) return authGate;
  authGate = new Promise((resolve) => {
    const form = $("#auth-gate");
    const input = $("#auth-input");
    const err = $("#auth-error");
    const submit = $("#auth-submit");
    form.hidden = false;
    err.hidden = true;
    input.value = "";
    input.focus();
    form.onsubmit = async (e) => {
      e.preventDefault();
      const text = input.value.trim();
      if (!text) return;
      submit.disabled = true;
      let ok = false;
      try {
        const link = pairingCodeFrom(text);
        const code = link || (/^[A-Za-z0-9_-]{32}$/.test(text) ? text : null);
        if (code) ok = await redeemPairingCode(code);
        if (!ok && !link) {
          // Not a (live) pairing code: try it as the token itself.
          const probe = await fetch("/api/v1/settings", { headers: { Authorization: "Bearer " + text } });
          if (probe.ok) {
            localStorage.setItem(TOKEN_KEY, text);
            ok = true;
          }
        }
      } catch (_) {
        ok = false;
      }
      submit.disabled = false;
      if (!ok) {
        err.textContent = "无效或已过期。运行 relayhub pair 获取新的配对链接，或粘贴 management.token 中的令牌。";
        err.hidden = false;
        return;
      }
      form.hidden = true;
      form.onsubmit = null;
      authGate = null;
      resolve();
    };
  });
  return authGate;
}

async function authFetch(url, init) {
  await pairingReady;
  const sent = localStorage.getItem(TOKEN_KEY);
  let r = await fetch(url, withAuth(init));
  if (r.status === 401) {
    // Only a rejection of the current token opens the gate; a request that
    // raced a fresh sign-in just retries with the new token.
    if (localStorage.getItem(TOKEN_KEY) === sent) {
      localStorage.removeItem(TOKEN_KEY);
      await askForCredential();
    }
    r = await fetch(url, withAuth(init));
  }
  return r;
}

async function api(path, init) {
  const r = await authFetch("/api/v1/" + path, init);
  if (!r.ok) throw new Error(path + " " + r.status);
  return r.json();
}

const JSON_POST = (body) => ({ method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) });

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
    : state.online
      ? "尚未配置渠道"
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
          return `<li><time>${esc(t)}</time><span>${esc(u.model_id || u.model || u.request_model || "request")}</span><span>${esc(
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
    : `<li class="row"><span class="n">暂无渠道</span><span class="m">通过 POST /api/v1/channels 创建</span></li>`;
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
    : `<li class="row"><span class="n">暂无模型</span><span class="m">通过 POST /api/v1/models 创建</span></li>`;
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
    ? rows
        .map((j, i) =>
          rowHTML(j.id || String(i), j.channel_id || j.provider_id || "job", j.reward || j.error_message || j.message || "", j.status || "")
        )
        .join("")
    : `<li class="row"><span class="n">暂无签到记录</span></li>`;
  $$("#ck-rows .row").forEach((el) =>
    el.addEventListener("click", () => {
      const j = rows.find((r) => (r.id || "") === el.dataset.id);
      if (!j) return;
      $("#ck-ins").innerHTML = `<header class="col-h"><h2>${esc(j.channel_id)}</h2><span class="badge ${j.status === "success" ? "" : "warn"}">${esc(j.status)}</span></header>
        <div class="ins-block"><dl class="kv">
          <dt>奖励</dt><dd>${esc(j.reward || "—")}</dd>
          <dt>错误</dt><dd>${esc(j.error_code ? j.error_code + ": " + (j.error_message || "") : "—")}</dd>
          <dt>开始</dt><dd>${esc(String(j.started_at || "").slice(0, 19))}</dd>
          <dt>结束</dt><dd>${esc(String(j.finished_at || "").slice(0, 19) || "—")}</dd>
        </dl></div>`;
    })
  );
}

function renderLab() {
  const items = state.lab || [];
  const btn = $("#lab-capture");
  if (btn) btn.textContent = state.labEnabled ? "停止捕获" : "开始捕获";
  $("#lab-rows").innerHTML = items.length
    ? items.map((c) => rowHTML(c.id, c.model || c.id, c.protocol || "", (c.size || 0) + (c.truncated ? " B (截断)" : " B"))).join("")
    : `<li class="row"><span class="n">实验室空闲</span><span class="m">${state.labEnabled ? "捕获已开启，等待网关请求" : "捕获默认关闭"}</span></li>`;
  $$("#lab-rows .row").forEach((el) =>
    el.addEventListener("click", () => {
      const c = items.find((x) => x.id === el.dataset.id);
      if (!c) return;
      $("#lab-ins").innerHTML = `<header class="col-h"><h2>${esc(c.id)}</h2></header>
        <div class="ins-block"><dl class="kv">
          <dt>模型</dt><dd>${esc(c.model || "—")}</dd>
          <dt>协议</dt><dd>${esc(c.protocol || "—")}</dd>
          <dt>渠道</dt><dd>${esc(c.channel_id || "—")}</dd>
          <dt>大小</dt><dd>${esc(c.size || 0)} B${c.truncated ? "（已截断到 256 KiB）" : ""}</dd>
        </dl><p class="lede">回放 / 主动探针 / 对冲尚未实现（POST /api/v1/lab/replay 返回 501）。</p></div>`;
    })
  );
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
    ? rec
        .map((u, i) => rowHTML(u.request_id || String(i), u.model_id || u.model || "req", u.channel_id || "", (u.latency_ms || "") + "ms"))
        .join("")
    : `<li class="row"><span class="n">暂无用量</span></li>`;
  $$("#usage-rows .row").forEach((el) => el.addEventListener("click", () => explainRequest(el.dataset.id)));
}

async function explainRequest(requestID) {
  const ins = $("#usage-ins");
  try {
    const res = await api("routes/explain?request_id=" + encodeURIComponent(requestID));
    const r = res.record;
    if (!r) {
      ins.innerHTML = `<div class="empty">没有找到请求 ${esc(requestID)} 的记录。</div>`;
      return;
    }
    ins.innerHTML = `<header class="col-h"><h2>${esc(r.request_id)}</h2><span class="badge ${r.status_code >= 200 && r.status_code < 300 ? "" : "bad"}">${esc(r.status_code)}</span></header>
      <div class="ins-block"><dl class="kv">
        <dt>协议</dt><dd>${esc(r.protocol || "—")}</dd>
        <dt>模型</dt><dd>${esc(r.model_id || "—")} → ${esc(r.upstream_model || "—")}</dd>
        <dt>渠道</dt><dd>${esc(r.channel_id || "—")}</dd>
        <dt>延迟</dt><dd>${esc(r.latency_ms)} ms · TTFT ${esc(r.ttft_ms || 0)} ms</dd>
        <dt>Token</dt><dd>in ${esc(r.input_tokens)} · out ${esc(r.output_tokens)} · cache ${esc(r.cache_read_tokens || 0)}</dd>
        <dt>结束</dt><dd>${esc(r.finish_reason || "—")}${r.error_class ? " · " + esc(r.error_class) : ""}</dd>
      </dl></div>`;
  } catch (e) {
    ins.innerHTML = `<div class="empty">路由解释不可用：${esc(e.message)}</div>`;
  }
}

const AGENT_LABELS = { claude: "Claude Code", codex: "Codex", hermes: "Hermes" };
const agentLabel = (cli) => AGENT_LABELS[cli] || cli;

function renderAgents() {
  // Targets come from GET /api/v1/cli-sync (detect); a hardcoded list used to
  // be shown here regardless of what the backend knew (AUDIT 2.3).
  const targets = state.agents || [];
  $("#ag-rows").innerHTML = targets.length
    ? targets.map((t) => rowHTML(t.cli, agentLabel(t.cli), t.config_path || "", t.exists ? "detected" : "absent")).join("")
    : `<li class="row"><span class="n">${state.online ? "CLI 同步服务未配置" : "Core 离线"}</span></li>`;
  $$("#ag-rows .row").forEach((el) => el.addEventListener("click", () => inspectAgent(el.dataset.id)));
}

async function inspectAgent(cli) {
  const ins = $("#ag-ins");
  ins.innerHTML = `<header class="col-h"><h2>${esc(agentLabel(cli))}</h2></header><div class="ins-block"><p class="lede">正在计算 diff…</p></div>`;
  try {
    // Preview is read-only and mints no key; the real key is issued on apply.
    const res = await api("cli-sync", JSON_POST({ cli, action: "preview", desired: {} }));
    const d = res.diff || {};
    const recs = (state.agentRecords || []).filter((r) => r.cli === cli).slice(0, 5);
    ins.innerHTML = `<header class="col-h"><h2>${esc(agentLabel(cli))}</h2><span class="badge ${d.has_changes ? "warn" : ""}">${d.has_changes ? "有差异" : "已同步"}</span></header>
      <div class="ins-block">
        <h3>目标</h3>
        <dl class="kv">
          <dt>配置</dt><dd>${esc(d.target?.config_path || "—")}</dd>
          <dt>变更键</dt><dd>${esc((d.changed_keys || []).join(", ") || "—")}</dd>
          <dt>密钥</dt><dd>apply 时铸造真实本地 key</dd>
        </dl>
        <h3>预览</h3>
        <pre class="diff">${esc(d.new_content || "")}</pre>
        <h3>历史</h3>
        <ul class="quiet-list">${
          recs.length
            ? recs.map((r) => `<li><span>${esc(r.status)}</span><span>${esc(String(r.created_at || "").slice(0, 19))}</span></li>`).join("")
            : "<li><span>尚无同步记录</span><span></span></li>"
        }</ul>
      </div>`;
  } catch (e) {
    ins.innerHTML = `<header class="col-h"><h2>${esc(agentLabel(cli))}</h2><span class="badge bad">失败</span></header><div class="ins-block"><p class="lede">${esc(e.message)}</p></div>`;
  }
}

function renderSettings() {
  const st = state.settings || {};
  const b = st.browser || {};
  $("#set-gw").textContent = st.gateway_address || "127.0.0.1:8789";
  $("#set-mgmt").textContent = st.management_address || "—";
  $("#set-browser").textContent = b.available ? b.version || b.path || "已检测" : "未检测到 Chromium";
  $("#set-token").textContent = st.token_configured ? "已配置" : "未配置（仅回环信任）";
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
    settings: renderSettings,
  };
  map[state.view]?.();
}

async function boot() {
  const [ov, ch, md, us, ck, sm, sc, idn, lb, ag, st] = await Promise.allSettled([
    api("overview"),
    api("channels"),
    api("models"),
    api("usage"),
    api("checkin"),
    api("usage/summary"),
    api("verify/scores"),
    api("identity"),
    api("lab"),
    api("cli-sync"),
    api("settings"),
  ]);
  const ok = (r) => r.status === "fulfilled";
  if (ok(ov)) state.overview = ov.value;
  if (ok(ch)) state.channels = ch.value.channels || ch.value.items || [];
  if (ok(md)) state.models = md.value.models || md.value.catalog || [];
  if (ok(us)) state.usage = us.value.records || us.value.items || us.value.usage || [];
  if (ok(ck)) state.checkin = ck.value.records || ck.value.jobs || ck.value.items || [];
  if (ok(sm)) state.summary = sm.value;
  if (ok(sc)) state.scores = sc.value.scores || [];
  if (ok(idn)) state.identity = idn.value.bundles || [];
  if (ok(lb)) {
    state.lab = lb.value.captures || [];
    state.labEnabled = !!lb.value.enabled;
  }
  if (ok(ag)) {
    state.agents = ag.value.targets || [];
    state.agentRecords = ag.value.records || [];
  }
  if (ok(st)) state.settings = st.value;

  // The Core indicator follows the overview heartbeat. Previously every fetch
  // could fail and the dot stayed green because the catch never ran (RH-29).
  state.online = ok(ov);
  const live = $("#core-live").classList;
  if (state.online) live.remove("off");
  else live.add("off");

  const ratio = state.summary?.cache_hit_ratio;
  $("#sb-cache").textContent = state.online && ratio != null ? "cache " + Math.round(Number(ratio) * 100) + "%" : "cache —";
  const worst = (state.scores || []).reduce((m, s) => (m == null || Number(s.score) < m ? Number(s.score) : m), null);
  $("#sb-trust").textContent = worst == null ? "trust —" : "trust min " + worst;
  $("#sb-gw").textContent = "gw " + (state.settings?.gateway_address || ":8789");
  $("#pulse-clock").textContent = state.online ? "更新 " + new Date().toLocaleTimeString("zh-CN", { hour12: false }) : "离线";
  render();
}

$("#ch-q")?.addEventListener("input", () => renderChannels());
$("#ck-now")?.addEventListener("click", async () => {
  await authFetch("/api/v1/checkin", { method: "POST", headers: { "Content-Type": "application/json" }, body: "{}" }).catch(() => {});
  boot();
});
$("#lab-capture")?.addEventListener("click", async () => {
  try {
    const r = await api("lab/capture", JSON_POST({ enabled: !state.labEnabled }));
    state.labEnabled = !!r.enabled;
  } catch {}
  boot();
});
$("#lab-clear")?.addEventListener("click", async () => {
  await authFetch("/api/v1/lab/capture", { method: "DELETE" }).catch(() => {});
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
