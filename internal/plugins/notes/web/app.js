/* 主界面：页面树、页面渲染、页面操作 */
(function () {
  "use strict";
  const N = window.Notes;
  const T = N.t;

  N.summaryOf = function (id) {
    return N.state.pages.find(function (p) { return p.id === id; }) || null;
  };
  N.childrenOf = function (parentId) {
    return N.state.pages.filter(function (p) { return !p.trashed && p.parentId === (parentId || ""); });
  };
  N.pageTreeOrder = function (list) {
    return list.slice().sort(function (a, b) {
      if (a.order === b.order) return String(a.createdAt).localeCompare(String(b.createdAt));
      return a.order - b.order;
    });
  };

  N.refreshWorkspace = async function () {
    const data = await N.api("/workspace");
    N.state.pages = (data.pages || []).slice();
    N.renderSidebar();
  };

  function ensureAncestors(id) {
    let summary = N.summaryOf(id);
    const seen = {};
    while (summary && summary.parentId && !seen[summary.id]) {
      seen[summary.id] = true;
      N.state.expanded[summary.parentId] = true;
      summary = N.summaryOf(summary.parentId);
    }
  }

  function makePageIcon(page, cls) {
    const icon = N.el("span", cls || "page-icon");
    if (page.icon) icon.textContent = page.icon;
    else icon.appendChild(N.svg(page.folder ? "folder" : "page"));
    return icon;
  }

  function dropPlacement(e, row) {
    const rect = row.getBoundingClientRect();
    const offset = e.clientY - rect.top;
    if (offset < rect.height * 0.3) return "before";
    if (offset > rect.height * 0.7) return "after";
    return "child";
  }

  function clearDropMarks() {
    document.querySelectorAll(".page-row.drop-child, .page-row.drop-before, .page-row.drop-after").forEach(function (row) {
      row.classList.remove("drop-child", "drop-before", "drop-after");
    });
  }
  N.clearPageDropMarks = clearDropMarks;

  function makeRow(page, depth, options) {
    options = options || {};
    const node = N.el("div", "tree-node");
    const row = N.el("div", "page-row");
    row.dataset.pageId = page.id;
    row.style.setProperty("--depth", String(depth));
    if (page.folder) row.classList.add("is-folder");
    if (N.state.view === "page" && N.state.page && N.state.page.id === page.id) row.classList.add("is-active");
    row.appendChild(makePageIcon(page, "page-icon tree-icon"));
    row.appendChild(N.el("span", "page-label", page.title || T(page.folder ? "notes.folder.untitled" : "notes.page.untitled")));

    if (!options.favorite) {
      const children = N.pageTreeOrder(N.childrenOf(page.id));
      const chevron = N.el("button", "tree-chevron" + (children.length === 0 ? " is-empty" : (N.state.expanded[page.id] === false ? "" : " is-open")));
      chevron.type = "button";
      chevron.appendChild(N.svg("chevron"));
      chevron.addEventListener("click", function (e) {
        e.stopPropagation();
        N.state.expanded[page.id] = N.state.expanded[page.id] === false;
        N.renderSidebar();
      });
      row.insertBefore(chevron, row.firstChild);
      const add = N.el("button", "row-add");
      add.type = "button";
      add.title = T("notes.sidebar.newSubPage");
      add.appendChild(N.svg("plus"));
      add.addEventListener("click", function (e) {
        e.stopPropagation();
        N.createChildPage(page.id);
      });
      row.appendChild(add);
    } else {
      const chevron = N.el("button", "tree-chevron is-empty");
      chevron.appendChild(N.svg("chevron"));
      row.insertBefore(chevron, row.firstChild);
    }

    row.addEventListener("click", function () { N.openPage(page.id); });
    if (!options.favorite) {
      row.draggable = true;
      row.addEventListener("dragstart", function (e) {
        N.state.dragPageId = page.id;
        try {
          e.dataTransfer.setData("text/plain", page.id);
          e.dataTransfer.effectAllowed = "move";
        } catch (_) { /* 忽略 */ }
      });
      row.addEventListener("dragover", function (e) {
        const dragId = N.state.dragPageId;
        if (!dragId || dragId === page.id) return;
        e.preventDefault();
        e.stopPropagation();
        clearDropMarks();
        row.classList.add("drop-" + dropPlacement(e, row));
      });
      row.addEventListener("dragleave", function () {
        row.classList.remove("drop-child", "drop-before", "drop-after");
      });
      row.addEventListener("drop", function (e) {
        e.preventDefault();
        e.stopPropagation();
        const placement = row.classList.contains("drop-before") ? "before" : row.classList.contains("drop-after") ? "after" : "child";
        clearDropMarks();
        const dragId = N.state.dragPageId;
        N.state.dragPageId = "";
        if (!dragId) return;
        if (placement === "child") N.movePageTo(dragId, page.id);
        else N.moveBeside(dragId, page.id, placement === "after");
      });
    }
    node.appendChild(row);

    if (!options.favorite) {
      const children = N.pageTreeOrder(N.childrenOf(page.id));
      if (children.length && N.state.expanded[page.id] !== false) {
        const box = N.el("div", "tree-children");
        children.forEach(function (child) { box.appendChild(makeRow(child, depth + 1)); });
        node.appendChild(box);
      }
    }
    return node;
  }

  N.renderSidebar = function () {
    const favSection = N.$("#favSection");
    const favList = N.$("#favList");
    const treeRoot = N.$("#treeRoot");
    if (!favSection || !favList || !treeRoot) return;
    N.clear(favList);
    N.clear(treeRoot);
    const favorites = N.state.pages.filter(function (p) { return p.favorite && !p.trashed; });
    favorites.sort(function (a, b) { return String(b.updatedAt).localeCompare(String(a.updatedAt)); });
    favSection.classList.toggle("hidden", favorites.length === 0);
    favorites.forEach(function (page) {
      const row = makeRow(page, 0, { favorite: true });
      row.querySelector(".page-row").classList.add("is-favorite");
      favList.appendChild(row);
    });
    const roots = N.pageTreeOrder(N.childrenOf(""));
    roots.forEach(function (page) { treeRoot.appendChild(makeRow(page, 0)); });
    const empty = N.$("#treeEmpty");
    if (empty) empty.classList.toggle("hidden", roots.length > 0);
    const count = N.state.pages.filter(function (p) { return p.trashed; }).length;
    const trashCount = N.$("#trashCount");
    if (trashCount) trashCount.textContent = count ? String(count) : "";
    document.querySelectorAll(".page-row").forEach(function (row) {
      row.classList.toggle("is-active", !!(N.state.view === "page" && N.state.page && row.dataset.pageId === N.state.page.id));
    });
  };

  N.renderCrumbs = function () {
    const nav = N.$("#crumbs");
    if (!nav) return;
    N.clear(nav);
    if (!N.state.page || N.state.view !== "page") return;
    const chain = [];
    let current = N.summaryOf(N.state.page.id);
    let guard = 0;
    while (current && current.parentId && guard++ < 50) {
      const parent = N.summaryOf(current.parentId);
      if (!parent) break;
      chain.unshift(parent);
      current = parent;
    }
    chain.forEach(function (page) {
      const btn = N.el("button", "crumb");
      btn.type = "button";
      if (page.icon) btn.appendChild(N.el("span", "", page.icon));
      else btn.appendChild(N.svg(page.folder ? "folder" : "page"));
      btn.appendChild(N.el("span", "", page.title || T("notes.page.untitled")));
      btn.title = page.title || "";
      btn.addEventListener("click", function () { N.openPage(page.id); });
      nav.appendChild(btn);
      nav.appendChild(N.el("span", "crumb-sep", "/"));
    });
  };

  N.renderPageHeader = function () {
    const page = N.state.page;
    if (!page) return;
    const title = N.$("#titleInput");
    if (title && document.activeElement !== title) title.value = page.title || "";
    if (title) title.placeholder = T(page.folder ? "notes.folder.untitled" : "notes.page.untitled");
    const icon = N.$("#iconBtn");
    N.clear(icon);
    icon.classList.toggle("empty", !page.icon);
    if (page.icon) icon.textContent = page.icon;
    else icon.appendChild(N.svg(page.folder ? "folder" : "page"));
    icon.title = T("notes.page.changeIcon");

    const coverWrap = N.$("#coverWrap");
    const cover = N.$("#cover");
    const addCover = N.$("#addCoverBtn");
    if (page.cover) {
      coverWrap.classList.remove("hidden");
      cover.setAttribute("style", N.coverCSS(page.cover));
      addCover.classList.add("hidden");
    } else {
      coverWrap.classList.add("hidden");
      cover.setAttribute("style", "");
      addCover.classList.remove("hidden");
    }

    const favBtn = N.$("#favBtn");
    N.clear(favBtn);
    favBtn.appendChild(N.svg(page.favorite ? "starFill" : "star"));
    favBtn.classList.toggle("is-on", !!page.favorite);
    favBtn.title = page.favorite ? T("notes.page.unfavorite") : T("notes.page.favorite");
    favBtn.classList.remove("hidden");
    N.$("#moreBtn").classList.remove("hidden");
    N.$("#saveBadge").classList.remove("hidden");
  };

  function renderChildPages() {
    const box = N.$("#childPages");
    N.clear(box);
    if (!N.state.page) return;
    const children = N.pageTreeOrder(N.childrenOf(N.state.page.id));
    if (children.length) {
      box.appendChild(N.el("div", "child-title", T("notes.page.childPages")));
      children.forEach(function (page) {
        const row = N.el("button", "child-row");
        row.type = "button";
        row.appendChild(makePageIcon(page, "page-icon"));
        row.appendChild(N.el("span", "", page.title || T("notes.page.untitled")));
        row.addEventListener("click", function () { N.openPage(page.id); });
        box.appendChild(row);
      });
    }
    const add = N.el("button", "child-add");
    add.type = "button";
    add.appendChild(N.svg("plus"));
    add.appendChild(N.el("span", "", children.length ? T("notes.page.addChild") : T("notes.page.firstChild")));
    add.addEventListener("click", function () { N.createChildPage(N.state.page.id); });
    box.appendChild(add);
  }

  N.renderPageView = function (options) {
    options = options || {};
    N.state.view = "page";
    N.$("#pageScroll").classList.remove("hidden");
    N.$("#trashView").classList.add("hidden");
    N.$("#emptyState").classList.add("hidden");
    N.renderPageHeader();
    N.renderCrumbs();
    const folder = !!N.state.page.folder;
    N.$("#editor").classList.toggle("hidden", folder);
    N.$("#folderView").classList.toggle("hidden", !folder);
    N.clear(N.$("#childPages"));
    if (folder) {
      N.$("#pageMeta").classList.add("hidden");
      renderFolderView();
    } else {
      N.$("#pageMeta").classList.remove("hidden");
      N.renderEditor();
      renderChildPages();
      N.updatePageMeta();
    }
    N.renderSidebar();
    if (options.focusTitle) {
      const input = N.$("#titleInput");
      input.focus();
      input.select();
    }
  };

  function renderFolderView() {
    const box = N.$("#folderView");
    if (!box || !N.state.page) return;
    N.clear(box);
    const children = N.pageTreeOrder(N.childrenOf(N.state.page.id));
    if (!children.length) {
      box.appendChild(N.el("div", "folder-empty", T("notes.folder.empty")));
    }
    children.forEach(function (page) {
      const card = N.el("button", "folder-card");
      card.type = "button";
      card.appendChild(makePageIcon(page));
      const main = N.el("div", "folder-card-main");
      main.appendChild(N.el("div", "folder-card-title", page.title || T(page.folder ? "notes.folder.untitled" : "notes.page.untitled")));
      const sub = page.folder
        ? T("notes.folder.items", { n: N.childrenOf(page.id).length })
        : T("notes.folder.edited", { time: N.fmtDate(page.updatedAt, true) });
      main.appendChild(N.el("div", "folder-card-sub", sub));
      card.appendChild(main);
      card.addEventListener("click", function () { N.openPage(page.id); });
      box.appendChild(card);
    });
    const actions = N.el("div", "folder-actions");
    const addPage = N.el("button", "pill", T("notes.folder.newPage"));
    addPage.type = "button";
    addPage.addEventListener("click", function () { N.createChildPage(N.state.page.id); });
    const addFolder = N.el("button", "pill", T("notes.folder.newFolder"));
    addFolder.type = "button";
    addFolder.addEventListener("click", function () { N.createFolder(N.state.page.id); });
    actions.appendChild(addPage);
    actions.appendChild(addFolder);
    box.appendChild(actions);
  }

  N.showEmptyState = function () {
    N.state.view = "empty";
    N.state.page = null;
    N.$("#pageScroll").classList.add("hidden");
    N.$("#trashView").classList.add("hidden");
    N.$("#emptyState").classList.remove("hidden");
    N.$("#favBtn").classList.add("hidden");
    N.$("#moreBtn").classList.add("hidden");
    N.$("#saveBadge").classList.add("hidden");
    N.clear(N.$("#crumbs"));
    N.setSaveState("saved");
    N.renderSidebar();
  };

  N.openPage = async function (id, options) {
    options = options || {};
    if (N.state.page && N.state.page.id === id && N.state.view === "page") {
      if (options.focusTitle) {
        const input = N.$("#titleInput");
        input.focus();
        input.select();
      }
      return;
    }
    await N.flushSave();
    try {
      const page = await N.api("/pages/" + id);
      N.state.page = page;
      ensureAncestors(id);
      N.resetHistory();
      N.setSaveState("saved");
      N.renderPageView(options);
      try { localStorage.setItem("devos.notes.last", id); } catch (_) { /* 忽略 */ }
    } catch (err) {
      N.toast(T("notes.toast.openFailed", { error: err.message }), true);
    }
  };

  N.createPage = async function (options) {
    options = options || {};
    try {
      const page = await N.api("/pages", {
        method: "POST",
        body: {
          parentId: options.parentId || "",
          title: options.title || "",
          icon: options.icon || "",
          cover: options.cover || "",
          folder: !!options.folder,
          blocks: options.blocks || [],
        },
      });
      if (options.parentId) N.state.expanded[options.parentId] = true;
      await N.refreshWorkspace();
      if (options.folder) {
        await N.openPage(page.id, { focusTitle: true });
      } else {
        await N.openPage(page.id, { focusTitle: options.focusTitle !== false });
      }
      return page;
    } catch (err) {
      N.toast(T("notes.toast.createFailed", { error: err.message }), true);
      return null;
    }
  };

  N.createChildPage = function (parentId) {
    return N.createPage({ parentId: parentId, title: "", blocks: [] });
  };

  N.createFolder = function (parentId) {
    return N.createPage({ parentId: parentId || "", folder: true, icon: "📁", title: "" });
  };

  N.movePageTo = async function (id, parentId, index) {
    try {
      await N.api("/pages/" + id + "/move", {
        method: "POST",
        body: { parentId: parentId, index: typeof index === "number" ? index : -1 },
      });
      if (parentId) N.state.expanded[parentId] = true;
      await N.refreshWorkspace();
      if (N.state.page && N.state.page.id === id) {
        N.renderCrumbs();
        if (N.state.page.folder) N.renderPageView();
      }
      N.toast(T("notes.toast.moved"));
    } catch (err) {
      N.toast(err.message, true);
    }
  };

  // 把页面拖到另一页面前/后，成为同一层级的相邻节点。
  N.moveBeside = function (dragId, targetId, after) {
    const target = N.summaryOf(targetId);
    if (!target || dragId === targetId) return;
    const parentId = target.parentId || "";
    const list = N.pageTreeOrder(N.childrenOf(parentId)).filter(function (page) { return page.id !== dragId; });
    let index = list.findIndex(function (page) { return page.id === targetId; });
    if (index < 0) index = list.length;
    if (after) index += 1;
    N.movePageTo(dragId, parentId, index);
  };

  N.toggleFavorite = async function () {
    if (!N.state.page) return;
    try {
      await N.api("/pages/" + N.state.page.id + "/favorite", { method: "POST", body: { value: !N.state.page.favorite } });
      N.state.page.favorite = !N.state.page.favorite;
      const summary = N.summaryOf(N.state.page.id);
      if (summary) summary.favorite = N.state.page.favorite;
      N.renderPageHeader();
      N.renderSidebar();
      N.toast(N.state.page.favorite ? T("notes.toast.favorited") : T("notes.toast.unfavorited"));
    } catch (err) {
      N.toast(err.message, true);
    }
  };

  N.setPageIcon = function (value) {
    if (!N.state.page) return;
    N.state.page.icon = value || "";
    const summary = N.summaryOf(N.state.page.id);
    if (summary) summary.icon = N.state.page.icon;
    N.renderPageHeader();
    N.renderSidebar();
    N.renderCrumbs();
    N.scheduleSave();
  };
  N.setPageCover = function (value) {
    if (!N.state.page) return;
    N.state.page.cover = value || "";
    N.renderPageHeader();
    N.scheduleSave();
  };

  /* ---------------- 图标与封面选择 ---------------- */
  N.openIconPicker = function () {
    if (!N.state.page) return;
    const menu = N.el("div", "icon-picker");
    const row = N.el("div", "menu-input");
    const input = document.createElement("input");
    input.placeholder = T("notes.icon.custom");
    input.maxLength = 4;
    const apply = N.el("button", "btn-plain", T("notes.common.apply"));
    apply.type = "button";
    function submit() {
      const value = input.value.trim();
      if (!value) return;
      N.hidePopover();
      N.setPageIcon(value);
    }
    apply.addEventListener("click", submit);
    input.addEventListener("keydown", function (e) {
      if (e.key === "Enter") { e.preventDefault(); submit(); }
    });
    row.appendChild(input);
    row.appendChild(apply);
    menu.appendChild(row);
    menu.appendChild(N.el("div", "menu-sep"));
    const grid = N.el("div", "emoji-grid");
    N.EMOJIS.forEach(function (emoji) {
      const btn = N.el("button", "emoji-cell", emoji);
      btn.type = "button";
      btn.addEventListener("click", function () {
        N.hidePopover();
        N.setPageIcon(emoji);
      });
      grid.appendChild(btn);
    });
    menu.appendChild(grid);
    menu.appendChild(N.el("div", "menu-sep"));
    const footer = N.el("div", "pop-foot");
    if (N.state.page.icon) {
      const remove = N.el("button", "pop-link danger", T("notes.icon.remove"));
      remove.type = "button";
      remove.addEventListener("click", function () {
        N.hidePopover();
        N.setPageIcon("");
      });
      footer.appendChild(remove);
    }
    menu.appendChild(footer);
    N.showPopover(menu, N.$("#iconBtn").getBoundingClientRect());
  };

  N.openCoverPicker = function () {
    if (!N.state.page) return;
    const menu = N.el("div", "cover-picker");
    menu.appendChild(N.el("div", "menu-label", T("notes.cover.gradients")));
    const grid = N.el("div", "gradient-grid");
    N.GRADIENTS.forEach(function (gradient) {
      const btn = N.el("button", "gradient-cell");
      btn.type = "button";
      btn.style.backgroundImage = gradient.css;
      btn.addEventListener("click", function () {
        N.hidePopover();
        N.setPageCover("gradient:" + gradient.key);
      });
      grid.appendChild(btn);
    });
    menu.appendChild(grid);
    menu.appendChild(N.el("div", "menu-sep"));
    menu.appendChild(N.el("div", "menu-label", T("notes.cover.image")));
    const row = N.el("div", "cover-form");
    const input = document.createElement("input");
    input.placeholder = "https://example.com/cover.jpg";
    const apply = N.el("button", "btn-plain", T("notes.common.apply"));
    apply.type = "button";
    function applyURL() {
      const url = input.value.trim();
      if (!url) return;
      if (!/^https?:\/\//i.test(url)) {
        N.toast(T("notes.cover.invalid"), true);
        return;
      }
      N.hidePopover();
      N.setPageCover(url);
    }
    apply.addEventListener("click", applyURL);
    input.addEventListener("keydown", function (e) {
      if (e.key === "Enter") { e.preventDefault(); applyURL(); }
    });
    row.appendChild(input);
    row.appendChild(apply);
    menu.appendChild(row);
    const actions = N.el("div", "pop-actions");
    const upload = N.el("button", "pop-link", T("notes.cover.upload"));
    upload.type = "button";
    upload.addEventListener("click", function () {
      N.hidePopover();
      N.state.pendingUpload = { kind: "cover" };
      N.$("#imageInput").click();
    });
    actions.appendChild(upload);
    if (N.state.page.cover) {
      const remove = N.el("button", "pop-link danger", T("notes.cover.remove"));
      remove.type = "button";
      remove.addEventListener("click", function () {
        N.hidePopover();
        N.setPageCover("");
      });
      actions.appendChild(remove);
    }
    menu.appendChild(actions);
    const anchor = N.state.page.cover ? N.$("#coverChangeBtn") : N.$("#addCoverBtn");
    N.showPopover(menu, anchor.getBoundingClientRect());
  };

  /* ---------------- 页面菜单 ---------------- */
  N.openPageMenu = function () {
    if (!N.state.page) return;
    const menu = N.el("div", "page-menu");
    const items = [
      { label: T("notes.page.favorite"), icon: N.state.page.favorite ? "starFill" : "star", run: function () { N.toggleFavorite(); } },
      { label: T("notes.page.addChild"), icon: "plus", run: function () { N.createChildPage(N.state.page.id); } },
      { label: T("notes.folder.newFolder"), icon: "folderPlus", run: function () { N.createFolder(N.state.page.id); } },
      { label: N.state.page.folder ? T("notes.folder.toPage") : T("notes.folder.toFolder"), icon: N.state.page.folder ? "page" : "folder", run: function () { N.toggleFolderType(); } },
      { label: T("notes.page.duplicate"), icon: "copy", run: function () { N.duplicateCurrentPage(); } },
      { label: T("notes.page.move"), icon: "arrowDown", run: function () { N.openMoveDialog(); } },
      { label: T("notes.page.export"), icon: "download", run: function () { N.exportCurrentPage(); } },
      { label: N.state.page.cover ? T("notes.page.changeCover") : T("notes.page.addCover"), icon: "image", run: function () { N.openCoverPicker(); } },
      { sep: true },
      { label: T("notes.page.delete"), icon: "trash", danger: true, run: function () { N.trashCurrentPage(); } },
    ];
    items.forEach(function (item) {
      if (item.sep) {
        menu.appendChild(N.el("div", "menu-sep"));
        return;
      }
      const btn = N.el("button", "menu-item" + (item.danger ? " is-danger" : ""));
      btn.type = "button";
      const icon = N.el("span", "mi-icon");
      icon.appendChild(N.svg(item.icon));
      btn.appendChild(icon);
      btn.appendChild(N.el("span", "mi-label", item.label));
      btn.addEventListener("click", function () {
        N.hidePopover();
        item.run();
      });
      menu.appendChild(btn);
    });
    N.showPopover(menu, N.$("#moreBtn").getBoundingClientRect(), { align: "right" });
  };

  N.toggleFolderType = function () {
    const page = N.state.page;
    if (!page) return;
    page.folder = !page.folder;
    if (page.folder && !page.icon) page.icon = "📁";
    const summary = N.summaryOf(page.id);
    if (summary) summary.folder = page.folder;
    N.renderPageView();
    N.scheduleSave();
    N.toast(page.folder ? T("notes.toast.toFolder") : T("notes.toast.toPage"));
  };

  N.duplicateCurrentPage = async function () {
    if (!N.state.page) return;
    await N.flushSave();
    const base = N.state.page.title || T("notes.page.untitled");
    try {
      const result = await N.api("/pages/" + N.state.page.id + "/duplicate", {
        method: "POST",
        body: { title: base + T("notes.page.duplicateSuffix") },
      });
      await N.refreshWorkspace();
      await N.openPage(result.pageId);
      N.toast(T("notes.toast.duplicated"));
    } catch (err) {
      N.toast(err.message, true);
    }
  };

  N.exportCurrentPage = function () {
    if (!N.state.page) return;
    const link = document.createElement("a");
    link.href = N.API_BASE + "/pages/" + N.state.page.id + "/export";
    link.download = (N.state.page.title || "note") + ".md";
    document.body.appendChild(link);
    link.click();
    document.body.removeChild(link);
    N.toast(T("notes.toast.exported"));
  };

  N.trashCurrentPage = async function () {
    if (!N.state.page) return;
    const page = N.state.page;
    const ok = await N.askConfirm({
      title: T("notes.confirm.trashTitle"),
      body: T("notes.confirm.trashBody", { title: page.title }),
      ok: T("notes.page.delete"),
    });
    if (!ok) return;
    try {
      await N.api("/pages/" + page.id + "/trash", { method: "POST", body: {} });
      N.discardSave();
      N.state.page = null;
      await N.refreshWorkspace();
      const next = N.pageTreeOrder(N.childrenOf(""))[0] || N.state.pages.filter(function (p) { return !p.trashed; })[0];
      if (next) await N.openPage(next.id);
      else N.showEmptyState();
      N.toast(T("notes.toast.trashed"));
    } catch (err) {
      N.toast(err.message, true);
    }
  };
})();
