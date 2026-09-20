// RelayHub — Desktop UI with slide-in channel editor (AxonHub-style interaction)

const I18N = {
  zh: {
    select_visible_channels:"全选当前筛选结果",select_channel:"选择渠道",selected_channels:"已选渠道",
    nav_overview:"概览",nav_channels:"渠道",nav_models:"模型",nav_checkin:"签到",nav_usage:"用量",nav_cli:"CLI 同步",nav_logs:"日志",nav_settings:"设置",
    core_running:"服务运行中",core_stopped:"服务已停止",sb_restart:"重启服务",
    btn_refresh:"刷新",btn_save:"保存",btn_back:"← 返回",btn_test:"测试",btn_delete:"删除",btn_edit:"编辑",btn_sync_now:"同步模型",
    btn_add_key:"添加",btn_enable:"启用",btn_disable:"停用",f_keys:"API Key 管理",f_key_label:"备注",
    f_keys_mgmt_hint:"多 Key 轮询；停用的 Key 不参与调用",msg_no_keys:"暂无 Key",msg_key_required:"请输入 Key",
    f_icon:"图标 URL",f_archived_model:"归档（隐藏）",btn_details:"详情",
    st_health:"健康",st_quota:"额度",st_keys:"启用Key",st_recent:"近期请求",st_ok:"成功",st_avg_latency:"平均延迟",
    set_access:"接入信息（把它填进你的 AI 客户端）",set_gw_base:"Base URL",set_gw_keys:"访问密钥",
    btn_copy:"复制",btn_new_key:"生成新密钥",gw_key_hint:"密钥只在生成时完整显示一次，请立即复制保存",
    gw_no_keys:"暂无密钥，点「生成新密钥」创建",gw_new_key_once:"新密钥（仅显示这一次，请复制保存）：",
    toast_copied:"已复制",toast_key_created:"密钥已生成",btn_duplicate:"复制",
    bulk_enable:"批量启用",bulk_disable:"批量停用",bulk_archive:"批量归档",bulk_delete:"批量删除",
    confirm_bulk:"确认对选中渠道执行",bulk_none:"请先勾选渠道",
    act_checkin_all:"立即全站签到",act_add_channel:"添加渠道",act_add_model:"新建模型",
    card_channels:"活跃渠道",card_models:"模型数",card_uptime:"运行时长",
    th_name:"名称",th_type:"类型",th_status:"状态",th_models:"模型",th_tags:"标签",th_health:"健康",th_actions:"操作",
    msg_no_channels:"暂无渠道，点击「添加渠道」开始",msg_select_model:"选择左侧模型查看绑定详情",msg_no_models:"暂无模型",
    f_all_status:"全部",f_enabled:"启用",f_disabled:"停用",f_archived:"已归档",
    new_channel:"新建渠道",edit_channel:"编辑渠道",new_model:"新建模型",
    f_name:"名称（选择站点自动填充地址）",f_baseurl:"Base URL",f_keys:"API Keys（每行一个，自动拆多渠道）",
    f_tags:"标签（按用途分类，如: 生产环境/备用/低价）",f_priority:"优先级（数字越小越优先调用）",f_weight:"权重（同优先级时按比例分流）",
    f_enabled:"启用",f_checkin:"自动签到",f_models:"支持模型（逗号分隔）",f_mapping:"模型映射（请求名 → 上游名）",
    f_display:"显示名称",f_tools:"工具调用",f_vision:"视觉",f_reasoning:"推理",f_ctx:"上下文长度",f_bindings:"渠道绑定",
    f_developer:"开发者",f_model_type:"类型",f_input_price:"输入价($/百万)",f_output_price:"输出价($/百万)",
    f_test_model:"测试模型",f_remark:"备注",f_stream_policy:"流式策略",f_status:"渠道状态",f_auto_sync:"自动同步模型",f_last_error:"最近错误",
    f_proxy:"代理（自动检测本机代理）",
    col_models:"个模型",col_bound:"个渠道",toast_saved:"已保存",toast_deleted:"已删除",
    cli_loading:"正在检测…",cli_history:"同步历史",cli_no_history:"暂无同步记录",th_time:"时间",th_path:"配置路径",
    btn_cancel:"取消",diff_before:"当前内容",diff_after:"同步后内容",diff_confirm:"确认写入",
    io_title:"配置导入 / 导出",io_password:"密码（导出/导入凭据时必填）",io_policy:"冲突策略",io_skip:"跳过已存在",io_overwrite:"覆盖已存在",
    io_include:"包含凭据（加密存放于导出文件中）",io_package:"配置包（导出后显示在此；导入时粘贴到此）",
    io_btn_export:"导出配置",io_btn_preview:"预览导入",io_btn_apply:"确认导入",
    toast_test_ok:"连通正常",toast_test_fail:"获取失败",
    confirm_delete:"确认删除",
    upstream_name:"上游模型名",fetch_models:"获取模型列表",
    set_general:"通用",set_language:"界面语言",set_network:"服务端口",
    set_browser:"浏览器运行时 (签到/Turnstile)",set_browser_status:"状态",set_browser_path:"浏览器路径",set_browser_version:"版本",set_browser_tier:"运行时级别",
    browser_available:"已检测到 (可用)",browser_unavailable:"未检测到 (需人工签到)",
    topo_title:"实时请求流转",node_client:"客户端",node_upstreams:"上游站点",
    // Provider types (AxonHub-style)
    type_openai:"OpenAI 兼容",type_anthropic:"Anthropic 兼容",type_gemini:"Gemini",type_deepseek:"DeepSeek",type_zhipu:"智谱",type_moonshot:"Moonshot",
    type_openrouter:"OpenRouter",type_ollama:"Ollama (本地)",type_custom:"自定义中转",
    // Built-in sites (fill base_url on select)
    site_agentrouter:"AgentRouter (签到站)",site_justdowork:"JustDoWork (签到站)",site_gorouter:"GoRouter (签到站)",site_seekai:"SeekAI (签到站)",site_kktoken:"KKtoken AI (签到站)",f_site:"站点",f_site_hint:"选择签到站自动填地址",f_type_hint:"决定请求格式",f_url_hint:"缺 /v1 自动补全",f_keys_hint:"每行一个 Key，多 Key 轮询使用",f_models_hint:"逗号分隔或从上游拉取",f_mapping_hint:"请求名→上游名",f_proxy_hint:"请求经此代理发出",f_priority_hint:"数字越小越优先调用",f_weight_hint:"同优先级按比例分流",f_test_model_hint:"用于测试连接的默认模型",f_tags_hint:"添加标签来分类和过滤渠道",f_tags_placeholder:"添加标签…",
    // Stream policy & status
    stream_unlimited:"允许流式",stream_disabled:"禁止流式",
    status_enabled:"启用",status_disabled:"停用",status_archived:"已归档"
  },
  en: {
    select_visible_channels:"Select all filtered channels",select_channel:"Select channel",selected_channels:"Selected channels",
    nav_overview:"Overview",nav_channels:"Channels",nav_models:"Models",nav_checkin:"Check-in",nav_usage:"Usage",nav_cli:"CLI Sync",nav_logs:"Logs",nav_settings:"Settings",
    core_running:"Service Running",core_stopped:"Service Stopped",sb_restart:"Restart Service",
    btn_refresh:"Refresh",btn_save:"Save",btn_back:"← Back",btn_test:"Test",btn_delete:"Delete",btn_edit:"Edit",btn_sync_now:"Sync Models",
    btn_add_key:"Add",btn_enable:"Enable",btn_disable:"Disable",f_keys:"API Key Management",f_key_label:"Label",
    f_keys_mgmt_hint:"Multi-key rotation; disabled keys are skipped",msg_no_keys:"No keys yet",msg_key_required:"Enter a key",
    f_icon:"Icon URL",f_archived_model:"Archive (hide)",btn_details:"Details",
    st_health:"Health",st_quota:"Quota",st_keys:"Enabled keys",st_recent:"Recent requests",st_ok:"ok",st_avg_latency:"Avg latency",
    set_access:"Access info (put this into your AI client)",set_gw_base:"Base URL",set_gw_keys:"Access keys",
    btn_copy:"Copy",btn_new_key:"New key",gw_key_hint:"The key is shown in full only once — copy it now",
    gw_no_keys:"No keys yet. Click 'New key' to create one",gw_new_key_once:"New key (shown only once — copy it now):",
    toast_copied:"Copied",toast_key_created:"Key created",btn_duplicate:"Duplicate",
    bulk_enable:"Enable",bulk_disable:"Disable",bulk_archive:"Archive",bulk_delete:"Delete",
    confirm_bulk:"Confirm bulk action on selected channels",bulk_none:"Select channels first",
    act_checkin_all:"Run Check-ins",act_add_channel:"Add Channel",act_add_model:"New Model",
    card_channels:"Active Channels",card_models:"Models",card_uptime:"Uptime",
    th_name:"Name",th_type:"Type",th_status:"Status",th_models:"Models",th_tags:"Tags",th_health:"Health",th_actions:"Actions",
    msg_no_channels:"No channels yet. Click 'Add Channel'",msg_select_model:"Select a model",msg_no_models:"No models",
    f_all_status:"All",f_enabled:"Enabled",f_disabled:"Disabled",f_archived:"Archived",
    new_channel:"New Channel",edit_channel:"Edit Channel",new_model:"New Model",
    f_name:"Name (select a site to auto-fill URL)",f_baseurl:"Base URL",f_keys:"API Keys (one per line, splits into channels)",
    f_tags:"Tags (categorize by purpose, e.g. prod/backup/cheap)",f_priority:"Priority (lower number = called first)",f_weight:"Weight (traffic ratio within same priority)",
    f_enabled:"Enabled",f_checkin:"Auto check-in",f_models:"Supported models",f_mapping:"Model mapping (request → upstream)",
    f_display:"Display name",f_tools:"Tools",f_vision:"Vision",f_reasoning:"Reasoning",f_ctx:"Context window",f_bindings:"Channel bindings",
    f_developer:"Developer",f_model_type:"Type",f_input_price:"Input $/1M",f_output_price:"Output $/1M",
    f_test_model:"Test model",f_remark:"Remark",f_stream_policy:"Stream policy",f_status:"Channel status",f_auto_sync:"Auto sync models",f_last_error:"Last error",
    f_proxy:"Proxy (auto-detected local proxies)",f_add_key:"Show/Hide",
    col_models:"models",col_bound:"channels",toast_saved:"Saved",toast_deleted:"Deleted",
    cli_loading:"Detecting…",cli_history:"Sync history",cli_no_history:"No sync records yet",th_time:"Time",th_path:"Config path",
    btn_cancel:"Cancel",diff_before:"Current content",diff_after:"After sync",diff_confirm:"Confirm write",
    io_title:"Import / Export",io_password:"Password (required for credentials)",io_policy:"Conflict policy",io_skip:"Skip existing",io_overwrite:"Overwrite existing",
    io_include:"Include credentials (encrypted inside the package)",io_package:"Package (filled on export; paste here to import)",
    io_btn_export:"Export",io_btn_preview:"Preview import",io_btn_apply:"Confirm import",
    toast_test_ok:"Connected",toast_test_fail:"Fetch failed",
    confirm_delete:"Confirm delete",
    upstream_name:"Upstream model",fetch_models:"Fetch Models",
    set_general:"General",set_language:"Language",set_network:"Service Ports",
    set_browser:"Browser Runtime (Check-in / Turnstile)",set_browser_status:"Status",set_browser_path:"Browser Path",set_browser_version:"Version",set_browser_tier:"Runtime Tier",
    browser_available:"Detected (Available)",browser_unavailable:"Not Detected (Manual Required)",
    topo_title:"Live Request Flow",node_client:"Client",node_upstreams:"Upstreams",
    type_openai:"OpenAI Compatible",type_anthropic:"Anthropic Compatible",type_gemini:"Gemini",type_deepseek:"DeepSeek",type_zhipu:"Zhipu",type_moonshot:"Moonshot",
    type_openrouter:"OpenRouter",type_ollama:"Ollama (Local)",type_custom:"Custom Relay",
    site_agentrouter:"AgentRouter (Check-in)",site_justdowork:"JustDoWork (Check-in)",site_gorouter:"GoRouter (Check-in)",site_seekai:"SeekAI (Check-in)",site_kktoken:"KKtoken AI (Check-in)",f_site:"Site",f_site_hint:"Auto-fills URL on select",f_type_hint:"Request format",f_url_hint:"Auto-append /v1",f_keys_hint:"One key per line, used in rotation",f_models_hint:"Comma sep or fetch",f_mapping_hint:"Request→upstream name",f_proxy_hint:"Requests via this proxy",f_priority_hint:"Lower = called first",f_weight_hint:"Ratio in same priority",f_test_model_hint:"Default model for testing",f_tags_hint:"Add tags to categorize and filter channels",f_tags_placeholder:"Add a tag…",
    stream_unlimited:"Allow streaming",stream_disabled:"Disable streaming",
    status_enabled:"Enabled",status_disabled:"Disabled",status_archived:"Archived"
  }
};

let currentLang = localStorage.getItem("relayhub_lang") || "zh";
const T = k => (I18N[currentLang] && I18N[currentLang][k]) || I18N.zh[k] || k;

let channels = [], providerModels = [], models = [];
let selChannel = null, selModel = null;
let editingChannelId = null;
let detectedProxies = [];

// AxonHub-style provider types with default URLs
const PROVIDER_TYPES = {
  openai:    { label:"type_openai",    url:"https://api.openai.com/v1" },
  anthropic: { label:"type_anthropic", url:"https://api.anthropic.com" },
  gemini:    { label:"type_gemini",    url:"https://generativelanguage.googleapis.com/v1" },
  deepseek:  { label:"type_deepseek",  url:"https://api.deepseek.com/v1" },
  zhipu:     { label:"type_zhipu",     url:"https://open.bigmodel.cn/api/paas/v4" },
  moonshot:  { label:"type_moonshot",  url:"https://api.moonshot.cn/v1" },
  openrouter:{ label:"type_openrouter",url:"https://openrouter.ai/api/v1" },
  ollama:    { label:"type_ollama",    url:"http://127.0.0.1:11434/v1" },
  custom:    { label:"type_custom",    url:"" }
};
// Built-in check-in sites (name → base_url)
const BUILTIN_SITES = {
  agentrouter: { label:"site_agentrouter", url:"https://agentrouter.org" },
  justdowork:  { label:"site_justdowork",  url:"https://api.justwoker.icu" },
  gorouter:    { label:"site_gorouter",    url:"https://gorouter.app" },
  seekai:      { label:"site_seekai",      url:"https://seekai.cc" },
  kktoken:     { label:"site_kktoken",     url:"https://kktoken.cc" }
};

function showToast(m){const t=document.getElementById("toast");t.textContent=m;t.style.display="block";setTimeout(()=>t.style.display="none",2500);}
function esc(s){return s?String(s).replace(/&/g,"&amp;").replace(/</g,"&lt;").replace(/>/g,"&gt;"):"";}
async function api(p,o){const r=await fetch(p,o);const d=await r.json().catch(()=>({}));if(!r.ok)throw new Error(d.error?.message||`HTTP ${r.status}`);return d;}

// ---------- i18n / sidebar / nav ----------
function setLanguage(lang){
  currentLang=lang;localStorage.setItem("relayhub_lang",lang);
  document.querySelectorAll("[data-i18n]").forEach(el=>{const k=el.getAttribute("data-i18n");if(I18N[lang][k])el.textContent=I18N[lang][k];});
  const sel=document.getElementById("settings-lang");
  if(sel)sel.value=lang;
  // Re-render dynamic selects
  if(channels.length)renderChannelTable();
  if(document.getElementById("ch-edit-pane").classList.contains("open"))renderChannelEditor();
}
// Top command-bar shell: no collapsible sidebar.
const langSel=document.getElementById("settings-lang");
if(langSel){langSel.value=currentLang;langSel.onchange=()=>setLanguage(langSel.value);}

document.querySelectorAll(".nav-item").forEach(i=>i.onclick=e=>{e.preventDefault();switchTab(i.dataset.nav);});
// Hash routing: same-tab in-page navigation, addressable + refresh-safe (no new window).
function syncHashRoute(){const h=(location.hash||"").replace(/^#\/?/,"");if(h&&h!=="diff"&&document.getElementById(`view-${h}`))switchTab(h);}
window.addEventListener("hashchange",syncHashRoute);
const PAGE_TITLES={overview:"概览",channels:"渠道列表",models:"模型列表",checkin:"签到记录",usage:"用量统计","cli-sync":"CLI 同步",logs:"服务日志",settings:"设置",diff:"同步预览"};
let _lastTab="overview";
function switchTab(v){
  if(v!=="diff"&&v!==_lastTab)_lastTab=v;
  document.querySelectorAll(".nav-item").forEach(i=>i.classList.toggle("active",i.dataset.nav===v));
  document.querySelectorAll(".view-panel").forEach(p=>p.classList.toggle("active",p.id===`view-${v}`));
  const pt=document.getElementById("page-title");
  if(pt)pt.textContent=PAGE_TITLES[v]||v;
  if(v!=="diff")history.replaceState(null,"","#/"+v);
  if(v==="overview")loadOverview();
  else if(v==="channels")loadChannels();
  else if(v==="models")loadModels();
  else if(v==="checkin")loadCheckin();
  else if(v==="usage")loadUsage();
  else if(v==="cli-sync")loadCLISync();
  else if(v==="logs")loadLogs();
  else if(v==="settings")loadSettings();
}

// ---------- Proxy auto-detection ----------
async function detectProxies(){
  detectedProxies=[];
  const commonPorts=[7897,7890,1080,8118,1087,7891,8889,10808];
  for(const port of commonPorts){
    try{
      const r=await fetch(`http://127.0.0.1:${port}`, {method:"HEAD", signal:AbortSignal.timeout(500)});
      // If we get any response (even error), port is open
      detectedProxies.push(port);
    }catch(e){
      // Check if it's a network error (port closed) vs CORS error (port open)
      if(e.name==="AbortError"){detectedProxies.push(port);}
    }
  }
  // Also check environment proxy
  if(navigator.userAgent){
    // check clash default
  }
  return detectedProxies;
}
detectProxies();

// ---------- Status bar ----------
let serviceDown=false;
async function pollStatusBar(){
  try{
    const t0=performance.now();
    const d=await api("/api/v1/health");
    const ping=Math.round(performance.now()-t0);
    const dot=document.getElementById("sb-dot");
    const txt=document.getElementById("sb-status-text");
    const rbtn=document.getElementById("sb-restart-btn");
    if(d.status==="ok"){
      dot.classList.remove("sb-dot-error");
      txt.textContent=`${T("core_running")} · ${ping}ms`;
      serviceDown=false;
      if(rbtn)rbtn.style.display="none";
    }else{dot.classList.add("sb-dot-error");txt.textContent=T("core_stopped");serviceDown=true;if(rbtn)rbtn.style.display="inline-flex";}
  }catch(e){
    const dot=document.getElementById("sb-dot");dot&&dot.classList.add("sb-dot-error");
    const txt=document.getElementById("sb-status-text");txt&&(txt.textContent=T("core_stopped"));
    const rbtn=document.getElementById("sb-restart-btn");if(rbtn)rbtn.style.display="inline-flex";
  }
}
async function pollTokensPerSec(){
  try{
    const d=await api("/api/v1/usage");
    const recs=d.records||[];
    const now=Date.now();
    let recentTokens=0;
    for(const r of recs){if(r.created_at){const ts=new Date(r.created_at).getTime();if(now-ts<10000)recentTokens+=(r.output_tokens||0);}}
    const el=document.getElementById("sb-tps");
    if(el)el.textContent=recentTokens>0?`${(recentTokens/10).toFixed(1)} t/s`:"-- t/s";
  }catch(e){}
}
setInterval(pollStatusBar,5000);
setInterval(pollTokensPerSec,3000);
pollStatusBar();pollTokensPerSec();
const restartBtn=document.getElementById("sb-restart-btn");
if(restartBtn){
  restartBtn.onclick=async()=>{
    restartBtn.disabled=true;restartBtn.textContent="…";
    try{const d=await api("/api/v1/health");if(d.status==="ok"){pollStatusBar();return;}}catch(e){}
    try{location.reload();}catch(e){}
    setTimeout(()=>{restartBtn.disabled=false;restartBtn.textContent=T("sb_restart");},3000);
  };
}

// ---------- Overview ----------
async function loadOverview(){
  try{
    const d=await api("/api/v1/overview");
    document.getElementById("val-channels-count").textContent=d.counts?.channels||0;
    document.getElementById("val-models-count").textContent=d.counts?.models||0;
    document.getElementById("val-uptime").textContent=`${d.uptime_seconds||0}s`;
  }catch(e){console.error(e);}
}
document.getElementById("action-manual-checkin").onclick=runCheckins;
document.getElementById("action-add-channel-quick").onclick=()=>{switchTab("channels");openChannelEditor(null);};

// ---------- Channels: full-width table + slide-in editor ----------
const selectedChannelIds = new Set();
function setChannelSelected(id, selected){
  if(!channels.some(c=>c.id===id))return;
  if(selected)selectedChannelIds.add(id);else selectedChannelIds.delete(id);
  renderChannelTable();
}
function selectVisibleChannels(selected){
  for(const c of filteredChannels()){
    if(selected)selectedChannelIds.add(c.id);else selectedChannelIds.delete(c.id);
  }
  renderChannelTable();
}
async function loadChannels(){
  try{
    const [c,p]=await Promise.all([api("/api/v1/channels"),api("/api/v1/provider-models")]);
    channels=c.channels||[];providerModels=p.provider_models||[];
    renderChannelTable();
  }catch(e){showToast(e.message);}
}
function channelModels(chId){return providerModels.filter(p=>p.channel_id===chId);}

function filteredChannels(){
  const q=(document.getElementById("ch-search").value||"").toLowerCase();
  const st=document.getElementById("ch-filter-status").value;
  return channels.filter(c=>{
    if(st==="enabled"&&c.status!=="enabled"&&c.enabled!==true)return false;
    if(st==="disabled"&&(c.status==="enabled"||c.enabled===true))return false;
    if(q&&!(c.name.toLowerCase().includes(q)||c.base_url.toLowerCase().includes(q)))return false;
    return true;
  });
}
function renderChannelTable(){
  const liveIds=new Set(channels.map(c=>c.id));
  for(const id of selectedChannelIds)if(!liveIds.has(id))selectedChannelIds.delete(id);
  const filtered=filteredChannels();
  const tbody=document.getElementById("channels-table-rows");
  const selectedVisible=filtered.filter(c=>selectedChannelIds.has(c.id)).length;
  const all=document.getElementById("ch-select-all");
  all.disabled=filtered.length===0;
  all.checked=filtered.length>0&&selectedVisible===filtered.length;
  all.indeterminate=selectedVisible>0&&selectedVisible<filtered.length;
  all.setAttribute?.("aria-label",T("select_visible_channels"));
  document.getElementById("ch-selected-count").textContent=`${T("selected_channels")}: ${selectedChannelIds.size}`;
  if(!filtered.length){
    tbody.innerHTML=`<tr><td colspan="8" style="text-align:center;color:var(--text-muted);padding:40px;">${T("msg_no_channels")}</td></tr>`;
    return;
  }
  tbody.innerHTML=filtered.map(c=>{
    const n=channelModels(c.id).length;
    const status=c.status|| (c.enabled?"enabled":"disabled");
    const statusBadge=status==="archived"?`<span class="badge badge-warning">${T("f_archived")}</span>`:
      `<label class="switch" style="margin:0;" onclick="event.stopPropagation()"><input type="checkbox" ${c.enabled?"checked":""} onchange="toggleChannel('${c.id}',this.checked)"><span class="slider"></span></label>`;
    const health=c.error_message?`<span class="badge badge-danger" title="${esc(c.error_message)}">ERR</span>`:
      c.health_state==="healthy"?`<span class="badge badge-success">OK</span>`:"—";
    const tags=(c.routing_tags||"").split(",").filter(Boolean).map(t=>`<span class="mini-badge">${esc(t.trim())}</span>`).join(" ");
    return `<tr>
      <td><input type="checkbox" data-channel-select="${encodeURIComponent(c.id)}" aria-label="${T("select_channel")}" ${selectedChannelIds.has(c.id)?"checked":""}></td>
      <td><button class="btn btn-secondary" style="padding:2px 8px;font-size:12px;" onclick="toggleChannelExpand('${c.id}')" aria-label="${T("btn_details")}">▸</button> <strong>${esc(c.name)}</strong></td>
      <td><span class="badge badge-info">${esc(c.provider_id.split("-")[0])}</span></td>
      <td>${statusBadge}</td>
      <td>${n} ${T("col_models")}</td>
      <td>${tags}</td>
      <td>${health}</td>
      <td style="white-space:nowrap;">
        <button class="btn btn-secondary" style="padding:4px 10px;font-size:12px;" onclick="testChannel('${c.id}')">${T("btn_test")}</button>
        <button class="btn btn-secondary" style="padding:4px 10px;font-size:12px;" onclick="openChannelEditor('${c.id}')">${T("btn_edit")}</button>
        <button class="btn btn-secondary" style="padding:4px 10px;font-size:12px;" onclick="duplicateChannel('${c.id}')">${T("btn_duplicate")}</button>
        <button class="btn btn-danger" style="padding:4px 10px;font-size:12px;" onclick="deleteChannel('${c.id}')">${T("btn_delete")}</button>
      </td>
    </tr>
    <tr class="ch-expand-row" data-expand="${encodeURIComponent(c.id)}" style="display:none;"><td colspan="8" style="background:var(--card);padding:12px 20px;"><div class="ch-expand-body" style="color:var(--text-secondary);font-size:13px;">…</div></td></tr>`;
  }).join("");
}
// Row enable/disable switch. Optimistic local update; rolls back and toasts
// on failure. Archived channels have no switch and must never be written.
async function toggleChannel(channelId, enabled){
  const c=channels.find(x=>x.id===channelId);
  if(!c||(c.status||"")==="archived")return;
  const prevEnabled=c.enabled,prevStatus=c.status;
  c.enabled=enabled;
  c.status=enabled?"enabled":"disabled";
  try{
    await api(`/api/v1/channels/${encodeURIComponent(channelId)}`,{
      method:"PATCH",
      headers:{"Content-Type":"application/json"},
      body:JSON.stringify({enabled:c.enabled,status:c.status}),
    });
  }catch(e){
    c.enabled=prevEnabled;
    c.status=prevStatus;
    showToast(e.message);
  }
  renderChannelTable();
}
document.getElementById("ch-select-all").onchange=e=>selectVisibleChannels(e.target.checked);
document.getElementById("channels-table-rows").onchange=e=>{
  const id=e.target.getAttribute("data-channel-select");
  if(id!==null)setChannelSelected(decodeURIComponent(id),e.target.checked);
};
document.getElementById("ch-search").oninput=renderChannelTable;
document.getElementById("ch-filter-status").onchange=renderChannelTable;

// ---- Expanded row: health / quota / recent request stats ----
async function toggleChannelExpand(channelId){
  const row=document.querySelector(`tr.ch-expand-row[data-expand="${encodeURIComponent(channelId)}"]`);
  if(!row)return;
  if(row.style.display!=="none"){row.style.display="none";return;}
  row.style.display="";
  const body=row.querySelector(".ch-expand-body");
  body.textContent="…";
  try{
    const d=await api(`/api/v1/channels/stats?channel_id=${encodeURIComponent(channelId)}`);
    const s=d.stats||{};
    const rate=s.recent_requests>0?Math.round(100*s.recent_success/s.recent_requests):null;
    body.innerHTML=`
      <div style="display:flex;gap:24px;flex-wrap:wrap;">
        <span>${T("st_health")}: <strong>${esc(s.health_state||"—")}</strong></span>
        <span>${T("st_quota")}: <strong>${esc(s.quota_state||"—")}</strong></span>
        <span>${T("st_keys")}: <strong>${s.enabled_key_count||0}/${s.key_count||0}</strong></span>
        <span>${T("st_recent")}: <strong>${s.recent_requests||0}</strong>${rate!==null?` (${rate}% ${T("st_ok")})`:""}</span>
        <span>${T("st_avg_latency")}: <strong>${s.avg_latency_ms||0} ms</strong></span>
      </div>
      ${s.error_message?`<div style="margin-top:8px;color:var(--danger);">${esc(s.error_message)}</div>`:""}`;
  }catch(e){body.innerHTML=`<span style="color:var(--danger);">${esc(e.message)}</span>`;}
}

// ---- Slide-in editor ----
function openChannelEditor(editId){
  editingChannelId=editId||null;
  const workspace=document.getElementById("ch-workspace");
  const editPane=document.getElementById("ch-edit-pane");
  const listPane=document.getElementById("ch-list-pane");
  document.getElementById("ch-edit-title").textContent=editId?T("edit_channel"):T("new_channel");
  const pt=document.getElementById("page-title");if(pt)pt.textContent=editId?T("edit_channel"):T("new_channel");
  renderChannelEditor();
  workspace.classList.add("editing");
  editPane.classList.add("open");
  listPane.classList.add("compressed");
}
function closeChannelEditor(){
  const workspace=document.getElementById("ch-workspace");
  const editPane=document.getElementById("ch-edit-pane");
  editPane.classList.remove("open");
  editingChannelId=null;
  const pt=document.getElementById("page-title");if(pt)pt.textContent=T("nav_channels");
}
document.getElementById("btn-new-channel").onclick=()=>openChannelEditor(null);
document.getElementById("btn-ch-back").onclick=closeChannelEditor;
document.getElementById("btn-ch-save").onclick=()=>saveChannel(!editingChannelId);

function maskKey(key){
  if(!key)return "";
  if(key.length<=8)return "****";
  return key.slice(0,4)+"••••••••"+key.slice(-4);
}

function renderChannelEditor(){
  const body=document.getElementById("ch-edit-body");
  const c=editingChannelId?channels.find(x=>x.id===editingChannelId):null;
  const isNew=!c;

  const typeOpts=Object.entries(PROVIDER_TYPES).map(([k,v])=>`<option value="${k}" ${c&&c.provider_id.startsWith(k)?"selected":""}>${T(v.label)}</option>`).join("");
  const siteOpts=Object.entries(BUILTIN_SITES).map(([k,v])=>`<option value="${k}">${T(v.label)}</option>`).join("");
  const proxyOpts=detectedProxies.map(p=>`<option value="http://127.0.0.1:${p}" ${c?.proxy_url===`http://127.0.0.1:${p}`?"selected":""}>127.0.0.1:${p}</option>`).join("");
  const existingTags=(c?.routing_tags||"").split(",").map(s=>s.trim()).filter(Boolean);
  const chMappings=c?channelModels(c.id).filter(pm=>pm.model_id&&pm.upstream_model_name&&pm.model_id!==pm.upstream_model_name):[];
  const mappingHtml=chMappings.length?chMappings.map(m=>`
    <div class="map-row">
      <input class="form-input map-from" style="flex:1;" value="${esc(m.model_id)}" placeholder="claude-3-7">
      <span style="color:var(--text-muted);font-size:16px;">→</span>
      <input class="form-input map-to" style="flex:1;" value="${esc(m.upstream_model_name)}" placeholder="anthropic/claude-3.7">
      <button class="map-del" onclick="removeMapRow(this)">×</button>
    </div>`).join(""):`
    <div class="map-row">
      <input class="form-input map-from" style="flex:1;" placeholder="claude-3-7">
      <span style="color:var(--text-muted);font-size:16px;">→</span>
      <input class="form-input map-to" style="flex:1;" placeholder="anthropic/claude-3.7">
      <button class="map-del" onclick="removeMapRow(this)">×</button>
    </div>`;

  body.innerHTML=`
  <div class="form-grid">
    <div class="fld"><label>${T("f_site")}<span class="fld-inline-hint">${T("f_site_hint")}</span></label>
      <select id="e-ch-site" class="form-input">
        <option value="">— ${T("type_custom")} —</option>
        ${siteOpts}
      </select>
    </div>
    <div class="fld"><label>${T("th_type")}<span class="fld-inline-hint">${T("f_type_hint")}</span></label>
      <select id="e-ch-type" class="form-input">${typeOpts}</select>
    </div>
    <div class="fld"><label>${T("f_name")}</label>
      <input id="e-ch-name" class="form-input" value="${esc(c?.name||"")}" placeholder="My Channel">
    </div>
    <div class="fld"><label>${T("f_baseurl")}<span class="fld-inline-hint">${T("f_url_hint")}</span></label>
      <input id="e-ch-url" class="form-input" value="${esc(c?.base_url||"")}" placeholder="https://api.example.com/v1">
    </div>
    <div class="fld full-width"><label>API Key<span class="fld-inline-hint">${T("f_keys_hint")}</span></label>
      <div style="display:flex;gap:8px;">
        ${isNew
          ? `<textarea id="e-ch-keys" class="form-input" rows="4" style="flex:1;font-family:ui-monospace,Menlo,monospace;" placeholder="sk-xxx&#10;sk-yyy"></textarea>`
          : `<input id="e-ch-key" class="form-input" type="password" style="flex:1;font-family:ui-monospace,Menlo,monospace;" value="••••••••••••••••••••" disabled>`
        }
        <button class="btn btn-secondary" style="align-self:flex-end;" onclick="toggleKeyVisibility()">👁</button>
      </div>
    </div>
    <div class="fld full-width"><label>${T("f_models")}<span class="fld-inline-hint">${T("f_models_hint")}</span></label>
      <div style="display:flex;gap:8px;">
        <input id="e-ch-models" class="form-input" style="flex:1;" value="${esc(c?channelModels(c.id).map(p=>p.upstream_model_name).join(", "):"")}" placeholder="claude-3-7-sonnet, gpt-4o, deepseek-v3">
        <button class="btn btn-primary" style="align-self:flex-end;white-space:nowrap;" onclick="handleFetchModels()" id="btn-fetch-models">${T("fetch_models")}</button>
      </div>
    </div>
    <div id="fetched-panel" class="full-width" style="display:none;">
      <div style="display:flex;align-items:center;gap:10px;margin-bottom:8px;">
        <span style="font-size:13px;color:var(--text-secondary);font-weight:600;" id="fetched-title"></span>
        <button class="btn btn-secondary" style="padding:4px 12px;font-size:12px;" onclick="fetchedSelectAll(true)">✓ ${T("btn_save")}</button>
      </div>
      <div id="fetched-list" class="fetched-list"></div>
    </div>
    <div class="fld full-width"><label>${T("f_mapping")}<span class="fld-inline-hint">${T("f_mapping_hint")}</span></label>
      <div id="e-ch-mapping" style="display:flex;flex-direction:column;gap:6px;">
        ${mappingHtml}
      </div>
      <button class="map-add" onclick="addMapRow()">+</button>
    </div>
    <div class="fld full-width"><label>${T("f_tags")}<span class="fld-inline-hint">${T("f_tags_hint")}</span></label>
      <div class="tag-input" id="e-ch-tags-container">
        ${existingTags.map(t=>`<span class="tag-chip">${esc(t)}<button class="tag-remove" onclick="removeTag(this)">×</button></span>`).join("")}
        <input id="e-ch-tags-input" class="tag-input-field" placeholder="${T("f_tags_placeholder")}">
      </div>
      <input type="hidden" id="e-ch-tags" value="${esc(c?.routing_tags||"")}">
    </div>
    <div class="fld"><label>${T("f_proxy")}<span class="fld-inline-hint">${T("f_proxy_hint")}</span></label>
      <select id="e-ch-proxy" class="form-input">
        <option value="">— 直连 —</option>
        ${proxyOpts}
        <option value="_custom">手动输入…</option>
      </select>
    </div>
    <div class="fld" id="proxy-custom-fld" style="display:none;"><label>Proxy URL</label>
      <input id="e-ch-proxy-custom" class="form-input" placeholder="socks5://127.0.0.1:1080">
    </div>
    <div class="fld"><label>${T("f_priority")}<span class="fld-inline-hint">${T("f_priority_hint")}</span></label>
      <input id="e-ch-priority" type="number" class="form-input" value="${c?.priority||0}">
    </div>
    <div class="fld"><label>${T("f_weight")}<span class="fld-inline-hint">${T("f_weight_hint")}</span></label>
      <input id="e-ch-weight" type="number" class="form-input" value="${c?.weight||1}">
    </div>
    <div class="fld"><label>${T("f_test_model")}<span class="fld-inline-hint">${T("f_test_model_hint")}</span></label>
      <input id="e-ch-testmodel" class="form-input" value="${esc(c?.default_test_model||"")}" placeholder="claude-3-7-sonnet">
    </div>
    <div class="fld"><label>${T("f_remark")}</label>
      <input id="e-ch-remark" class="form-input" value="${esc(c?.remark||"")}" placeholder="…">
    </div>
    <div class="fld"><label>${T("f_stream_policy")}</label>
      <select id="e-ch-stream" class="form-input">
        <option value="unlimited" ${(c?.stream_policy||"unlimited")==="unlimited"?"selected":""}>${T("stream_unlimited")}</option>
        <option value="disabled" ${c?.stream_policy==="disabled"?"selected":""}>${T("stream_disabled")}</option>
      </select>
    </div>
    <div class="fld"><label>签到模式</label>
      <select id="e-ch-checkin-mode" class="form-input">
        <option value="auto" ${(c?.checkin_mode||"auto")==="auto"?"selected":""}>自动 (优先浏览器)</option>
        <option value="manual" ${c?.checkin_mode==="manual"?"selected":""}>人工 (Tier 3 兜底)</option>
      </select>
    </div>
    <div class="fld"><label>${T("f_status")}</label>
      <select id="e-ch-status" class="form-input">
        <option value="enabled" ${(c?.status||"enabled")==="enabled"?"selected":""}>${T("status_enabled")}</option>
        <option value="disabled" ${c?.status==="disabled"?"selected":""}>${T("status_disabled")}</option>
        <option value="archived" ${c?.status==="archived"?"selected":""}>${T("status_archived")}</option>
      </select>
    </div>
    <div class="fld full-width" style="margin-top:2px;">
      <label class="chk">
        <input id="e-ch-autosync" type="checkbox" ${c?.auto_sync?"checked":""}>
        <span style="font-weight:600;color:var(--text-primary);">${T("f_auto_sync")}</span>
      </label>
    </div>
    ${c?.error_message?`
    <div class="fld full-width"><label>${T("f_last_error")}</label>
      <input class="form-input" value="${esc(c.error_message)}" disabled style="color:var(--danger);">
    </div>`:""}
    ${!isNew?`
    <div class="fld full-width" style="padding-top:12px;border-top:1px solid var(--border-soft);margin-top:8px;">
      <label>${T("f_keys")}<span class="fld-inline-hint">${T("f_keys_mgmt_hint")}</span></label>
      <div id="e-ch-keylist" style="display:flex;flex-direction:column;gap:6px;margin-bottom:8px;"></div>
      <div style="display:flex;gap:8px;">
        <input id="e-ch-newkey" class="form-input" type="password" style="flex:1;font-family:ui-monospace,Menlo,monospace;" placeholder="sk-...">
        <input id="e-ch-newkey-label" class="form-input" style="width:130px;" placeholder="${T("f_key_label")}">
        <button class="btn btn-primary" style="white-space:nowrap;" onclick="addChannelKey('${c.id}')">${T("btn_add_key")}</button>
      </div>
    </div>
    <div class="full-width" style="display:flex;gap:10px;padding-top:12px;border-top:1px solid var(--border-soft);margin-top:8px;">
      <button class="btn btn-secondary" onclick="syncChannelModels('${c.id}')">${T("btn_sync_now")}</button>
    </div>`:""}
  </div>`;

  if(!isNew&&c)renderChannelKeys(c.id);

  // Tag input logic (AxonHub-style: enter/comma/space adds tag, × removes)
  const tagInput=document.getElementById("e-ch-tags-input");
  const tagContainer=document.getElementById("e-ch-tags-container");
  const tagHidden=document.getElementById("e-ch-tags");
  function syncTags(){
    const tags=[...tagContainer.querySelectorAll(".tag-chip")].map(c=>c.textContent.replace(/×$/,"").trim());
    tagHidden.value=tags.join(",");
  }
  tagInput.addEventListener("keydown",(e)=>{
    if(e.key==="Enter"||e.key===","||e.key===" "){
      e.preventDefault();
      const val=tagInput.value.trim();
      if(val){
        const chip=document.createElement("span");
        chip.className="tag-chip";
        chip.innerHTML=`${esc(val)}<button class="tag-remove" onclick="removeTag(this)">×</button>`;
        tagContainer.insertBefore(chip,tagInput);
        tagInput.value="";
        syncTags();
      }
    }else if(e.key==="Backspace"&&!tagInput.value){
      const chips=tagContainer.querySelectorAll(".tag-chip");
      if(chips.length)chips[chips.length-1].remove();
      syncTags();
    }
  });
  window._syncTags=syncTags;

  // Site select auto-fill
  document.getElementById("e-ch-site").onchange=(e)=>{
    const site=BUILTIN_SITES[e.target.value];
    if(site){
      document.getElementById("e-ch-url").value=site.url;
      document.getElementById("e-ch-name").value=e.target.options[e.target.selectedIndex].text.split(" (")[0];
    }
  };
  document.getElementById("e-ch-proxy").onchange=(e)=>{
    document.getElementById("proxy-custom-fld").style.display=e.target.value==="_custom"?"block":"none";
  };
  document.getElementById("e-ch-url").addEventListener("blur",(e)=>{
    let url=e.target.value.trim();
    if(url&&!/\/v\d+\/?$/.test(url)&&!url.includes("/v1")){
      const type=document.getElementById("e-ch-type").value;
      if(["openai","deepseek","moonshot","zhipu","openrouter"].includes(type)){
        e.target.value=url.replace(/\/+$/,"")+"/v1";
      }
    }
  });
}

function removeTag(btn){
  btn.closest(".tag-chip").remove();
  if(window._syncTags)window._syncTags();
}
// ---- Per-channel API key management (metadata only; secrets never returned) ----
async function renderChannelKeys(channelId){
  const box=document.getElementById("e-ch-keylist");
  if(!box)return;
  try{
    const d=await api(`/api/v1/channel-keys?channel_id=${encodeURIComponent(channelId)}`);
    const keys=d.keys||[];
    if(!keys.length){box.innerHTML=`<div style="color:var(--text-muted);font-size:12px;">${T("msg_no_keys")}</div>`;return;}
    box.innerHTML=keys.map(k=>`
      <div class="map-row" style="align-items:center;">
        <span style="flex:1;font-size:13px;${k.disabled?"opacity:0.5;text-decoration:line-through;":""}">${esc(k.label||k.id)}${k.disabled?` <span class="mini-badge">${T("status_disabled")}</span>`:""}</span>
        <button class="btn btn-secondary" style="padding:3px 10px;font-size:12px;" onclick="toggleChannelKey('${channelId}','${k.id}',${k.disabled?"false":"true"})">${k.disabled?T("btn_enable"):T("btn_disable")}</button>
        <button class="btn btn-danger" style="padding:3px 10px;font-size:12px;" onclick="deleteChannelKey('${channelId}','${k.id}')">${T("btn_delete")}</button>
      </div>`).join("");
  }catch(e){box.innerHTML=`<div style="color:var(--danger);font-size:12px;">${esc(e.message)}</div>`;}
}
async function addChannelKey(channelId){
  const val=document.getElementById("e-ch-newkey").value.trim();
  if(!val){showToast(T("msg_key_required"));return;}
  const label=document.getElementById("e-ch-newkey-label").value.trim();
  try{
    await api("/api/v1/channel-keys",{method:"POST",headers:{"Content-Type":"application/json"},body:JSON.stringify({channel_id:channelId,value:val,label})});
    document.getElementById("e-ch-newkey").value="";
    document.getElementById("e-ch-newkey-label").value="";
    showToast(T("toast_saved"));
    renderChannelKeys(channelId);
  }catch(e){showToast(e.message);}
}
async function toggleChannelKey(channelId,keyId,disabled){
  try{
    await api(`/api/v1/channel-keys/${keyId}`,{method:"PATCH",headers:{"Content-Type":"application/json"},body:JSON.stringify({disabled})});
    renderChannelKeys(channelId);
  }catch(e){showToast(e.message);}
}
// In-page confirmation bar (replaces native confirm(): no popup window).
// Shows a red action bar pinned to the top of the content area.
let _confirmTimer=null;
function confirmAction(message,onConfirm){
  let bar=document.getElementById("confirm-bar");
  if(!bar){
    bar=document.createElement("div");
    bar.id="confirm-bar";bar.className="confirm-bar";
    const host=document.querySelector(".content-body")||document.body;
    host.prepend(bar);
  }
  clearTimeout(_confirmTimer);
  bar.innerHTML=`<span class="confirm-bar-msg">${esc(message)}</span>
    <button class="btn btn-danger confirm-bar-ok">${T("btn_confirm")||"确认"}</button>
    <button class="btn btn-secondary confirm-bar-cancel">${T("btn_cancel")}</button>`;
  bar.querySelector(".confirm-bar-ok").onclick=()=>{bar.remove();clearTimeout(_confirmTimer);onConfirm();};
  bar.querySelector(".confirm-bar-cancel").onclick=()=>{bar.remove();clearTimeout(_confirmTimer);};
  _confirmTimer=setTimeout(()=>bar.remove(),8000);
  bar.scrollIntoView({block:"nearest"});
}

// In-page single-input prompt (replaces native prompt(): no popup window).
function promptInline(label,defVal,onSubmit){
  let bar=document.getElementById("confirm-bar");
  if(!bar){bar=document.createElement("div");bar.id="confirm-bar";bar.className="confirm-bar";
    (document.querySelector(".content-body")||document.body).prepend(bar);}
  clearTimeout(_confirmTimer);
  bar.innerHTML=`<span class="confirm-bar-msg" style="color:var(--text-primary)">${esc(label)}</span>
    <input class="form-input confirm-bar-input" style="width:220px" value="${esc(defVal||"")}">
    <button class="btn btn-primary confirm-bar-ok">${T("btn_confirm")||"确认"}</button>
    <button class="btn btn-secondary confirm-bar-cancel">${T("btn_cancel")}</button>`;
  const input=bar.querySelector(".confirm-bar-input");input.focus();input.select();
  const submit=()=>{const v=input.value.trim();bar.remove();clearTimeout(_confirmTimer);onSubmit(v);};
  bar.querySelector(".confirm-bar-ok").onclick=submit;
  bar.querySelector(".confirm-bar-cancel").onclick=()=>{bar.remove();clearTimeout(_confirmTimer);};
  input.onkeydown=e=>{if(e.key==="Enter")submit();};
}

async function deleteChannelKey(channelId,keyId){
  confirmAction(`${T("confirm_delete")} ${keyId}?`,async()=>{ try{
    await api(`/api/v1/channel-keys/${keyId}`,{method:"DELETE"});
    showToast(T("toast_deleted"));
    renderChannelKeys(channelId);
  }catch(e){showToast(e.message);}
  });
}

let keyVisible=false;
function toggleKeyVisibility(){
  keyVisible=!keyVisible;
  const el=document.getElementById("e-ch-keys")||document.getElementById("e-ch-key");
  if(el){
    if(el.tagName==="TEXTAREA"){
      // Toggle between masked and real
      if(keyVisible){el.style.webkitTextSecurity="none";el.style.fontFamily="ui-monospace,Menlo,monospace";}
      else{el.style.webkitTextSecurity="disc";}
    }else{
      el.type=keyVisible?"text":"password";
    }
  }
}

function addMapRow(){
  const box=document.getElementById("e-ch-mapping");
  const row=document.createElement("div");
  row.className="map-row";
  row.innerHTML=`<input class="form-input map-from" style="flex:1;" placeholder="from"><span style="color:var(--text-muted);">→</span><input class="form-input map-to" style="flex:1;" placeholder="to"><button class="map-del" onclick="removeMapRow(this)">×</button>`;
  box.appendChild(row);
}
function removeMapRow(btn){btn.closest(".map-row").remove();}

async function handleFetchModels(){
  const btn=document.getElementById("btn-fetch-models");
  btn.disabled=true;btn.textContent="…";
  try{
    let payload;
    if(!editingChannelId){
      const baseURL=document.getElementById("e-ch-url").value.trim();
      const keysRaw=(document.getElementById("e-ch-keys").value||"").split("\n").map(s=>s.trim()).filter(Boolean);
      if(!baseURL){showToast(T("f_baseurl")+" !");return;}
      payload={base_url:baseURL,api_key:keysRaw[0]||""};
    }else{
      payload={channel_id:editingChannelId};
    }
    const d=await api("/api/v1/fetch-models",{method:"POST",headers:{"Content-Type":"application/json"},body:JSON.stringify(payload)});
    const list=d.models||[];
    if(!list.length){showToast(T("toast_test_fail"));return;}
    document.getElementById("fetched-panel").style.display="block";
    document.getElementById("fetched-title").textContent=`${list.length} ${T("col_models")}`;
    const existing=(document.getElementById("e-ch-models").value||"").split(",").map(s=>s.trim()).filter(Boolean);
    document.getElementById("fetched-list").innerHTML=list.map(m=>`
      <label class="fetched-item">
        <input type="checkbox" class="fetched-chk" value="${esc(m)}" ${existing.includes(m)?"checked":""}>
        <code>${esc(m)}</code>
      </label>`).join("");
  }catch(e){showToast(`${T("toast_test_fail")}: ${e.message}`);}
  finally{btn.disabled=false;btn.textContent=T("fetch_models");}
}
function fetchedSelectAll(on){document.querySelectorAll(".fetched-chk").forEach(c=>c.checked=on);
  const selected=[...document.querySelectorAll(".fetched-chk:checked")].map(c=>c.value);
  const input=document.getElementById("e-ch-models");
  const existing=(input.value||"").split(",").map(s=>s.trim()).filter(Boolean);
  input.value=[...new Set([...existing,...selected])].join(", ");
  document.getElementById("fetched-panel").style.display="none";
  showToast(`${selected.length} ${T("col_models")}`);
}

function collectMapping(){
  return [...document.querySelectorAll("#e-ch-mapping .map-row")].map(r=>({
    from:r.querySelector(".map-from").value.trim(),
    to:r.querySelector(".map-to").value.trim()
  })).filter(m=>m.from&&m.to);
}

async function testChannel(id){
  const t0=performance.now();
  try{
    const d=await api("/api/v1/channels/test",{method:"POST",headers:{"Content-Type":"application/json"},body:JSON.stringify({channel_id:id})});
    if(d.success)showToast(`${T("toast_test_ok")} · ${d.model_count} ${T("col_models")} · ${d.latency_ms}ms`);
    else showToast(`${T("toast_test_fail")}: ${d.error}`);
    loadChannels();
  }catch(e){showToast(`${T("toast_test_fail")}: ${e.message}`);}
}

async function bulkChannelAction(action){
  const ids=[...selectedChannelIds];
  if(!ids.length){showToast(T("bulk_none"));return;}
  const label={enable:T("bulk_enable"),disable:T("bulk_disable"),archive:T("bulk_archive"),delete:T("bulk_delete")}[action];
  confirmAction(`${T("confirm_bulk")} ${label}? (${ids.length})`,async()=>{ try{
    const d=await api("/api/v1/channels/batch",{method:"POST",headers:{"Content-Type":"application/json"},body:JSON.stringify({action,ids})});
    showToast(`${label}: ${d.affected||0}`);
    selectedChannelIds.clear();
    await loadChannels();
  }catch(e){showToast(e.message);}
  });
}

async function duplicateChannel(channelId){
  promptInline("新渠道 ID (new channel id):",channelId+"-copy",async(newId)=>{
  if(!newId)return;
  try{
    await api("/api/v1/channels/duplicate",{method:"POST",headers:{"Content-Type":"application/json"},body:JSON.stringify({channel_id:channelId,new_id:newId.trim()})});
    showToast(T("toast_saved"));
    await loadChannels();
  }catch(e){showToast(e.message);}
  });
}

async function syncChannelModels(channelId){
  showToast("…");
  try{
    const d=await api("/api/v1/channels/sync-models",{method:"POST",headers:{"Content-Type":"application/json"},body:JSON.stringify({channel_id:channelId})});
    showToast(`${T("btn_sync_now")}: ${d.total||0} ${T("col_models")}`);
    await loadChannels();
    openChannelEditor(channelId);
  }catch(e){showToast(e.message);}
}

async function saveChannel(isNew){
  const siteSel=document.getElementById("e-ch-site");
  const type=document.getElementById("e-ch-type").value;
  let name=document.getElementById("e-ch-name").value.trim();
  let url=document.getElementById("e-ch-url").value.trim();
  const tags=document.getElementById("e-ch-tags").value.trim();
  const modelsRaw=document.getElementById("e-ch-models").value.trim();
  const priority=parseInt(document.getElementById("e-ch-priority").value||"0",10);
  const weight=parseInt(document.getElementById("e-ch-weight").value||"1",10);
  const status=document.getElementById("e-ch-status").value;
  const enabled=status!=="disabled";
  const isCheckinSite=Object.keys(BUILTIN_SITES).includes(document.getElementById("e-ch-site").value);
  const checkin=isCheckinSite;
  const testModel=document.getElementById("e-ch-testmodel").value.trim();
  const remark=document.getElementById("e-ch-remark").value.trim();
  const streamPolicy=document.getElementById("e-ch-stream").value;
  const checkinMode=document.getElementById("e-ch-checkin-mode")?.value||"auto";
  const autoSync=document.getElementById("e-ch-autosync").checked;
  const proxySel=document.getElementById("e-ch-proxy");
  let proxyUrl=proxySel.value==="_custom"?document.getElementById("e-ch-proxy-custom").value.trim():proxySel.value;
  const mapping=collectMapping();

  // Site selection overrides type
  let finalType=type;
  if(siteSel.value&&BUILTIN_SITES[siteSel.value]){finalType=siteSel.value;}
  if(!name){
    // Auto-derive from site or type
    name=siteSel.value?siteSel.options[siteSel.selectedIndex].text.split(" (")[0]:T(PROVIDER_TYPES[type].label);
  }
  if(!url){showToast(T("f_baseurl")+" !");return;}

  // Auto-append /v1 for openai-compatible types
  if(!url.includes("/v1")&&["openai","deepseek","moonshot","zhipu","openrouter"].includes(finalType)){
    url=url.replace(/\/+$/,"")+"/v1";
  }

  const modelList=modelsRaw.split(",").map(s=>s.trim()).filter(Boolean);
  const isCheckin=Object.keys(BUILTIN_SITES).includes(finalType);
  const protocol=finalType==="anthropic"?"anthropic-messages":"openai-chat";
  const slug=name.toLowerCase().replace(/[^a-z0-9]+/g,"-").replace(/^-|-$/g,"")||"chan";
  const keys=isNew?(document.getElementById("e-ch-keys").value.split("\n").map(s=>s.trim()).filter(Boolean)):[""];

  try{
    for(let i=0;i<keys.length;i++){
      const sfx=keys.length>1?`-${i+1}`:"";
      const pid=`${finalType}-${slug}${sfx}`, cid=`${pid}-c1`;
      await api("/api/v1/providers",{method:"POST",headers:{"Content-Type":"application/json"},body:JSON.stringify({id:pid,name:name+sfx,adapter_type:isCheckin?finalType:"generic",protocol,enabled:true})}).catch(()=>{});
      const body={id:cid,provider_id:pid,name:name+sfx,base_url:url,routing_tags:tags,priority,weight,checkin_enabled:checkin||isCheckin,routing_enabled:true,enabled,
        status,stream_policy:streamPolicy,default_test_model:testModel,proxy_url:proxyUrl,remark,auto_sync:autoSync,checkin_mode:checkinMode};
      if(isNew){
        // New channel: mint a credential ref only when a key was entered.
        const credRef=keys[i]?`cred-${cid}`:"";
        body.credential_ref=credRef;
        await api("/api/v1/channels",{method:"POST",headers:{"Content-Type":"application/json"},body:JSON.stringify(body)}).catch(()=>{});
        if(keys[i])await api("/api/v1/secrets",{method:"POST",headers:{"Content-Type":"application/json"},body:JSON.stringify({ref:credRef,value:keys[i]})}).catch(()=>{});
      }else{
        // Edit: preserve the existing credential_ref (keys are managed separately
        // via the API-Key manager). Merging existing first, then our fields, but
        // NOT overwriting credential_ref, avoids wiping the channel's saved key.
        const existing=channels.find(x=>x.id===editingChannelId)||{};
        const merged={...existing,...body,credential_ref:existing.credential_ref||""};
        await api(`/api/v1/channels/${editingChannelId}`,{method:"PUT",headers:{"Content-Type":"application/json"},body:JSON.stringify(merged)});
      }
      for(const mn of modelList){
        await api("/api/v1/models",{method:"POST",headers:{"Content-Type":"application/json"},body:JSON.stringify({id:mn,display_name:mn,enabled:true})}).catch(()=>{});
        await api("/api/v1/provider-models",{method:"POST",headers:{"Content-Type":"application/json"},body:JSON.stringify({id:`pm-${cid}-${mn}`,provider_id:pid,channel_id:cid,model_id:mn,upstream_model_name:mn,protocol,priority,weight,enabled:true})}).catch(()=>{});
      }
      for(const mp of mapping){
        await api("/api/v1/models",{method:"POST",headers:{"Content-Type":"application/json"},body:JSON.stringify({id:mp.from,display_name:mp.from,enabled:true})}).catch(()=>{});
        await api("/api/v1/provider-models",{method:"POST",headers:{"Content-Type":"application/json"},body:JSON.stringify({id:`pm-${cid}-${mp.from}`,provider_id:pid,channel_id:cid,model_id:mp.from,upstream_model_name:mp.to,protocol,priority,weight,enabled:true})}).catch(()=>{});
      }
    }
    showToast(T("toast_saved"));
    closeChannelEditor();
    await loadChannels();
  }catch(e){showToast(e.message);}
}

async function deleteChannel(id){
  confirmAction(`${T("confirm_delete")} ${id}?`,async()=>{ try{
    for(const pm of channelModels(id))await api(`/api/v1/provider-models/${pm.id}`,{method:"DELETE"}).catch(()=>{});
    await api(`/api/v1/channels/${id}`,{method:"DELETE"});
    showToast(T("toast_deleted"));
    loadChannels();
  }catch(e){showToast(e.message);}
  });
}

// ---------- Models ----------
async function loadModels(){
  try{
    const [m,p,c]=await Promise.all([api("/api/v1/models"),api("/api/v1/provider-models"),api("/api/v1/channels")]);
    models=m.models||[];providerModels=p.provider_models||[];channels=c.channels||[];
    renderModelList();
    if(selModel){const f=models.find(x=>x.id===selModel.id);selModel=f||null;renderModelDetail();}
  }catch(e){showToast(e.message);}
}
function modelBindings(mid){return providerModels.filter(p=>p.model_id===mid);}

function renderModelList(){
  const q=(document.getElementById("md-search").value||"").toLowerCase();
  const list=document.getElementById("model-list");
  const filtered=models.filter(m=>!q||m.id.toLowerCase().includes(q)||(m.display_name||"").toLowerCase().includes(q));
  if(!filtered.length){list.innerHTML=`<div style="color:var(--text-muted);padding:40px;text-align:center;">${T("msg_no_models")}</div>`;return;}
  list.innerHTML=filtered.map(m=>{
    const n=modelBindings(m.id).length;
    const caps=[m.tool_call_support?"T":"",m.vision_support?"V":"",m.reasoning_support?"R":""].filter(Boolean).join("");
    const icon=m.icon_url?`<img src="${esc(m.icon_url)}" alt="" style="width:16px;height:16px;border-radius:3px;vertical-align:middle;margin-right:5px;" onerror="this.style.display='none'">`:"";
    const archBadge=m.archived?` <span class="badge badge-warning">${T("f_archived")}</span>`:"";
    return `<div class="list-item ${selModel&&selModel.id===m.id?"active":""}" onclick="selectModel('${m.id}')" style="${m.archived?"opacity:0.6;":""}">
      <div class="li-main">
        <div class="li-title">${icon}${esc(m.display_name||m.id)} ${caps?`<span class="mini-badge">${caps}</span>`:""}${archBadge}</div>
        <div class="li-sub">${esc(m.id)}${m.developer?` · ${esc(m.developer)}`:""}${m.model_type&&m.model_type!=="llm"?` · ${esc(m.model_type)}`:""} · ${n} ${T("col_bound")}</div>
      </div>
    </div>`;
  }).join("");
}
document.getElementById("md-search").oninput=renderModelList;

function selectModel(id){selModel=models.find(m=>m.id===id);renderModelList();renderModelDetail();}
function newModel(){selModel=null;renderModelList();renderModelDetail(true);}
document.getElementById("btn-new-model").onclick=()=>{if(!channels.length)loadChannels().then(newModel);else newModel();};

function renderModelDetail(isNew){
  const pane=document.getElementById("model-detail");
  if(!isNew&&!selModel){pane.innerHTML=`<div style="color:var(--text-secondary);padding:60px 0;text-align:center;width:100%;">${T("msg_select_model")}</div>`;return;}
  const m=isNew?{id:"",display_name:"",tool_call_support:true,vision_support:false,reasoning_support:false,context_window:0,developer:"",model_type:"llm",input_price:0,output_price:0,icon_url:"",archived:false}:selModel;
  const binds=isNew?[]:modelBindings(m.id);
  const chanRows=channels.map(c=>{
    const ex=binds.find(b=>b.channel_id===c.id);
    return `<div class="map-row bind-row" data-ch="${c.id}">
      <label class="chk" style="flex:0 0 auto;"><input type="checkbox" class="bind-chk" ${ex?"checked":""}> ${esc(c.name)}</label>
      <input class="form-input bind-upstream" style="flex:1;" placeholder="${T("upstream_name")}" value="${ex?esc(ex.upstream_model_name):""}">
      <input class="form-input bind-pri" style="width:70px;" type="number" placeholder="P" value="${ex?ex.priority:0}">
      <input class="form-input bind-w" style="width:70px;" type="number" placeholder="W" value="${ex?ex.weight:1}">
    </div>`;
  }).join("")||`<p style="color:var(--text-muted);">${T("msg_no_channels")}</p>`;

  pane.innerHTML=`
    <div class="detail-head">
      <h3>${isNew?T("new_model"):esc(m.display_name||m.id)}</h3>
      ${isNew?"":`<button class="btn btn-danger" onclick="deleteModel('${m.id}')">${T("btn_delete")}</button>`}
    </div>
    <div class="detail-form">
      <div class="form-row">
        <div class="fld"><label>模型 ID</label><input id="d-md-id" class="form-input" value="${esc(m.id)}" ${isNew?"":"disabled"}></div>
        <div class="fld"><label>${T("f_display")}</label><input id="d-md-name" class="form-input" value="${esc(m.display_name)}"></div>
      </div>
      <div class="form-row">
        <div class="fld"><label>${T("f_developer")}</label><input id="d-md-dev" class="form-input" value="${esc(m.developer||"")}"></div>
        <div class="fld" style="width:150px;"><label>${T("f_model_type")}</label>
          <select id="d-md-type" class="form-input">
            ${["llm","embedding","image","rerank","audio"].map(t=>`<option value="${t}" ${(m.model_type||"llm")===t?"selected":""}>${t}</option>`).join("")}
          </select>
        </div>
      </div>
      <div class="form-row">
        <label class="chk"><input id="d-md-tools" type="checkbox" ${m.tool_call_support?"checked":""}> ${T("f_tools")}</label>
        <label class="chk"><input id="d-md-vision" type="checkbox" ${m.vision_support?"checked":""}> ${T("f_vision")}</label>
        <label class="chk"><input id="d-md-reasoning" type="checkbox" ${m.reasoning_support?"checked":""}> ${T("f_reasoning")}</label>
        <div class="fld" style="width:140px;"><label>${T("f_ctx")}</label><input id="d-md-ctx" type="number" class="form-input" value="${m.context_window||0}"></div>
      </div>
      <div class="form-row">
        <div class="fld" style="width:170px;"><label>${T("f_input_price")}</label><input id="d-md-inprice" type="number" step="0.01" class="form-input" value="${m.input_price||0}"></div>
        <div class="fld" style="width:170px;"><label>${T("f_output_price")}</label><input id="d-md-outprice" type="number" step="0.01" class="form-input" value="${m.output_price||0}"></div>
      </div>
      <div class="form-row">
        <div class="fld" style="flex:1;"><label>${T("f_icon")}</label><input id="d-md-icon" class="form-input" value="${esc(m.icon_url||"")}" placeholder="https://…/icon.png"></div>
        <label class="chk" style="align-self:flex-end;padding-bottom:8px;"><input id="d-md-archived" type="checkbox" ${m.archived?"checked":""}> ${T("f_archived_model")}</label>
      </div>
      <div class="form-row"><div class="fld" style="flex:1;"><label>${T("f_bindings")}</label><div id="d-md-binds">${chanRows}</div></div></div>
      <div class="form-row"><button class="btn btn-primary" onclick="saveModel(${isNew?"true":"false"})">${T("btn_save")}</button></div>
    </div>`;
}

async function saveModel(isNew){
  const id=document.getElementById("d-md-id").value.trim();
  if(!id){showToast("ID !");return;}
  const body={id,display_name:document.getElementById("d-md-name").value.trim()||id,
    tool_call_support:document.getElementById("d-md-tools").checked,
    vision_support:document.getElementById("d-md-vision").checked,
    reasoning_support:document.getElementById("d-md-reasoning").checked,
    context_window:parseInt(document.getElementById("d-md-ctx").value||"0",10),
    developer:document.getElementById("d-md-dev").value.trim(),
    model_type:document.getElementById("d-md-type").value,
    input_price:parseFloat(document.getElementById("d-md-inprice").value||"0"),
    output_price:parseFloat(document.getElementById("d-md-outprice").value||"0"),
    icon_url:document.getElementById("d-md-icon").value.trim(),
    archived:document.getElementById("d-md-archived").checked,
    enabled:true};
  try{
    if(isNew)await api("/api/v1/models",{method:"POST",headers:{"Content-Type":"application/json"},body:JSON.stringify(body)}).catch(()=>{
      return api(`/api/v1/models/${id}`,{method:"PUT",headers:{"Content-Type":"application/json"},body:JSON.stringify(body)});
    });
    else await api(`/api/v1/models/${id}`,{method:"PUT",headers:{"Content-Type":"application/json"},body:JSON.stringify(body)});
    for(const pm of modelBindings(id))await api(`/api/v1/provider-models/${pm.id}`,{method:"DELETE"}).catch(()=>{});
    document.querySelectorAll("#d-md-binds .bind-row").forEach(row=>{
      if(!row.querySelector(".bind-chk").checked)return;
      const cid=row.dataset.ch;
      const chan=channels.find(c=>c.id===cid);
      const upstream=row.querySelector(".bind-upstream").value.trim()||id;
      const pri=parseInt(row.querySelector(".bind-pri").value||"0",10);
      const w=parseInt(row.querySelector(".bind-w").value||"1",10);
      api("/api/v1/provider-models",{method:"POST",headers:{"Content-Type":"application/json"},
        body:JSON.stringify({id:`pm-${cid}-${id}`,provider_id:chan?chan.provider_id:"",channel_id:cid,model_id:id,upstream_model_name:upstream,protocol:"openai-chat",priority:pri,weight:w,enabled:true})}).catch(()=>{});
    });
    showToast(T("toast_saved"));
    selModel=null;await loadModels();
  }catch(e){showToast(e.message);}
}

async function deleteModel(id){
  confirmAction(`${T("confirm_delete")} ${id}?`,async()=>{ try{
    for(const pm of modelBindings(id))await api(`/api/v1/provider-models/${pm.id}`,{method:"DELETE"}).catch(()=>{});
    await api(`/api/v1/models/${id}`,{method:"DELETE"});
    showToast(T("toast_deleted"));selModel=null;loadModels();
  }catch(e){showToast(e.message);}
  });
}

// ---------- Checkin / Usage / Logs ----------
document.getElementById("btn-trigger-checkin-now").onclick=runCheckins;
async function runCheckins(){
  const buttons=[document.getElementById("action-manual-checkin"),document.getElementById("btn-trigger-checkin-now")];
  buttons.forEach(b=>b.disabled=true);
  let result=document.getElementById("checkin-run-result");
  if(!result){result=document.createElement("div");result.id="checkin-run-result";result.className="form-card";document.getElementById("view-checkin").appendChild(result);}
  result.textContent="正在执行签到…";
  try{
    const d=await api("/api/v1/checkin",{method:"POST",headers:{"Content-Type":"application/json"},body:"{}"});
    result.textContent="";
    for(const item of d.results||[]){
      const row=document.createElement("p");
      row.textContent=`${item.channel_id}：${item.status==="success"?"站点确认成功":item.status==="need_manual"?"需人工操作":item.status==="persistence_failed"?`结果保存失败（执行状态：${item.execution_status||"未知"}，勿直接重试`:"未完成"} ${item.message||""} `;
      if(item.status==="need_manual" && item.manual_url){
        try{const url=new URL(item.manual_url);if(["http:","https:"].includes(url.protocol)&&!url.username&&!url.password){const a=document.createElement("a");a.href=url.href;a.target="_blank";a.rel="noopener noreferrer";a.textContent="打开站点";row.appendChild(a);}}catch{}
      }
      result.appendChild(row);
    }
    if(!(d.results||[]).length)result.textContent="没有启用签到的渠道，未执行任何签到。";
    switchTab("checkin");
  }catch(e){result.textContent=`签到请求失败：${e.message}`;showToast(result.textContent);}
  finally{buttons.forEach(b=>b.disabled=false);}
}
async function loadCheckin(){
  try{const d=await api("/api/v1/checkin");const r=d.records||[];const tb=document.getElementById("checkin-table-rows");
    if(!r.length){tb.innerHTML='<tr><td colspan="5">暂无签到记录</td></tr>';return;}
    tb.innerHTML=r.sort((a,b)=>new Date(b.started_at)-new Date(a.started_at)).slice(0,30).map(x=>`<tr><td><code>${esc(x.channel_id)}</code></td><td>${x.started_at?new Date(x.started_at).toLocaleString():"-"}</td><td>${x.reward?`+${esc(x.reward)}`:"-"}</td><td>${x.status==="success"?'<span class="badge badge-success">OK</span>':x.status==="need_manual"?'<span class="badge badge-warning">需人工</span>':x.status==="persistence_failed"?'<span class="badge badge-warning">保存失败</span>':'<span class="badge badge-danger">FAIL</span>'}</td><td>${esc(x.error_message||"")}</td></tr>`).join("");
  }catch(e){console.error(e);}
}
async function loadUsage(){
  try{const d=await api("/api/v1/usage");
    if(d.summary){document.getElementById("stat-total-reqs").textContent=d.summary.total_requests||0;
      document.getElementById("stat-success-rate").textContent=d.summary.success_rate||"100%";
      document.getElementById("stat-total-tokens").textContent=(d.summary.total_tokens||0).toLocaleString();
      document.getElementById("stat-avg-latency").textContent=`${d.summary.avg_latency_ms||0} ms`;}
    const r=d.records||[];const tb=document.getElementById("usage-table-rows");if(!r.length)return;
    tb.innerHTML=r.slice(-50).reverse().map(u=>`<tr><td><code>${esc(u.request_id)}</code></td><td>${esc(u.model_id)}</td><td><code>${esc(u.channel_id)}</code></td><td>${u.latency_ms}ms</td><td>↑${u.input_tokens||0}/↓${u.output_tokens||0}</td><td>${u.status_code===200?'<span class="badge badge-success">200</span>':`<span class="badge badge-danger">${u.status_code}</span>`}</td></tr>`).join("");
  }catch(e){console.error(e);}
}
async function loadLogs(){
  try{const d=await api("/api/v1/logs");const r=d.records||[];
    const tb=document.getElementById("logs-table-rows");
    if(!r.length){tb.innerHTML=`<tr><td colspan="4" style="text-align:center;color:var(--text-muted);">暂无日志</td></tr>`;return;}
    tb.innerHTML=r.slice(-100).reverse().map(l=>{
      const level=l.error_class||"info";
      const levelBadge=level.includes("error")||level.includes("fail")?'<span class="badge badge-danger">WARN</span>':'<span class="badge badge-info">INFO</span>';
      return `<tr>
        <td>${l.created_at?new Date(l.created_at).toLocaleString():"-"}</td>
        <td>${levelBadge}</td>
        <td><code>${esc(l.model_id||"-")}</code></td>
        <td><code style="color:var(--text-muted);">${esc(l.channel_id||"-")}</code> ${esc(l.error_class||"")}</td>
      </tr>`;
    }).join("");
  }catch(e){console.error(e);}
}
// ---------- CLI sync (P1-6) ----------
// Never report success without a server-confirmed write: the button previews the
// real diff first, and only an explicit confirmation triggers the apply call.
const CLI_LABELS={claude:"Claude Code",codex:"Codex",hermes:"Hermes"};
let cliSyncTargets=[];

async function loadCLISync(){
  const cards=document.getElementById("cli-sync-cards");
  const hist=document.getElementById("cli-sync-history");
  if(!cards)return;
  try{
    const d=await api("/api/v1/cli-sync");
    if(!d.supported){
      cards.innerHTML=`<div class="metric-card"><span class="metric-label">${esc(d.message||"CLI 同步不可用")}</span></div>`;
      return;
    }
    cliSyncTargets=d.targets||[];
    cards.innerHTML=cliSyncTargets.map(t=>{
      const label=CLI_LABELS[t.cli]||t.cli;
      const badge=t.exists?'<span class="badge badge-success">已存在</span>':'<span class="badge badge-neutral">将新建</span>';
      return `<div class="metric-card">
        <span class="metric-value" style="font-size:1.15rem;margin-bottom:4px;">${esc(label)} ${badge}</span>
        <span class="metric-label" style="text-transform:none;font-family:ui-monospace,Menlo,monospace;word-break:break-all;">${esc(t.config_path)}</span>
        <div style="margin-top:16px;"><button class="btn btn-secondary" onclick="previewCLISync('${esc(t.cli)}')">同步</button></div>
      </div>`;
    }).join("")||`<div class="metric-card"><span class="metric-label">未检测到可同步的 CLI</span></div>`;

    const recs=d.records||[];
    if(hist){
      hist.innerHTML=recs.length?recs.slice(-30).reverse().map(rec=>{
        const ok=rec.status==="success";
        const badge=ok?'<span class="badge badge-success">成功</span>':`<span class="badge badge-danger">失败</span>`;
        const detail=ok?"":`<div style="color:var(--danger);font-size:12px;">${esc(rec.error)}</div>`;
        return `<tr><td>${rec.created_at?new Date(rec.created_at).toLocaleString():"-"}</td><td>${esc(CLI_LABELS[rec.cli]||rec.cli)}</td><td>${badge}${detail}</td><td><code style="word-break:break-all;">${esc(rec.config_path)}</code></td></tr>`;
      }).join(""):`<tr><td colspan="4" style="text-align:center;color:var(--text-muted);">暂无同步记录</td></tr>`;
    }
  }catch(e){
    cards.innerHTML=`<div class="metric-card"><span class="metric-label" style="color:var(--danger);">加载失败：${esc(e.message)}</span></div>`;
  }
}

function closeCLIDiff(){switchTab(_lastTab==="diff"?"cli-sync":_lastTab);}

async function previewCLISync(cli){
  try{
    const d=await api("/api/v1/cli-sync",{method:"POST",headers:{"Content-Type":"application/json"},
      body:JSON.stringify({cli,action:"preview",desired:{}})});
    const diff=d.diff||{};
    document.getElementById("cli-diff-title").textContent=`${CLI_LABELS[cli]||cli} · 同步预览`;
    document.getElementById("cli-diff-summary").textContent=
      `目标文件：${diff.target?.config_path||"-"}　变更项：${(diff.changed_keys||[]).join("、")||"无"}`;
    document.getElementById("cli-diff-old").textContent=diff.old_content||"（文件当前不存在）";
    document.getElementById("cli-diff-new").textContent=diff.new_content||"";
    const btn=document.getElementById("cli-diff-apply");
    btn.disabled=!diff.has_changes;
    btn.onclick=()=>applyCLISync(cli);
    switchTab("diff");
  }catch(e){showToast(`预览失败：${e.message}`);}
}

async function applyCLISync(cli){
  const btn=document.getElementById("cli-diff-apply");
  btn.disabled=true;
  try{
    const d=await api("/api/v1/cli-sync",{method:"POST",headers:{"Content-Type":"application/json"},
      body:JSON.stringify({cli,action:"apply",desired:{}})});
    closeCLIDiff();
    const backup=d.backup?.backup_path;
    showToast(`${CLI_LABELS[cli]||cli}：已写入并校验通过${backup?`（备份 ${backup.split("/").pop()}）`:""}`);
    loadCLISync();
  }catch(e){
    showToast(`同步失败：${e.message}`);
  }finally{btn.disabled=false;}
}

// ---------- Import / export (P1-7) ----------
function ioShow(text){const el=document.getElementById("io-result");el.style.display="block";el.textContent=text;}

async function doExport(){
  const includeSecrets=document.getElementById("io-include-secrets").checked;
  const password=document.getElementById("io-password").value;
  if(includeSecrets&&!password){showToast("包含凭据时必须设置密码");return;}
  try{
    const d=await api("/api/v1/import-export",{method:"POST",headers:{"Content-Type":"application/json"},
      body:JSON.stringify({action:"export",include_secrets:includeSecrets,password})});
    document.getElementById("io-package").value=d.package||"";
    ioShow(`导出成功，${(d.package||"").length} 字节。已填入下方文本框，可复制保存。`);
    showToast("配置已导出");
  }catch(e){ioShow(`导出失败：${e.message}`);}
}

async function doImportPreview(){
  const pkg=document.getElementById("io-package").value.trim();
  if(!pkg){showToast("请先粘贴配置包");return;}
  try{
    const d=await api("/api/v1/import-export",{method:"POST",headers:{"Content-Type":"application/json"},
      body:JSON.stringify({action:"preview",package:pkg})});
    const p=d.preview||{};
    const conflicts=(p.conflicts||[]).map(c=>`${c.type} ${c.id}${c.name?` (${c.name})`:""}`);
    ioShow(`包版本 ${p.version}　创建于 ${p.created_at?new Date(p.created_at).toLocaleString():"-"}\n`+
      `条目总数 ${p.total_entities}　含加密凭据：${p.has_secrets?"是":"否"}\n`+
      `冲突 ${conflicts.length} 项${conflicts.length?`：\n  - ${conflicts.join("\n  - ")}`:""}\n\n`+
      `确认无误后点「确认导入」。冲突策略：${document.getElementById("io-policy").value==="overwrite"?"覆盖":"跳过"}`);
    document.getElementById("io-apply-btn").disabled=false;
  }catch(e){
    ioShow(`预览失败：${e.message}`);
    document.getElementById("io-apply-btn").disabled=true;
  }
}

async function doImportApply(){
  const pkg=document.getElementById("io-package").value.trim();
  if(!pkg){showToast("请先粘贴配置包");return;}
  const btn=document.getElementById("io-apply-btn");
  btn.disabled=true;
  try{
    const d=await api("/api/v1/import-export",{method:"POST",headers:{"Content-Type":"application/json"},
      body:JSON.stringify({action:"apply",package:pkg,password:document.getElementById("io-password").value,
        policy:document.getElementById("io-policy").value})});
    ioShow(`导入成功（策略：${d.policy}）。路由快照已刷新。`);
    showToast("配置已导入");
    loadOverview();
  }catch(e){
    ioShow(`导入失败：${e.message}`);
    btn.disabled=false;
  }
}

// ---------- Gateway access keys ----------
function copyText(elId){
  const el=document.getElementById(elId);
  const text=el?.textContent||el?.value||"";
  if(navigator.clipboard&&text){navigator.clipboard.writeText(text).then(()=>showToast(T("toast_copied"))).catch(()=>fallbackCopy(text));}
  else fallbackCopy(text);
}
function fallbackCopy(text){
  const ta=document.createElement("textarea");ta.value=text;document.body.appendChild(ta);ta.select();
  try{document.execCommand("copy");showToast(T("toast_copied"));}catch{}
  document.body.removeChild(ta);
}
async function loadGatewayKeys(){
  const box=document.getElementById("gw-keys-list");
  if(!box)return;
  try{
    const d=await api("/api/v1/keys");
    const keys=d.keys||[];
    if(!keys.length){box.innerHTML=`<span style="color:var(--text-muted);font-size:13px;">${T("gw_no_keys")}</span>`;return;}
    box.innerHTML=keys.map(k=>`
      <div class="map-row" style="align-items:center;">
        <code style="flex:1;font-size:13px;">${esc(k.id)}<span style="color:var(--text-muted);">···（已隐藏）</span></code>
        <button class="btn btn-danger" style="padding:3px 10px;font-size:12px;" onclick="revokeGatewayKey('${esc(k.id)}')">${T("btn_delete")}</button>
      </div>`).join("");
  }catch(e){box.innerHTML=`<span style="color:var(--danger);font-size:13px;">${esc(e.message)}</span>`;}
}
async function mintGatewayKey(){
  try{
    const d=await api("/api/v1/keys",{method:"POST",headers:{"Content-Type":"application/json"},body:"{}"});
    // The raw key is returned ONCE — surface it prominently so the user copies it now.
    const box=document.getElementById("gw-keys-list");
    const banner=document.createElement("div");
    banner.className="form-card";
    banner.style.cssText="border:1px solid var(--accent);padding:12px;margin-bottom:8px;";
    banner.innerHTML=`<div style="font-size:12px;color:var(--text-secondary);margin-bottom:6px;">${T("gw_new_key_once")}</div>
      <div style="display:flex;gap:8px;align-items:center;">
        <code id="gw-fresh-key" style="flex:1;font-size:13px;word-break:break-all;">${esc(d.key)}</code>
        <button class="btn btn-primary" style="padding:3px 12px;font-size:12px;white-space:nowrap;" onclick="copyText('gw-fresh-key')">${T("btn_copy")}</button>
      </div>`;
    box.parentNode.insertBefore(banner,box);
    showToast(T("toast_key_created"));
    loadGatewayKeys();
  }catch(e){showToast(e.message);}
}
async function revokeGatewayKey(id){
  confirmAction(`${T("confirm_delete")} ${id}?`,async()=>{ try{
    await api(`/api/v1/keys/${id}`,{method:"DELETE"});
    showToast(T("toast_deleted"));
    loadGatewayKeys();
  }catch(e){showToast(e.message);}
  });
}

// ---------- Settings ----------
async function loadSettings(){
  loadGatewayKeys();
  try{
    const d=await api("/api/v1/browser");
    const statusEl=document.getElementById("set-browser-status");
    const tierEl=document.getElementById("set-browser-tier");
    const pathEl=document.getElementById("set-browser-path");
    const verEl=document.getElementById("set-browser-version");
    if(!statusEl)return;

    if(d.available){
      statusEl.className="badge badge-success";
      statusEl.textContent=T("browser_available");
      tierEl.className="badge badge-info";
      tierEl.textContent=`Tier ${d.tier||1}`;
      pathEl.textContent=d.path||"--";
      verEl.textContent=d.version||"--";
    }else{
      statusEl.className="badge badge-danger";
      statusEl.textContent=T("browser_unavailable");
      tierEl.className="badge badge-neutral";
      tierEl.textContent=`Tier ${d.tier||3}`;
      pathEl.textContent=d.path||(d.error?`(${d.error})`:"--");
      verEl.textContent=d.version||"--";
    }
  }catch(e){console.error("loadSettings error:", e);}
}

setLanguage(currentLang);
if(location.hash)syncHashRoute();else loadOverview();
