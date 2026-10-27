const state = {
  apps: [],
  system: null,
  security: null,
  windows: new Map(),
  mru: [],
  switcher: { visible: false, wins: [], index: 0 },
  seq: 0,
  z: 20,
  selectedId: null,
  activeId: null,
};

const $ = (sel) => document.querySelector(sel);
const el = (tag, cls) => {
  const node = document.createElement(tag);
  if (cls) node.className = cls;
  return node;
};

const desktopEl = $("#desktop");
const layerEl = $("#windowLayer");
const iconsEl = $("#icons");
const tasksEl = $("#tasks");
const startMenu = $("#startMenu");
const startBtn = $("#startBtn");
const startApps = $("#startApps");
const searchInput = $("#appSearch");
const clockEl = $("#clock");
const bootEl = $("#boot");

function esc(value) {
  return String(value ?? "").replace(/[&<>"']/g, (ch) => ({
    "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;",
  }[ch]));
}

// 默认禁用浏览器右键菜单，并在同源插件页面内同步启用 Alt+Tab 窗口切换。
function installFrameGuards(doc) {
  if (!doc || doc.__devosGuarded) return;
  doc.__devosGuarded = true;
  doc.addEventListener("contextmenu", (event) => event.preventDefault(), true);
  doc.addEventListener("keydown", handleWindowKeyDown, true);
  doc.addEventListener("keyup", handleWindowKeyUp, true);
}
installFrameGuards(document);

function iconContent(icon) {
  const value = String(icon ?? "").trim();
  if (!value) return "";
  if (value.startsWith("<") || value.startsWith("data:")) return icon;
  if (value.startsWith("/") || value.startsWith("http://") || value.startsWith("https://")) {
    return `<img src="${esc(value)}" alt="" loading="lazy" />`;
  }
  return esc(value);
}

function iconMarkup(app) {
  return `<span class="icon-tile" style="--accent:${esc(app.accent || "#0ea5e9")}">${iconContent(app.icon)}</span>`;
}

async function fetchJSON(url, options) {
  const resp = await fetch(url, options);
  if (!resp.ok) {
    let message = `请求失败 (${resp.status})`;
    try {
      const data = await resp.json();
      message = data.message || data.error || message;
    } catch (_) { /* ignore */ }
    throw new Error(message);
  }
  return resp.json();
}

function t(key, params) {
  return window.DevOSI18n ? window.DevOSI18n.t(key, params) : key;
}

// 内置应用的清单名称是中文，界面名称按当前语言翻译；未翻译时回退清单值。
function appName(app) {
  const key = `app.${app.id}.name`;
  const value = t(key);
  return value === key ? app.name : value;
}

function appDesc(app) {
  const key = `app.${app.id}.desc`;
  const value = t(key);
  return value === key ? (app.description || app.category || "") : value;
}

// 应用壁纸：预设使用 CSS 渐变，自定义壁纸来自内核 /api/security/wallpaper/file。
const wallpaperEl = $("#wallpaper");
const WALLPAPER_PRESETS = ["aurora", "dusk", "mint", "sand", "midnight"];

function applyWallpaper(wallpaper) {
  if (!wallpaperEl) return;
  WALLPAPER_PRESETS.forEach((preset) => wallpaperEl.classList.remove(`preset-${preset}`));
  wallpaperEl.classList.remove("is-custom");
  wallpaperEl.style.backgroundImage = "";
  if (wallpaper && wallpaper.kind === "custom" && wallpaper.file) {
    wallpaperEl.classList.add("is-custom");
    wallpaperEl.style.backgroundImage = `url("/api/security/wallpaper/file/${encodeURIComponent(wallpaper.file)}")`;
    return;
  }
  const preset = wallpaper && WALLPAPER_PRESETS.includes(wallpaper.preset) ? wallpaper.preset : "aurora";
  wallpaperEl.classList.add(`preset-${preset}`);
}

function toast(message) {
  const node = el("div", "toast");
  node.innerHTML = `<svg class="h-4 w-4 text-sky-300" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><circle cx="12" cy="12" r="9"/><path d="M12 8v5"/><path d="M12 16h.01"/></svg><span>${esc(message)}</span>`;
  $("#toasts").appendChild(node);
  setTimeout(() => {
    node.style.opacity = "0";
    node.style.transform = "translateY(6px)";
    node.style.transition = "all .25s ease";
    setTimeout(() => node.remove(), 260);
  }, 3200);
}

function renderIcons() {
  iconsEl.innerHTML = "";
  for (const app of state.apps) {
    const btn = el("button");
    btn.className = "desktop-icon";
    btn.dataset.app = app.id;
    btn.title = appDesc(app) || appName(app);
    btn.innerHTML = `${iconMarkup(app)}<span class="icon-label">${esc(appName(app))}</span>`;
    btn.addEventListener("click", (event) => {
      event.stopPropagation();
      selectIcon(app.id);
    });
    btn.addEventListener("dblclick", () => openApp(app.id));
    iconsEl.appendChild(btn);
  }
}

function selectIcon(id) {
  state.selectedId = id;
  iconsEl.querySelectorAll(".desktop-icon").forEach((node) => {
    node.classList.toggle("is-selected", node.dataset.app === id);
  });
}

function renderStartApps(filter = "") {
  const keyword = filter.trim().toLowerCase();
  const list = state.apps.filter((app) => {
    if (!keyword) return true;
    return `${appName(app)} ${appDesc(app)} ${app.category}`.toLowerCase().includes(keyword);
  });
  if (list.length === 0) {
    startApps.innerHTML = `<div class="start-empty">${esc(t("start.empty"))}</div>`;
    return;
  }
  startApps.innerHTML = "";
  for (const app of list) {
    const btn = el("button", "start-app");
    btn.innerHTML = `${iconMarkup(app)}<span class="min-w-0">
      <span class="start-app-name">${esc(appName(app))}</span>
      <span class="start-app-desc">${esc(appDesc(app) || app.category)}</span>
    </span>`;
    btn.addEventListener("click", () => {
      closeStartMenu();
      openApp(app.id);
    });
    startApps.appendChild(btn);
  }
}

function renderTasks() {
  tasksEl.innerHTML = "";
  for (const win of state.windows.values()) {
    const btn = el("button", "task-btn");
    btn.title = appName(win.app);
    if (win.id === state.activeId && !win.minimized) btn.classList.add("is-active");
    if (win.minimized) btn.classList.add("is-minimized");
    btn.innerHTML = `<span class="task-icon" style="color:${esc(win.app.accent || "#0ea5e9")}">${iconContent(win.app.icon)}</span><span class="task-title">${esc(appName(win.app))}</span>`;
    btn.addEventListener("click", () => {
      if (win.minimized) {
        restoreWindow(win);
      } else if (win.id === state.activeId) {
        minimizeWindow(win);
      } else {
        focusWindow(win);
      }
    });
    tasksEl.appendChild(btn);
  }
}

function renderClock() {
  const now = new Date();
  const bcp47 = window.DevOSI18n ? window.DevOSI18n.bcp47() : "zh-CN";
  const time = now.toLocaleTimeString(bcp47, { hour: "2-digit", minute: "2-digit", hour12: false });
  const date = now.toLocaleDateString(bcp47, { month: "long", day: "numeric", weekday: "short" });
  clockEl.innerHTML = `<span class="clock-time">${time}</span><span class="clock-date">${date}</span>`;
}

function renderStatus() {
  const sys = state.system;
  if (!sys) return;
  $("#startMeta").textContent = `${sys.hostname} · ${sys.os} · ${sys.dataDir}`;
}

function openApp(appId, params) {
  const app = state.apps.find((item) => item.id === appId);
  if (!app) {
    toast(t("toast.appMissing", { id: appId }));
    return;
  }

  const search = new URLSearchParams();
  for (const [key, value] of Object.entries(params || {})) {
    if (value !== undefined && value !== null && value !== "") search.set(key, value);
  }
  const query = search.toString();

  const existing = [...state.windows.values()].find((win) => win.appId === appId);
  if (existing && !app.multi) {
    if (query) {
      const frame = existing.el.querySelector("iframe");
      if (frame) {
        const entry = app.entry || "index.html";
        frame.src = `/apps/${encodeURIComponent(app.id)}/${encodeURIComponent(entry)}?${query}`;
      }
    }
    restoreWindow(existing);
    return;
  }

  const win = createWindow(app, query);
  state.windows.set(win.id, win);
  layerEl.appendChild(win.el);
  focusWindow(win);
  renderTasks();
}

function createWindow(app, query = "") {
  const bounds = desktopEl.getBoundingClientRect();
  const width = Math.min(app.width || 960, bounds.width - 40);
  const height = Math.min(app.height || 640, bounds.height - 40);
  const offset = (state.seq % 6) * 26;
  const left = Math.max(12, Math.round((bounds.width - width) / 2) - 60 + offset);
  const top = Math.max(12, Math.round((bounds.height - height) / 2) - 20 + offset);

  const node = el("section", "window");
  node.style.width = `${width}px`;
  node.style.height = `${height}px`;
  node.style.left = `${left}px`;
  node.style.top = `${top}px`;

  const entry = app.entry || "index.html";
  node.innerHTML = `
    <header class="window-header">
      <span class="window-app-icon" style="color:${esc(app.accent || "#0ea5e9")}">${iconContent(app.icon)}</span>
      <span class="window-title">${esc(appName(app))}</span>
      <span class="ml-auto flex items-center gap-0.5">
        <button class="window-btn" data-act="min" title="${esc(t("window.min"))}">
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M5 12h14"/></svg>
        </button>
        <button class="window-btn" data-act="max" title="${esc(t("window.max"))}">
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8"><rect x="5" y="5" width="14" height="14" rx="1.2"/></svg>
        </button>
        <button class="window-btn close" data-act="close" title="${esc(t("window.close"))}">
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M6 6l12 12M18 6L6 18"/></svg>
        </button>
      </span>
    </header>
    <div class="window-body">
      <iframe src="/apps/${encodeURIComponent(app.id)}/${encodeURIComponent(entry)}${query ? "?" + query : ""}" title="${esc(appName(app))}" loading="eager"></iframe>
    </div>
    <div class="window-resize"></div>`;

  const win = {
    id: `w${++state.seq}`,
    appId: app.id,
    app,
    el: node,
    minimized: false,
    maximized: false,
    prev: null,
  };

  // 插件页面与桌面同源：统一禁用右键菜单并接管 Alt+Tab。
  const frame = node.querySelector("iframe");
  if (frame) {
    frame.addEventListener("load", () => {
      installFrameGuards(frame.contentDocument);
      sendLocaleToFrame(frame);
    });
  }

  node.addEventListener("pointerdown", () => focusWindow(win), true);

  node.querySelectorAll(".window-btn").forEach((btn) => {
    btn.addEventListener("click", (event) => {
      event.stopPropagation();
      const act = btn.dataset.act;
      if (act === "min") minimizeWindow(win);
      if (act === "max") toggleMaximize(win);
      if (act === "close") closeWindow(win);
    });
  });

  const header = node.querySelector(".window-header");
  header.addEventListener("dblclick", (event) => {
    if (event.target.closest(".window-btn")) return;
    toggleMaximize(win);
  });
  enableDrag(win, header);
  enableResize(win, node.querySelector(".window-resize"));
  return win;
}

function focusWindow(win) {
  state.activeId = win.id;
  state.mru = [win.id, ...state.mru.filter((id) => id !== win.id)];
  for (const item of state.windows.values()) {
    item.el.classList.toggle("is-active", item.id === win.id);
  }
  win.el.style.zIndex = String(++state.z);
  renderTasks();
}

// 语言切换后同步已打开窗口的按钮提示。
function refreshWindowTitles() {
  const labels = { min: t("window.min"), max: t("window.max"), close: t("window.close") };
  for (const win of state.windows.values()) {
    const title = win.el.querySelector(".window-title");
    if (title) title.textContent = appName(win.app);
    win.el.querySelectorAll(".window-btn").forEach((btn) => {
      const label = labels[btn.dataset.act];
      if (label) btn.title = label;
    });
  }
}

function restoreWindow(win) {
  win.minimized = false;
  win.el.style.display = "flex";
  focusWindow(win);
}

function minimizeWindow(win) {
  win.minimized = true;
  win.el.style.display = "none";
  if (state.activeId === win.id) state.activeId = null;
  renderTasks();
}

function closeWindow(win) {
  const frame = win.el.querySelector("iframe");
  if (frame) frame.src = "about:blank";
  win.el.remove();
  state.windows.delete(win.id);
  state.mru = state.mru.filter((id) => id !== win.id);
  if (state.activeId === win.id) state.activeId = null;
  renderTasks();
}

// Alt+Tab 窗口切换：按住 Alt 连按 Tab 依次选择，松开 Alt 切换到选中窗口。
const switcherEl = el("div", "switcher hidden");
document.body.appendChild(switcherEl);

function switcherCandidates() {
  const ids = state.mru.filter((id) => state.windows.has(id));
  for (const id of state.windows.keys()) {
    if (!ids.includes(id)) ids.push(id);
  }
  return ids.map((id) => state.windows.get(id));
}

function renderSwitcher() {
  switcherEl.innerHTML = state.switcher.wins.map((win, index) => `
    <div class="switcher-item${index === state.switcher.index ? " is-active" : ""}">
      <span class="switcher-icon" style="--accent:${esc(win.app.accent || "#0ea5e9")}">${iconContent(win.app.icon)}</span>
      <span class="switcher-name">${esc(appName(win.app))}</span>
    </div>`).join("");
}

function stepSwitcher(dir) {
  const wins = switcherCandidates();
  if (wins.length === 0) return;
  const sw = state.switcher;
  if (!sw.visible) {
    sw.visible = true;
    sw.wins = wins;
    const current = wins.findIndex((win) => win.id === state.activeId);
    sw.index = current < 0 ? 0 : current;
    switcherEl.classList.remove("hidden");
  } else {
    sw.wins = wins;
  }
  const total = sw.wins.length;
  sw.index = (sw.index + dir + total) % total;
  renderSwitcher();
}

function commitSwitcher() {
  const sw = state.switcher;
  sw.visible = false;
  switcherEl.classList.add("hidden");
  const win = sw.wins[sw.index];
  sw.wins = [];
  sw.index = 0;
  if (win && state.windows.has(win.id)) {
    if (win.minimized) restoreWindow(win);
    else focusWindow(win);
  }
}

function cancelSwitcher() {
  state.switcher.visible = false;
  state.switcher.wins = [];
  state.switcher.index = 0;
  switcherEl.classList.add("hidden");
}

function handleWindowKeyDown(event) {
  if (state.switcher.visible && event.key === "Escape") {
    event.preventDefault();
    cancelSwitcher();
    return;
  }
  if (event.key !== "Tab" || !event.altKey) return;
  event.preventDefault();
  event.stopPropagation();
  stepSwitcher(event.shiftKey ? -1 : 1);
}

function handleWindowKeyUp(event) {
  if (event.key === "Alt" && state.switcher.visible) {
    event.preventDefault();
    commitSwitcher();
  }
}

function toggleMaximize(win) {
  const bounds = desktopEl.getBoundingClientRect();
  if (win.maximized) {
    win.maximized = false;
    win.el.classList.remove("is-maximized");
    if (win.prev) {
      Object.assign(win.el.style, win.prev);
    }
    return;
  }
  win.prev = {
    left: win.el.style.left,
    top: win.el.style.top,
    width: win.el.style.width,
    height: win.el.style.height,
  };
  win.maximized = true;
  win.el.classList.add("is-maximized");
  win.el.style.left = "8px";
  win.el.style.top = "8px";
  win.el.style.width = `${bounds.width - 16}px`;
  win.el.style.height = `${bounds.height - 16}px`;
  focusWindow(win);
}

function enableDrag(win, handle) {
  let dragging = false;
  let startX = 0;
  let startY = 0;
  let originLeft = 0;
  let originTop = 0;

  handle.addEventListener("pointerdown", (event) => {
    if (event.button !== 0 || event.target.closest(".window-btn")) return;
    if (win.maximized) return;
    dragging = true;
    startX = event.clientX;
    startY = event.clientY;
    originLeft = parseInt(win.el.style.left || "0", 10);
    originTop = parseInt(win.el.style.top || "0", 10);
    handle.setPointerCapture(event.pointerId);
    event.preventDefault();
  });

  handle.addEventListener("pointermove", (event) => {
    if (!dragging) return;
    const bounds = desktopEl.getBoundingClientRect();
    const maxLeft = Math.max(0, bounds.width - win.el.offsetWidth);
    const maxTop = Math.max(0, bounds.height - win.el.offsetHeight);
    const left = Math.min(maxLeft, Math.max(0, originLeft + event.clientX - startX));
    const top = Math.min(maxTop, Math.max(0, originTop + event.clientY - startY));
    win.el.style.left = `${left}px`;
    win.el.style.top = `${top}px`;
  });

  const stop = (event) => {
    if (!dragging) return;
    dragging = false;
    if (handle.hasPointerCapture(event.pointerId)) handle.releasePointerCapture(event.pointerId);
  };
  handle.addEventListener("pointerup", stop);
  handle.addEventListener("pointercancel", stop);
}

function enableResize(win, handle) {
  let resizing = false;
  let startX = 0;
  let startY = 0;
  let originWidth = 0;
  let originHeight = 0;

  handle.addEventListener("pointerdown", (event) => {
    if (event.button !== 0) return;
    resizing = true;
    startX = event.clientX;
    startY = event.clientY;
    originWidth = win.el.offsetWidth;
    originHeight = win.el.offsetHeight;
    handle.setPointerCapture(event.pointerId);
    event.preventDefault();
  });

  handle.addEventListener("pointermove", (event) => {
    if (!resizing) return;
    const bounds = desktopEl.getBoundingClientRect();
    const left = parseInt(win.el.style.left || "0", 10);
    const top = parseInt(win.el.style.top || "0", 10);
    const width = Math.min(Math.max(420, originWidth + event.clientX - startX), bounds.width - left);
    const height = Math.min(Math.max(320, originHeight + event.clientY - startY), bounds.height - top);
    win.el.style.width = `${width}px`;
    win.el.style.height = `${height}px`;
  });

  const stop = (event) => {
    if (!resizing) return;
    resizing = false;
    if (handle.hasPointerCapture(event.pointerId)) handle.releasePointerCapture(event.pointerId);
  };
  handle.addEventListener("pointerup", stop);
  handle.addEventListener("pointercancel", stop);
}

function openStartMenu() {
  startMenu.classList.remove("hidden");
  startBtn.classList.add("is-open");
  searchInput.value = "";
  renderStartApps();
  setTimeout(() => searchInput.focus(), 30);
}

function closeStartMenu() {
  startMenu.classList.add("hidden");
  startBtn.classList.remove("is-open");
}

function toggleStartMenu() {
  if (startMenu.classList.contains("hidden")) openStartMenu();
  else closeStartMenu();
}

async function loadApps() {
  const [apps, system] = await Promise.all([
    fetchJSON("/api/apps"),
    fetchJSON("/api/system"),
  ]);
  state.apps = apps;
  state.system = system;
  renderIcons();
  renderStartApps();
  renderStatus();
}

function hideBoot() {
  if (!bootEl || !bootEl.isConnected) return;
  bootEl.classList.add("boot-done");
  setTimeout(() => bootEl.remove(), 600);
}

async function startDesktop() {
  try {
    await loadApps();
  } catch (err) {
    toast(t("toast.loadAppsFailed", { error: err.message }));
  }
  const wanted = new URLSearchParams(location.search).get("app");
  if (wanted && state.apps.some((app) => app.id === wanted)) {
    setTimeout(() => openApp(wanted), 120);
    history.replaceState(null, "", location.pathname);
  }
  hideBoot();
}

// 手动锁定：撤销会话、关闭所有窗口并回到锁屏。
async function lockDesktop() {
  let data;
  try {
    data = await fetchJSON("/api/security/lock", {
      method: "POST",
      headers: { "Content-Type": "application/json", "X-DevOS-Request": "1" },
      body: "{}",
    });
  } catch (err) {
    toast(t("toast.lockFailed", { error: err.message }));
    return;
  }
  if (!data.locked) {
    toast(t("toast.lockNoPassword"));
    return;
  }
  closeStartMenu();
  if (window.DevOSSettings) window.DevOSSettings.close();
  for (const win of [...state.windows.values()]) closeWindow(win);
  state.security = Object.assign({}, state.security, { locked: true });
  window.DevOSLock.show({ onUnlock: startDesktop });
}

async function boot() {
  renderClock();
  setInterval(renderClock, 1000);
  let status = null;
  try {
    status = await fetchJSON("/api/security/status");
  } catch (_) { /* 读取失败时按未设置密码处理 */ }
  if (status) {
    state.security = status;
    if (status.locale && window.DevOSI18n) window.DevOSI18n.setLocale(status.locale, { silent: true });
    applyWallpaper(status.wallpaper);
    if (status.hasPassword && status.locked) {
      hideBoot();
      window.DevOSLock.show({ onUnlock: startDesktop });
      return;
    }
  }
  await startDesktop();
}

startBtn.addEventListener("click", (event) => {
  event.stopPropagation();
  toggleStartMenu();
});
searchInput.addEventListener("input", () => renderStartApps(searchInput.value));
$("#refreshApps").addEventListener("click", async () => {
  try {
    await loadApps();
    toast(t("toast.appsRefreshed"));
  } catch (err) {
    toast(t("toast.refreshFailed", { error: err.message }));
  }
});

$("#openSettings").addEventListener("click", () => {
  closeStartMenu();
  if (window.DevOSSettings) window.DevOSSettings.open();
});
$("#lockNow").addEventListener("click", () => {
  closeStartMenu();
  lockDesktop();
});

window.addEventListener("message", (event) => {
  const data = event.data;
  if (!data || data.type !== "devos:apps-changed") return;
  loadApps()
    .then(() => toast(data.message || t("toast.appsUpdated")))
    .catch((err) => toast(t("toast.appsRefreshFailed", { error: err.message })));
});

desktopEl.addEventListener("click", (event) => {
  if (event.target.closest(".window")) return;
  selectIcon(null);
  closeStartMenu();
});
document.addEventListener("click", (event) => {
  if (!event.target.closest("#startMenu") && !event.target.closest("#startBtn")) closeStartMenu();
});
document.addEventListener("keydown", (event) => {
  if (event.key === "Escape") closeStartMenu();
});
document.addEventListener("keydown", handleWindowKeyDown, true);
document.addEventListener("keyup", handleWindowKeyUp, true);
window.addEventListener("blur", () => {
  if (state.switcher.visible) commitSwitcher();
});
document.addEventListener("visibilitychange", () => {
  if (!document.hidden) renderClock();
});
// 把外壳语言推给已打开的应用页面，实现联动切换。
function sendLocaleToFrame(frame) {
  if (!frame || !frame.contentWindow || !window.DevOSI18n) return;
  frame.contentWindow.postMessage({ type: "devos:locale", locale: window.DevOSI18n.getLocale() }, location.origin);
}

function broadcastLocale() {
  for (const win of state.windows.values()) {
    sendLocaleToFrame(win.el.querySelector("iframe"));
  }
}

document.addEventListener("devos:locale-changed", () => {
  renderClock();
  renderStatus();
  renderIcons();
  renderTasks();
  renderStartApps(searchInput.value);
  refreshWindowTitles();
  broadcastLocale();
});

// 供设置面板与锁屏复用桌面能力。
window.DevOSDesktop = {
  start: startDesktop,
  lock: lockDesktop,
  applyWallpaper,
  notify: toast,
  security: () => state.security,
};

boot();
