/* Relay Panel 前端（无依赖 vanilla JS） */
"use strict";

const state = { user: null, version: "", view: "dashboard" };

/* ---------- utils ---------- */
async function api(path, opts = {}) {
  const res = await fetch(path, {
    headers: { "Content-Type": "application/json" },
    ...opts,
    body: opts.body ? JSON.stringify(opts.body) : undefined,
  });
  const data = await res.json().catch(() => ({}));
  if (res.status === 401) { showLogin(); throw new Error(data.error || "未登录"); }
  if (!res.ok) throw new Error(data.error || `HTTP ${res.status}`);
  return data;
}
const $ = (sel, el = document) => el.querySelector(sel);
const esc = (s) => String(s ?? "").replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));
const fmtBytes = (b) => b >= 1 << 30 ? (b / 1073741824).toFixed(1) + " GB" : b >= 1 << 20 ? (b / 1048576).toFixed(0) + " MB" : b >= 1024 ? (b / 1024).toFixed(0) + " KB" : b + " B";

function toast(msg, ok = true) {
  const el = document.createElement("div");
  el.className = "toast " + (ok ? "ok" : "err");
  el.textContent = msg;
  $("#toast-root").appendChild(el);
  setTimeout(() => el.remove(), 3600);
}

function openModal(html) {
  const root = $("#modal-root");
  root.innerHTML = `<div class="modal-mask"><div class="modal">${html}</div></div>`;
  const mask = $(".modal-mask", root);
  mask.addEventListener("click", (e) => { if (e.target === mask) closeModal(); });
  return root;
}
function closeModal() { $("#modal-root").innerHTML = ""; }

/* ---------- advanced param meta ---------- */
const ADV = [
  ["POOL_SIZE", "预连接池大小（0 关闭）", 24, "0-256"],
  ["REFILL_BATCH", "每轮补连接并发数", 8, "1-256"],
  ["CONNECT_TIMEOUT", "出站连接超时(秒)", 5, "1-120"],
  ["IDLE_TIMEOUT", "空闲连接回收(秒)", 240, "30-86400"],
  ["HALF_CLOSE_TIMEOUT", "半关闭保留(秒)", 10, "1-300"],
  ["PRECONNECT_TTL_MS", "预连接轮换(毫秒)", 50000, "10000-3600000"],
  ["SPLICE_CHUNK", "splice 缓冲(字节)", 262144, "16384-1048576"],
  ["UDP_IDLE_TIMEOUT", "UDP 会话回收(秒)", 60, "5-3600"],
  ["UDP_SOCKET_BUFFER", "UDP 缓冲(字节)", 4194304, "65536-67108864"],
  ["LISTEN_BACKLOG", "listen backlog", 16384, "128-65535"],
  ["LOG_ENABLE", "日志开关(1/0)", 1, "0-1"],
  ["LOG_RATE_PER_SEC", "每秒日志上限", 24, "0-10000"],
  ["TCP_KEEPIDLE", "keepalive 起始(秒)", 360, "30-86400"],
  ["TCP_KEEPINTVL", "keepalive 间隔(秒)", 15, "1-3600"],
  ["TCP_KEEPCNT", "keepalive 次数", 1, "1-30"],
  ["TCP_USER_TIMEOUT_MS", "未确认数据等待(ms)", 0, "0-3600000"],
];

/* ---------- login ---------- */
function showLogin() {
  state.user = null;
  $("#app").innerHTML = `
  <div class="login-wrap">
    <div class="login-card">
      <div class="logo">🛰️</div>
      <h1>Relay Panel</h1>
      <div class="sub">TCP/UDP 预连接转发管理面板</div>
      <form id="login-form">
        <label class="field"><span class="lab">用户名</span>
          <input type="text" name="username" autocomplete="username" autofocus required>
        </label>
        <label class="field"><span class="lab">密码</span>
          <input type="password" name="password" autocomplete="current-password" required>
        </label>
        <button class="btn" style="width:100%;justify-content:center" type="submit">登 录</button>
      </form>
    </div>
  </div>`;
  $("#login-form").addEventListener("submit", async (e) => {
    e.preventDefault();
    const fd = new FormData(e.target);
    try {
      const data = await api("/api/login", { method: "POST", body: { username: fd.get("username"), password: fd.get("password") } });
      state.user = data.user;
      await boot();
    } catch (err) { toast(err.message, false); }
  });
}

async function boot() {
  try {
    const me = await api("/api/me");
    state.user = me.user; state.version = me.version;
  } catch { return; }
  renderLayout();
  renderView();
}

/* ---------- layout ---------- */
function renderLayout() {
  const isAdmin = state.user.is_admin;
  $("#app").innerHTML = `
  <div class="layout">
    <div class="sidebar">
      <div class="brand">🛰️ Relay Panel<small>${esc(state.version)}</small></div>
      <div class="nav-item active" data-view="dashboard">📊 仪表盘</div>
      <div class="nav-item" data-view="traffic">📈 流量统计</div>
      <div class="nav-item" data-view="rules">🔀 转发管理</div>
      <div class="nav-item" data-view="nodes" style="${isAdmin ? "" : "display:none"}">🌐 节点管理</div>
      <div class="nav-item" data-view="system" style="${isAdmin ? "" : "display:none"}">⚙️ 系统调优</div>
      <div class="nav-item" data-view="users" style="${isAdmin ? "" : "display:none"}">👥 用户管理</div>
      <div class="nav-item" data-view="password">🔑 修改密码</div>
      <div class="spacer"></div>
      <div class="user-box">
        <div>${esc(state.user.username)} ${isAdmin ? '<span class="badge blue">管理员</span>' : ""}</div>
        <a href="javascript:void(0)" id="logout-link">退出登录</a>
      </div>
    </div>
    <div class="main" id="view-root"></div>
  </div>`;
  document.querySelectorAll(".nav-item").forEach((el) =>
    el.addEventListener("click", () => {
      document.querySelectorAll(".nav-item").forEach((x) => x.classList.remove("active"));
      el.classList.add("active");
      state.view = el.dataset.view;
      renderView();
    }));
  $("#logout-link").addEventListener("click", async () => {
    await api("/api/logout", { method: "POST" }).catch(() => {});
    showLogin();
  });
}

function renderView() {
  ({ dashboard: renderDashboard, traffic: renderTraffic, rules: renderRules, nodes: renderNodes, system: renderSystem, users: renderUsers, password: renderPassword })[state.view]();
}

/* ---------- dashboard ---------- */
async function renderDashboard() {
  const root = $("#view-root");
  root.innerHTML = `<div class="empty">加载中…</div>`;
  let d;
  try { d = await api("/api/overview"); } catch (e) { root.innerHTML = `<div class="empty">${esc(e.message)}</div>`; return; }
  const t = d.tuning || {};
  root.innerHTML = `
    <div class="page-head"><div><h2>仪表盘</h2><div class="desc">${esc(d.version || "")}</div></div></div>
    <div class="grid-stats">
      <div class="stat"><div class="k">转发规则</div><div class="v">${d.rules_running} <small>运行 / ${d.rules_total} 总计（${d.rules_stopped} 停止）</small></div></div>
      <div class="stat"><div class="k">节点</div><div class="v">${d.nodes_online} <small>在线 / ${d.nodes_total} 总计</small></div></div>
      <div class="stat"><div class="k">累计流量</div><div class="v">${fmtBytes(d.traffic_total)} <small>自实例启动起</small></div></div>
      <div class="stat"><div class="k">用户</div><div class="v">${d.users_total} <small>个账号</small></div></div>
      <div class="stat"><div class="k">本机负载</div><div class="v">${(d.machine.cpu_cores || 0)} 核 <small>${esc(d.machine.load_avg || "-")}</small></div></div>
      <div class="stat"><div class="k">拥塞算法</div><div class="v">${esc(t.congestion_control || "-")} ${t.bbr_active ? '<span class="badge green">BBR</span>' : ""}</div></div>
    </div>
    ${(d.over_quota && d.over_quota.length) ? `
    <div class="card" style="border-color:rgba(255,92,114,.5)">
      <h3>⚠️ 超配额转发</h3>
      <p class="hint">以下转发已达流量配额被自动停机：${d.over_quota.map(esc).join("、")}。可在「转发管理」编辑配额后重新启动。</p>
    </div>` : ""}
    <div class="card">
      <h3>本机机器信息</h3>
      <div class="hint">
        系统 ${esc(d.machine.os || "-")} · 内核 ${esc(d.machine.kernel || "-")} ·
        内存 ${fmtBytes(d.machine.mem_avail || 0)} 可用 / ${fmtBytes(d.machine.mem_total || 0)}
      </div>
    </div>`;
}

/* ---------- traffic view（cc-switch 风格流量统计） ---------- */
let trafficRange = "24h";

async function renderTraffic() {
  const root = $("#view-root");
  root.innerHTML = `<div class="empty">加载中…</div>`;
  let d;
  try { d = await api(`/api/traffic?range=${trafficRange}`); } catch (e) { root.innerHTML = `<div class="empty">${esc(e.message)}</div>`; return; }

  const tabs = [["24h", "今天"], ["7d", "近 7 天"], ["30d", "近 30 天"], ["365d", "全年"]];
  const t = d.totals || {};
  root.innerHTML = `
    <div class="page-head">
      <div><h2>流量统计</h2><div class="desc">基于转发内核计数采样（每分钟），5 分钟粒度聚合，保留 48h 明细 + 400 天日汇总</div></div>
      <div class="range-tabs">
        ${tabs.map(([k, label]) => `<div class="rtab${trafficRange === k ? " active" : ""}" data-range="${k}">${label}</div>`).join("")}
      </div>
    </div>
    <div class="grid-stats">
      <div class="stat"><div class="k">⬇️ 下载（远端→客户端）</div><div class="v">${fmtBytes(t.down || 0)}</div></div>
      <div class="stat"><div class="k">⬆️ 上传（客户端→远端）</div><div class="v">${fmtBytes(t.up || 0)}</div></div>
      <div class="stat"><div class="k">💠 合计流量</div><div class="v">${fmtBytes((t.up || 0) + (t.down || 0))}</div></div>
      <div class="stat"><div class="k">🔌 新建连接</div><div class="v">${t.conns || 0} <small>次</small></div></div>
    </div>
    <div class="card">
      <div style="display:flex;justify-content:space-between;align-items:center;margin-bottom:10px">
        <h3 style="margin:0">流量趋势</h3>
        <div class="legend">
          <span><i style="background:#3b82f6"></i>下载</span>
          <span><i style="background:#22c55e"></i>上传</span>
        </div>
      </div>
      <canvas id="traffic-chart" style="width:100%;height:260px"></canvas>
    </div>
    <div class="card">
      <h3>年度热力图</h3>
      <div class="heatmap-wrap" id="heatmap"></div>
    </div>
    <div class="card" style="padding:0;overflow-x:auto">
      <div style="display:flex;justify-content:space-between;align-items:center;padding:16px 20px 0">
        <h3 style="margin:0">分组统计（本期）</h3>
        <div class="range-tabs" id="group-mode">
          <div class="rtab active" data-mode="rule">按转发</div>
          <div class="rtab" data-mode="node">按节点</div>
        </div>
      </div>
      <table>
        <thead><tr><th>名称</th><th>节点</th><th>下载</th><th>上传</th><th>合计</th><th style="width:220px">占比</th><th>连接</th></tr></thead>
        <tbody id="group-rows"></tbody>
      </table>
    </div>`;

  root.querySelectorAll(".rtab[data-range]").forEach(el =>
    el.addEventListener("click", () => { trafficRange = el.dataset.range; renderTraffic(); }));
  drawTrafficChart($("#traffic-chart"), d.points || [], trafficRange);
  drawHeatmap($("#heatmap"), d.heatmap || []);
  let groupMode = "rule";
  const renderRows = () => {
    const nodeName = state.nodesCache && state.nodesCache[0] ? "" : "";
    const rows = d.rows || [];
    let groups;
    if (groupMode === "node") {
      const m = new Map();
      for (const r of rows) {
        const k = r.node_id || 0;
        const g = m.get(k) || { tag: k === 0 ? "本机" : `节点 #${k}`, node_id: k, up: 0, down: 0, conns: 0 };
        g.up += r.up; g.down += r.down; g.conns += r.conns;
        m.set(k, g);
      }
      groups = [...m.values()].sort((a, b) => (b.up + b.down) - (a.up + a.down));
    } else {
      groups = rows;
    }
    const total = groups.reduce((s, g) => s + g.up + g.down, 0) || 1;
    $("#group-rows").innerHTML = groups.length === 0
      ? `<tr><td colspan="7"><div class="empty">本期没有流量数据</div></td></tr>`
      : groups.map((g) => {
        const v = g.up + g.down;
        const pct = (v / total * 100);
        return `<tr>
          <td><b>${esc(g.tag)}</b></td>
          <td>${groupMode === "rule" ? (g.node_id === 0 ? '<span class="badge gray">本机</span>' : `<span class="badge blue">#${g.node_id}</span>`) : "-"}</td>
          <td class="mono">${fmtBytes(g.down)}</td>
          <td class="mono">${fmtBytes(g.up)}</td>
          <td class="mono"><b>${fmtBytes(v)}</b></td>
          <td><div class="traffic-bar" style="width:100%"><div class="traffic-fill" style="width:${pct.toFixed(1)}%"></div></div><span class="hint">${pct.toFixed(1)}%</span></td>
          <td class="mono">${g.conns}</td>
        </tr>`;
      }).join("");
  };
  renderRows();
  $("#group-mode").querySelectorAll(".rtab").forEach(el =>
    el.addEventListener("click", () => {
      groupMode = el.dataset.mode;
      $("#group-mode").querySelectorAll(".rtab").forEach(x => x.classList.toggle("active", x === el));
      renderRows();
    }));
}

/* canvas 堆叠面积图：下载 + 上传 */
function drawTrafficChart(canvas, points, range) {
  const dpr = window.devicePixelRatio || 1;
  const W = canvas.clientWidth || 800, H = canvas.clientHeight || 260;
  canvas.width = W * dpr; canvas.height = H * dpr;
  const ctx = canvas.getContext("2d");
  ctx.scale(dpr, dpr);
  ctx.clearRect(0, 0, W, H);

  const padL = 64, padR = 14, padT = 14, padB = 30;
  const cw = W - padL - padR, ch = H - padT - padB;
  if (!points.length) {
    ctx.fillStyle = "#8b97b3"; ctx.font = "13px sans-serif"; ctx.textAlign = "center";
    ctx.fillText("暂无数据", W / 2, H / 2);
    return;
  }
  const n = points.length;
  const totals = points.map(p => (p.up || 0) + (p.down || 0));
  const maxV = Math.max(...totals, 1024) * 1.15;
  const x = (i) => padL + (i / Math.max(1, n - 1)) * cw;
  const y = (v) => padT + ch - (v / maxV) * ch;

  // 网格与 y 轴
  ctx.strokeStyle = "rgba(139,151,179,.15)"; ctx.lineWidth = 1;
  ctx.fillStyle = "#8b97b3"; ctx.font = "11px ui-monospace,Menlo,monospace"; ctx.textAlign = "right";
  for (let g = 0; g <= 4; g++) {
    const v = maxV * g / 4, gy = y(v);
    ctx.beginPath(); ctx.moveTo(padL, gy); ctx.lineTo(W - padR, gy); ctx.stroke();
    ctx.fillText(fmtBytes(v).replace(" ", ""), padL - 8, gy + 3.5);
  }
  // x 轴标签
  ctx.textAlign = "center";
  const step = Math.max(1, Math.floor(n / 7));
  for (let i = 0; i < n; i += step) {
    const t = new Date((points[i].t || 0) * 1000);
    const label = range === "24h" ? `${t.getHours()}:00`
      : range === "7d" ? `${t.getMonth() + 1}/${t.getDate()} ${t.getHours()}时`
      : `${t.getMonth() + 1}/${t.getDate()}`;
    ctx.fillText(label, x(i), H - 8);
  }

  // 堆叠面积：down 在下（蓝），up 叠上（绿）
  const area = (getV, fill, stroke) => {
    ctx.beginPath();
    for (let i = 0; i < n; i++) ctx.lineTo(x(i), y(getV(points[i], i)));
    ctx.lineTo(x(n - 1), y(0)); ctx.lineTo(x(0), y(0)); ctx.closePath();
    const grad = ctx.createLinearGradient(0, padT, 0, padT + ch);
    grad.addColorStop(0, fill); grad.addColorStop(1, "rgba(0,0,0,0)");
    ctx.fillStyle = grad; ctx.fill();
    ctx.beginPath();
    for (let i = 0; i < n; i++) ctx.lineTo(x(i), y(getV(points[i], i)));
    ctx.strokeStyle = stroke; ctx.lineWidth = 2; ctx.stroke();
  };
  area((p) => p.down || 0, "rgba(59,130,246,.35)", "#3b82f6");
  area((p, i) => (p.down || 0) + (p.up || 0), "rgba(52,201,142,.22)", "#22c55e");
}

/* GitHub 风格年度热力图 */
function drawHeatmap(el, days) {
  const map = new Map(days.map((d) => [d.date, (d.up || 0) + (d.down || 0)]));
  const today = new Date();
  today.setHours(0, 0, 0, 0);
  const end = new Date(today); end.setDate(end.getDate() + (6 - end.getDay())); //到本周日
  const start = new Date(end); start.setDate(start.getDate() - 52 * 7);        //53 周
  const weeks = Math.ceil((end - start) / 7 / 86400000);
  const vals = [...map.values()].filter((v) => v > 0);
  const max = Math.max(...vals, 1);
  const lvl = (v) => v <= 0 ? 0 : v < max * 0.25 ? 1 : v < max * 0.5 ? 2 : v < max * 0.75 ? 3 : 4;
  const colors = ["#1d2639", "rgba(59,130,246,.25)", "rgba(59,130,246,.5)", "rgba(59,130,246,.75)", "#3b82f6"];
  const cell = 11, gap = 3;
  const monthNames = ["1月","2月","3月","4月","5月","6月","7月","8月","9月","10月","11月","12月"];

  let html = `<div style="display:flex;gap:14px;overflow-x:auto;padding:4px 0">`;
  html += `<div class="hm-months" style="position:relative;height:16px;flex:1;min-width:${weeks * (cell + gap)}px"></div>`;
  html += `<div style="display:flex;gap:${gap}px">`;
  const dayLabels = ["", "一", "", "三", "", "五", ""];
  html += `<div style="display:flex;flex-direction:column;gap:${gap}px;font-size:10px;color:var(--text-dim)">${dayLabels.map((l) => `<div style="height:${cell}px;line-height:${cell}px">${l}</div>`).join("")}</div>`;
  let monthSpans = [];
  let lastMonth = -1;
  for (let w = 0; w < weeks; w++) {
    html += `<div style="display:flex;flex-direction:column;gap:${gap}px">`;
    for (let dow = 0; dow < 7; dow++) {
      const day = new Date(start);
      day.setDate(day.getDate() + w * 7 + dow);
      if (day > today) {
        html += `<div style="width:${cell}px;height:${cell}px;border-radius:2.5px;background:transparent"></div>`;
        continue;
      }
      const key = `${day.getFullYear()}-${String(day.getMonth() + 1).padStart(2, "0")}-${String(day.getDate()).padStart(2, "0")}`;
      const v = map.get(key) || 0;
      if (dow === 0 && day.getMonth() !== lastMonth) {
        lastMonth = day.getMonth();
        monthSpans.push({ left: w * (cell + gap), label: monthNames[day.getMonth()] });
      }
      html += `<div style="width:${cell}px;height:${cell}px;border-radius:2.5px;background:${colors[lvl(v)]}" title="${key} · ${fmtBytes(v)}"></div>`;
    }
    html += `</div>`;
  }
  html += `</div></div>`;
  html += `<div class="hm-months" style="position:relative;height:16px;margin-top:6px;min-width:${weeks * (cell + gap)}px">${
    monthSpans.map((m) => `<span style="position:absolute;left:${m.left}px;font-size:10.5px;color:var(--text-dim)">${m.label}</span>`).join("")
  }</div>`;
  html += `<div style="display:flex;align-items:center;gap:6px;margin-top:10px;font-size:11px;color:var(--text-dim)">少
    ${colors.map((c) => `<i style="width:${cell}px;height:${cell}px;border-radius:2.5px;background:${c};display:inline-block"></i>`).join("")} 多</div>`;
  el.innerHTML = html;
}

/* ---------- rules view ---------- */
async function renderRules() {
  const root = $("#view-root");
  root.innerHTML = `<div class="empty">加载中…</div>`;
  let data;
  try { data = await api("/api/rules"); } catch (e) { root.innerHTML = `<div class="empty">${esc(e.message)}</div>`; return; }
  const rules = data.rules || [];

  root.innerHTML = `
    <div class="page-head">
      <div><h2>转发管理</h2><div class="desc">共 ${rules.length} 条转发；远端规则的变更在节点下次轮询（约 5 秒内）生效</div></div>
      <div style="display:flex;gap:8px;flex-wrap:wrap">
        ${state.user.is_admin ? `<button class="btn secondary" id="btn-export">导出</button>
        <button class="btn secondary" id="btn-import">导入</button>` : ""}
        <button class="btn" id="btn-new">＋ 新建转发</button>
      </div>
    </div>
    <div class="card" id="batch-bar" style="display:none;margin-bottom:14px;padding:12px 16px">
      <div style="display:flex;align-items:center;gap:10px;flex-wrap:wrap">
        <b id="batch-count">0</b><span class="hint">条已选</span>
        <button class="btn sm" id="batch-start">批量启动</button>
        <button class="btn sm secondary" id="batch-stop">批量停止</button>
        <button class="btn sm danger" id="batch-del">批量删除</button>
        <button class="btn sm secondary" id="batch-clear">取消选择</button>
      </div>
    </div>
    <div class="card" style="padding:0;overflow-x:auto">
      <table id="rules-table">
        <thead><tr>
          <th style="width:34px"><input type="checkbox" id="sel-all"></th>
          <th>标签</th><th>节点</th><th>监听</th><th>转发到</th><th>协议</th>
          <th>流量 / 配额</th>
          ${state.user.is_admin ? "<th>归属</th>" : ""}
          <th>状态</th><th>期望</th><th>操作</th>
        </tr></thead>
        <tbody>
        ${rules.length === 0 ? `<tr><td colspan="11"><div class="empty">还没有转发，点击右上角「新建转发」创建第一条</div></td></tr>` : ""}
        ${rules.map((r) => trafficCell(r)).join("")}
        </tbody>
      </table>
    </div>`;

  // 批量选择
  const selected = new Set();
  const refreshBar = () => {
    $("#batch-count").textContent = selected.size;
    $("#batch-bar").style.display = selected.size ? "" : "none";
  };
  $("#sel-all").addEventListener("change", (e) => {
    document.querySelectorAll(".rule-check").forEach((c) => { c.checked = e.target.checked; });
    selected.clear();
    if (e.target.checked) rules.forEach((r) => selected.add(r.tag));
    refreshBar();
  });
  document.querySelectorAll(".rule-check").forEach((c) =>
    c.addEventListener("change", () => {
      if (c.checked) selected.add(c.dataset.tag); else selected.delete(c.dataset.tag);
      refreshBar();
    }));
  const batch = async (action) => {
    if (!selected.size) return;
    if (action === "delete" && !confirm(`确认批量${action === "delete" ? "删除" : "操作"} ${selected.size} 条转发？`)) return;
    try {
      const res = await api("/api/rules/batch", { method: "POST", body: { tags: [...selected], action } });
      toast(`批量操作完成：${res.done} 成功${res.skipped ? "，" + res.skipped + " 跳过" : ""}`);
      renderRules();
    } catch (e) { toast(e.message, false); }
  };
  $("#batch-start").addEventListener("click", () => batch("start"));
  $("#batch-stop").addEventListener("click", () => batch("stop"));
  $("#batch-del").addEventListener("click", () => batch("delete"));
  $("#batch-clear").addEventListener("click", () => renderRules());

  // 导入导出
  if (state.user.is_admin) {
    $("#btn-export").addEventListener("click", async () => {
      const d = await api("/api/rules/export");
      cmdModal("导出的转发规则（JSON）", JSON.stringify(d.rules, null, 2));
    });
    $("#btn-import").addEventListener("click", () => importModal());
  }

  $("#btn-new").addEventListener("click", () => ruleModal(null));
  root.querySelectorAll("tr[data-tag]").forEach((tr) => {
    const tag = tr.dataset.tag;
    const rule = rules.find((r) => r.tag === tag);
    $(".act-toggle", tr).addEventListener("click", () => ruleAction(rule, (rule.desired_state || "running") === "running" ? "stop" : "start"));
    if (rule.node_id === 0) $(".act-logs", tr).addEventListener("click", () => logsModal(rule));
    $(".act-edit", tr).addEventListener("click", () => ruleModal(rule));
    $(".act-del", tr).addEventListener("click", () => deleteRule(rule));
  });
}

function trafficCell(r) {
  const st = r.stats || {};
  const used = (st.up || 0) + (st.down || 0);
  const quota = r.traffic_quota_bytes || 0;
  const pct = quota ? Math.min(100, (used / quota) * 100) : 0;
  const quotaInfo = quota
    ? `<div class="traffic-bar"><div class="traffic-fill${pct >= 100 ? " over" : pct >= 80 ? " warn" : ""}" style="width:${pct}%"></div></div>
       <span class="hint">${fmtBytes(used)} / ${fmtBytes(quota)}${st.over_quota || r.over_quota ? ' <span class="badge red">超限</span>' : ""}</span>`
    : `<span class="hint">${fmtBytes(used)}${r.rate_limit_kbps ? ` · 限速 ${r.rate_limit_kbps}KB/s` : ""}</span>`;
  return `
    <tr data-tag="${esc(r.tag)}">
      <td><input type="checkbox" class="rule-check" data-tag="${esc(r.tag)}"></td>
      <td><b>${esc(r.tag)}</b>${r.remark ? `<div class="hint">${esc(r.remark)}</div>` : ""}</td>
      <td>${r.node_id === 0 ? '<span class="badge gray">本机</span>' : `<span class="badge blue">${esc(r.node_name)}</span>`}</td>
      <td class="mono">${esc(r.listen_ip)}:${r.listen_port}</td>
      <td class="mono">${esc(r.remote_host)}<br><span class="hint">tcp:${r.remote_tcp_port} / udp:${r.remote_udp_port}</span></td>
      <td>${r.enable_tcp ? '<span class="badge blue">TCP</span> ' : ""}${r.enable_udp ? '<span class="badge yellow">UDP</span>' : ""}</td>
      <td>${quotaInfo}</td>
      ${state.user.is_admin ? `<td>${esc(r.owner)}${r.my_rule ? ' <span class="badge gray">我</span>' : ""}</td>` : ""}
      <td>${statusBadge(r.status.active)}</td>
      <td>${(r.desired_state || "running") === "running" ? '<span class="badge green">运行</span>' : '<span class="badge gray">停止</span>'}</td>
      <td><div class="actions-cell">
        <button class="btn sm secondary act-toggle" data-act="toggle">${(r.desired_state || "running") === "running" ? "停止" : "启动"}</button>
        ${r.node_id === 0 ? '<button class="btn sm secondary act-logs">日志</button>' : ""}
        <button class="btn sm secondary act-edit">编辑</button>
        <button class="btn sm danger act-del">删除</button>
      </div></td>
    </tr>`;
}

function importModal() {
  openModal(`
    <div class="modal-head"><h3>导入转发规则</h3>
      <button class="modal-close" onclick="document.querySelector('.modal-mask').remove()">×</button></div>
    <div class="modal-body">
      <p class="hint" style="margin-bottom:12px">粘贴「导出」得到的 JSON 规则数组；已存在的标签会跳过，导入的规则一律落在本机（可再编辑迁移到节点）。</p>
      <textarea id="imp-json" style="width:100%;height:260px;background:var(--bg-card2);color:var(--text);border:1px solid var(--border);border-radius:8px;padding:10px;font-family:ui-monospace,Menlo,monospace;font-size:12.5px" placeholder='[{"tag":"HK01",...}]'></textarea>
    </div>
    <div class="modal-foot">
      <button class="btn secondary" id="m-cancel">取消</button>
      <button class="btn" id="m-import">导入</button>
    </div>`);
  $("#m-cancel").addEventListener("click", closeModal);
  $("#m-import").addEventListener("click", async () => {
    let rules;
    try { rules = JSON.parse($("#imp-json").value); } catch { toast("JSON 解析失败", false); return; }
    if (!Array.isArray(rules)) { toast("需要 JSON 数组", false); return; }
    try {
      const res = await api("/api/rules/import", { method: "POST", body: { rules } });
      toast(`导入完成：${res.imported} 成功，${res.skipped} 跳过，${res.failed} 失败${res.error ? "（" + res.error + "）" : ""}`, res.failed === 0);
      closeModal();
      renderRules();
    } catch (e) { toast(e.message, false); }
  });
}

function statusBadge(active) {
  if (active === "active") return '<span class="badge green">运行中</span>';
  if (active === "failed") return '<span class="badge red">失败</span>';
  if (active === "inactive") return '<span class="badge gray">已停止</span>';
  if (active === "activating") return '<span class="badge yellow">启动中</span>';
  if (active === "pending") return '<span class="badge yellow">待节点上报</span>';
  if (active === "node-offline") return '<span class="badge red">节点离线</span>';
  return `<span class="badge gray">${esc(active)}</span>`;
}

async function ruleAction(rule, action) {
  try {
    await api(`/api/rules/${encodeURIComponent(rule.tag)}/${action}`, { method: "POST" });
    toast(action === "start" ? `已下发启动指令：${rule.tag}` : `已下发停止指令：${rule.tag}`);
    renderRules();
  } catch (e) { toast(e.message, false); }
}

async function deleteRule(rule) {
  const where = rule.node_id === 0 ? "" : `\n（远端规则：删除后节点会在下次轮询时自动清理实例）`;
  if (!confirm(`确认删除转发 [${rule.tag}]？\n监听 ${rule.listen_ip}:${rule.listen_port} → ${rule.remote_host}${where}`)) return;
  try {
    await api(`/api/rules/${encodeURIComponent(rule.tag)}`, { method: "DELETE" });
    toast(`已删除 ${rule.tag}`);
    renderRules();
  } catch (e) { toast(e.message, false); }
}

function advGrid(adv) {
  return `
    <span class="adv-toggle" id="adv-toggle">▸ 高级参数（可选，默认使用程序内置值）</span>
    <div class="adv-grid" id="adv-grid">
      ${ADV.map(([k, d, def, range]) => `
        <div class="adv-item">
          <span class="k" title="${esc(k)}">${esc(k)}</span>
          <input type="number" data-adv="${esc(k)}" placeholder="${def}" value="${adv && adv[k] !== undefined ? adv[k] : ""}">
          <span class="d">${esc(d)} · ${range}</span>
        </div>`).join("")}
    </div>`;
}

function ruleModal(rule) {
  const isEdit = !!rule;
  rule = rule || { listen_ip: "0.0.0.0", enable_tcp: true, enable_udp: true, remote_udp_port: null, node_id: 0 };
  openModal(`
    <div class="modal-head"><h3>${isEdit ? "编辑转发" : "新建转发"}</h3>
      <button class="modal-close" onclick="document.querySelector('.modal-mask').remove()">×</button></div>
    <div class="modal-body">
      <div class="form-row">
        <label class="field"><span class="lab"><b>标签</b>（实例名 tcp-pool@标签）</span>
          <input type="text" id="f-tag" value="${esc(rule.tag || "")}" ${isEdit ? "disabled" : ""} placeholder="如 HK-JP-01">
          <div class="hint">字母/数字/_/-，创建后不可修改</div></label>
        <label class="field"><span class="lab"><b>运行节点</b></span>
          <select id="f-node"><option value="0">本机（主控服务器）</option></select>
          <div class="hint">远端节点需先在「节点管理」添加并完成被控安装</div></label>
      </div>
      <div class="form-row">
        <label class="field"><span class="lab"><b>备注</b>（可选）</span>
          <input type="text" id="f-remark" value="${esc(rule.remark || "")}" placeholder="如 电信优化线路"></label>
        <label class="field"><span class="lab"><b>监听 IP</b>（所选节点上的网卡地址）</span>
          <input type="text" id="f-lip" value="${esc(rule.listen_ip)}" placeholder="0.0.0.0 或具体 IP / IPv6">
          <div class="hint">0.0.0.0 = 全部 IPv4；:: = 全部 IPv6</div></label>
      </div>
      <div class="form-row">
        <label class="field"><span class="lab"><b>监听端口</b></span>
          <input type="number" id="f-lport" value="${rule.listen_port || ""}" placeholder="如 20000"></label>
        <label class="field"><span class="lab"><b>远端地址</b>（落地机 IP 或域名）</span>
          <input type="text" id="f-rip" value="${esc(rule.remote_host || "")}" placeholder="如 1.2.3.4 或 us.example.com"></label>
      </div>
      <div class="form-row">
        <label class="field"><span class="lab"><b>远端 TCP 端口</b></span>
          <input type="number" id="f-rtcp" value="${rule.remote_tcp_port || ""}" placeholder="如 443"></label>
        <label class="field"><span class="lab">远端 UDP 端口（留空 = 同 TCP 端口）</span>
          <input type="number" id="f-rudp" value="${rule.remote_udp_port || ""}" placeholder="同 TCP"></label>
      </div>
      <label class="field"><span class="lab"><b>协议</b></span>
        <div style="display:flex;gap:26px">
          <label class="switch"><input type="checkbox" id="f-tcp" ${rule.enable_tcp ? "checked" : ""}><span class="track"></span> TCP</label>
          <label class="switch"><input type="checkbox" id="f-udp" ${rule.enable_udp ? "checked" : ""}><span class="track"></span> UDP</label>
        </div></label>
      <div class="form-row">
        <label class="field"><span class="lab">限速（KB/s，0 = 不限）</span>
          <input type="number" id="f-rate" value="${rule.rate_limit_kbps || ""}" placeholder="如 10240 (10MB/s)">
          <div class="hint">实例级双向各自限速，内核令牌桶实现，秒级精度</div></label>
        <label class="field"><span class="lab">流量配额（GB，0 = 不限）</span>
          <input type="number" id="f-quota" step="0.1" value="${rule.traffic_quota_bytes ? (rule.traffic_quota_bytes / 1073741824).toFixed(1) : ""}" placeholder="如 100">
          <div class="hint">上行+下行累计达到配额后自动停机（重启实例后计数归零）</div></label>
      </div>
      ${advGrid(rule.advanced)}
    </div>
    <div class="modal-foot">
      <button class="btn secondary" id="m-cancel">取消</button>
      <button class="btn" id="m-save">${isEdit ? "保存并重启实例" : "创建并启动"}</button>
    </div>`);

  // 填充节点下拉
  (async () => {
    try {
      const d = await api("/api/nodes");
      const sel = $("#f-node");
      (d.nodes || []).forEach((n) => {
        const opt = document.createElement("option");
        opt.value = n.id;
        opt.textContent = `${n.name}${n.online ? "（在线）" : "（离线）"}`;
        sel.appendChild(opt);
      });
      sel.value = String(rule.node_id || 0);
    } catch { /* 节点列表加载失败时仅保留本机 */ }
  })();

  $("#adv-toggle").addEventListener("click", () => {
    const g = $("#adv-grid");
    g.classList.toggle("open");
    $("#adv-toggle").textContent = g.classList.contains("open") ? "▾ 高级参数" : "▸ 高级参数（可选，默认使用程序内置值）";
  });
  $("#m-cancel").addEventListener("click", closeModal);
  $("#m-save").addEventListener("click", async () => {
    const body = {
      tag: $("#f-tag").value.trim(),
      remark: $("#f-remark").value.trim(),
      listen_ip: $("#f-lip").value.trim(),
      listen_port: parseInt($("#f-lport").value, 10),
      remote_host: $("#f-rip").value.trim(),
      remote_tcp_port: parseInt($("#f-rtcp").value, 10),
      remote_udp_port: parseInt($("#f-rudp").value, 10) || 0,
      enable_tcp: $("#f-tcp").checked,
      enable_udp: $("#f-udp").checked,
      node_id: parseInt($("#f-node").value, 10) || 0,
      rate_limit_kbps: parseInt($("#f-rate").value, 10) || 0,
      traffic_quota_bytes: Math.round((parseFloat($("#f-quota").value) || 0) * 1073741824),
      advanced: {},
    };
    document.querySelectorAll("[data-adv]").forEach((inp) => {
      if (inp.value !== "") body.advanced[inp.dataset.adv] = parseInt(inp.value, 10);
    });
    if (!body.enable_tcp && !body.enable_udp) { toast("TCP / UDP 至少启用一个", false); return; }
    try {
      await api(isEdit ? `/api/rules/${encodeURIComponent(body.tag)}` : "/api/rules", { method: isEdit ? "PUT" : "POST", body });
      toast(isEdit ? `已保存并重启 ${body.tag}` : `已创建 ${body.tag}`);
      closeModal();
      renderRules();
    } catch (e) { toast(e.message, false); }
  });
}

async function logsModal(rule) {
  openModal(`
    <div class="modal-head"><h3>日志 · ${esc(rule.tag)}</h3>
      <button class="modal-close" onclick="document.querySelector('.modal-mask').remove()">×</button></div>
    <div class="modal-body"><pre class="logs" id="logs-box">加载中…</pre></div>
    <div class="modal-foot">
      <button class="btn secondary" id="logs-refresh">刷新</button>
      <button class="btn secondary" id="logs-close">关闭</button>
    </div>`);
  const load = async () => {
    try {
      const d = await api(`/api/rules/${encodeURIComponent(rule.tag)}/logs?lines=300`);
      $("#logs-box").textContent = d.logs || "（无日志）";
    } catch (e) { $("#logs-box").textContent = "读取失败: " + e.message; }
  };
  $("#logs-refresh").addEventListener("click", load);
  $("#logs-close").addEventListener("click", closeModal);
  load();
}

/* ---------- nodes view (主从) ---------- */
async function renderNodes() {
  const root = $("#view-root");
  root.innerHTML = `<div class="empty">加载中…</div>`;
  let data, tlsInfo;
  try { data = await api("/api/nodes"); } catch (e) { root.innerHTML = `<div class="empty">${esc(e.message)}</div>`; return; }
  tlsInfo = await api("/api/tls").catch(() => ({}));
  const nodes = data.nodes || [];
  const master = location.origin;

  root.innerHTML = `
    <div class="page-head">
      <div><h2>节点管理</h2><div class="desc">主控统一下发转发配置；被控节点出站轮询（约 5 秒），无需开放入站端口，可在 NAT 后面</div></div>
      <button class="btn" id="btn-new-node">＋ 添加节点</button>
    </div>
    <div class="card" style="padding:0;overflow-x:auto">
      <table>
        <thead><tr><th>节点</th><th>状态</th><th>转发数</th><th>机器信息</th><th>tcp_pool</th><th>最近心跳</th><th>操作</th></tr></thead>
        <tbody>
        ${nodes.length === 0 ? `<tr><td colspan="7"><div class="empty">还没有节点。点击「添加节点」生成密钥后，在被控机上执行安装命令即可接管。</div></td></tr>` : ""}
        ${nodes.map((n) => {
          const m = n.report?.machine || {};
          const seen = n.last_seen ? n.last_seen.replace("T", " ").slice(0, 19) : "从未";
          return `
          <tr data-id="${n.id}">
            <td><b>${esc(n.name)}</b><div class="hint mono">#${n.id}</div></td>
            <td>${n.online ? '<span class="badge green">在线</span>' : '<span class="badge red">离线</span>'}</td>
            <td>${n.rule_count}</td>
            <td class="hint">${esc(m.os || "-")} · ${m.cpu_cores || "?"} 核 · ${m.mem_total ? fmtBytes(m.mem_total) : "-"}</td>
            <td class="hint mono">${esc((n.report?.tcp_pool || "未上报").split("(")[0])}</td>
            <td class="hint">${esc(seen)}</td>
            <td><div class="actions-cell">
              <button class="btn sm secondary n-cmd">安装命令</button>
              <button class="btn sm secondary n-key">显示密钥</button>
              <button class="btn sm secondary n-regen">重置密钥</button>
              <button class="btn sm danger n-del">删除</button>
            </div></td>
          </tr>`;
        }).join("")}
        </tbody>
      </table>
    </div>`;

  // TLS 开启时给被控机命令附带证书指纹（防中间人）
  const tlsArgs = tlsInfo && tlsInfo.enabled && tlsInfo.fingerprint
    ? ` PANEL_TLS_FP=${tlsInfo.fingerprint}` : "";
  const slaveCmd = (n) =>
    `# 在被控机（root）执行：\n` +
    `curl -fsSL https://raw.githubusercontent.com/leoch627/relay-panel/main/panel_install.sh -o panel_install.sh\n` +
    `PANEL_SLAVE=1 PANEL_MASTER_URL=${master} PANEL_NODE_KEY=${n.key}${tlsArgs} bash panel_install.sh` +
    (tlsArgs ? `\n\n# 主控证书 SHA256 指纹：\n# ${tlsInfo.fingerprint}` : `\n\n# 注意：主控未启用 TLS，节点通信为明文（建议主控安装时保持 PANEL_TLS 默认开启）`);

  $("#btn-new-node").addEventListener("click", () => nodeAddModal(master));
  root.querySelectorAll("tr[data-id]").forEach((tr) => {
    const n = nodes.find((x) => x.id === Number(tr.dataset.id));
    $(".n-cmd", tr).addEventListener("click", () => cmdModal(`被控安装命令 · ${n.name}`, slaveCmd(n)));
    $(".n-key", tr).addEventListener("click", () => cmdModal(`节点密钥 · ${n.name}`, n.key || "（仅管理员可见）"));
    $(".n-regen", tr).addEventListener("click", async () => {
      if (!confirm(`重置 [${n.name}] 的密钥？旧密钥立即失效，该节点会持续 401 并保持现有转发不变，需更新其配置后重启服务。`)) return;
      try {
        const d = await api(`/api/nodes/${n.id}/key`, { method: "POST" });
        cmdModal(`新密钥 · ${n.name}`, d.key);
        renderNodes();
      } catch (e) { toast(e.message, false); }
    });
    $(".n-del", tr).addEventListener("click", async () => {
      if (!confirm(`确认删除节点 [${n.name}]？\n其名下 ${n.rule_count} 条转发规则会一并删除。\n若节点仍在线，它会在下次轮询时自动清理本机实例。`)) return;
      try {
        await api(`/api/nodes/${n.id}`, { method: "DELETE" });
        toast(`已删除节点 ${n.name}`);
        renderNodes();
      } catch (e) { toast(e.message, false); }
    });
  });
}

function nodeAddModal(master) {
  openModal(`
    <div class="modal-head"><h3>添加节点</h3>
      <button class="modal-close" onclick="document.querySelector('.modal-mask').remove()">×</button></div>
    <div class="modal-body">
      <label class="field"><span class="lab"><b>节点名称</b>（如 HK-line1）</span>
        <input type="text" id="n-name" placeholder="1-64 字符"></label>
    </div>
    <div class="modal-foot">
      <button class="btn secondary" id="m-cancel">取消</button>
      <button class="btn" id="m-save">创建并生成密钥</button>
    </div>`);
  $("#m-cancel").addEventListener("click", closeModal);
  $("#m-save").addEventListener("click", async () => {
    try {
      const d = await api("/api/nodes", { method: "POST", body: { name: $("#n-name").value.trim() } });
      closeModal();
      const key = d.node.key;
      const tlsInfo = await api("/api/tls").catch(() => ({}));
      const tlsEnv = tlsInfo.enabled && tlsInfo.fingerprint
        ? ` PANEL_TLS_FP=${tlsInfo.fingerprint}` : "";
      cmdModal(`节点已创建 · ${d.node.name}`,
        `# 1) 在被控机（root）执行：\n` +
        `curl -fsSL https://raw.githubusercontent.com/leoch627/relay-panel/main/panel_install.sh -o panel_install.sh\n` +
        `PANEL_SLAVE=1 PANEL_MASTER_URL=${master} PANEL_NODE_KEY=${key}${tlsEnv} bash panel_install.sh\n` +
        (tlsEnv
          ? `\n# 主控已启用 TLS（自签证书），PANEL_TLS_FP 为证书指纹固定，防中间人。\n`
          : `\n# 注意：主控未启用 TLS，节点通信为明文 HTTP。\n`) +
        `\n# 2) 安装完成后节点自动上线（数秒内），在「新建转发」里选择该节点即可。`);
      renderNodes();
    } catch (e) { toast(e.message, false); }
  });
}

function cmdModal(title, text) {
  openModal(`
    <div class="modal-head"><h3>${esc(title)}</h3>
      <button class="modal-close" onclick="document.querySelector('.modal-mask').remove()">×</button></div>
    <div class="modal-body"><pre class="logs" id="cmd-box">${esc(text)}</pre></div>
    <div class="modal-foot">
      <button class="btn secondary" id="cmd-copy">复制</button>
      <button class="btn" id="cmd-close">关闭</button>
    </div>`);
  $("#cmd-copy").addEventListener("click", async () => {
    try { await navigator.clipboard.writeText(text); toast("已复制到剪贴板"); }
    catch { toast("复制失败，请手动选择文本", false); }
  });
  $("#cmd-close").addEventListener("click", closeModal);
}

/* ---------- system view ---------- */
async function renderSystem() {
  const root = $("#view-root");
  root.innerHTML = `<div class="empty">加载中…</div>`;
  let data;
  try { data = await api("/api/system"); } catch (e) { root.innerHTML = `<div class="empty">${esc(e.message)}</div>`; return; }
  const t = data.tuning, m = data.machine;

  root.innerHTML = `
    <div class="page-head"><div><h2>系统调优</h2><div class="desc">BBR 拥塞控制与 TCP 内核参数（仅管理员可操作）</div></div></div>
    <div class="grid-stats">
      <div class="stat"><div class="k">拥塞算法</div><div class="v">${esc(t.congestion_control || "-")} ${t.bbr_active ? '<span class="badge green">BBR 已生效</span>' : (t.bbr_supported ? '<span class="badge yellow">支持未启用</span>' : '<span class="badge red">内核不支持</span>')}</div></div>
      <div class="stat"><div class="k">队列算法 qdisc</div><div class="v">${esc(t.qdisc || "-")}</div></div>
      <div class="stat"><div class="k">内核版本</div><div class="v">${esc(t.kernel || "-")}</div></div>
      <div class="stat"><div class="k">CPU / 负载</div><div class="v">${m.cpu_cores} 核 <small>${esc(m.load_avg || "-")}</small></div></div>
      <div class="stat"><div class="k">内存</div><div class="v">${fmtBytes(m.mem_avail)} <small>可用 / ${fmtBytes(m.mem_total)}</small></div></div>
      <div class="stat"><div class="k">系统</div><div class="v" style="font-size:14px">${esc(m.os || m.hostname || "-")}</div></div>
    </div>
    <div class="card">
      <h3>拥塞控制</h3>
      <p class="hint" style="margin-bottom:12px">可用算法：${(t.available_cc || []).map(esc).join(" · ") || "-"}</p>
      <div style="display:flex;gap:10px;flex-wrap:wrap">
        <button class="btn" id="btn-bbr" ${t.bbr_supported ? "" : "disabled"}>${t.bbr_active ? "重新应用 BBR" : "开启 BBR + fq"}</button>
        ${t.bbr_supported ? "" : '<span class="hint" style="align-self:center">当前内核未提供 bbr 模块</span>'}
      </div>
    </div>
    <div class="card">
      <h3>TCP 调优档位</h3>
      <p class="hint" style="margin-bottom:14px">写入 ${esc(t.config_file)} 并立即生效；所有档位默认搭配 BBR + fq。</p>
      <div style="display:grid;gap:12px">
        ${(t.profiles || []).map((p) => `
          <div style="display:flex;align-items:center;justify-content:space-between;gap:12px;border:1px solid var(--border);border-radius:10px;padding:14px 16px">
            <div><b>${esc(p.label)}</b> <span class="mono hint">${esc(p.name)}</span><div class="hint">${esc(p.desc)}</div></div>
            <button class="btn" data-profile="${esc(p.name)}">应用</button>
          </div>`).join("")}
      </div>
      ${t.file_exists ? `<p class="hint" style="margin-top:12px">${t.file_managed ? "当前调优文件由面板管理。" : "存在非面板管理的调优文件，应用档位后将接管该文件。"}</p>` : ""}
    </div>`;

  $("#btn-bbr").addEventListener("click", async (e) => {
    e.target.disabled = true;
    try { await api("/api/system/bbr", { method: "POST" }); toast("BBR 已应用"); renderSystem(); }
    catch (err) { toast(err.message, false); e.target.disabled = false; }
  });
  root.querySelectorAll("[data-profile]").forEach((btn) =>
    btn.addEventListener("click", async () => {
      btn.disabled = true;
      try {
        await api("/api/system/tuning", { method: "POST", body: { profile: btn.dataset.profile, bbr: true } });
        toast(`已应用「${btn.dataset.profile}」调优`);
        renderSystem();
      } catch (err) { toast(err.message, false); btn.disabled = false; }
    }));
}

/* ---------- users view ---------- */
async function renderUsers() {
  const root = $("#view-root");
  root.innerHTML = `<div class="empty">加载中…</div>`;
  let data;
  try { data = await api("/api/users"); } catch (e) { root.innerHTML = `<div class="empty">${esc(e.message)}</div>`; return; }
  const users = data.users || [];

  root.innerHTML = `
    <div class="page-head">
      <div><h2>用户管理</h2><div class="desc">多用户隔离：普通用户只能看到和操作自己的转发</div></div>
      <button class="btn" id="btn-new-user">＋ 新建用户</button>
    </div>
    <div class="card" style="padding:0;overflow-x:auto">
      <table>
        <thead><tr><th>ID</th><th>用户名</th><th>角色</th><th>转发数</th><th>状态</th><th>创建时间</th><th>操作</th></tr></thead>
        <tbody>
        ${users.map((u) => `
          <tr data-id="${u.id}">
            <td class="mono">${u.id}</td>
            <td><b>${esc(u.username)}</b></td>
            <td>${u.is_admin ? '<span class="badge blue">管理员</span>' : '<span class="badge gray">普通用户</span>'}</td>
            <td>${u.rule_count}</td>
            <td>${u.disabled ? '<span class="badge red">已禁用</span>' : '<span class="badge green">正常</span>'}</td>
            <td class="hint">${esc(u.created_at.replace("T", " ").slice(0, 19))}</td>
            <td><div class="actions-cell">
              <button class="btn sm secondary u-pw">改密码</button>
              <button class="btn sm secondary u-toggle">${u.disabled ? "启用" : "禁用"}</button>
              <button class="btn sm danger u-del" ${u.id === state.user.id ? "disabled" : ""}>删除</button>
            </div></td>
          </tr>`).join("")}
        </tbody>
      </table>
    </div>`;

  $("#btn-new-user").addEventListener("click", () => userModal());
  root.querySelectorAll("tr[data-id]").forEach((tr) => {
    const u = users.find((x) => x.id === Number(tr.dataset.id));
    $(".u-pw", tr).addEventListener("click", () => pwModal(u));
    $(".u-toggle", tr).addEventListener("click", async () => {
      try {
        await api(`/api/users/${u.id}`, { method: "PUT", body: { disabled: !u.disabled } });
        toast(u.disabled ? `已启用 ${u.username}` : `已禁用 ${u.username}`);
        renderUsers();
      } catch (e) { toast(e.message, false); }
    });
    $(".u-del", tr).addEventListener("click", async () => {
      if (!confirm(`确认删除用户 [${u.username}]？其名下 ${u.rule_count} 条转发将移交给当前管理员。`)) return;
      try {
        await api(`/api/users/${u.id}`, { method: "DELETE" });
        toast(`已删除 ${u.username}`);
        renderUsers();
      } catch (e) { toast(e.message, false); }
    });
  });
}

function userModal() {
  openModal(`
    <div class="modal-head"><h3>新建用户</h3>
      <button class="modal-close" onclick="document.querySelector('.modal-mask').remove()">×</button></div>
    <div class="modal-body">
      <div class="form-row">
        <label class="field"><span class="lab"><b>用户名</b></span><input type="text" id="u-name" placeholder="2-32 字符"></label>
        <label class="field"><span class="lab"><b>密码</b></span><input type="text" id="u-pass" placeholder="至少 6 位"></label>
      </div>
      <label class="switch"><input type="checkbox" id="u-admin"><span class="track"></span> 设为管理员（可管理所有转发/用户/调优）</label>
    </div>
    <div class="modal-foot">
      <button class="btn secondary" id="m-cancel">取消</button>
      <button class="btn" id="m-save">创建</button>
    </div>`);
  $("#m-cancel").addEventListener("click", closeModal);
  $("#m-save").addEventListener("click", async () => {
    try {
      await api("/api/users", { method: "POST", body: {
        username: $("#u-name").value.trim(),
        password: $("#u-pass").value,
        is_admin: $("#u-admin").checked,
      }});
      toast("用户已创建");
      closeModal();
      renderUsers();
    } catch (e) { toast(e.message, false); }
  });
}

function pwModal(u) {
  openModal(`
    <div class="modal-head"><h3>重置密码 · ${esc(u.username)}</h3>
      <button class="modal-close" onclick="document.querySelector('.modal-mask').remove()">×</button></div>
    <div class="modal-body">
      <label class="field"><span class="lab"><b>新密码</b></span><input type="text" id="u-newpass" placeholder="至少 6 位"></label>
    </div>
    <div class="modal-foot">
      <button class="btn secondary" id="m-cancel">取消</button>
      <button class="btn" id="m-save">重置</button>
    </div>`);
  $("#m-cancel").addEventListener("click", closeModal);
  $("#m-save").addEventListener("click", async () => {
    try {
      await api(`/api/users/${u.id}`, { method: "PUT", body: { password: $("#u-newpass").value } });
      toast("密码已重置");
      closeModal();
    } catch (e) { toast(e.message, false); }
  });
}

/* ---------- password view ---------- */
function renderPassword() {
  $("#view-root").innerHTML = `
    <div class="page-head"><div><h2>修改密码</h2><div class="desc">修改后需要重新登录</div></div></div>
    <div class="card" style="max-width:460px">
      <label class="field"><span class="lab"><b>原密码</b></span><input type="password" id="p-old"></label>
      <label class="field"><span class="lab"><b>新密码</b></span><input type="password" id="p-new" placeholder="至少 6 位"></label>
      <label class="field"><span class="lab"><b>确认新密码</b></span><input type="password" id="p-new2"></label>
      <button class="btn" id="p-save">修改密码</button>
    </div>`;
  $("#p-save").addEventListener("click", async () => {
    if ($("#p-new").value !== $("#p-new2").value) { toast("两次输入的新密码不一致", false); return; }
    try {
      await api("/api/password", { method: "POST", body: { old: $("#p-old").value, new: $("#p-new").value } });
      toast("密码已修改，即将重新登录");
      setTimeout(showLogin, 800);
    } catch (e) { toast(e.message, false); }
  });
}

/* ---------- entry ---------- */
boot();
