// 100zip 前端主逻辑（原生 ES Module，无框架、无构建链）。
const $ = (id) => document.getElementById(id);

// 全局错误兜底：任何前端异常都要让用户看见，避免「点了没反应」
window.addEventListener("error", (e) => {
  try { toast("前端错误：" + (e.message || e), true); } catch (_) {}
});
window.addEventListener("unhandledrejection", (e) => {
  const msg = (e.reason && e.reason.message) ? e.reason.message : String(e.reason);
  try { toast("前端异常：" + msg, true); } catch (_) {}
});

// 统一网关下页面地址形如 /app/100zip/，API 与静态资源都要带上该前缀。
const APP_BASE = (() => {
  const m = location.pathname.match(/^(\/app\/[^/]+)/);
  return m ? m[1] : "";
})();

let currentVersion = "dev";
let latestRelease = null;
const VERSION_MANIFEST_URL = "https://raw.githubusercontent.com/Alicace/100zip/main/version.json";
const RELEASES_API_URL = "https://api.github.com/repos/Alicace/100zip/releases/latest";

function normalizeVersion(value) {
  const m = String(value == null ? "" : value).trim().replace(/^v/i, "").match(/^(\d+(?:\.\d+){0,3})/);
  return m ? m[1].split(".").map((n) => Number(n || 0)) : [];
}

function compareVersions(a, b) {
  const av = normalizeVersion(a), bv = normalizeVersion(b);
  if (!av.length || !bv.length) return 0;
  for (let i = 0; i < Math.max(av.length, bv.length); i++) {
    const x = av[i] || 0, y = bv[i] || 0;
    if (x !== y) return x > y ? 1 : -1;
  }
  return 0;
}

function openLatestRelease() {
  const url = latestRelease?.url || "https://github.com/Alicace/100zip/releases";
  window.open(url, "_blank", "noopener,noreferrer");
}

function renderUpdateState(state, message = "") {
  const top = $("topUpdateButton");
  const about = $("btnOpenUpdate");
  const hint = $("updateHint");
  if (!top || !about || !hint) return;
  top.hidden = state !== "available";
  about.hidden = state !== "available";
  hint.classList.toggle("update-available", state === "available");
  hint.classList.toggle("update-error", state === "error");
  hint.textContent = message || (state === "available" ? `发现新版本 v${latestRelease.version}，点击按钮前往 GitHub 下载。` : "启动时会自动检查 GitHub 最新版本。");
  if (state === "available") {
    top.textContent = `升级 v${latestRelease.version}`;
    top.title = `发现新版本 v${latestRelease.version}`;
    about.textContent = `升级到 v${latestRelease.version}`;
  }
}

async function checkForUpdates({ silent = false } = {}) {
  const hint = $("updateHint");
  const check = $("btnCheckUpdate");
  if (!hint || !check) return false;
  check.disabled = true;
  if (!silent) hint.textContent = "正在检查最新版本…";
  try {
    // 版本清单走 raw.githubusercontent.com，不占用 GitHub Releases API 的公共配额。
    // 加时间参数避免 NAS/浏览器长期复用旧的 CDN 响应；version.json 本身不包含用户数据。
    const manifestRes = await fetch(`${VERSION_MANIFEST_URL}?t=${Date.now()}`, {
      cache: "no-store",
    });
    if (!manifestRes.ok) throw new Error(`版本清单请求失败（HTTP ${manifestRes.status}）`);
    const manifest = await manifestRes.json();
    const latest = String(manifest.version || "").replace(/^v/i, "").trim();
    if (!normalizeVersion(latest).length) throw new Error("版本清单未返回有效版本号");
    latestRelease = {
      version: latest,
      url: manifest.release_url || manifest.url || "https://github.com/Alicace/100zip/releases",
      publishedAt: manifest.published_at || "",
    };
    const newer = compareVersions(latest, currentVersion) > 0;
    if (newer) {
      renderUpdateState("available");
    } else {
      latestRelease = null;
      renderUpdateState("current", `当前已是最新版本 v${currentVersion || latest}。`);
    }
    return newer;
  } catch (e) {
    // 兼容旧版部署或 raw CDN 临时不可用时，才回退到 Releases API。
    try {
      const res = await fetch(RELEASES_API_URL, {
        headers: { Accept: "application/vnd.github+json" },
        cache: "no-store",
      });
      if (!res.ok) throw new Error(`更新服务暂不可用（HTTP ${res.status}）`);
      const rel = await res.json();
      const latest = String(rel.tag_name || rel.name || "").replace(/^v/i, "").trim();
      if (!normalizeVersion(latest).length) throw new Error("GitHub 未返回有效版本号");
      latestRelease = { version: latest, url: rel.html_url || "https://github.com/Alicace/100zip/releases", publishedAt: rel.published_at || "" };
      const newer = compareVersions(latest, currentVersion) > 0;
      if (newer) renderUpdateState("available");
      else {
        latestRelease = null;
        renderUpdateState("current", `当前已是最新版本 v${currentVersion || latest}。`);
      }
      return newer;
    } catch (fallbackError) {
      if (!silent) renderUpdateState("error", `${fallbackError.message}。可稍后重试。`);
    }
    return false;
  } finally {
    check.disabled = false;
    check.textContent = "检查更新";
  }
}

async function api(path, options = {}) {
  const res = await fetch(APP_BASE + path, {
    headers: { "Content-Type": "application/json" },
    ...options,
  });
  const body = await res.json().catch(() => ({ ok: false, error: { code: "INTERNAL", message: "响应解析失败" } }));
  if (!res.ok || body.ok === false) {
    const err = body.error || { code: "INTERNAL", message: `HTTP ${res.status}` };
    const e = new Error(err.message || "请求失败");
    e.code = err.code;
    e.hint = err.hint;
    e.detail = err.detail || "";
    throw e;
  }
  return body.data;
}

function toast(msg, isError = false) {
  const el = $("toast");
  $("toastText").textContent = msg;
  el.className = "toast" + (isError ? " error" : "");
  $("toastAction").hidden = true;
  toast._onAction = null;
  el.hidden = false;
  if (isError) logAction("错误", msg);
  clearTimeout(toast._t);
  toast._t = setTimeout(() => { el.hidden = true; }, 4000);
}

// ---------------- 用户操作日志（随诊断报告导出，用于问题排查）
const ACT_MAX = 300;
const activityLog = [];
try {
  const saved = JSON.parse(localStorage.getItem("100zip.activity") || "[]");
  if (Array.isArray(saved)) activityLog.push(...saved.slice(-ACT_MAX));
} catch (_) { /* 忽略 */ }
function logAction(action, detail = "") {
  activityLog.push({ ts: new Date().toISOString(), action, detail: String(detail).slice(0, 400) });
  if (activityLog.length > ACT_MAX) activityLog.splice(0, activityLog.length - ACT_MAX);
  try { localStorage.setItem("100zip.activity", JSON.stringify(activityLog.slice(-ACT_MAX))); } catch (_) { /* 忽略 */ }
}

// ---------------- 统一安全选择器：未就绪给明确提示 + 防重入（修复「点了没反应」）
let picking = false;
async function safePick(kind, args = {}) {
  const method = kind === "dir" ? "pickDirectory" : "pickFiles";
  const fb = window.fnosBridge;
  if (!fb || typeof fb[method] !== "function") {
    toast("飞牛选择器未就绪，请稍候重试或手动输入路径", true);
    return [];
  }
  if (picking) { toast("选择器正在打开，请稍候…", true); return []; }
  picking = true;
  let timer;
  try {
    const pending = Promise.resolve(fb[method](args));
    const timeout = new Promise((_, reject) => { timer = setTimeout(() => reject(new Error("选择器响应超时，请重试或手动输入路径")), 15000); });
    return await Promise.race([pending, timeout]);
  } finally {
    clearTimeout(timer);
    picking = false;
  }
}
(function guardPicker() {
  const patch = () => {
    const fb = window.fnosBridge;
    if (!fb || fb.__guarded) return false;
    const wrap = (fn) => async (...args) => fn.apply(fb, args);
    if (typeof fb.pickFiles === "function") fb.pickFiles = wrap(fb.pickFiles);
    if (typeof fb.pickDirectory === "function") fb.pickDirectory = wrap(fb.pickDirectory);
    fb.__guarded = true;
    return true;
  };
  if (patch()) return;
  let n = 0;
  const t = setInterval(() => {
    n++;
    if (patch()) clearInterval(t);
    else if (n > 10) clearInterval(t);
  }, 300);
})();

// toast 带动作按钮（如「查看任务」）
function toastAction(msg, actionText, onAction, isError = false) {
  const el = $("toast");
  const act = $("toastAction");
  $("toastText").textContent = msg;
  el.className = "toast" + (isError ? " error" : "");
  toast._onAction = onAction || null;
  if (toast._onAction) {
    act.textContent = actionText || "查看";
    act.hidden = false;
  }
  el.hidden = false;
  clearTimeout(toast._t);
  toast._t = setTimeout(() => { el.hidden = true; }, isError ? 8000 : 4000);
}

$("toastAction").addEventListener("click", () => {
  $("toast").hidden = true;
  if (typeof toast._onAction === "function") toast._onAction();
});
$("toastClose").addEventListener("click", () => { $("toast").hidden = true; });

// ---------------- 统一弹窗体系
function openDialog(id) { $(id).hidden = false; }
function closeDialog(id) { $(id).hidden = true; }

// 原生 confirm 的替代：Promise 化，danger=true 时确认键为红色
function confirmDialog(text, { title = "确认", okText = "确认", danger = false } = {}) {
  return new Promise((resolve) => {
    $("cfTitle").textContent = title;
    $("cfText").textContent = text;
    const okBtn = $("btnCfOk");
    okBtn.textContent = okText;
    okBtn.classList.toggle("danger", danger);
    okBtn.classList.toggle("primary", !danger);
    confirmDialog._resolve = resolve;
    openDialog("confirmModal");
  });
}

$("btnCfOk").addEventListener("click", () => {
  closeDialog("confirmModal");
  if (confirmDialog._resolve) { confirmDialog._resolve(true); confirmDialog._resolve = null; }
});
$("btnCfCancel").addEventListener("click", () => {
  closeDialog("confirmModal");
  if (confirmDialog._resolve) { confirmDialog._resolve(false); confirmDialog._resolve = null; }
});

function setHint(text, cls = "") {
  const el = $("listHint");
  el.textContent = text;
  el.className = "hint " + cls;
}

// ---------------- 视图切换
function switchTab(view) {
  document.querySelector(`.tab[data-view="${view}"]`)?.click();
}
document.querySelectorAll(".tab").forEach((btn) => {
  btn.addEventListener("click", () => {
    document.querySelectorAll(".tab").forEach((b) => b.classList.toggle("active", b === btn));
    document.querySelectorAll(".view").forEach((v) => v.classList.toggle("active", v.id === "view-" + btn.dataset.view));
    if (btn.dataset.view === "jobs") refreshJobs();
    if (btn.dataset.view === "compress") renderSources();
  });
});

// ---------------- 解压
let entries = [];
let listNeedsPassword = false;

async function openArchive() {
  const path = $("srcPath").value.trim();
  if (!path) return toast("请填写压缩包路径", true);
  checkedIndexes.clear();
  selectedEntryIndex = null;
  $("withPreview").classList.remove("open");
  $("previewPane").hidden = true;
  const base = path.replace(/\/+$/, "").split("/").pop() || "—";
  $("infoName").textContent = base;
  $("infoPath").textContent = path;
  $("pkgBadge").textContent = (base.split(".").pop() || "PKG").toUpperCase();
  $("extractEmpty").hidden = true;
  $("extractWork").hidden = false;
  $("extractBar").hidden = false;
  setHint("正在读取…");
  try {
    const data = await api("/api/archive/list", {
      method: "POST",
      body: JSON.stringify({ path, password: $("password").value }),
    });
    entries = data.entries || [];
    listNeedsPassword = !!data.needsPassword;
    renderEntries();
    // 默认全选（WinRAR 习惯：打开即全部勾选，用户可取消部分）
    for (const [i, e] of entries.entries()) checkedIndexes.add(entryIdx(e, i));
    syncAllDirStates();
    renderEntries();
    const totalSize = (data.entries || []).reduce((s, e) => s + (e.size || 0), 0);
    const metrics = $("infoMetrics");
    metrics.innerHTML = "";
    const addMetric = (t, v) => {
      const d = document.createElement("div");
      const dt = document.createElement("dt"); dt.textContent = t;
      const dd = document.createElement("dd"); dd.textContent = v;
      d.append(dt, dd);
      metrics.append(d);
    };
    addMetric("条目", String(data.total));
    addMetric("总大小", fmtBytes(totalSize));
    addMetric("加密", data.needsPassword ? "已加密" : "否");
    const vol = data.volume || {};
    const alert = $("volumeAlert");
    if (vol.isVolume && Array.isArray(vol.missing) && vol.missing.length) {
      alert.hidden = false;
      alert.innerHTML = `<svg><use href="#i-warn"></use></svg><span>分卷包缺少 ${vol.missing.length} 个卷：` +
        escapeHtml(vol.missing.slice(0, 4).join("、")) + (vol.missing.length > 4 ? " 等" : "") +
        `。请把全部分卷放在同一目录后再解压。</span>`;
    } else {
      alert.hidden = true;
    }
    if (data.encoding && data.encoding.needsFix) {
      setHint(`检测到 ${data.encoding.codepage.toUpperCase()} 编码的文件名（置信度 ${(data.encoding.confidence * 100).toFixed(0)}%）：列表已按正确编码显示，解压后文件名同样会被修复`, "ok");
    } else if (data.needsPassword && !$("password").value.trim()) {
      setHint("该压缩包已加密，请填写密码后重试", "error");
    } else if (data.needsPassword) {
      setHint("读取成功（已加密，解压将使用你填写的密码）", "ok");
    } else {
      setHint("读取成功", "ok");
    }
    if (!$("destPath").value) {
      $("destPath").value = path.replace(/\/[^/]+$/, "");
    }
    // 条目级加密（List 能成功但内容需要密码）：主动弹密码框，避免用户不知道去哪填
    if (data.needsPassword && !$("password").value.trim()) {
      openListPasswordModal("该压缩包已加密，输入密码后可预览与解压");
    }
    const sample = entries.slice(0, 8).map((x) => x.displayPath || x.path).join(" | ");
    logAction("打开压缩包", path + " · 格式 " + ((path.split(".").pop() || "").toUpperCase()) + " · 条目 " + data.total + (data.needsPassword ? " · 已加密" : "") + (sample ? " · " + sample : ""));
  } catch (e) {
    setHint(`${e.message}${e.hint ? "（" + e.hint + "）" : ""}`, "error");
    logAction("打开压缩包失败", path + " · " + e.message + (e.detail ? " · " + e.detail : ""));
    if (e.code === "PASSWORD_REQUIRED" || e.code === "PASSWORD_WRONG") {
      openListPasswordModal(e.message);
    }
  }
}

// 解压视图空状态与入口
$("btnEmptyOpen").addEventListener("click", () => $("btnPickArchive").click());
$("btnEmptyBatch").addEventListener("click", () => switchTab("batch"));
$("btnClearArchive").addEventListener("click", () => {
  $("srcPath").value = "";
  $("srcPathInput").value = "";
  entries = [];
  listNeedsPassword = false;
  checkedIndexes.clear();
  selectedEntryIndex = null;
  $("withPreview").classList.remove("open");
  $("previewPane").hidden = true;
  $("extractWork").hidden = true;
  $("extractEmpty").hidden = false;
  $("extractBar").hidden = true;
  $("infoName").textContent = "—";
  $("infoPath").textContent = "—";
  $("infoMetrics").innerHTML = "";
  $("volumeAlert").hidden = true;
  logAction("清除压缩包", "已清除当前选择");
});
$("btnOpenTyped").addEventListener("click", () => {
  const v = $("srcPathInput").value.trim();
  if (!v) return toast("请先粘贴压缩包路径", true);
  $("srcPath").value = v;
  openArchive();
});
$("srcPathInput").addEventListener("keydown", (e) => {
  if (e.key === "Enter") $("btnOpenTyped").click();
});
$("btnBannerGo").addEventListener("click", () => switchTab("jobs"));
$("btnBannerClose").addEventListener("click", () => { $("globalBanner").hidden = true; });

function renderEntries() {
  const box = $("entryList");
  box.innerHTML = "";
  const frag = document.createDocumentFragment();
  const filter = ($("entryFilter") && $("entryFilter").value || "").trim().toLowerCase();
  let shown = 0;
  for (const [i, e] of entries.entries()) {
    const label = e.displayPath || e.path;
    if (filter && !label.toLowerCase().includes(filter)) continue;
    shown++;
    const idx = entryIdx(e, i);
    const row = document.createElement("div");
    row.className = "item" + (e.isDir ? " dir" : "");
    row.dataset.path = e.path;
    const cb = document.createElement("input");
    cb.type = "checkbox";
    cb.dataset.index = idx;
    cb.dataset.path = e.path;
    cb.dataset.isDir = e.isDir ? "1" : "0";
    cb.checked = checkedIndexes.has(idx);
    const name = document.createElement("span");
    name.className = "name";
    name.textContent = fileIcon(label, e.isDir) + "  " + label;
    row.title = e.isDir ? "勾选 = 解压整个文件夹 · 单击查看统计" : "单击预览 · 双击放大";
    row.style.cursor = "pointer";
    row.addEventListener("click", (ev) => {
      if (ev.target === cb) return;
      showEntryPreview(e, row);
    });
    if (!e.isDir) row.addEventListener("dblclick", () => previewEntry(idx, label));
    const size = document.createElement("span");
    if (e.isDir) {
      size.className = "dir-count";
      size.textContent = countChildren(e.path) + " 项";
    } else {
      size.className = "size";
      size.textContent = humanSize(e.size);
    }
    row.append(cb, name, size);
    frag.append(row);
  }
  box.append(frag);
  if (filter) {
    const tip = document.createElement("div");
    tip.className = "item dir";
    tip.textContent = `筛选出 ${shown} / ${entries.length} 项`;
    box.append(tip);
  }
  syncAllDirStates();
  restoreSelection();
}

// 勾选状态：以 entry.index 为键，跨过滤/重渲染保持
const checkedIndexes = new Set();
let selectedEntryIndex = null;

function entryIdx(e, i = 0) { return String(e.index != null ? e.index : i); }

function countChildren(dirPath) {
  const prefix = dirPath.replace(/\/+$/, "") + "/";
  let n = 0;
  for (const c of entries) {
    if (c.path !== dirPath && c.path.startsWith(prefix)) n++;
  }
  return n;
}

function cbByIndex(idx) {
  return $("entryList").querySelector(`input[data-index="${idx}"]`);
}

// 目录勾选状态由其子孙文件推导（全勾/部分/全无），渲染后统一刷新
function syncAllDirStates() {
  for (const [i, e] of entries.entries()) {
    if (!e.isDir) continue;
    const idx = entryIdx(e, i);
    const cb = cbByIndex(idx);
    if (!cb) continue;
    const prefix = e.path.replace(/\/+$/, "") + "/";
    let total = 0, on = 0;
    for (const [j, c] of entries.entries()) {
      if (c.isDir || !c.path.startsWith(prefix)) continue;
      total++;
      if (checkedIndexes.has(entryIdx(c, j))) on++;
    }
    cb.checked = total > 0 && on === total;
    cb.indeterminate = on > 0 && on < total;
    if (cb.checked) checkedIndexes.add(idx);
    else if (!cb.indeterminate) checkedIndexes.delete(idx);
  }
}

// 勾/取消目录时同步子孙；勾选文件后回溯更新各级父目录状态
$("entryList").addEventListener("change", (ev) => {
  const cb = ev.target;
  if (cb.type !== "checkbox") return;
  const idx = cb.dataset.index;
  if (cb.checked) checkedIndexes.add(idx); else checkedIndexes.delete(idx);
  if (cb.dataset.isDir === "1") {
    const prefix = cb.dataset.path.replace(/\/+$/, "") + "/";
    for (const [j, c] of entries.entries()) {
      if (!c.path.startsWith(prefix)) continue;
      const cIdx = entryIdx(c, j);
      if (cb.checked) checkedIndexes.add(cIdx);
      else if (!c.isDir) checkedIndexes.delete(cIdx);
      const ccb = cbByIndex(cIdx);
      if (ccb) { ccb.checked = cb.checked; ccb.indeterminate = false; }
    }
  }
  syncAllDirStates();
});

$("entryFilter").addEventListener("input", renderEntries);

function restoreSelection() {
  if (selectedEntryIndex == null) return;
  for (const [i, e] of entries.entries()) {
    if (entryIdx(e, i) !== selectedEntryIndex) continue;
    const row = [...$("entryList").children].find((r) => r.dataset.path === e.path);
    if (row) row.classList.add("selected");
    break;
  }
}

// ---------------- 单击预览分栏（文本 / 图片 / 其他格式显示文件信息）
async function showEntryPreview(e, row) {
  selectedEntryIndex = entryIdx(e, 0);
  const myIdx = selectedEntryIndex;
  for (const r of $("entryList").children) r.classList.toggle("selected", r === row);
  $("withPreview").classList.add("open");
  $("previewPane").hidden = false;
  $("paneTitle").textContent = (e.displayPath || e.path).split("/").pop() || e.path;
  const body = $("paneBody");
  $("paneHint").textContent = "正在读取…";
  body.innerHTML = "";
  if (e.isDir) {
    renderEntryInfo(e, body);
    $("paneHint").textContent = "文件夹不支持内容预览；勾选后可整目录解压";
    return;
  }
  try {
    const res = await fetch(APP_BASE + "/api/archive/preview", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        path: $("srcPath").value.trim(),
        entryIndex: Number(selectedEntryIndex),
        password: $("password").value,
        vaultLabel: $("vaultSelect").value,
      }),
    });
    const ctype = res.headers.get("content-type") || "";
    if (!res.ok) {
      const err = await res.json().catch(() => ({}));
      const ex = new Error((err.error && err.error.message) || ("HTTP " + res.status));
      ex.code = err.error && err.error.code;
      ex.detail = err.error && err.error.detail;
      throw ex;
    }
    if (ctype.startsWith("image/")) {
      if (selectedEntryIndex !== myIdx) return;
      const blob = await res.blob();
      const img = document.createElement("img");
      img.src = URL.createObjectURL(blob);
      body.append(img);
      $("paneHint").textContent = "图片预览";
      return;
    }
    if (ctype.startsWith("audio/") || ctype.startsWith("video/") || ctype === "application/pdf") {
      if (selectedEntryIndex !== myIdx) return;
      const blob = await res.blob();
      const url = URL.createObjectURL(blob);
      const media = ctype === "application/pdf" ? document.createElement("iframe") : document.createElement(ctype.startsWith("audio/") ? "audio" : "video");
      media.src = ctype === "application/pdf" ? url + "#page=1&zoom=page-width" : url;
      if (ctype === "application/pdf") {
        media.title = "PDF 预览";
        media.setAttribute("loading", "lazy");
      }
      media.controls = true;
      media.style.width = "100%";
      media.style.maxHeight = "320px";
      body.append(media);
      $("paneHint").textContent = ctype === "application/pdf" ? "PDF 预览" : "媒体预览（浏览器支持情况取决于格式）";
      return;
    }
    const data = await res.json();
    if (selectedEntryIndex !== myIdx) return;
    const d = data.data || {};
    if (["text", "document", "spreadsheet", "presentation"].includes(d.kind)) {
      const pre = document.createElement("pre");
      pre.textContent = d.content || "";
      body.append(pre);
      $("paneHint").textContent = d.kind === "document" ? "Word 文字预览（不还原排版）" : d.kind === "spreadsheet" ? "Excel 内容预览（不还原表格样式）" : d.kind === "presentation" ? "PPT 文字预览（不还原版式）" : (d.truncated ? "内容过长，仅显示前 256 KB" : "文本预览");
      return;
    }
    renderEntryInfo(e, body);
    $("paneHint").textContent = d.message || "该格式暂不支持在线预览，解压后可直接查看";
  } catch (err) {
    renderEntryInfo(e, body);
    if (err.code === "PASSWORD_REQUIRED" || err.code === "PASSWORD_WRONG") {
      $("paneHint").textContent = "输入密码后即可预览";
      openListPasswordModal(err.message);
    } else {
      $("paneHint").textContent = "预览读取失败：" + err.message + (err.detail ? "（" + err.detail + "）" : "") + "（下方为文件信息）";
    }
  }
}

function renderEntryInfo(e, body) {
  const dl = document.createElement("dl");
  dl.className = "fileinfo";
  const add = (t, v) => {
    const dt = document.createElement("dt"); dt.textContent = t;
    const dd = document.createElement("dd"); dd.textContent = v == null || v === "" ? "—" : v;
    dl.append(dt, dd);
  };
  add("名称", (e.displayPath || e.path).split("/").pop());
  add("类型", e.isDir ? "文件夹" : ((e.path.split(".").pop() || "").toUpperCase() + " 文件"));
  add("大小", e.isDir ? countChildren(e.path) + " 项" : humanSize(e.size));
  if (!e.isDir && e.packedSize) add("压缩后", humanSize(e.packedSize));
  if (!e.isDir && e.mtime) add("修改时间", e.mtime);
  add("完整路径", e.displayPath || e.path);
  body.append(dl);
}

// 密码验证探针：只验证不渲染（用于密码弹窗）
async function probeEntryPreview(entry, pw) {
  const res = await fetch(APP_BASE + "/api/archive/preview", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      path: $("srcPath").value.trim(),
      entryIndex: Number(selectedEntryIndex),
      password: pw,
      vaultLabel: $("vaultSelect").value,
    }),
  });
  if (!res.ok) {
    const err = await res.json().catch(() => ({}));
    const ex = new Error((err.error && err.error.message) || ("HTTP " + res.status));
    ex.code = err.error && err.error.code;
    throw ex;
  }
}

$("btnPaneClose").addEventListener("click", () => {
  selectedEntryIndex = null;
  for (const r of $("entryList").children) r.classList.remove("selected");
  $("withPreview").classList.remove("open");
  $("previewPane").hidden = true;
});

function humanSize(n) {
  if (!n) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB"];
  let i = 0;
  while (n >= 1024 && i < units.length - 1) { n /= 1024; i++; }
  return n.toFixed(n >= 10 || i === 0 ? 0 : 1) + " " + units[i];
}

$("btnList").addEventListener("click", openArchive);
$("srcPath").addEventListener("keydown", (e) => { if (e.key === "Enter") openArchive(); });
$("btnSelectAll").addEventListener("click", () => {
  const boxes = [...$("entryList").querySelectorAll("input[type=checkbox]:not(:disabled)")];
  const allChecked = boxes.every((b) => b.checked);
  // 有搜索过滤时只作用于可见行（搜索后全选 = 只解压匹配项）；无过滤时作用于全部
  const hasFilter = ($("entryFilter").value || "").trim() !== "";
  const applyTo = hasFilter ? boxes : null;
  if (!allChecked) {
    if (applyTo) {
      for (const b of applyTo) { checkedIndexes.add(b.dataset.index); b.checked = true; b.indeterminate = false; }
    } else {
      for (const [i, e] of entries.entries()) checkedIndexes.add(entryIdx(e, i));
      boxes.forEach((b) => { b.checked = true; b.indeterminate = false; });
    }
  } else if (applyTo) {
    for (const b of applyTo) { checkedIndexes.delete(b.dataset.index); b.checked = false; b.indeterminate = false; }
  } else {
    checkedIndexes.clear();
    boxes.forEach((b) => { b.checked = false; b.indeterminate = false; });
  }
  syncAllDirStates();
});

$("btnExtract").addEventListener("click", async () => {
  const path = $("srcPath").value.trim();
  const dest = $("destPath").value.trim();
  if (!path || !dest) return toast("请填写压缩包与目标目录", true);
  if (listNeedsPassword && !$("password").value.trim()) {
    openListPasswordModal("该压缩包已加密，输入密码后开始解压");
    return;
  }
  const selected = [...$("entryList").querySelectorAll("input[type=checkbox]:checked")]
    .map((b) => Number(b.dataset.index));
  // 目录勾选会自动勾上子孙：提交前剔除被目录覆盖的子项，避免 7z 重复解压同名文件
  const deduped = dedupeSelected(selected);
  if (!deduped.length) return toast("请先勾选要解压的文件或文件夹", true);
  try {
    const cap = await estimateCapacity("extract", [path], dest, $("password").value);
    if (cap && cap.freeKnown) {
      logAction("容量预估", "解压 " + path + "：预计 " + fmtBytes(cap.estimatedOutputBytes) + "，可用 " + fmtBytes(cap.freeBytes));
    }
    const data = await api("/api/archive/extract", {
      method: "POST",
      body: JSON.stringify({
        path, dest,
        entryIndexes: deduped,
        password: $("password").value,
          overwrite: $("overwrite").value,
        createSubdir: $("createSubdir").checked,
        fixEncoding: $("fixEncoding").checked,
        vaultLabel: $("vaultSelect").value,
        tryVault: true,
        }),
    });
    toastAction("解压任务已创建", "查看", () => switchTab("jobs"));
  } catch (e) {
    toast(`${e.message}${e.hint ? "（" + e.hint + "）" : ""}`, true);
  }
});

// 树去重：若某目录路径已被勾选，其子孙条目跳过
function dedupeSelected(indexes) {
  const byIdx = new Map(entries.map((e, i) => [entryIdx(e, i), e]));
  const items = indexes.map((n) => byIdx.get(String(n))).filter(Boolean);
  items.sort((a, b) => a.path.localeCompare(b.path));
  const kept = [];
  const out = [];
  for (const e of items) {
    const p = e.path.replace(/\/+$/, "");
    if (kept.some((k) => p === k || p.startsWith(k + "/"))) continue;
    kept.push(p);
    out.push(Number(entryIdx(e)));
  }
  return out;
}

// ---------------- 压缩
let srcItems = [];

// 来源列表渲染：有来源时展开工作区，无来源时只显示入口
function renderSources() {
  const box = $("srcList");
  if (!box) return;
  box.innerHTML = "";
  for (const [i, p] of srcItems.entries()) {
    const row = document.createElement("div");
    row.className = "item";
    const isDir = /\/$/.test(p);
    const name = document.createElement("span");
    name.className = "name";
    name.title = p;
    name.textContent = `${fileIcon(p, isDir)}  ${p}`;
    const rm = document.createElement("button");
    rm.className = "icon-btn sm";
    rm.setAttribute("aria-label", "移除");
    rm.title = "移除";
    rm.innerHTML = '<svg><use href="#i-x"></use></svg>';
    rm.dataset.remove = String(i);
    row.append(name, rm);
    box.append(row);
  }
  $("srcCount").textContent = srcItems.length ? `共 ${srcItems.length} 项` : "";
  const has = srcItems.length > 0;
  $("compressEmpty").hidden = has;
  $("compressWork").hidden = !has;
}

$("srcList").addEventListener("click", (ev) => {
  const rm = ev.target.closest("[data-remove]");
  if (!rm) return;
  srcItems.splice(Number(rm.dataset.remove), 1);
  renderSources();
  saveUIState();
});

$("btnEmptyFiles").addEventListener("click", () => $("btnPickSrcFiles").click());
$("btnEmptyDirs").addEventListener("click", () => $("btnPickSrcDir").click());

const EXT_MAP = {
  "7z": ".7z", "zip": ".zip", "tar": ".tar",
  "gz": ".tar.gz", "bz2": ".tar.bz2", "xz": ".tar.xz",
  "tar.zst": ".tar.zst", "tar.lz4": ".tar.lz4", "tar.br": ".tar.br",
  "zst": ".zst", "lz4": ".lz4", "br": ".br",
};
function currentExt() { return EXT_MAP[$("compFormat").value] || ".7z"; }
function compressionTimestamp() {
  if (!( $("compAutoTimestamp") && $("compAutoTimestamp").checked )) return "";
  const now = new Date();
  const pad = (n) => String(n).padStart(2, "0");
  const date = `${now.getFullYear()}-${pad(now.getMonth() + 1)}-${pad(now.getDate())}`;
  return $("compTimestampFormat").value === "date"
    ? date
    : `${date}_${pad(now.getHours())}-${pad(now.getMinutes())}-${pad(now.getSeconds())}`;
}
function archiveBaseName(name) {
  return String(name || "").replace(/\.(7z|zip|tar|tar\.gz|tar\.bz2|tar\.xz|gz|bz2|xz|zst|lz4|br)$/i, "");
}
function addCompressionTimestamp(name, stamp) {
  const base = archiveBaseName(name).replace(/_(\d{4}-\d{2}-\d{2})(?:_\d{2}-\d{2}-\d{2})?$/, "");
  return stamp ? `${base}_${stamp}` : base;
}
function syncCompExt() {
  const cur = $("compName").value.trim();
  if (!cur) return;
  $("compName").value = archiveBaseName(cur) + currentExt();
}
function compDestination(stamp = "") {
  const dir = $("compDestDir").value.trim().replace(/\/+$/, "") || "/";
  let name = $("compName").value.trim();
  if (!name) name = "archive";
  name = addCompressionTimestamp(name, stamp) + currentExt();
  return dir + "/" + name.replace(/^\/+/, "");
}
// 分卷提示随格式联动：仅 7z/zip 支持分卷
function syncSplitHint() {
  const fmt = $("compFormat").value;
  const el = $("compSplitHint");
  if (!el) return;
  const ok = fmt === "7z" || fmt === "zip";
  el.classList.toggle("error", !ok);
  el.textContent = ok
    ? `支持分卷，可与密码同时使用；产物命名如 name.${fmt}.001，解压时选择 .001 首卷即可自动识别全部卷。`
    : `${fmt} 不支持分卷，分卷大小将被忽略；如需分卷请选择 7z 或 zip。`;
}
function splitSizeValue() {
  const n = ($("compSplit").value || "").trim();
  return n ? n + ($("compSplitUnit").value || "M") : "";
}
function syncCompressionAdvanced() {
  const fmt = $("compFormat").value;
  $("compMethod").disabled = !["7z", "zip"].includes(fmt);
  $("compDict").disabled = fmt !== "7z";
  $("compSolid").disabled = fmt !== "7z";
}
function compressionMethodValue() {
  const fmt = $("compFormat").value;
  const method = $("compMethod").value;
  if (fmt === "7z") return method === "Deflate" ? "LZMA2" : method;
  if (fmt === "zip") return method === "LZMA2" ? "Deflate" : method;
  return "";
}
$("compFormat").addEventListener("change", () => { syncCompExt(); syncSplitHint(); syncCompressionAdvanced(); });
$("compSplit").addEventListener("input", syncSplitHint);
$("compSplitUnit").addEventListener("change", syncSplitHint);
function syncTimestampOptions() {
  const enabled = $("compAutoTimestamp").checked;
  $("compTimestampFormat").disabled = !enabled;
  $("compTimestampHint").textContent = enabled
    ? `任务开始时自动添加${$("compTimestampFormat").value === "date" ? "日期" : "日期和时间"}，例如：我的资料_${$("compTimestampFormat").value === "date" ? "2026-09-15" : "2026-09-15_14-30-00"}${currentExt()}`
    : "开启后会在文件名扩展名前自动添加时间戳。";
}
$("compAutoTimestamp").addEventListener("change", syncTimestampOptions);
$("compTimestampFormat").addEventListener("change", syncTimestampOptions);
syncSplitHint();
syncCompressionAdvanced();
syncTimestampOptions();

$("compShowPassword").addEventListener("change", (e) => {
  $("compPassword").type = e.target.checked ? "text" : "password";
  $("compPasswordConfirm").type = e.target.checked ? "text" : "password";
});
$("compPassword").addEventListener("input", () => {
  const has = !!$("compPassword").value;
  $("compPasswordConfirmRow").hidden = !has;
  $("compPasswordHint").textContent = has ? "为避免输错密码，请再次输入确认。" : "";
});

$("btnCompress").addEventListener("click", async () => {
  const sources = [...srcItems];
  const taskTimestamp = compressionTimestamp();
  const dest = compDestination(taskTimestamp);
  if (!sources.length || !dest) return toast("请填写来源与输出文件", true);
  const password = $("compPassword").value;
  if (password && password !== $("compPasswordConfirm").value) {
    $("compPasswordHint").textContent = "两次输入的密码不一致，请重新确认";
    $("compPasswordHint").classList.add("error");
    return toast("密码确认不一致", true);
  }
  $("compPasswordHint").classList.remove("error");
  if ($("compDeleteSource").checked) {
    const go = await confirmDialog("压缩成功并通过测试后，将删除所选源文件/文件夹。此操作不可撤销，确定继续吗？", {
      danger: true, okText: "压缩并删除源文件", title: "危险操作",
    });
    if (!go) return;
  }
  logAction("开始压缩", "来源 " + sources.join(" | ") + " · 输出 " + dest + " · 格式 " + $("compFormat").value + ($("compBatch").checked ? " · 批量独立输出" : ""));
  try {
    const common = {
      format: $("compFormat").value,
      level: Number($("compLevel").value),
      password,
      vaultLabel: $("vaultSelect").value,
      splitSize: splitSizeValue(),
      method: compressionMethodValue(),
      dictionary: $("compFormat").value === "7z" ? $("compDict").value : "",
      solid: $("compFormat").value === "7z" && $("compSolid").checked,
      testAfter: $("compTest").checked,
      deleteSource: $("compDeleteSource").checked,
    };
    if ($("compBatch").checked && sources.length > 1) {
      // 批量模式：每个来源各打一个包，输出到同一目录（文件名 = 来源名 + 扩展名）
      const dir = $("compDestDir").value.trim().replace(/\/+$/, "") || "/";
      const ext = currentExt();
      await estimateCapacity("compress", sources, dir, password);
      const usedNames = new Set();
      let n = 0;
      for (const s of sources) {
        const rawBase = s.replace(/\/+$/, "").split("/").pop() || "archive";
        let base = taskTimestamp ? addCompressionTimestamp(rawBase, taskTimestamp) : rawBase;
        let serial = 2;
        while (usedNames.has(base.toLowerCase())) {
          base = taskTimestamp ? addCompressionTimestamp(rawBase + " (" + serial++ + ")", taskTimestamp) : rawBase + " (" + serial++ + ")";
        }
        usedNames.add(base.toLowerCase());
        await api("/api/archive/compress", {
          method: "POST",
          body: JSON.stringify(Object.assign({ sources: [s], dest: dir + "/" + base + ext }, common)),
        });
        n++;
      }
      toast(`已加入 ${n} 个独立压缩任务`);
    } else {
      // 空间检查针对输出目录，不是尚未创建的输出文件路径
      await estimateCapacity("compress", sources, $("compDestDir").value.trim() || "/", password);
      const data = await api("/api/archive/compress", {
        method: "POST",
        body: JSON.stringify(Object.assign({ sources, dest }, common)),
      });
      toastAction("压缩任务已创建", "查看", () => switchTab("jobs"));
    }
  } catch (e) {
    toast(`${e.message}${e.hint ? "（" + e.hint + "）" : ""}`, true);
  }
});

async function estimateCapacity(operation, paths, dest, password = "", options = {}) {
  let data;
  try {
    data = await api("/api/archive/estimate", {
      method: "POST",
      body: JSON.stringify({ operation, paths, dest, password }),
    });
  } catch (e) {
    if (options.allowPassword && (e.code === "PASSWORD_REQUIRED" || e.code === "PASSWORD_WRONG")) {
      toast("部分加密包暂无法预估展开大小，将在任务运行时按实际结果检查空间", false);
      return null;
    }
    throw e;
  }
  if (data.freeKnown && !data.sufficient) {
    throw new Error("预计空间不足：需要约 " + fmtBytes(data.estimatedOutputBytes) + "，当前位置可用 " + fmtBytes(data.freeBytes) + "。请清理空间或更换输出位置");
  }
  if (data.freeKnown) {
    const targetHint = operation === "compress" ? $("compHint") : ($("batchHint") || $("listHint"));
    if (targetHint) targetHint.textContent = "空间检查通过：预计占用 " + fmtBytes(data.estimatedOutputBytes) + "，可用 " + fmtBytes(data.freeBytes);
  }
  if (data.freeKnown && data.estimatedOutputBytes > 10 * 1024 * 1024 * 1024) {
    toast("这是大任务：预计写入 " + fmtBytes(data.estimatedOutputBytes) + "，任务期间请保持磁盘可用空间", false);
  }
  return data;
}

// ---------------- 任务
const STATE_META = {
  queued: { text: "排队中", cls: "state-queued" },
  running: { text: "进行中", cls: "state-running" },
  waiting_password: { text: "等待密码", cls: "state-waiting_password" },
  manual: { text: "手动", cls: "state-manual" },
  done: { text: "完成", cls: "state-done" },
  failed: { text: "失败", cls: "state-failed" },
  cancelled: { text: "已取消", cls: "state-cancelled" },
};
let jobFilter = "all";

async function refreshJobs() {
  try {
    const data = await api("/api/jobs?limit=100");
    const jobs = data.jobs || [];
    const counts = { all: jobs.length, active: 0, waiting: 0, manual: 0, done: 0, failed: 0 };
    for (const j of jobs) {
      if (["queued", "running"].includes(j.state)) counts.active++;
      else if (j.state === "waiting_password") counts.waiting++;
      else if (j.state === "manual") counts.manual++;
      else if (j.state === "done") counts.done++;
      else if (j.state === "failed") counts.failed++;
    }
    for (const [k, v] of Object.entries(counts)) {
      const el = $("cnt" + k[0].toUpperCase() + k.slice(1));
      if (el) el.textContent = v ? String(v) : "";
    }
    const badge = $("jobBadge");
    if (counts.waiting) { badge.hidden = false; badge.textContent = String(counts.waiting); }
    else if (counts.active) { badge.hidden = false; badge.textContent = String(counts.active); }
    else badge.hidden = true;
    const banner = $("globalBanner");
    if (counts.waiting) {
      $("globalBannerText").textContent = `${counts.waiting} 个任务等待输入密码`;
      banner.hidden = false;
    } else {
      banner.hidden = true;
    }
    const list = jobs.filter((j) => {
      if (jobFilter === "all") return true;
      if (jobFilter === "active") return ["queued", "running"].includes(j.state);
      if (jobFilter === "waiting") return j.state === "waiting_password";
      if (jobFilter === "manual") return j.state === "manual";
      if (jobFilter === "done") return ["done", "cancelled"].includes(j.state);
      if (jobFilter === "failed") return j.state === "failed";
      return true;
    });
    const box = $("jobList");
    box.innerHTML = "";
    $("jobEmpty").hidden = list.length > 0;
    for (const j of list) box.append(jobCard(j));
  } catch (e) {
    toast(e.message, true);
  }
}

function jobCard(j) {
  const meta = STATE_META[j.state] || { text: j.state, cls: "" };
  const pct = Math.max(0, Math.min(100, j.progress?.percent ?? 0));
  const speed = j.progress?.speedBps ? fmtBytes(j.progress.speedBps) + "/s" : "";
  const eta = j.state === "running" && j.progress?.etaSeconds > 0 ? "剩余 " + fmtDuration(j.progress.etaSeconds) : "";
  const sub = [j.srcPath, j.destPath].filter(Boolean).join("  →  ");
  const when = j.createdAt ? new Date(j.createdAt).toLocaleString("zh-CN", { hour12: false }) : "";
  const running = ["queued", "running"].includes(j.state);
  const isExtract = j.type === "extract";
  const actions = [];
  if (j.state === "waiting_password") {
    actions.push(`<button class="btn primary sm" data-act="password" data-id="${j.id}">输入密码</button>`);
    actions.push(`<button class="btn ghost sm" data-act="defer" data-id="${j.id}">稍后</button>`);
  }
  if (j.state === "manual") {
    actions.push(`<button class="btn primary sm" data-act="password" data-id="${j.id}">输密码并启动</button>`);
  }
  if (running) actions.push(`<button class="btn ghost sm" data-act="cancel" data-id="${j.id}">取消</button>`);
  if (j.state === "done" && j.destPath) {
    actions.push(`<button class="btn ghost sm" data-act="open" data-open="${escapeHtml(j.destPath)}">打开目录</button>`);
  }
  if (j.state === "failed" && j.error && j.error.detail) {
    actions.push(`<button class="btn ghost sm" data-act="detail" data-id="${j.id}">查看详情</button>`);
  }
  if (!running) actions.push(`<button class="btn ghost sm" data-act="del" data-id="${j.id}">删除</button>`);
  const card = document.createElement("div");
  card.className = "job-card";
  const showBar = running;
  const errLine = j.error
    ? `<div class="job-err"><span class="danger-text">${escapeHtml(j.error.message)}</span>${j.error.detail ? `<span class="muted"> ${escapeHtml(j.error.detail)}</span>` : ""}</div>`
    : (j.needsPassword ? `<div class="job-err"><span class="danger-text">输入密码后将继续解压</span></div>` : "");
  card.innerHTML = `
    <div class="job-row">
      <div class="job-icon sm"><svg><use href="${isExtract ? "#i-archive" : "#i-file"}"></use></svg></div>
      <div class="job-main">
        <div class="job-line1">
          <span class="job-title">${escapeHtml(j.title || j.type)}</span>
          <span class="state-badge ${meta.cls}">${meta.text}</span>
        </div>
        <div class="job-line2" title="${escapeHtml(sub)}">${escapeHtml(when)} · ${escapeHtml(sub)}</div>
      </div>
      <div class="job-actions">${actions.join(" ")}</div>
    </div>
    ${showBar ? `
    <div class="job-bar-row slim">
      <div class="bar"><i style="width:${pct}%"></i></div>
      <span class="pct">${pct.toFixed(0)}%</span>
      ${speed ? `<span class="muted">${speed}</span>` : ""}
      ${eta ? `<span class="muted">${eta}</span>` : ""}
    </div>` : ""}
    ${errLine}
    <div class="detail-box" id="detail-${j.id}" hidden>${escapeHtml(j.error?.detail || "")}</div>
  `;
  return card;
}

// 任务列表事件委托：卡片内按钮统一处理
$("jobList").addEventListener("click", async (ev) => {
  const btn = ev.target.closest("[data-act]");
  if (!btn) return;
  const id = btn.dataset.id;
  const act = btn.dataset.act;
  if (act === "cancel") {
    await api(`/api/jobs/${id}/cancel`, { method: "POST" });
    refreshJobs();
  } else if (act === "del") {
    await api(`/api/jobs/${id}`, { method: "DELETE" });
    refreshJobs();
  } else if (act === "open") {
    const p = btn.dataset.open;
    const done = window.fnosBridge ? await window.fnosBridge.openFileManager(p) : false;
    if (!done) toast("无法打开文件管理器，路径：" + p);
  } else if (act === "password") {
    const j = (await api("/api/jobs?limit=100")).jobs?.find((x) => x.id === id);
    if (j) openPasswordModal(j);
  } else if (act === "defer") {
    await api(`/api/jobs/${id}/password`, { method: "POST", body: JSON.stringify({ action: "defer" }) });
    toast("已转为手动任务，随时可输密码启动");
    refreshJobs();
  } else if (act === "detail") {
    const box = $("detail-" + id);
    if (box) box.hidden = !box.hidden;
  }
});

// 任务过滤 tab：右上清空按钮随选中分类联动
const FILTER_CLEAR = {
  all: { label: "清空全部", states: ["queued", "running", "waiting_password", "manual", "done", "failed", "cancelled"], confirm: true },
  waiting: { label: "清空等待密码", states: ["waiting_password"] },
  manual: { label: "清空手动", states: ["manual"] },
  done: { label: "清空已完成", states: ["done", "cancelled"] },
  failed: { label: "清空失败", states: ["failed"] },
  active: null,
};

function updateClearButton() {
  const cfg = FILTER_CLEAR[jobFilter];
  const btn = $("btnClearCurrent");
  if (!cfg) { btn.hidden = true; return; }
  btn.hidden = false;
  btn.textContent = cfg.label;
}

$("jobFilter").addEventListener("click", (ev) => {
  const chip = ev.target.closest(".chip");
  if (!chip) return;
  jobFilter = chip.dataset.filter;
  document.querySelectorAll("#jobFilter .chip").forEach((c) => c.classList.toggle("active", c === chip));
  updateClearButton();
  refreshJobs();
});

// ---------------- 任务密码续跑
let pwJobId = null;
let pwMode = "job"; // job = 任务密码续跑；list = 当前压缩包需要密码（打开/解压/预览）

function openListPasswordModal(message) {
  pwMode = "list";
  pwJobId = null;
  $("pwTitle").textContent = "需要密码";
  $("pwSub").textContent = $("srcPath").value.trim().split("/").pop() || "";
  $("pwInput").value = $("password").value || "";
  $("pwSave").checked = false;
  $("pwLabel").value = "";
  $("pwLabel").hidden = true;
  $("pwHint").textContent = message || "";
  $("pwHint").classList.remove("error");
  $("btnPwDefer").hidden = true;
  openDialog("passwordModal");
  setTimeout(() => $("pwInput").focus(), 80);
}

function openPasswordModal(job) {
  pwMode = "job";
  pwJobId = job.id;
  $("pwTitle").textContent = job.state === "manual" ? "手动任务：输密码启动" : "需要密码";
  $("pwSub").textContent = (job.srcPath || "").split("/").pop() || job.title || "";
  $("pwInput").value = "";
  $("pwSave").checked = false;
  $("pwLabel").value = "";
  $("pwLabel").hidden = true;
  $("pwHint").textContent = job.error?.message || "";
  $("pwHint").classList.remove("error");
  $("btnPwDefer").hidden = false;
  openDialog("passwordModal");
  setTimeout(() => $("pwInput").focus(), 80);
}

$("pwSave").addEventListener("change", (e) => { $("pwLabel").hidden = !e.target.checked; });

$("btnPwResume").addEventListener("click", async (ev) => {
  const pw = $("pwInput").value;
  if (!pw) { $("pwHint").textContent = "请输入密码"; return; }
  if (pwMode === "list") {
    // 先验证密码：失败保持弹框并红字提示，成功才应用到密码框
    $("pwHint").textContent = "正在验证密码…";
    $("pwHint").classList.remove("error");
    try {
      if (selectedEntryIndex != null) {
        const found = entries.find((x) => entryIdx(x) === selectedEntryIndex);
        if (found) await probeEntryPreview(found, pw);
      } else {
        await api("/api/archive/list", {
          method: "POST",
          body: JSON.stringify({ path: $("srcPath").value.trim(), password: pw }),
        });
      }
    } catch (e) {
      $("pwHint").textContent = (e.code === "PASSWORD_WRONG" || e.code === "PASSWORD_REQUIRED")
        ? "密码错误，请重试"
        : `${e.message}${e.detail ? "（" + e.detail + "）" : ""}`;
      $("pwHint").classList.add("error");
      return;
    }
    $("pwHint").classList.remove("error");
    $("password").value = pw;
    if ($("pwSave").checked) {
      const label = ($("srcPath").value.trim().replace(/\/+$/, "").split("/").pop() || "常用密码");
      try { await api("/api/vault", { method: "POST", body: JSON.stringify({ label, password: pw }) }); loadVault(); } catch (_) {}
    }
    closeDialog("passwordModal");
    if (selectedEntryIndex != null) {
      const found = entries.find((x) => entryIdx(x) === selectedEntryIndex);
      if (found) {
        const row = [...$("entryList").children].find((r) => r.dataset.path === found.path);
        showEntryPreview(found, row);
        return;
      }
    }
    openArchive();
    return;
  }
  const btn = ev.currentTarget;
  btn.classList.add("loading");
  try {
    await api(`/api/jobs/${pwJobId}/password`, {
      method: "POST",
      body: JSON.stringify({
        password: pw,
        action: "resume",
        save: $("pwSave").checked,
        label: $("pwLabel").value.trim(),
      }),
    });
    closeDialog("passwordModal");
    toast("已提交密码，任务继续");
    loadVault();
    refreshJobs();
  } catch (e) {
    $("pwHint").textContent = `${e.message}${e.hint ? "（" + e.hint + "）" : ""}`;
  } finally {
    btn.classList.remove("loading");
  }
});

$("btnPwDefer").addEventListener("click", async () => {
  try {
    await api(`/api/jobs/${pwJobId}/password`, { method: "POST", body: JSON.stringify({ action: "defer" }) });
    closeDialog("passwordModal");
    toast("已转为手动任务，不会自动运行");
    refreshJobs();
  } catch (e) { toast(e.message, true); }
});


$("btnPwClose").addEventListener("click", () => closeDialog("passwordModal"));

function escapeHtml(s) {
  return String(s == null ? "" : s)
    .replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;").replace(/'/g, "&#39;");
}

// 按状态集合清空任务；运行/排队中的任务先取消再删除
async function clearJobsOf(states) {
  const data = await api("/api/jobs?limit=100");
  let n = 0;
  for (const j of data.jobs || []) {
    if (!states.includes(j.state)) continue;
    if (["queued", "running"].includes(j.state)) {
      await api(`/api/jobs/${j.id}/cancel`, { method: "POST" });
    }
    await api(`/api/jobs/${j.id}`, { method: "DELETE" });
    n++;
  }
  refreshJobs();
  return n;
}

$("btnClearCurrent").addEventListener("click", async () => {
  const cfg = FILTER_CLEAR[jobFilter];
  if (!cfg) return;
  if (cfg.confirm) {
  const go = await confirmDialog("清空全部任务？运行中 / 排队中的任务会先被取消。", { danger: true, okText: "清空全部", title: "清空任务" });
  if (!go) return;
  }
  const n = await clearJobsOf(cfg.states);
  toast("已清空 " + n + " 条任务");
});

function fmtBytes(n) {
  if (!n) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB"];
  let i = 0;
  while (n >= 1024 && i < units.length - 1) { n /= 1024; i++; }
  return n.toFixed(n >= 10 || i === 0 ? 0 : 1) + " " + units[i];
}
function fmtDuration(sec) {
  sec = Math.round(sec);
  if (sec < 60) return sec + " 秒";
  const m = Math.floor(sec / 60);
  if (m < 60) return m + " 分 " + (sec % 60) + " 秒";
  return Math.floor(m / 60) + " 时 " + (m % 60) + " 分";
}

// 诊断日志：记录按钮、路径和格式变更，绝不记录密码字段。
document.addEventListener("click", (ev) => {
  const btn = ev.target.closest("button");
  if (btn && btn.id) logAction("点击按钮", btn.id + "：" + (btn.textContent || "").trim().slice(0, 80));
});
["srcPathInput", "destPath", "compDestDir", "compName", "scanPath", "batchDest", "browsePath"].forEach((id) => {
  const el = $(id);
  if (el) el.addEventListener("change", () => logAction("路径变更", id + "：" + el.value.trim()));
});
["compFormat", "compLevel", "compSplit", "batchMode", "batchConcurrent"].forEach((id) => {
  const el = $(id);
  if (el) el.addEventListener("change", () => logAction("选项变更", id + "：" + el.value));
});
$("btnRefresh").addEventListener("click", refreshJobs);
setInterval(() => {
  refreshJobs();
}, 3000);

// ---------------- 启动：支持文件管理器右键 ?path=
(async function init() {
  restoreUIState();
  syncCompExt();
  syncSplitHint();
  syncCompressionAdvanced();
  // 处理授权回调带回的目录（callback.html → sessionStorage → 在此登记）
  if (window.fnosBridge) {
    const pending = window.fnosBridge.takePendingPaths();
    if (pending.length) {
      try {
        const res = await api("/api/authorize/register", {
          method: "POST",
          body: JSON.stringify({ paths: pending }),
        });
        if ((res.registered || []).length) toast("授权成功：" + res.registered.join("、"));
      } catch (e) { /* 忽略：下面用 /api/context 复查 */ }
    }
  }
  try {
    const caps = await api("/api/capabilities");
    currentVersion = String(caps.appVersion || "dev");
    $("version").textContent = "v" + currentVersion;
    $("aboutVersion").textContent = "v" + currentVersion;
    $("aboutPlatform").textContent = caps.platform || "x86_64";
    $("aboutInfo").textContent = `飞牛 NAS 本地运行 · ${caps.cpuCores || "—"} 个 CPU 核心 · 任务与密码数据保存在本机`;
    await checkForUpdates({ silent: true });
  } catch { /* 忽略 */ }
  refreshJobs();
  updateClearButton();
  await refreshContextAndMaybeGate();
  const q = new URLSearchParams(location.search);
  const p = q.get("path");
  if (p) {
    $("srcPath").value = p;
    openArchive();
  }
})();

// ---------------- 记住上次使用的路径
const UI_KEY = "100zip.uiState";
function saveUIState() {
  try {
    localStorage.setItem(UI_KEY, JSON.stringify({
      dest: $("destPath").value,
      compDestDir: $("compDestDir").value,
      compName: $("compName").value,
      srcItems: srcItems,
      format: $("compFormat").value,
      autoTimestamp: $("compAutoTimestamp").checked,
      timestampFormat: $("compTimestampFormat").value,
    }));
  } catch (e) { /* 忽略 */ }
}
function restoreUIState() {
  try {
    const s = JSON.parse(localStorage.getItem(UI_KEY) || "{}");
    if (s.dest) $("destPath").value = s.dest;
    if (s.compDestDir) $("compDestDir").value = s.compDestDir;
    if (s.compName) $("compName").value = s.compName;
    if (!s.compName && s.compDest) {
      const old = String(s.compDest);
      $("compDestDir").value = old.replace(/\/[^/]*$/, "") || "/";
      $("compName").value = old.split("/").pop() || ("archive" + currentExt());
    }
    if (Array.isArray(s.srcItems)) srcItems = s.srcItems.filter((x) => typeof x === "string");
    else if (s.compSources) srcItems = String(s.compSources).split("\n").map((x) => x.trim()).filter(Boolean);
    if (s.format) $("compFormat").value = s.format;
    if (typeof s.autoTimestamp === "boolean") $("compAutoTimestamp").checked = s.autoTimestamp;
    if (s.timestampFormat) $("compTimestampFormat").value = s.timestampFormat;
  } catch (e) { /* 忽略 */ }
}
["destPath", "compDestDir", "compName", "compAutoTimestamp", "compTimestampFormat"].forEach((id) => {
  $(id).addEventListener("change", saveUIState);
});

// ---------------- 主题
function applyTheme(theme) {
  document.body.classList.toggle("dark", theme === "dark");
  const use = $("btnTheme").querySelector("use");
  if (use) use.setAttribute("href", theme === "dark" ? "#i-sun" : "#i-moon");
  $("btnTheme").title = theme === "dark" ? "切换到浅色" : "切换到深色";
  const sel = $("setTheme");
  if (sel) sel.value = theme;
  try { localStorage.setItem("100zip.theme", theme); } catch (e) {}
}
$("btnTheme").addEventListener("click", () => {
  applyTheme(document.body.classList.contains("dark") ? "light" : "dark");
});
$("setTheme").addEventListener("change", (e) => applyTheme(e.target.value));
try { applyTheme(localStorage.getItem("100zip.theme") || "light"); } catch (e) {}

// ---------------- 密码库
function fileIcon(name, isDir) {
  if (isDir) return "📁";
  const ext = (name.split(".").pop() || "").toLowerCase();
  if (["png", "jpg", "jpeg", "gif", "webp", "bmp", "svg", "heic", "ico"].includes(ext)) return "🖼️";
  if (["mp4", "mkv", "avi", "mov", "wmv", "flv", "ts", "m2ts"].includes(ext)) return "🎬";
  if (["mp3", "flac", "wav", "aac", "m4a", "ogg", "ape"].includes(ext)) return "🎵";
  if (ext === "pdf") return "📕";
  if (["doc", "docx"].includes(ext)) return "📝";
  if (["xls", "xlsx", "csv"].includes(ext)) return "📊";
  if (["ppt", "pptx"].includes(ext)) return "📽️";
  if (["txt", "md", "json", "xml", "log", "csv", "ini", "yml", "yaml", "srt", "ass"].includes(ext)) return "📄";
  return "📦";
}

async function loadVault() {
  try {
    const data = await api("/api/vault");
    const items = data.items || [];
    const opts = ['<option value="">（从密码库选择…）</option>']
      .concat(items.map((i) => `<option value="${escapeHtml(i.label)}">${escapeHtml(i.label)}（${i.length} 位）</option>`));
    $("vaultSelect").innerHTML = opts.join("");
    $("vaultListSet").innerHTML = items.length
      ? items.map((i) => `<option value="${escapeHtml(i.label)}">${escapeHtml(i.label)}（${i.length} 位）</option>`).join("")
      : '<option value="">（密码库为空）</option>';
    $("vaultPlaintext").value = "";
    $("vaultPlaintext").type = "password";
    $("btnVaultReveal").textContent = "显示";
  } catch (e) { /* 忽略 */ }
}

$("btnVaultSave").addEventListener("click", async () => {
  const pw = $("password").value;
  if (!pw) return toast("请先在「密码」框里输入密码", true);
  const auto = ($("srcPath").value.trim().replace(/\/+$/, "").split("/").pop() || "常用密码");
  const go = await confirmDialog(`将当前输入的密码以「${auto}」保存到密码库？`, { title: "保存密码", okText: "保存" });
  if (!go) return;
  const label = auto;
  try {
    await api("/api/vault", { method: "POST", body: JSON.stringify({ label, password: pw }) });
    toast("已保存到密码库：" + label);
    loadVault();
  } catch (e) { toast(e.message, true); }
});

async function deleteVault(label) {
  if (!label) return toast("请先选择一个密码", true);
  try {
    await api(`/api/vault/${encodeURIComponent(label)}`, { method: "DELETE" });
    toast("已删除：" + label);
    loadVault();
  } catch (e) { toast(e.message, true); }
}
$("btnVaultDelete2").addEventListener("click", () => deleteVault($("vaultListSet").value));
$("btnVaultReveal").addEventListener("click", async () => {
  const label = $("vaultListSet").value;
  if (!label) return toast("请先选择一个密码", true);
  const input = $("vaultPlaintext");
  if (input.value && input.type === "text") {
    input.type = "password";
    $("btnVaultReveal").textContent = "显示";
    return;
  }
  try {
    const data = await api("/api/vault/" + encodeURIComponent(label));
    input.type = "text";
    input.value = data.password || "";
    $("btnVaultReveal").textContent = "隐藏";
  } catch (e) { toast(e.message, true); }
});
$("vaultListSet").addEventListener("change", () => {
  $("vaultPlaintext").value = "";
  $("vaultPlaintext").type = "password";
  $("btnVaultReveal").textContent = "显示";
});
$("vaultSelect").addEventListener("change", (e) => {
  if (e.target.value) $("password").value = "";
});

// ---------------- 设置页
async function loadPrefs() {
  try {
    const p = await api("/api/prefs");
    const caps = await api("/api/capabilities").catch(() => ({}));
    const cores = Math.max(1, Number(caps.cpuCores || 1));
    const max = Math.max(cores, Number(p.maxConcurrent || caps.recommendedConcurrent || cores));
    const sel = $("setConcurrent");
    sel.innerHTML = "";
    for (let n = 1; n <= Math.min(32, max); n++) {
      const opt = document.createElement("option");
      opt.value = String(n);
      opt.textContent = n + (n === cores ? "（等于 CPU 核心数）" : "");
      sel.append(opt);
    }
    sel.value = String(Math.min(Math.min(32, max), Number(p.maxConcurrent || caps.recommendedConcurrent || cores)));
    $("settingsHint").textContent = "检测到 NAS " + cores + " 个 CPU 核心，建议并发 " + Math.min(cores, 8) + "；压缩/解压主要受 CPU 与磁盘吞吐影响，当前版本不使用显卡。";
  } catch (e) { /* 忽略 */ }
}

// ---------------- 包内预览
async function previewEntry(index, label) {
  const body = $("previewBody");
  body.classList.remove("text-preview");
  $("previewTitle").textContent = label;
  $("previewHint").textContent = "正在读取…";
  body.innerHTML = "";
  $("previewModal").hidden = false;
  try {
    const res = await fetch(APP_BASE + "/api/archive/preview", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        path: $("srcPath").value.trim(),
        entryIndex: Number(index),
        password: $("password").value,
        vaultLabel: $("vaultSelect").value,
      }),
    });
    const ctype = res.headers.get("content-type") || "";
    if (!res.ok) {
      const err = await res.json().catch(() => ({}));
      const ex = new Error((err.error && err.error.message) || ("HTTP " + res.status));
      ex.code = err.error && err.error.code;
      ex.detail = err.error && err.error.detail;
      throw ex;
    }
    if (ctype.startsWith("image/")) {
      const blob = await res.blob();
      const img = document.createElement("img");
      img.src = URL.createObjectURL(blob);
      img.style.maxWidth = "100%";
      body.append(img);
      $("previewHint").textContent = "图片预览";
      return;
    }
    if (ctype.startsWith("audio/") || ctype.startsWith("video/") || ctype === "application/pdf") {
      const blob = await res.blob();
      const media = ctype === "application/pdf" ? document.createElement("iframe") : document.createElement(ctype.startsWith("audio/") ? "audio" : "video");
      const url = URL.createObjectURL(blob);
      media.src = ctype === "application/pdf" ? url + "#page=1&zoom=page-width" : url;
      if (ctype === "application/pdf") {
        media.title = "PDF 预览";
        media.setAttribute("loading", "lazy");
      }
      media.controls = true;
      media.style.width = "100%";
      media.style.maxHeight = "70vh";
      body.append(media);
      $("previewHint").textContent = ctype === "application/pdf" ? "PDF 预览" : "媒体预览（浏览器支持情况取决于格式）";
      return;
    }
    const data = await res.json();
    const d = data.data || {};
    if (["text", "document", "spreadsheet", "presentation"].includes(d.kind)) {
      const pre = document.createElement("pre");
      pre.style.whiteSpace = "pre-wrap";
      pre.style.wordBreak = "break-all";
      pre.textContent = d.content || "";
      body.append(pre);
      body.classList.add("text-preview");
      $("previewHint").textContent = d.kind === "document" ? "Word 文字预览（不还原排版）" : d.kind === "spreadsheet" ? "Excel 内容预览（不还原表格样式）" : d.kind === "presentation" ? "PPT 文字预览（不还原版式）" : (d.truncated ? "内容过长，仅显示前 256 KB" : "文本预览");
      return;
    }
    $("previewHint").textContent = d.message || "暂不支持预览该格式";
  } catch (e) {
    $("previewHint").textContent = "预览失败：" + e.message + (e.detail ? "（" + e.detail + "）" : "");
    if (e.code === "PASSWORD_REQUIRED" || e.code === "PASSWORD_WRONG") openListPasswordModal(e.message);
  }
}

$("btnPreviewClose").addEventListener("click", () => {
  $("previewBody").querySelectorAll("img,iframe,audio,video").forEach((media) => {
    if (media.src && media.src.startsWith("blob:")) URL.revokeObjectURL(media.src.split("#")[0]);
  });
  $("previewBody").innerHTML = "";
  closeDialog("previewModal");
});
$("btnPreviewFullscreen").addEventListener("click", async () => {
  const card = $("previewModal").querySelector(".dialog-card");
  try {
    if (!document.fullscreenElement) {
      await card.requestFullscreen();
      $("btnPreviewFullscreen").title = "退出全屏";
      $("btnPreviewFullscreen").setAttribute("aria-label", "退出全屏");
    } else {
      await document.exitFullscreen();
      $("btnPreviewFullscreen").title = "全屏预览";
      $("btnPreviewFullscreen").setAttribute("aria-label", "全屏预览");
    }
  } catch (e) {
    toast("当前环境不支持全屏预览，请使用浏览器放大", true);
  }
});
document.addEventListener("fullscreenchange", () => {
  if (!document.fullscreenElement) {
    $("btnPreviewFullscreen").title = "全屏预览";
    $("btnPreviewFullscreen").setAttribute("aria-label", "全屏预览");
  }
});

// 弹窗：遮罩点击 / Esc 关闭
document.querySelectorAll(".dialog").forEach((d) => {
  d.addEventListener("click", (e) => {
    if (e.target !== d) return;
    if (d.id === "confirmModal" && confirmDialog._resolve) {
      confirmDialog._resolve(false);
      confirmDialog._resolve = null;
    }
    d.hidden = true;
  });
});
document.addEventListener("keydown", (e) => {
  if (e.key !== "Escape") return;
  if (confirmDialog._resolve) {
    confirmDialog._resolve(false);
    confirmDialog._resolve = null;
  }
  for (const id of ["previewModal", "passwordModal", "confirmModal", "browseModal"]) {
    const el = $(id);
    if (el && !el.hidden) el.hidden = true;
  }
});
$("btnSavePrefs").addEventListener("click", async () => {
  try {
    await api("/api/prefs", {
      method: "POST",
      body: JSON.stringify({ maxConcurrent: Number($("setConcurrent").value) }),
    });
    $("settingsHint").textContent = "已保存并立即生效。建议不要超过 NAS CPU 核心数，机械硬盘或网络盘可适当调低。";
  } catch (e) { $("settingsHint").textContent = e.message; }
});

$("btnDiagnostics").addEventListener("click", async () => {
  try {
    const data = await api("/api/diagnostics");
    data.client = { userAgent: navigator.userAgent, language: navigator.language, viewport: String(innerWidth) + "x" + String(innerHeight), view: document.querySelector(".view.active")?.id || "" };
    data.userActions = activityLog.slice(-ACT_MAX);
    const blob = new Blob([JSON.stringify(data, null, 2)], { type: "application/json" });
    const a = document.createElement("a");
    a.href = URL.createObjectURL(blob);
    a.download = "100zip-diagnostics.json";
    a.click();
    URL.revokeObjectURL(a.href);
  } catch (e) { toast(e.message, true); }
});

// 更新检查只读取 GitHub Releases 元数据，不上传用户文件、密码或诊断日志。
$("btnCheckUpdate").addEventListener("click", () => checkForUpdates());
$("topUpdateButton").addEventListener("click", openLatestRelease);
$("btnOpenUpdate").addEventListener("click", openLatestRelease);

// 启动时加载密码库/偏好，并填充关于信息
loadVault();
loadPrefs();
api("/api/capabilities").then((c) => {
    currentVersion = String(c.appVersion || currentVersion || "dev");
    $("version").textContent = "v" + currentVersion;
    $("aboutVersion").textContent = "v" + currentVersion;
    $("aboutPlatform").textContent = c.platform || "x86_64";
    $("aboutInfo").textContent = `飞牛 NAS 本地运行 · ${c.cpuCores || "—"} 个 CPU 核心 · 任务与密码数据保存在本机`;
}).catch(() => {});

// ---------------- 批量扫描与解压
let scanFiles = [];

$("btnPickScanDir").addEventListener("click", async () => {
  try {
    const dirs = await safePick("dir");
    if (!dirs.length) return;
    await registerPaths(dirs);
    $("scanPath").value = dirs[0];
    refreshContextAndMaybeGate();
  } catch (e) { toast(e.message, true); }
});

$("btnScan").addEventListener("click", async () => {
  const path = $("scanPath").value.trim();
  if (!path) return toast("请填写要扫描的目录", true);
  $("scanHint").textContent = "正在扫描…";
  try {
    const res = await api("/api/scan", {
      method: "POST",
      body: JSON.stringify({
        path,
        recursive: $("scanRecursive").checked,
        archivesOnly: $("scanArchivesOnly").checked,
      }),
    });
    scanFiles = res.files || [];
    renderScanList();
    $("scanResultCard").hidden = false;
    const missingCount = scanFiles.filter((f) => Array.isArray(f.missing) && f.missing.length).length;
    $("scanCount").textContent = `共 ${res.total} 项（已自动跳过后续分卷）`;
    if (!scanFiles.length) {
      $("scanHint").textContent = "该目录下没有找到可处理的文件";
      $("scanHint").classList.remove("error");
    } else if (missingCount) {
      $("scanHint").textContent = `扫描完成：有 ${missingCount} 个分卷包缺少分卷文件（已默认不勾选），请补齐同一目录下全部卷后再解压`;
      $("scanHint").classList.add("error");
    } else {
      $("scanHint").textContent = "扫描完成，勾选后点下方「开始批量解压」";
      $("scanHint").classList.remove("error");
    }
  } catch (e) {
    $("scanHint").textContent = `${e.message}${e.hint ? "（" + e.hint + "）" : ""}`;
  }
});

function renderScanList() {
  const box = $("scanList");
  box.innerHTML = "";
  for (const [i, f] of scanFiles.entries()) {
    const missing = Array.isArray(f.missing) ? f.missing : [];
    const isDirRow = f.kind === "dir";
    const row = document.createElement("div");
    row.className = "item" + (isDirRow ? " dir" : "");
    const cb = document.createElement("input");
    cb.type = "checkbox";
    // 缺卷的包默认不勾选；目录行仅供展示（批量解压不支持目录条目）
    cb.disabled = isDirRow;
    cb.checked = f.kind === "archive" && missing.length === 0;
    cb.dataset.index = String(i);
    const name = document.createElement("span");
    const enc = f.encrypted === true ? "🔒 " : "";
    let volText = "";
    if (f.volume) {
      const t = f.total || 0;
      if (missing.length) volText = `（分卷 ${t || "?"} 卷 · 缺 ${missing.length}）`;
      else if (t > 1) volText = `（分卷 ${t} 卷 · 完整）`;
      else volText = "（分卷首卷）";
    }
    name.textContent = `${fileIcon(f.name, isDirRow)} ${enc}${f.name}${volText}`;
    const size = document.createElement("span");
    size.className = "size";
    if (missing.length) {
      size.style.color = "#f87171";
      size.textContent = `⚠️ 缺 ${missing.length} 卷（${missing.slice(0, 3).join("、")}${missing.length > 3 ? "…" : ""}）· ${fmtBytes(f.size)}`;
    } else {
      size.textContent = fmtBytes(f.size);
    }
    row.append(cb, name, size);
    box.append(row);
  }
}

$("btnScanSelectAll").addEventListener("click", () => {
  const boxes = [...$("scanList").querySelectorAll("input[type=checkbox]")];
  const all = boxes.every((b) => b.checked);
  boxes.forEach((b) => { b.checked = !all; });
});

$("btnPickBatchDest").addEventListener("click", async () => {
  try {
    const dirs = await safePick("dir");
    if (!dirs.length) return;
    await registerPaths(dirs);
    $("batchDest").value = dirs[0];
    $("batchMode").value = "custom";
  } catch (e) { toast(e.message, true); }
});

// 「指定目录」模式才显示目标输入行
$("batchMode").addEventListener("change", () => {
  $("batchCustomRow").hidden = $("batchMode").value !== "custom";
});

$("btnBatchExtract").addEventListener("click", async () => {
  const picked = [...$("scanList").querySelectorAll("input[type=checkbox]:checked")]
    .map((b) => scanFiles[Number(b.dataset.index)]);
  if (!picked.length) return toast("请先勾选要处理的文件", true);
  logAction("开始批量解压", picked.map((f) => f.path).join(" | ") + " · 数量 " + picked.length);
  const missing = picked.filter((f) => Array.isArray(f.missing) && f.missing.length);
  if (missing.length) {
    const names = missing.slice(0, 3).map((f) => f.name).join("、");
    return toast(`有 ${missing.length} 个分卷包缺少分卷文件，无法解压：${names}${missing.length > 3 ? " 等" : ""}`, true);
  }
  const mode = $("batchMode").value;
  if (mode === "custom" && !$("batchDest").value.trim()) return toast("请选择指定目录", true);
  if ($("batchAutoDelete").checked) {
    const go = await confirmDialog("已勾选「解压成功后删除源包」。\n\n这会永久删除这些压缩包（分卷包的其它卷不会被删除）。确定继续吗？", {
      danger: true, okText: "删除并解压", title: "危险操作",
    });
    if (!go) return;
  }
  $("batchHint").textContent = "正在创建任务…";
  try {
    await api("/api/prefs", {
      method: "POST",
      body: JSON.stringify({ maxConcurrent: Number($("batchConcurrent").value) }),
    });
    let n = 0;
    for (const f of picked) {
      await estimateCapacity("extract", [f.path], $("batchDest").value.trim() || $("scanPath").value.trim(), "", { allowPassword: true });
      await api("/api/archive/extract", {
        method: "POST",
        body: JSON.stringify({
          path: f.path,
          dest: $("batchDest").value.trim() || $("scanPath").value.trim(),
          destMode: mode,
          // 批量模式的“压缩包所在文件夹”就是直接解到该目录；
          // “以包名新建子文件夹”由 destMode=subdir 负责。
          createSubdir: false,
          overwrite: "rename",
          fixEncoding: true,
          tryVault: $("batchTryVault").checked,
          autoDelete: $("batchAutoDelete").checked && f.kind === "archive",
        }),
      });
      n++;
    }
    $("batchHint").textContent = `已创建 ${n} 个解压任务（并发 ${$("batchConcurrent").value}）`;
    switchTab("jobs");
  } catch (e) {
    $("batchHint").textContent = `${e.message}${e.hint ? "（" + e.hint + "）" : ""}`;
  }
});

// ---------------- 首次使用授权引导
async function refreshContextAndMaybeGate() {
  try {
    const ctx = await api("/api/context");
    const folders = ctx.accessibleFolders || [];
      if (!folders.length) {
        showAuthGate();
        $("authBanner").hidden = false;
      } else {
          hideAuthGate();
          $("authBanner").hidden = true;
          if (!$("destPath").value) $("destPath").value = folders[0];
      }
      $("rootInfo").textContent = folders.length ? ("已授权目录：" + folders.join("、")) : "";
      return ctx;
  } catch (e) {
    return null;
  }
}

function showAuthGate() {
  $("authGate").hidden = false;
}
function hideAuthGate() {
  $("authGate").hidden = true;
}

async function doAuthorize(btn) {
  const hint = $("gateHint");
  if (!window.fnosBridge) { hint.textContent = "SDK 桥接未加载"; return; }
  try {
    // 注意：桥接层的取值方法名是 getSdk()（返回 {sdk, via}）；早期版本误用 resolveSdk() 会导致静默失败
    const found = (typeof window.fnosBridge.getSdk === "function") ? window.fnosBridge.getSdk() : null;
    if (!found || !found.sdk) {
      hint.textContent = "授权 SDK 不可用（未检测到宿主授权能力）。请点「如何手动授权？」按步骤操作。";
      return;
    }
    btn.disabled = true;
    hint.textContent = "正在打开目录选择器…";
    const paths = await safePick("dir");
    if (!paths || !paths.length) { hint.textContent = "未选择目录"; return; }
    const res = await api("/api/authorize/register", {
      method: "POST",
      body: JSON.stringify({ paths }),
    });
    if ((res.registered || []).length) {
      hint.textContent = "已授权：" + res.registered.join("、");
      hideAuthGate();
      toast("授权成功：" + res.registered[0]);
      refreshContextAndMaybeGate();
    } else {
      hint.textContent = "授权未通过校验：" + (res.rejected || []).join("、");
    }
  } catch (e) {
    hint.textContent = `${e.message}${e.hint ? "（" + e.hint + "）" : ""}`;
  } finally {
    btn.disabled = false;
  }
}

$("btnGateAuth").addEventListener("click", (ev) => doAuthorize(ev.currentTarget));
$("btnGateLater").addEventListener("click", hideAuthGate);
$("btnGateSettings").addEventListener("click", async () => {
  const opened = window.fnosBridge ? await window.fnosBridge.openAppSetting() : false;
  if (opened) {
    $("gateHint").textContent = "已打开应用设置：请在「访问权限」中添加目录，完成后回到本页点「我已授权，刷新」。";
    return;
  }
  $("gateHelp").hidden = false;
  $("gateHint").textContent = "当前宿主未提供应用设置跳转接口，请按上面步骤手动授权。";
});
$("btnGateRefresh").addEventListener("click", async () => {
  $("gateHint").textContent = "正在检查授权状态…";
  const ctx = await refreshContextAndMaybeGate();
  const folders = (ctx && ctx.accessibleFolders) || [];
  if (folders.length) {
    $("gateHint").textContent = "已检测到授权目录：" + folders.join("、");
  } else {
    $("gateHint").textContent = "仍未检测到授权目录。若已在设置中授权，请稍等片刻或重启应用后再试（授权通过环境变量注入，需服务重启后生效）。";
  }
});

$("btnBannerAuth").addEventListener("click", showAuthGate);

// ---------------- 路径选择器（全部走飞牛系统选择器，免手打）
const ARCHIVE_ACCEPT = [".zip", ".7z", ".rar", ".tar", ".gz", ".tgz", ".bz2", ".xz", ".zst", ".001"];

async function registerPaths(paths) {
  if (!paths || !paths.length) return [];
  try {
    const res = await api("/api/authorize/register", {
      method: "POST",
      body: JSON.stringify({ paths }),
    });
    return res.registered || [];
  } catch (e) {
    toast("登记授权失败：" + e.message, true);
    return [];
  }
}

$("btnPickArchive").addEventListener("click", async () => {
  try {
    const files = await safePick("file", { accept: ARCHIVE_ACCEPT, multiple: false, title: "选择压缩包" });
    if (!files.length) return toast("未选择文件");
    $("srcPath").value = files[0];
    await registerPaths(files);          // 文件授权本身已由系统完成，这里同步登记
    openArchive();
  } catch (e) { toast(e.message, true); }
});

$("btnPickDest").addEventListener("click", async () => {
  try {
    const dirs = await safePick("dir");
    if (!dirs.length) return toast("未选择目录");
    await registerPaths(dirs);
    $("destPath").value = dirs[0];
    refreshContextAndMaybeGate();
  } catch (e) { toast(e.message, true); }
});

$("btnPickSrcFiles").addEventListener("click", async () => {
  try {
    const files = await safePick("file", { multiple: true, title: "选择要压缩的文件" });
    if (!files.length) return;
    let added = 0;
    for (const f of files) {
      if (!srcItems.includes(f)) { srcItems.push(f); added++; }
    }
    await registerPaths(files);
    renderSources();
    saveUIState();
    if (added) toast(`已加入 ${added} 项到来源`);
  } catch (e) { toast(e.message, true); }
});

$("btnPickSrcDir").addEventListener("click", async () => {
  // 优先走飞牛原生目录选择器（与「添加文件」同款交互）；无宿主环境时退回树状浏览
  if (window.fnosBridge && typeof window.fnosBridge.pickDirectory === "function") {
    try {
      const dirs = await safePick("dir");
      if (!dirs || !dirs.length) return;
      let added = 0;
      for (const d of dirs) {
        if (!srcItems.includes(d)) { srcItems.push(d); added++; }
      }
      await registerPaths(dirs);
      renderSources();
      saveUIState();
      if (added) toast(`已加入 ${added} 个文件夹到来源`);
      return;
    } catch (e) {
      // 宿主选择器不可用：继续走下方自定义浏览
    }
  }
  openDialog("browseModal");
  browsePathRestore();
});

// ---------------- 压缩来源浏览
const browseChecked = new Set();
const browseExpanded = new Set();

// 树状懒加载浏览：勾目录 = 整目录打包，可展开到任意层级勾选具体文件
async function browsePathRestore() {
  let saved = "";
  try { saved = localStorage.getItem("100zip.browsePath") || ""; } catch (_) {}
  // 记忆值必须落在授权根内，否则用授权根第一个起步
  // （v0.5.7 教训：0.5.6 曾把 "/" 写进记忆，记忆优先会绕过授权目录起步）
  try {
    const ctx = await api("/api/context");
    const folders = (ctx.accessibleFolders || []).map((f) => f.replace(/\/+$/, ""));
    if (folders.length) {
      const inside = folders.some((f) => saved && (saved === f || saved.startsWith(f + "/")));
      if (!inside) saved = folders[0];
    } else if (saved === "/") {
      saved = "";
    }
  } catch (_) {
    if (saved === "/") saved = "";
  }
  if (saved) {
    $("browsePath").value = saved;
    browseLoad();
    return;
  }
  $("browsePath").value = "";
  $("browseHint").textContent = "请填写文件夹路径，然后点「载入」";
}

async function browseLoad() {
  const p = $("browsePath").value.trim().replace(/\/+$/, "") || "/";
  $("browsePath").value = p;
  try { localStorage.setItem("100zip.browsePath", p); } catch (_) {}
  browseChecked.clear();
  browseExpanded.clear();
  const box = $("browseList");
  box.innerHTML = "";
  $("browseHint").textContent = "载入中…";
  try {
    const res = await api("/api/scan", {
      method: "POST",
      body: JSON.stringify({ path: p, recursive: false, archivesOnly: false }),
    });
    const files = res.files || [];
    for (const f of files) box.append(browseItemRow(f));
    $("browseHint").textContent = files.length
      ? "勾选要打包的内容（选文件夹 = 整个文件夹打包），然后点「加入来源」"
      : "该目录为空";
  } catch (e) {
    $("browseHint").textContent = `${e.message}${e.hint ? "（" + e.hint + "）" : ""}`;
  }
}

function browseItemRow(f) {
  const isDir = f.kind === "dir";
  const row = document.createElement("div");
  row.className = "item browse-item";
  row.dataset.path = f.path;
  row.dataset.isdir = isDir ? "1" : "0";
  if (isDir) {
    const exp = document.createElement("button");
    exp.className = "expander";
    exp.dataset.expand = f.path;
    exp.title = "展开";
    exp.setAttribute("aria-label", "展开");
    exp.innerHTML = '<svg><use href="#i-play"></use></svg>';
    row.append(exp);
  } else {
    const pad = document.createElement("span");
    pad.className = "expander-pad";
    row.append(pad);
  }
  const cb = document.createElement("input");
  cb.type = "checkbox";
  cb.dataset.path = f.path;
  cb.dataset.isdir = isDir ? "1" : "0";
  cb.checked = browseChecked.has(f.path);
  row.append(cb);
  const name = document.createElement("span");
  name.className = "name";
  name.textContent = `${fileIcon(f.name || f.path, isDir)}  ${f.name || f.path.split("/").pop()}`;
  row.append(name);
  const size = document.createElement("span");
  size.className = "size";
  size.textContent = isDir ? "文件夹" : fmtBytes(f.size || 0);
  row.append(size);
  return row;
}

// 展开 / 收起子目录（懒加载一层）
$("browseList").addEventListener("click", async (ev) => {
  const exp = ev.target.closest("[data-expand]");
  if (!exp) return;
  const path = exp.dataset.expand;
  const row = exp.closest(".item");
  let kids = row.nextElementSibling;
  if (kids && kids.classList.contains("browse-children")) {
    kids.hidden = !kids.hidden;
    exp.classList.toggle("expanded", !kids.hidden);
    if (!kids.hidden) browseExpanded.add(path); else browseExpanded.delete(path);
    return;
  }
  exp.classList.add("busy");
  kids = document.createElement("div");
  kids.className = "browse-children";
  row.after(kids);
  try {
    const res = await api("/api/scan", {
      method: "POST",
      body: JSON.stringify({ path, recursive: false, archivesOnly: false }),
    });
    for (const f of res.files || []) kids.append(browseItemRow(f));
    if (!(res.files || []).length) {
      const t = document.createElement("div");
      t.className = "item dir";
      t.textContent = "（空）";
      kids.append(t);
    }
    kids.hidden = false;
    exp.classList.add("expanded");
    browseExpanded.add(path);
  } catch (e) {
    kids.remove();
    toast(e.message, true);
  } finally {
    exp.classList.remove("busy");
  }
});

// 勾选：目录 = 整目录打包；已加载的子孙视觉同步；父链显示半选
$("browseList").addEventListener("change", (ev) => {
  const cb = ev.target;
  if (cb.type !== "checkbox") return;
  const path = cb.dataset.path;
  if (cb.checked) browseChecked.add(path); else browseChecked.delete(path);
  const row = cb.closest(".item");
  if (cb.dataset.isdir === "1") syncBrowseSubtree(row, cb.checked);
  syncBrowseAncestors(cb);
  updateBrowseCount();
});

function syncBrowseSubtree(row, checked) {
  const kids = row.nextElementSibling;
  if (!kids || !kids.classList.contains("browse-children")) return;
  for (const c of kids.querySelectorAll("input[type=checkbox]")) {
    c.checked = checked;
    c.indeterminate = false;
    if (checked) browseChecked.add(c.dataset.path); else browseChecked.delete(c.dataset.path);
  }
}

function syncBrowseAncestors(cb) {
  let cont = cb.closest(".browse-children");
  while (cont) {
    const prow = cont.previousElementSibling;
    if (!prow || !prow.classList.contains("item")) break;
    const pcb = prow.querySelector("input[type=checkbox]");
    if (pcb) {
      const kids = cont.querySelectorAll("input[type=checkbox]");
      const total = kids.length;
      const on = [...kids].filter((k) => k.checked).length;
      pcb.checked = total > 0 && on === total;
      pcb.indeterminate = on > 0 && on < total;
      if (pcb.checked) browseChecked.add(pcb.dataset.path);
      else browseChecked.delete(pcb.dataset.path);
    }
    cont = cont.parentElement ? cont.parentElement.closest(".browse-children") : null;
  }
}

function updateBrowseCount() {
  $("browseCount").textContent = browseChecked.size ? `已勾选 ${browseChecked.size} 项` : "";
}

$("btnBrowseSelectAll").addEventListener("click", () => {
  const boxes = [...$("browseList").querySelectorAll("input[type=checkbox]")];
  const all = boxes.length > 0 && boxes.every((b) => b.checked);
  for (const b of boxes) {
    b.checked = !all;
    b.indeterminate = false;
    if (!all) browseChecked.add(b.dataset.path); else browseChecked.delete(b.dataset.path);
  }
  updateBrowseCount();
});

$("btnBrowseLoad").addEventListener("click", browseLoad);
$("browsePath").addEventListener("keydown", (e) => { if (e.key === "Enter") browseLoad(); });
$("btnBrowseClose").addEventListener("click", () => closeDialog("browseModal"));

// 提交：树去重（勾父 = 父覆盖子孙），加入来源
$("btnBrowseAdd").addEventListener("click", () => {
  const picks = dedupePaths([...browseChecked]);
  if (!picks.length) { $("browseHint").textContent = "请先勾选要打包的内容"; return; }
  let added = 0;
  for (const p of picks) {
    if (!srcItems.includes(p)) { srcItems.push(p); added++; }
  }
  closeDialog("browseModal");
  renderSources();
  saveUIState();
  if (added) toast(`已加入 ${added} 项到来源`);
});

function dedupePaths(paths) {
  const norm = (p) => p.replace(/\/+$/, "");
  const sorted = paths.map(norm).sort();
  const kept = [];
  const out = [];
  for (const p of sorted) {
    if (kept.some((k) => p === k || p.startsWith(k + "/"))) continue;
    kept.push(p);
    out.push(p);
  }
  return out;
}

$("btnClearSrc").addEventListener("click", () => { srcItems = []; renderSources(); saveUIState(); });

$("btnPickCompDest").addEventListener("click", async () => {
  try {
    const dirs = await safePick("dir");
    if (!dirs.length) return toast("未选择目录");
    await registerPaths(dirs);
    $("compDestDir").value = dirs[0].replace(/\/$/, "");
    if (!$("compName").value.trim()) $("compName").value = "archive" + currentExt();
    saveUIState();
  } catch (e) { toast(e.message, true); }
});
