// 飞牛授权桥接层（ES Module）。
//
// 依据官方文档采用「随包分发官方 SDK」的方式：@trimjs/web-app（已 vendor 到 js/vendor/）。
// 两种运行环境都要支持：
//   1) 桌面内嵌（micro_app=true + 统一网关）：isStandaloneWeb=false → 直接 pickUserFile
//   2) 独立浏览器标签：isStandaloneWeb=true → openAppAuth 授权路由 + callback.html 回调
// 另保留「宿主注入 SDK」的兜底探测。

import { TrimApp } from "./vendor/trimjs-web-app.js";

const APP_NAME = "100zip";
const CALLBACK_PATH = "/app/100zip/callback.html";
const STORAGE_KEY = "100zip.pendingAuthPaths";

let cached = null;

function tryHostInjected() {
  const scopes = [];
  const push = (s) => { if (s && !scopes.includes(s)) scopes.push(s); };
  push(window);
  try { push(window.parent); } catch (e) { /* 跨域 */ }
  try { push(window.top); } catch (e) { /* 跨域 */ }
  for (const scope of scopes) {
    const list = [scope.fnApp];
    for (const ctorName of ["TrimApp", "FnApp"]) {
      if (typeof scope[ctorName] === "function") {
        try { list.push(new (scope[ctorName])()); } catch (e) { /* 忽略 */ }
      }
    }
    for (const c of list) {
      if (c && (typeof c.pickUserFile === "function" || typeof c.authorizeUserFile === "function")) {
        return { sdk: c, via: "host-injected" };
      }
    }
  }
  return null;
}

function getSdk() {
  if (cached) return cached;
  try {
    cached = { sdk: new TrimApp(), via: "npm:@trimjs/web-app" };
    return cached;
  } catch (e) { /* 继续兜底 */ }
  const injected = tryHostInjected();
  if (injected) cached = injected;
  return cached;
}

function requireSdk() {
  const found = getSdk();
  if (!found) {
    const err = new Error("飞牛授权 SDK 不可用");
    err.code = "SDK_UNAVAILABLE";
    throw err;
  }
  return found.sdk;
}

function isStandalone() {
  const found = getSdk();
  if (found && typeof found.sdk.isStandaloneWeb === "boolean") return found.sdk.isStandaloneWeb;
  return window === window.top;
}

function newState() {
  return Math.random().toString(36).slice(2) + Date.now().toString(36);
}

// 选择目录并授权；返回已授权路径数组（独立浏览器环境下返回空数组，由 callback.html 处理）。
async function pickDirectory() {
  const sdk = requireSdk();
  if (isStandalone() && typeof sdk.openAppAuth === "function") {
    const state = newState();
    sessionStorage.setItem("100zip.authState", state);
    await sdk.openAppAuth("pickUserFile", {
      appName: APP_NAME,
      directory: true,
      redirectUri: CALLBACK_PATH,
      state,
    }, { target: "_self" });
    return [];
  }
  const res = await sdk.pickUserFile({
    directory: true,
    title: "选择要授权给 100解压 的目录",
    okText: "确认授权",
  });
  return (res && res.data) || [];
}

// 选择文件（可限定扩展名）；返回被授权/选中的文件路径数组。
async function pickFiles(options = {}) {
  const sdk = requireSdk();
  const params = {
    directory: false,
    multiple: options.multiple !== false,
    title: options.title || "选择文件",
    okText: "确认",
  };
  if (options.accept && options.accept.length) params.accept = options.accept;
  if (isStandalone() && typeof sdk.openAppAuth === "function") {
    const state = newState();
    sessionStorage.setItem("100zip.authState", state);
    await sdk.openAppAuth("pickUserFile", Object.assign({ appName: APP_NAME, redirectUri: CALLBACK_PATH, state }, params), { target: "_self" });
    return [];
  }
  const res = await sdk.pickUserFile(params);
  return (res && res.data) || [];
}

// 对已知路径重新申请授权。
async function authorizePath(path) {
  const sdk = requireSdk();
  if (typeof sdk.authorizeUserFile === "function") {
    const res = await sdk.authorizeUserFile(path);
    if (res && typeof res.data === "boolean") return res.data ? [path] : [];
    return (res && res.data) || [];
  }
  const res = await sdk.pickUserFile({ directory: true, path });
  return (res && res.data) || [];
}

async function openAppSetting() {
  const found = getSdk();
  if (!found || typeof found.sdk.openAppSetting !== "function") return false;
  try {
    await found.sdk.openAppSetting();
    return true;
  } catch (e) {
    return false;
  }
}

// 在飞牛文件管理器中定位到指定路径（完成后跳转用）。
async function openFileManager(path) {
  const found = getSdk();
  if (!found || typeof found.sdk.openFileManager !== "function") return false;
  try {
    await found.sdk.openFileManager(path);
    return true;
  } catch (e) {
    return false;
  }
}

// 诊断信息（排查宿主/SDK 环境）
function describe() {
  const found = getSdk();
  const sdk = found ? found.sdk : null;
  const out = {
    sdkLoaded: !!sdk,
    via: found ? found.via : null,
    isTop: window === window.top,
    isStandaloneWeb: isStandalone(),
    isWeb: sdk ? sdk.isWeb : undefined,
    methods: [],
    injectedGlobals: {},
  };
  if (sdk) {
    for (const name of ["pickUserFile", "authorizeUserFile", "pickSharedFile", "authorizeSharedFile",
                        "openAppSetting", "openAppAuth", "getPlatformConfig", "parseAppAuthCallback"]) {
      if (typeof sdk[name] === "function") out.methods.push(name);
    }
  }
  for (const k of ["fnApp", "TrimApp", "trimApp", "FnApp", "createFnApp"]) {
    out.injectedGlobals[k] = typeof window[k];
  }
  return out;
}

function parseCallback(href) {
  const found = getSdk();
  if (found && typeof found.sdk.parseAppAuthCallback === "function") {
    try { return found.sdk.parseAppAuthCallback(href); } catch (e) { /* 回退 */ }
  }
  const url = new URL(href);
  return {
    status: url.searchParams.get("status"),
    paths: url.searchParams.getAll("path").concat(url.searchParams.getAll("paths")),
    state: url.searchParams.get("state"),
  };
}

function takePendingPaths() {
  const raw = sessionStorage.getItem(STORAGE_KEY);
  sessionStorage.removeItem(STORAGE_KEY);
  if (!raw) return [];
  try { return JSON.parse(raw); } catch (e) { return []; }
}

function setPendingPaths(paths) {
  sessionStorage.setItem(STORAGE_KEY, JSON.stringify(paths || []));
}

const api = {
  getSdk, describe, isStandalone, pickDirectory, pickFiles, authorizePath, openAppSetting,
  openFileManager, parseCallback, takePendingPaths, setPendingPaths, APP_NAME,
};

window.fnosBridge = api;
export default api;
