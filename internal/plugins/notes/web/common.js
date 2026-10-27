/* 笔记应用共享内核：状态、API、图标、弹层、保存与工具函数。 */
(function () {
  "use strict";
  const N = (window.Notes = window.Notes || {});
  const I18N = window.DevOSAppI18n;
  N.API_BASE = "/api/apps/notes";
  N.I18N = I18N;
  N.t = function (key, params) { return I18N.t(key, params); };

  N.state = {
    pages: [],
    page: null,
    view: "page",
    expanded: {},
    booted: false,
    dirty: false,
    saveTimer: 0,
    saveKind: "saved",
    history: [],
    histIndex: -1,
    typingTimer: 0,
    searchTimer: 0,
    searchSel: 0,
    searchItems: [],
    dragPageId: "",
    dragBlockId: "",
    pendingUpload: null,
    metaTimer: 0,
  };

  N.GRADIENTS = [
    { key: "g1", css: "linear-gradient(120deg,#fde68a,#fecaca 55%,#fbcfe8)" },
    { key: "g2", css: "linear-gradient(120deg,#bfdbfe,#c7d2fe)" },
    { key: "g3", css: "linear-gradient(120deg,#bbf7d0,#a7f3d0 60%,#bae6fd)" },
    { key: "g4", css: "linear-gradient(120deg,#fed7aa,#fdba74 60%,#fecdd3)" },
    { key: "g5", css: "linear-gradient(120deg,#e9d5ff,#fbcfe8)" },
    { key: "g6", css: "linear-gradient(120deg,#cffafe,#bae6fd 60%,#c7d2fe)" },
    { key: "g7", css: "linear-gradient(120deg,#fecdd3,#fed7aa)" },
    { key: "g8", css: "linear-gradient(120deg,#e2e8f0,#cbd5e1)" },
    { key: "g9", css: "linear-gradient(120deg,#fef3c7,#fde68a 55%,#fca5a5)" },
    { key: "g10", css: "linear-gradient(120deg,#d1fae5,#a7f3d0 55%,#fef3c7)" },
  ];

  N.EMOJIS = [
    "📄", "📝", "📌", "📎", "📁", "🗂️", "📚", "📖", "📕", "📗", "📘", "📙",
    "🧠", "💡", "🎯", "✅", "☑️", "🔥", "⚡", "✨", "⭐", "🌟", "💎", "🏆",
    "🚀", "🛠️", "⚙️", "🧩", "🔍", "🔗", "📊", "📈", "📉", "🗓️", "⏰", "⏳",
    "💻", "⌨️", "🖥️", "📱", "🎨", "🖌️", "✏️", "🖊️", "📐", "🔬", "🧪", "🧭",
    "❤️", "🧡", "💛", "💚", "💙", "💜", "🖤", "🤍", "☕", "🍵", "🍀", "🌱",
    "🌿", "🌸", "🌻", "🌙", "☀️", "🌈", "☁️", "🎵", "🎧", "🎮", "🏃", "🧘",
    "🍎", "🍰", "🍜", "🥗", "✈️", "🏠", "🏢", "🌍", "🗺️", "📮", "📬", "🔔",
    "👋", "👍", "🙌", "🤝", "💪", "🙂", "😀", "🤔", "🧐", "🎉", "🎁", "🏷️",
  ];

  N.CODE_LANGS = [
    { value: "text", label: "Plain text" },
    { value: "bash", label: "Bash" },
    { value: "c", label: "C" },
    { value: "cpp", label: "C++" },
    { value: "csharp", label: "C#" },
    { value: "css", label: "CSS" },
    { value: "dockerfile", label: "Dockerfile" },
    { value: "go", label: "Go" },
    { value: "html", label: "HTML" },
    { value: "java", label: "Java" },
    { value: "javascript", label: "JavaScript" },
    { value: "json", label: "JSON" },
    { value: "kotlin", label: "Kotlin" },
    { value: "lua", label: "Lua" },
    { value: "markdown", label: "Markdown" },
    { value: "php", label: "PHP" },
    { value: "python", label: "Python" },
    { value: "r", label: "R" },
    { value: "ruby", label: "Ruby" },
    { value: "rust", label: "Rust" },
    { value: "shell", label: "Shell" },
    { value: "sql", label: "SQL" },
    { value: "swift", label: "Swift" },
    { value: "typescript", label: "TypeScript" },
    { value: "xml", label: "XML" },
    { value: "yaml", label: "YAML" },
  ];

  N.ICONS = {
    page: '<path d="M6 3h8l4 4v14a1 1 0 0 1-1 1H6a1 1 0 0 1-1-1V4a1 1 0 0 1 1-1Z"/><path d="M14 3v4h4"/><path d="M9 12h6M9 16h6"/>',
    folder: '<path d="M3 7a1 1 0 0 1 1-1h5l2 2h9a1 1 0 0 1 1 1v10a1 1 0 0 1-1 1H4a1 1 0 0 1-1-1Z"/>',
    folderPlus: '<path d="M3 7a1 1 0 0 1 1-1h5l2 2h9a1 1 0 0 1 1 1v10a1 1 0 0 1-1 1H4a1 1 0 0 1-1-1Z"/><path d="M12 11v6M9 14h6"/>',
    audio: '<path d="M4 14V10M8 18V6M12 15V9M16 19V5M20 13v-2"/>',
    video: '<rect x="3" y="5" width="13" height="14" rx="2"/><path d="M16 10l5-3v10l-5-3Z"/>',
    file: '<path d="M6 3h8l4 4v14a1 1 0 0 1-1 1H6a1 1 0 0 1-1-1V4a1 1 0 0 1 1-1Z"/><path d="M14 3v4h4"/>',
    equation: '<path d="M5 6h5l4 6-4 6H5"/><path d="M15 9l4 6M19 9l-4 6"/>',
    diagram: '<rect x="3" y="3" width="7" height="5" rx="1"/><rect x="14" y="16" width="7" height="5" rx="1"/><path d="M6.5 8v3.5a2 2 0 0 0 2 2H17"/><path d="M15 11.5 17 13.5l-2 2"/>',
    plus: '<path d="M12 5v14M5 12h14"/>',
    chevron: '<path d="M9 6l6 6-6 6"/>',
    search: '<circle cx="11" cy="11" r="7"/><path d="M20 20l-3.5-3.5"/>',
    trash: '<path d="M4 7h16M9 7V4h6v3M6 7l1 13h10l1-13"/><path d="M10 11v5M14 11v5"/>',
    upload: '<path d="M12 16V4M8 8l4-4 4 4"/><path d="M4 16v3a1 1 0 0 0 1 1h14a1 1 0 0 0 1-1v-3"/>',
    download: '<path d="M12 4v12M8 12l4 4 4-4"/><path d="M4 20h16"/>',
    more: '<circle cx="5" cy="12" r="1"/><circle cx="12" cy="12" r="1"/><circle cx="19" cy="12" r="1"/>',
    star: '<path d="M12 4l2.5 5.2 5.5.8-4 3.9 1 5.5-5-2.7-5 2.7 1-5.5-4-3.9 5.5-.8Z"/>',
    starFill: '<path d="M12 4l2.5 5.2 5.5.8-4 3.9 1 5.5-5-2.7-5 2.7 1-5.5-4-3.9 5.5-.8Z" fill="currentColor"/>',
    x: '<path d="M6 6l12 12M18 6 6 18"/>',
    check: '<path d="M5 12l5 5L20 7"/>',
    grip: '<circle cx="9" cy="7" r="1.2" fill="currentColor"/><circle cx="15" cy="7" r="1.2" fill="currentColor"/><circle cx="9" cy="12" r="1.2" fill="currentColor"/><circle cx="15" cy="12" r="1.2" fill="currentColor"/><circle cx="9" cy="17" r="1.2" fill="currentColor"/><circle cx="15" cy="17" r="1.2" fill="currentColor"/>',
    arrowUp: '<path d="M12 19V5M5 12l7-7 7 7"/>',
    arrowDown: '<path d="M12 5v14M5 12l7 7 7-7"/>',
    copy: '<rect x="9" y="9" width="11" height="11" rx="2"/><path d="M5 15V5a2 2 0 0 1 2-2h8"/>',
    link: '<path d="M10 14a4 4 0 0 0 5.7 0l3-3a4 4 0 0 0-5.7-5.7l-1.5 1.5"/><path d="M14 10a4 4 0 0 0-5.7 0l-3 3a4 4 0 0 0 5.7 5.7l1.5-1.5"/>',
    code: '<path d="M8 7l-5 5 5 5M16 7l5 5-5 5"/>',
    image: '<rect x="3" y="4" width="18" height="16" rx="2"/><circle cx="9" cy="10" r="2"/><path d="M4 17l5-5 4 4 2-2 5 5"/>',
    quote: '<path d="M7 7h4v6H5V9a2 2 0 0 1 2-2ZM17 7h4v6h-6V9a2 2 0 0 1 2-2Z"/>',
    bookmark: '<path d="M7 4h10a1 1 0 0 1 1 1v16l-6-4-6 4V5a1 1 0 0 1 1-1Z"/>',
    play: '<path d="M8 5l11 7-11 7Z"/>',
    menu: '<path d="M4 5h16M4 12h10M4 19h16"/>',
    text: '<path d="M5 6h14M12 6v13M8 19h8"/>',
    h1: '<path d="M4 6v12M13 6v12M4 12h9"/><path d="M19 10.5l2-1.5v9"/>',
    h2: '<path d="M4 6v12M13 6v12M4 12h9"/><path d="M18 10a2 2 0 1 1 4 0c0 1-1 2-4 4h4"/>',
    h3: '<path d="M4 6v12M13 6v12M4 12h9"/><path d="M18 9h4l-2.5 3.5A2.5 2.5 0 1 1 18 16"/>',
    list: '<path d="M9 6h12M9 12h12M9 18h12"/><circle cx="4.5" cy="6" r="1" fill="currentColor"/><circle cx="4.5" cy="12" r="1" fill="currentColor"/><circle cx="4.5" cy="18" r="1" fill="currentColor"/>',
    numbered: '<path d="M9 6h12M9 12h12M9 18h12"/><path d="M4 5l1.5-1v5M4 12h2.5L4 15h2.5"/>',
    todo: '<rect x="3.5" y="4.5" width="8" height="8" rx="1.5"/><path d="M5.5 8.5l1.5 1.5 2-2.5"/><path d="M14 7h6M14 12h6M14 17h6"/>',
    toggle: '<path d="M9 6l6 6-6 6"/><path d="M15 5h4v14h-4"/>',
    callout: '<path d="M12 3a7 7 0 0 0-4 12.7V19h8v-3.3A7 7 0 0 0 12 3Z"/><path d="M10 21h4"/>',
    table: '<rect x="3" y="4" width="18" height="16" rx="2"/><path d="M3 10h18M3 15h18M9 4v16M15 4v16"/>',
    divider: '<path d="M4 12h16"/>',
    calendar: '<rect x="3" y="5" width="18" height="16" rx="2"/><path d="M8 3v4M16 3v4M3 10h18"/>',
    book: '<path d="M4 5a2 2 0 0 1 2-2h13v18H6a2 2 0 0 1-2-2Z"/><path d="M8 3v18M12 7h4M12 11h4"/>',
    sparkle: '<path d="M12 3l1.8 5.2L19 10l-5.2 1.8L12 17l-1.8-5.2L5 10l5.2-1.8Z"/>',
    palette: '<circle cx="12" cy="12" r="9"/><circle cx="9" cy="9" r="1.2" fill="currentColor"/><circle cx="15" cy="9" r="1.2" fill="currentColor"/><circle cx="9" cy="15" r="1.2" fill="currentColor"/><circle cx="15" cy="15" r="1.2" fill="currentColor"/>',
  };

  N.$ = function (selector) { return document.querySelector(selector); };
  N.el = function (tag, cls, text) {
    const node = document.createElement(tag);
    if (cls) node.className = cls;
    if (text !== undefined && text !== null) node.textContent = text;
    return node;
  };
  N.svg = function (name) {
    const span = document.createElement("span");
    span.innerHTML = '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round">' + (N.ICONS[name] || "") + "</svg>";
    return span.firstElementChild;
  };
  N.clear = function (node) { while (node && node.firstChild) node.removeChild(node.firstChild); };
  N.uid = function () {
    const bytes = new Uint8Array(8);
    if (window.crypto && window.crypto.getRandomValues) window.crypto.getRandomValues(bytes);
    else for (let i = 0; i < bytes.length; i++) bytes[i] = Math.floor(Math.random() * 256);
    return Array.from(bytes, (b) => b.toString(16).padStart(2, "0")).join("");
  };
  N.escapeHTML = function (text) {
    return String(text == null ? "" : text)
      .replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;")
      .replace(/"/g, "&quot;").replace(/'/g, "&#39;");
  };

  N.toast = function (message, isError) {
    const node = N.$("#toast");
    if (!node) return;
    node.textContent = message;
    node.classList.toggle("is-error", !!isError);
    node.classList.remove("hidden");
    clearTimeout(N.toastTimer);
    N.toastTimer = setTimeout(function () { node.classList.add("hidden"); }, 2600);
  };

  N.api = async function (path, options) {
    options = options || {};
    const init = { method: options.method || "GET", headers: {} };
    if (options.body !== undefined) {
      init.headers["Content-Type"] = "application/json";
      init.body = JSON.stringify(options.body);
    }
    const res = await fetch(N.API_BASE + path, init);
    if (!res.ok) {
      let message = "";
      try {
        const data = await res.json();
        message = data.message || data.error || "";
      } catch (_) { /* 非 JSON 错误响应 */ }
      throw new Error(message || res.statusText || ("HTTP " + res.status));
    }
    if (options.raw) return res;
    const type = res.headers.get("content-type") || "";
    if (type.indexOf("application/json") >= 0) return res.json();
    return res.text();
  };

  /* ---------------- 行内 HTML 清洗 ---------------- */
  const ALLOWED_TAGS = ["B", "STRONG", "I", "EM", "U", "S", "STRIKE", "DEL", "CODE", "A", "BR", "MARK"];
  function cleanNode(node) {
    Array.prototype.slice.call(node.childNodes).forEach(function (child) {
      if (child.nodeType === 1) {
        if (ALLOWED_TAGS.indexOf(child.tagName) < 0) {
          const frag = document.createDocumentFragment();
          while (child.firstChild) frag.appendChild(child.firstChild);
          child.parentNode.replaceChild(frag, child);
          return;
        }
        Array.prototype.slice.call(child.attributes).forEach(function (attr) {
          const keep = child.tagName === "A" && attr.name === "href" && /^(https?:|mailto:)/i.test(attr.value);
          if (!keep) child.removeAttribute(attr.name);
        });
        if (child.tagName === "A") {
          child.setAttribute("target", "_blank");
          child.setAttribute("rel", "noopener noreferrer");
        }
        cleanNode(child);
      }
    });
  }
  N.safeHTML = function (html) {
    if (!html) return "";
    const tpl = document.createElement("template");
    tpl.innerHTML = String(html);
    cleanNode(tpl.content);
    return tpl.innerHTML;
  };
  N.plainText = function (html) {
    if (!html) return "";
    const tpl = document.createElement("template");
    tpl.innerHTML = String(html);
    return (tpl.content.textContent || "").replace(/\s+/g, " ").trim();
  };

  N.assetURL = function (value) {
    if (!value) return "";
    if (/^(https?:)?\/\//i.test(value) || value.charAt(0) === "/") return value;
    if (value.indexOf("files/") === 0) return N.API_BASE + "/" + value;
    return value;
  };
  N.coverCSS = function (cover) {
    if (!cover) return "";
    if (cover.indexOf("gradient:") === 0) {
      const item = N.GRADIENTS.find(function (g) { return g.key === cover.slice(9); });
      return item ? "background-image:" + item.css : "";
    }
    return "background-image:url(" + JSON.stringify(N.assetURL(cover)) + ")";
  };

  N.fmtDate = function (iso, withTime) {
    if (!iso) return "";
    const date = new Date(iso);
    if (isNaN(date.getTime())) return "";
    try {
      return new Intl.DateTimeFormat(I18N.bcp47(), withTime ? { year: "numeric", month: "short", day: "numeric", hour: "2-digit", minute: "2-digit" } : { year: "numeric", month: "short", day: "numeric" }).format(date);
    } catch (_) {
      return date.toLocaleString();
    }
  };
  N.fmtTime = function (iso) {
    if (!iso) return "";
    const date = new Date(iso);
    if (isNaN(date.getTime())) return "";
    try {
      return new Intl.DateTimeFormat(I18N.bcp47(), { hour: "2-digit", minute: "2-digit" }).format(date);
    } catch (_) { return date.toLocaleTimeString(); }
  };

  /* ---------------- 弹层与对话框 ---------------- */
  N.showPopover = function (content, rect, options) {
    options = options || {};
    const pop = N.$("#popover");
    N.clear(pop);
    pop.appendChild(content);
    pop.classList.remove("hidden");
    const width = pop.offsetWidth;
    const height = pop.offsetHeight;
    let left = rect.left;
    if (options.align === "right") left = rect.right - width;
    left = Math.max(8, Math.min(left, window.innerWidth - width - 8));
    let top = rect.bottom + 6;
    if (top + height > window.innerHeight - 8) {
      top = rect.top - height - 6;
      if (top < 8) top = Math.max(8, window.innerHeight - height - 8);
    }
    pop.style.left = left + "px";
    pop.style.top = top + "px";
  };
  N.hidePopover = function () {
    const pop = N.$("#popover");
    if (pop) pop.classList.add("hidden");
    N.closeSelectMenu();
  };
  N.rectOf = function (node) { return node.getBoundingClientRect(); };
  N.caretRect = function () {
    const sel = window.getSelection();
    if (!sel || !sel.rangeCount) return { left: 40, top: 80, bottom: 96 };
    const range = sel.getRangeAt(0).cloneRange();
    range.collapse(false);
    let rect = range.getBoundingClientRect();
    if (!rect || (!rect.width && !rect.height)) {
      const node = sel.anchorNode && (sel.anchorNode.nodeType === 1 ? sel.anchorNode : sel.anchorNode.parentElement);
      if (node) rect = node.getBoundingClientRect();
    }
    if (!rect) return { left: 40, top: 80, bottom: 96 };
    return { left: rect.left, right: rect.right, top: rect.top, bottom: rect.bottom };
  };

  N.openOverlay = function (id) {
    const node = N.$("#" + id);
    if (node) node.classList.remove("hidden");
  };
  N.closeOverlay = function (id) {
    const node = N.$("#" + id);
    if (node) node.classList.add("hidden");
  };
  N.closeAllOverlays = function () {
    ["searchOverlay", "templateOverlay", "moveOverlay", "confirmOverlay", "inputOverlay"].forEach(function (id) { N.closeOverlay(id); });
    N.hidePopover();
  };

  N.askText = function (options) {
    options = options || {};
    return new Promise(function (resolve) {
      const overlay = N.$("#inputOverlay");
      const field = N.$("#inputField");
      const ok = N.$("#inputOk");
      const cancel = N.$("#inputCancel");
      N.$("#inputTitle").textContent = options.title || "";
      N.$("#inputHint").textContent = options.hint || "";
      field.value = options.value || "";
      field.placeholder = options.placeholder || "";
      N.openOverlay("inputOverlay");
      setTimeout(function () { field.focus(); field.select(); }, 20);
      function cleanup(result) {
        overlay.classList.add("hidden");
        ok.removeEventListener("click", onOk);
        cancel.removeEventListener("click", onCancel);
        field.removeEventListener("keydown", onKey);
        overlay.removeEventListener("mousedown", onBackdrop);
        resolve(result);
      }
      function onOk() { cleanup(field.value.trim()); }
      function onCancel() { cleanup(null); }
      function onKey(e) {
        if (e.key === "Enter") { e.preventDefault(); onOk(); }
        else if (e.key === "Escape") { e.preventDefault(); onCancel(); }
      }
      function onBackdrop(e) { if (e.target === overlay) onCancel(); }
      ok.addEventListener("click", onOk);
      cancel.addEventListener("click", onCancel);
      field.addEventListener("keydown", onKey);
      overlay.addEventListener("mousedown", onBackdrop);
    });
  };

  N.askConfirm = function (options) {
    options = options || {};
    return new Promise(function (resolve) {
      const overlay = N.$("#confirmOverlay");
      const ok = N.$("#confirmOk");
      const cancel = N.$("#confirmCancel");
      N.$("#confirmTitle").textContent = options.title || "";
      N.$("#confirmBody").textContent = options.body || "";
      ok.textContent = options.ok || N.t("notes.common.delete");
      ok.className = options.danger === false ? "btn-accent" : "btn-danger";
      N.openOverlay("confirmOverlay");
      function cleanup(result) {
        overlay.classList.add("hidden");
        ok.removeEventListener("click", onOk);
        cancel.removeEventListener("click", onCancel);
        overlay.removeEventListener("mousedown", onBackdrop);
        resolve(result);
      }
      function onOk() { cleanup(true); }
      function onCancel() { cleanup(false); }
      function onBackdrop(e) { if (e.target === overlay) onCancel(); }
      ok.addEventListener("click", onOk);
      cancel.addEventListener("click", onCancel);
      overlay.addEventListener("mousedown", onBackdrop);
    });
  };

  /* ---------------- 自绘下拉（代码语言等） ---------------- */
  N.openSelectMenu = function (trigger, items, value, onPick) {
    N.closeSelectMenu();
    const menu = N.el("div", "ui-select-menu");
    items.forEach(function (item) {
      const btn = N.el("button", "ui-select-item" + (item.value === value ? " is-active" : ""), item.label);
      btn.type = "button";
      btn.addEventListener("click", function () {
        N.closeSelectMenu();
        onPick(item.value);
      });
      menu.appendChild(btn);
    });
    document.body.appendChild(menu);
    const rect = trigger.getBoundingClientRect();
    menu.style.left = Math.min(rect.left, window.innerWidth - menu.offsetWidth - 8) + "px";
    menu.style.top = (rect.bottom + 4) + "px";
    if (rect.bottom + 4 + menu.offsetHeight > window.innerHeight - 8) {
      menu.style.top = Math.max(8, rect.top - menu.offsetHeight - 4) + "px";
    }
    N._selectMenu = menu;
  };
  N.closeSelectMenu = function () {
    if (N._selectMenu && N._selectMenu.parentNode) N._selectMenu.parentNode.removeChild(N._selectMenu);
    N._selectMenu = null;
  };

  /* ---------------- 保存状态 ---------------- */
  N.setSaveState = function (kind) {
    N.state.saveKind = kind;
    const badge = N.$("#saveBadge");
    const status = N.$("#saveStatus");
    const dot = N.$("#saveDot");
    if (!badge || !status || !dot) return;
    dot.className = "save-dot " + (kind === "saving" ? "saving" : kind === "error" ? "error" : "");
    let text = "";
    if (kind === "saving") text = N.t("notes.save.saving");
    else if (kind === "error") text = N.t("notes.save.failed");
    else if (kind === "dirty") text = N.t("notes.save.pending");
    else text = N.t("notes.save.saved", { time: N.fmtTime(new Date().toISOString()) });
    badge.textContent = text;
    status.textContent = text;
  };

  let saveChain = Promise.resolve();
  N.savePage = function () {
    const page = N.state.page;
    if (!page || !N.state.dirty) return Promise.resolve();
    const snapshot = {
      title: page.title,
      icon: page.icon || "",
      cover: page.cover || "",
      folder: !!page.folder,
      blocks: page.blocks,
    };
    const id = page.id;
    N.state.dirty = false;
    N.setSaveState("saving");
    saveChain = saveChain.then(async function () {
      try {
        const saved = await N.api("/pages/" + id, { method: "PUT", body: snapshot });
        if (N.state.page && N.state.page.id === id) {
          N.state.page.updatedAt = saved.updatedAt;
          if (!N.state.dirty) N.setSaveState("saved");
        }
        const summary = N.state.pages.find(function (p) { return p.id === id; });
        if (summary) {
          summary.title = saved.title;
          summary.icon = saved.icon;
          summary.cover = saved.cover;
          summary.updatedAt = saved.updatedAt;
          summary.blockCount = (saved.blocks || []).length;
        }
        N.renderSidebar();
      } catch (err) {
        N.state.dirty = true;
        N.setSaveState("error");
        N.toast(N.t("notes.toast.saveFailed", { error: err.message }), true);
      }
    });
    return saveChain;
  };

  N.scheduleSave = function () {
    N.state.dirty = true;
    N.setSaveState("dirty");
    clearTimeout(N.state.saveTimer);
    N.state.saveTimer = setTimeout(function () { N.savePage(); }, 650);
  };

  N.flushSave = async function () {
    clearTimeout(N.state.saveTimer);
    await N.savePage();
    await saveChain;
  };

  N.discardSave = function () {
    clearTimeout(N.state.saveTimer);
    N.state.dirty = false;
  };

  /* ---------------- 撤销/重做 ---------------- */
  N.pushHistory = function () {
    const page = N.state.page;
    if (!page) return;
    const snap = JSON.stringify(page.blocks || []);
    if (N.state.history[N.state.histIndex] === snap) return;
    N.state.history = N.state.history.slice(0, N.state.histIndex + 1);
    N.state.history.push(snap);
    if (N.state.history.length > 80) N.state.history.shift();
    N.state.histIndex = N.state.history.length - 1;
  };
  N.resetHistory = function () {
    N.state.history = [];
    N.state.histIndex = -1;
    N.pushHistory();
  };
  N.undo = function () {
    if (N.state.histIndex <= 0) return false;
    N.state.histIndex--;
    N.applySnapshot();
    return true;
  };
  N.redo = function () {
    if (N.state.histIndex >= N.state.history.length - 1) return false;
    N.state.histIndex++;
    N.applySnapshot();
    return true;
  };
  N.applySnapshot = function () {
    const page = N.state.page;
    if (!page) return;
    try {
      page.blocks = JSON.parse(N.state.history[N.state.histIndex] || "[]");
    } catch (_) { return; }
    if (page.blocks.length === 0) page.blocks = [N.newBlock("paragraph")];
    if (N.renderEditor) N.renderEditor();
    N.scheduleSave();
    N.updatePageMeta();
  };
  N.newBlock = function (type, content, props) {
    return { id: N.uid(), type: type || "paragraph", content: content || "", props: Object.assign({}, props || {}) };
  };

  N.countWords = function (page) {
    if (!page) return 0;
    let text = "";
    (page.blocks || []).forEach(function (block) {
      if (block.type === "code") text += " " + (block.content || "");
      else if (block.type === "table") (block.props.rows || []).forEach(function (row) { text += " " + row.join(" "); });
      else if (block.type === "image" || block.type === "bookmark" || block.type === "audio" || block.type === "video" || block.type === "file") text += " " + (block.props.caption || "");
      else text += " " + N.plainText(block.content || "");
    });
    const trimmed = text.trim();
    if (!trimmed) return 0;
    const cjk = (trimmed.match(/[\u3000-\u9fff\uf900-\ufaff]/g) || []).length;
    const words = trimmed.replace(/[\u3000-\u9fff\uf900-\ufaff]/g, " ").split(/\s+/).filter(Boolean).length;
    return cjk + words;
  };

  N.updatePageMeta = function () {
    clearTimeout(N.state.metaTimer);
    N.state.metaTimer = setTimeout(function () {
      const meta = N.$("#pageMeta");
      if (!meta || !N.state.page) return;
      N.clear(meta);
      const words = N.countWords(N.state.page);
      const blocks = (N.state.page.blocks || []).length;
      meta.appendChild(N.el("span", "", N.t("notes.page.words", { n: words })));
      meta.appendChild(N.el("span", "", N.t("notes.page.blocks", { n: blocks })));
      meta.appendChild(N.el("span", "", N.t("notes.page.edited", { time: N.fmtDate(N.state.page.updatedAt, true) })));
    }, 180);
  };
})();
