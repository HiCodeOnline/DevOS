/* 模板、搜索、回收站、移动、Markdown 导入与启动绑定 */
(function () {
  "use strict";
  const N = window.Notes;
  const T = N.t;

  function blk(type, content, props) { return N.newBlock(type, content, props); }

  function welcomeBlocks() {
    return [
      blk("callout", "<b>" + T("notes.welcome.calloutTitle") + "</b><br>" + T("notes.welcome.calloutBody"), { icon: "👋", color: "yellow" }),
      blk("heading2", T("notes.welcome.features")),
      blk("bulleted", T("notes.welcome.f1")),
      blk("bulleted", T("notes.welcome.f2")),
      blk("bulleted", T("notes.welcome.f3")),
      blk("bulleted", T("notes.welcome.f4")),
      blk("bulleted", T("notes.welcome.f5")),
      blk("heading2", T("notes.welcome.shortcuts")),
      blk("bulleted", T("notes.welcome.s1")),
      blk("bulleted", T("notes.welcome.s2")),
      blk("bulleted", T("notes.welcome.s3")),
      blk("bulleted", T("notes.welcome.s4")),
      blk("callout", T("notes.welcome.tip"), { icon: "💡", color: "blue" }),
      blk("heading2", T("notes.welcome.try")),
      blk("todo", T("notes.welcome.t1")),
      blk("todo", T("notes.welcome.t2")),
      blk("todo", T("notes.welcome.t3")),
      blk("heading2", T("notes.welcome.sample")),
      blk("code", 'func main() {\n\tfmt.Println("Hello, DevOS Notes")\n}', { language: "go" }),
      blk("quote", T("notes.welcome.quote")),
      blk("divider", ""),
      blk("paragraph", T("notes.welcome.end")),
    ];
  }

  function todoBlocks() {
    return [
      blk("heading2", T("notes.tpl.todo.head1")),
      blk("todo", T("notes.tpl.todo.t1")),
      blk("todo", T("notes.tpl.todo.t2")),
      blk("todo", T("notes.tpl.todo.t3")),
      blk("divider", ""),
      blk("heading2", T("notes.tpl.todo.head2")),
      blk("bulleted", T("notes.tpl.todo.b1")),
      blk("bulleted", T("notes.tpl.todo.b2")),
      blk("callout", T("notes.tpl.todo.note"), { icon: "💡", color: "yellow" }),
      blk("paragraph", ""),
    ];
  }

  function meetingBlocks() {
    return [
      blk("callout", T("notes.tpl.meeting.info"), { icon: "🗓️", color: "blue" }),
      blk("heading2", T("notes.tpl.meeting.agenda")),
      blk("numbered", T("notes.tpl.meeting.a1")),
      blk("numbered", T("notes.tpl.meeting.a2")),
      blk("numbered", T("notes.tpl.meeting.a3")),
      blk("heading2", T("notes.tpl.meeting.notes")),
      blk("paragraph", ""),
      blk("heading2", T("notes.tpl.meeting.decisions")),
      blk("quote", T("notes.tpl.meeting.decision1")),
      blk("heading2", T("notes.tpl.meeting.actions")),
      blk("todo", T("notes.tpl.meeting.action1")),
      blk("todo", T("notes.tpl.meeting.action2")),
      blk("paragraph", ""),
    ];
  }

  function weeklyBlocks() {
    return [
      blk("callout", T("notes.tpl.weekly.info"), { icon: "🎯", color: "orange" }),
      blk("heading2", T("notes.tpl.weekly.goals")),
      blk("todo", T("notes.tpl.weekly.g1")),
      blk("todo", T("notes.tpl.weekly.g2")),
      blk("todo", T("notes.tpl.weekly.g3")),
      blk("heading2", T("notes.tpl.weekly.plan")),
      blk("table", "", {
        hasHeader: true,
        rows: [
          [T("notes.tpl.weekly.day"), T("notes.tpl.weekly.focus")],
          [T("notes.tpl.weekly.mon"), ""],
          [T("notes.tpl.weekly.tue"), ""],
          [T("notes.tpl.weekly.wed"), ""],
          [T("notes.tpl.weekly.thu"), ""],
          [T("notes.tpl.weekly.fri"), ""],
          [T("notes.tpl.weekly.sat"), ""],
          [T("notes.tpl.weekly.sun"), ""],
        ],
      }),
      blk("heading2", T("notes.tpl.weekly.review")),
      blk("paragraph", ""),
    ];
  }

  function readingBlocks() {
    return [
      blk("callout", T("notes.tpl.reading.info"), { icon: "📚", color: "purple" }),
      blk("paragraph", "<b>" + T("notes.tpl.reading.book") + "</b> "),
      blk("paragraph", "<b>" + T("notes.tpl.reading.author") + "</b> "),
      blk("heading2", T("notes.tpl.reading.points")),
      blk("bulleted", T("notes.tpl.reading.p1")),
      blk("bulleted", T("notes.tpl.reading.p2")),
      blk("bulleted", T("notes.tpl.reading.p3")),
      blk("heading2", T("notes.tpl.reading.quotes")),
      blk("quote", T("notes.tpl.reading.q1")),
      blk("heading2", T("notes.tpl.reading.actions")),
      blk("todo", T("notes.tpl.reading.a1")),
      blk("todo", T("notes.tpl.reading.a2")),
      blk("paragraph", ""),
    ];
  }

  N.TEMPLATES = [
    { key: "blank", icon: "📄", titleKey: "notes.template.blank", descKey: "notes.template.blankDesc", cover: "", blocks: function () { return []; } },
    { key: "todo", icon: "✅", titleKey: "notes.template.todo", descKey: "notes.template.todoDesc", cover: "gradient:g3", blocks: todoBlocks },
    { key: "meeting", icon: "🗓️", titleKey: "notes.template.meeting", descKey: "notes.template.meetingDesc", cover: "gradient:g2", blocks: meetingBlocks },
    { key: "weekly", icon: "🎯", titleKey: "notes.template.weekly", descKey: "notes.template.weeklyDesc", cover: "gradient:g9", blocks: weeklyBlocks },
    { key: "reading", icon: "📚", titleKey: "notes.template.reading", descKey: "notes.template.readingDesc", cover: "gradient:g5", blocks: readingBlocks },
  ];

  N.openTemplateDialog = function (parentId) {
    N.state.templateParent = parentId || "";
    const grid = N.$("#templateGrid");
    N.clear(grid);
    N.TEMPLATES.forEach(function (tmpl) {
      const card = N.el("button", "tpl-card");
      card.type = "button";
      card.appendChild(N.el("span", "tpl-icon", tmpl.icon));
      card.appendChild(N.el("span", "tpl-name", T(tmpl.titleKey)));
      card.appendChild(N.el("span", "tpl-desc", T(tmpl.descKey)));
      card.addEventListener("click", async function () {
        N.closeOverlay("templateOverlay");
        await N.createPage({
          parentId: N.state.templateParent || "",
          title: T(tmpl.titleKey),
          icon: tmpl.icon,
          cover: tmpl.cover,
          blocks: tmpl.blocks(),
          focusTitle: tmpl.key === "blank",
        });
      });
      grid.appendChild(card);
    });
    N.openOverlay("templateOverlay");
  };

  /* ---------------- 搜索 ---------------- */
  let searchSeq = 0;
  function searchRow(item, badgeKey) {
    const row = N.el("button", "result-row");
    row.type = "button";
    const icon = N.el("span", "result-icon");
    if (item.icon) icon.textContent = item.icon;
    else icon.appendChild(N.svg("page"));
    row.appendChild(icon);
    const main = N.el("div", "result-main");
    main.appendChild(N.el("div", "result-title", item.title || T("notes.page.untitled")));
    main.appendChild(N.el("div", "result-snippet", item.snippet || item.preview || T("notes.search.noPreview")));
    row.appendChild(main);
    if (badgeKey) row.appendChild(N.el("span", "result-badge", T(badgeKey)));
    row.addEventListener("click", function () {
      N.closeOverlay("searchOverlay");
      N.openPage(item.id);
    });
    return row;
  }

  function renderSearchItems(items, badgeFor) {
    const box = N.$("#searchResults");
    N.clear(box);
    N.state.searchItems = items;
    N.state.searchSel = 0;
    if (!items.length) {
      box.appendChild(N.el("div", "search-hint", T("notes.search.empty")));
      return;
    }
    items.forEach(function (item, index) {
      const row = searchRow(item, badgeFor ? badgeFor(item) : null);
      if (index === 0) row.classList.add("is-active");
      box.appendChild(row);
    });
  }

  function setSearchActive(index) {
    const rows = N.$("#searchResults").querySelectorAll(".result-row");
    if (!rows.length) return;
    N.state.searchSel = (index + rows.length) % rows.length;
    rows.forEach(function (row, i) { row.classList.toggle("is-active", i === N.state.searchSel); });
    const active = rows[N.state.searchSel];
    if (active && active.scrollIntoView) active.scrollIntoView({ block: "nearest" });
  }

  function showRecentSearches() {
    const items = N.state.pages
      .filter(function (p) { return !p.trashed; })
      .sort(function (a, b) { return String(b.updatedAt).localeCompare(String(a.updatedAt)); })
      .slice(0, 8);
    N.$("#searchHint").textContent = items.length ? T("notes.search.recent") : T("notes.search.emptyWorkspace");
    renderSearchItems(items.map(function (p) {
      return { id: p.id, title: p.title, icon: p.icon, snippet: p.preview || T("notes.search.noPreview") };
    }), null);
  }

  async function runSearch(query) {
    const seq = ++searchSeq;
    try {
      const results = await N.api("/search?q=" + encodeURIComponent(query));
      if (seq !== searchSeq) return;
      N.$("#searchHint").textContent = T("notes.search.results", { n: results.length });
      renderSearchItems(results, function (item) { return item.match === "title" ? "notes.search.matchTitle" : "notes.search.matchContent"; });
    } catch (err) {
      if (seq !== searchSeq) return;
      N.$("#searchHint").textContent = err.message;
      renderSearchItems([], null);
    }
  }

  N.openSearch = function () {
    N.openOverlay("searchOverlay");
    const input = N.$("#searchInput");
    input.value = "";
    showRecentSearches();
    setTimeout(function () { input.focus(); }, 20);
  };

  /* ---------------- 回收站 ---------------- */
  N.renderTrashView = function () {
    N.state.view = "trash";
    N.$("#pageScroll").classList.add("hidden");
    N.$("#emptyState").classList.add("hidden");
    N.$("#trashView").classList.remove("hidden");
    N.$("#favBtn").classList.add("hidden");
    N.$("#moreBtn").classList.add("hidden");
    N.$("#saveBadge").classList.add("hidden");
    N.clear(N.$("#crumbs"));
    const list = N.$("#trashList");
    N.clear(list);
    const items = N.state.pages.filter(function (p) { return p.trashed; })
      .sort(function (a, b) { return String(b.trashedAt).localeCompare(String(a.trashedAt)); });
    N.$("#emptyTrashBtn").disabled = items.length === 0;
    if (!items.length) {
      const empty = N.el("div", "trash-empty");
      empty.appendChild(N.svg("trash"));
      empty.appendChild(N.el("div", "", T("notes.trash.empty")));
      list.appendChild(empty);
      return;
    }
    items.forEach(function (item) {
      const row = N.el("div", "trash-row");
      const icon = N.el("span", "page-icon");
      if (item.icon) icon.textContent = item.icon;
      else icon.appendChild(N.svg("page"));
      row.appendChild(icon);
      const main = N.el("div", "trash-main");
      main.appendChild(N.el("div", "trash-name", item.title || T("notes.page.untitled")));
      main.appendChild(N.el("div", "trash-date", T("notes.trash.deletedAt", { time: N.fmtDate(item.trashedAt, true) })));
      row.appendChild(main);
      const actions = N.el("div", "trash-actions");
      const restore = N.el("button", "btn-plain", T("notes.trash.restore"));
      restore.type = "button";
      restore.addEventListener("click", function () { N.restorePage(item.id); });
      const remove = N.el("button", "btn-danger", T("notes.trash.delete"));
      remove.type = "button";
      remove.addEventListener("click", function () { N.deleteForever(item.id); });
      actions.appendChild(restore);
      actions.appendChild(remove);
      row.appendChild(actions);
      list.appendChild(row);
    });
  };

  N.openTrashView = async function () {
    await N.flushSave();
    N.renderTrashView();
  };

  N.restorePage = async function (id) {
    try {
      await N.api("/pages/" + id + "/restore", { method: "POST", body: {} });
      await N.refreshWorkspace();
      N.renderTrashView();
      N.toast(T("notes.toast.restored"));
    } catch (err) {
      N.toast(err.message, true);
    }
  };

  N.deleteForever = async function (id) {
    const page = N.summaryOf(id);
    const ok = await N.askConfirm({
      title: T("notes.confirm.deleteTitle"),
      body: T("notes.confirm.deleteBody", { title: page ? page.title : "" }),
      ok: T("notes.common.delete"),
    });
    if (!ok) return;
    try {
      await N.api("/pages/" + id, { method: "DELETE" });
      await N.refreshWorkspace();
      N.renderTrashView();
      N.toast(T("notes.toast.deleted"));
    } catch (err) {
      N.toast(err.message, true);
    }
  };

  /* ---------------- 移动到 ---------------- */
  N.openMoveDialog = function () {
    if (!N.state.page) return;
    const pageId = N.state.page.id;
    const blocked = {};
    function markSubtree(id) {
      blocked[id] = true;
      N.state.pages.forEach(function (p) { if (p.parentId === id) markSubtree(p.id); });
    }
    markSubtree(pageId);
    const list = N.$("#moveList");
    N.clear(list);
    const currentParent = N.summaryOf(pageId) ? N.summaryOf(pageId).parentId : "";
    function row(parentId, label, depth, isRoot) {
      const btn = N.el("button", "move-row" + (parentId === currentParent ? " is-active" : ""));
      btn.type = "button";
      if (isRoot) {
        btn.appendChild(N.svg("page"));
      } else {
        const page = N.summaryOf(parentId);
        const icon = N.el("span", "page-icon");
        if (page && page.icon) icon.textContent = page.icon;
        else icon.appendChild(N.svg(page && page.folder ? "folder" : "page"));
        btn.appendChild(icon);
      }
      btn.appendChild(N.el("span", "", label));
      btn.style.paddingLeft = (8 + depth * 16) + "px";
      if (parentId === currentParent) btn.appendChild(N.el("span", "move-depth", T("notes.common.current")));
      btn.addEventListener("click", function () {
        N.closeOverlay("moveOverlay");
        N.movePageTo(pageId, parentId);
      });
      list.appendChild(btn);
    }
    row("", T("notes.move.root"), 0, true);
    function walk(parentId, depth) {
      N.pageTreeOrder(N.childrenOf(parentId)).forEach(function (page) {
        if (blocked[page.id]) return;
        row(page.id, page.title || T(page.folder ? "notes.folder.untitled" : "notes.page.untitled"), depth, false);
        walk(page.id, depth + 1);
      });
    }
    walk("", 1);
    N.openOverlay("moveOverlay");
  };

  /* ---------------- Markdown 导入 ---------------- */
  function mdInline(text) {
    let out = N.escapeHTML(text);
    out = out.replace(/`([^`]+)`/g, "<code>$1</code>");
    out = out.replace(/\[([^\]]+)\]\(([^)\s]+)\)/g, function (match, label, url) {
      return /^(https?:|mailto:)/i.test(url) ? '<a href="' + url + '">' + label + "</a>" : match;
    });
    out = out.replace(/\*\*([^*]+)\*\*/g, "<b>$1</b>");
    out = out.replace(/__([^_]+)__/g, "<b>$1</b>");
    out = out.replace(/~~([^~]+)~~/g, "<s>$1</s>");
    out = out.replace(/==([^=]+)==/g, "<mark>$1</mark>");
    out = out.replace(/(^|[\s(])\*([^*\s][^*]*)\*/g, "$1<i>$2</i>");
    out = out.replace(/(^|[\s(])_([^_\s][^_]*)_/g, "$1<i>$2</i>");
    return out;
  }

  function splitRow(line) {
    let text = line.trim();
    if (text.charAt(0) === "|") text = text.slice(1);
    if (text.charAt(text.length - 1) === "|") text = text.slice(0, -1);
    return text.split("|").map(function (cell) { return cell.trim(); });
  }

  function isTableSeparator(line) {
    return /^\s*\|?\s*:?-{2,}:?\s*(\|\s*:?-{2,}:?\s*)*\|?\s*$/.test(line);
  }

  function parseMarkdown(md) {
    const lines = String(md || "").replace(/\r\n?/g, "\n").split("\n");
    const blocks = [];
    let i = 0;
    while (i < lines.length) {
      const line = lines[i];
      let match;
      if (/^\s{0,3}\$\$\s*$/.test(line)) {
        i++;
        const buf = [];
        while (i < lines.length && !/^\s{0,3}\$\$\s*$/.test(lines[i])) { buf.push(lines[i]); i++; }
        if (i < lines.length) i++;
        blocks.push(blk("equation", buf.join("\n").trim(), {}));
        continue;
      }
      if ((match = line.match(/^\s{0,3}\$\$(.+)\$\$\s*$/))) {
        blocks.push(blk("equation", match[1].trim(), {}));
        i++;
        continue;
      }
      if (/^\s{0,3}```/.test(line)) {
        const lang = line.replace(/^\s*```/, "").trim() || "text";
        i++;
        const buf = [];
        while (i < lines.length && !/^\s*```\s*$/.test(lines[i])) { buf.push(lines[i]); i++; }
        if (i < lines.length) i++;
        if (lang.toLowerCase() === "mermaid") {
          blocks.push(blk("diagram", buf.join("\n"), {}));
        } else {
          blocks.push(blk("code", buf.join("\n"), { language: lang }));
        }
        continue;
      }
      if (!line.trim()) { i++; continue; }
      if ((match = line.match(/^\s{0,3}(#{1,3})\s+(.*)$/))) {
        blocks.push(blk("heading" + match[1].length, mdInline(match[2].trim())));
        i++;
        continue;
      }
      if (/^\s{0,3}(-{3,}|\*{3,}|_{3,})\s*$/.test(line)) {
        blocks.push(blk("divider", ""));
        i++;
        continue;
      }
      if ((match = line.match(/^\s{0,3}[-*+]\s+\[([ xX]?)\]\s+(.*)$/))) {
        blocks.push(blk("todo", mdInline(match[2].trim()), { checked: match[1].toLowerCase() === "x" }));
        i++;
        continue;
      }
      if ((match = line.match(/^\s{0,3}[-*+]\s+(.*)$/))) {
        blocks.push(blk("bulleted", mdInline(match[1].trim())));
        i++;
        continue;
      }
      if ((match = line.match(/^\s{0,3}\d+[.)]\s+(.*)$/))) {
        blocks.push(blk("numbered", mdInline(match[1].trim())));
        i++;
        continue;
      }
      if ((match = line.match(/^\s{0,3}>\s?(.*)$/))) {
        const buf = [];
        while (i < lines.length && /^\s{0,3}>/.test(lines[i])) {
          buf.push(lines[i].replace(/^\s{0,3}>\s?/, ""));
          i++;
        }
        blocks.push(blk("quote", mdInline(buf.join(" ").trim())));
        continue;
      }
      if ((match = line.match(/^\s{0,3}!\[([^\]]*)\]\(([^)\s]+)\)\s*$/))) {
        if (/^https?:\/\//i.test(match[2])) {
          blocks.push(blk("image", "", { url: match[2], caption: match[1] }));
        } else {
          blocks.push(blk("paragraph", mdInline(line.trim())));
        }
        i++;
        continue;
      }
      if (line.indexOf("|") >= 0 && i + 1 < lines.length && isTableSeparator(lines[i + 1])) {
        const rows = [splitRow(line)];
        i += 2;
        while (i < lines.length && lines[i].indexOf("|") >= 0 && lines[i].trim()) {
          rows.push(splitRow(lines[i]));
          i++;
        }
        blocks.push(blk("table", "", { rows: rows, hasHeader: true }));
        continue;
      }
      const paragraph = [];
      while (i < lines.length && lines[i].trim() && !/^\s{0,3}(#{1,3}\s|```|>|[-*+]\s|\d+[.)]\s|-{3,}\s*$)/.test(lines[i])) {
        paragraph.push(lines[i].trim());
        i++;
      }
      if (paragraph.length) blocks.push(blk("paragraph", mdInline(paragraph.join(" "))));
      else { blocks.push(blk("paragraph", mdInline(line.trim()))); i++; }
    }
    return blocks.filter(function (block) {
      return block.type !== "paragraph" || N.plainText(block.content) !== "" || block.content.indexOf("<img") >= 0;
    });
  }
  N.parseMarkdown = parseMarkdown;

  async function importMarkdown(file) {
    try {
      const text = await readFileText(file);
      const blocks = parseMarkdown(text);
      let title = file.name.replace(/\.(md|markdown|txt)$/i, "");
      if (blocks.length && blocks[0].type === "heading1") {
        const heading = N.plainText(blocks[0].content);
        if (heading) title = heading;
        blocks.shift();
      }
      await N.createPage({ title: title, blocks: blocks, icon: "📥" });
      N.toast(T("notes.toast.imported", { n: blocks.length }));
    } catch (err) {
      N.toast(T("notes.toast.importFailed", { error: err.message }), true);
    }
  }

  function readFileText(file) {
    return new Promise(function (resolve, reject) {
      const reader = new FileReader();
      reader.onload = function () { resolve(String(reader.result || "")); };
      reader.onerror = function () { reject(new Error("read failed")); };
      reader.readAsText(file);
    });
  }

  /* ---------------- 附件上传 ---------------- */
  async function handleUploadFile(file) {
    const pending = N.state.pendingUpload;
    N.state.pendingUpload = null;
    if (!file) return;
    const kind = pending && pending.kind === "block" && pending.blockKind ? pending.blockKind : "image";
    try {
      const data = await N.uploadFile(file, kind);
      if (pending && pending.kind === "cover") {
        N.setPageCover(data.url);
      } else if (pending && pending.kind === "block") {
        const block = N.findBlock(pending.blockId);
        if (block) {
          N.pushHistory();
          block.props.url = data.url;
          if (block.type === "file") block.props.caption = data.name || "";
          N.renderEditor();
          N.scheduleSave();
        }
      }
      N.toast(T("notes.toast.uploaded"));
    } catch (err) {
      N.toast(T("notes.toast.uploadFailed", { error: err.message }), true);
    }
  }

  /* ---------------- 静态绑定 ---------------- */
  function syncMenuBtn() {
    const app = N.$("#app");
    const mobile = window.innerWidth <= 860;
    const collapsed = app.classList.contains("side-collapsed");
    N.$("#menuBtn").classList.toggle("hidden", !(mobile || collapsed));
    if (mobile) {
      const open = app.classList.contains("side-open-mobile");
      N.$("#sidebarMask").classList.toggle("hidden", !open);
    } else {
      app.classList.remove("side-open-mobile");
      N.$("#sidebarMask").classList.add("hidden");
    }
  }

  function bindStatic() {
    const app = N.$("#app");
    N.$("#collapseBtn").addEventListener("click", function () {
      if (window.innerWidth <= 860) {
        app.classList.remove("side-open-mobile");
        syncMenuBtn();
      } else {
        app.classList.toggle("side-collapsed");
        syncMenuBtn();
      }
    });
    N.$("#menuBtn").addEventListener("click", function () {
      if (window.innerWidth <= 860) app.classList.toggle("side-open-mobile");
      else app.classList.remove("side-collapsed");
      syncMenuBtn();
    });
    N.$("#sidebarMask").addEventListener("click", function () {
      app.classList.remove("side-open-mobile");
      syncMenuBtn();
    });
    window.addEventListener("resize", syncMenuBtn);

    N.$("#searchBtn").addEventListener("click", N.openSearch);
    N.$("#newPageBtn").addEventListener("click", function () { N.openTemplateDialog(""); });
    N.$("#rootNewBtn").addEventListener("click", function () { N.openTemplateDialog(""); });
    N.$("#rootNewFolderBtn").addEventListener("click", function () { N.createFolder(""); });
    N.$("#emptyCreateBtn").addEventListener("click", function () { N.openTemplateDialog(""); });
    N.$("#trashBtn").addEventListener("click", N.openTrashView);
    N.$("#importBtn").addEventListener("click", function () { N.$("#mdInput").click(); });
    N.$("#iconBtn").addEventListener("click", N.openIconPicker);
    N.$("#addCoverBtn").addEventListener("click", N.openCoverPicker);
    N.$("#coverChangeBtn").addEventListener("click", N.openCoverPicker);
    N.$("#coverRemoveBtn").addEventListener("click", function () { N.setPageCover(""); });
    N.$("#favBtn").addEventListener("click", N.toggleFavorite);
    N.$("#moreBtn").addEventListener("click", N.openPageMenu);

    const title = N.$("#titleInput");
    title.addEventListener("input", function () {
      if (!N.state.page) return;
      N.state.page.title = title.value;
      N.scheduleSave();
      N.renderCrumbs();
      scheduleSidebar();
    });
    title.addEventListener("blur", function () {
      if (!N.state.page) return;
      // 空标题保留为空，界面按语言显示「无标题」占位，避免写入某种语言的固定文案。
      N.state.page.title = title.value.trim();
      N.scheduleSave();
      N.renderSidebar();
    });
    title.addEventListener("keydown", function (e) {
      if (e.key === "Enter") {
        e.preventDefault();
        const blocks = N.state.page ? N.state.page.blocks : [];
        if (blocks.length) N.focusBlock(blocks[0].id, "start");
      }
    });

    N.$("#mdInput").addEventListener("change", function () {
      const file = this.files && this.files[0];
      this.value = "";
      if (file) importMarkdown(file);
    });
    N.$("#imageInput").addEventListener("change", function () {
      const file = this.files && this.files[0];
      this.value = "";
      if (file) handleUploadFile(file);
    });
    ["audioInput", "videoInput", "fileInput"].forEach(function (id) {
      const input = N.$("#" + id);
      if (!input) return;
      input.addEventListener("change", function () {
        const file = input.files && input.files[0];
        input.value = "";
        if (file) handleUploadFile(file);
      });
    });

    document.querySelectorAll("[data-close]").forEach(function (btn) {
      btn.addEventListener("click", function () { N.closeOverlay(btn.dataset.close); });
    });
    ["searchOverlay", "templateOverlay", "moveOverlay"].forEach(function (id) {
      const overlay = N.$("#" + id);
      overlay.addEventListener("mousedown", function (e) {
        if (e.target === overlay) N.closeOverlay(id);
      });
    });
    N.$("#emptyTrashBtn").addEventListener("click", async function () {
      const count = N.state.pages.filter(function (p) { return p.trashed; }).length;
      if (!count) return;
      const ok = await N.askConfirm({
        title: T("notes.confirm.emptyTitle"),
        body: T("notes.confirm.emptyBody", { n: count }),
        ok: T("notes.trash.emptyAction"),
      });
      if (!ok) return;
      try {
        await N.api("/trash", { method: "DELETE" });
        await N.refreshWorkspace();
        N.renderTrashView();
        N.toast(T("notes.toast.trashEmptied"));
      } catch (err) {
        N.toast(err.message, true);
      }
    });

    const searchInput = N.$("#searchInput");
    searchInput.addEventListener("input", function () {
      const query = searchInput.value.trim();
      clearTimeout(N.state.searchTimer);
      if (!query) { showRecentSearches(); return; }
      N.state.searchTimer = setTimeout(function () { runSearch(query); }, 220);
    });
    searchInput.addEventListener("keydown", function (e) {
      if (e.key === "ArrowDown") { e.preventDefault(); setSearchActive(N.state.searchSel + 1); }
      else if (e.key === "ArrowUp") { e.preventDefault(); setSearchActive(N.state.searchSel - 1); }
      else if (e.key === "Enter") {
        e.preventDefault();
        const item = N.state.searchItems[N.state.searchSel];
        if (item) {
          N.closeOverlay("searchOverlay");
          N.openPage(item.id);
        }
      } else if (e.key === "Escape") {
        N.closeOverlay("searchOverlay");
      }
    });

    document.addEventListener("keydown", function (e) {
      const mod = e.ctrlKey || e.metaKey;
      const key = (e.key || "").toLowerCase();
      if (mod && (key === "k" || key === "p")) {
        e.preventDefault();
        N.openSearch();
        return;
      }
      if (mod && key === "s") {
        e.preventDefault();
        N.flushSave();
        return;
      }
      if (e.key === "Escape") {
        if (!N.$("#searchOverlay").classList.contains("hidden")) { N.closeOverlay("searchOverlay"); return; }
        if (!N.$("#templateOverlay").classList.contains("hidden")) { N.closeOverlay("templateOverlay"); return; }
        if (!N.$("#moveOverlay").classList.contains("hidden")) { N.closeOverlay("moveOverlay"); return; }
        N.hidePopover();
      }
    });
    document.addEventListener("mousedown", function (e) {
      if (N._selectMenu && !N._selectMenu.contains(e.target) && !(e.target.closest && e.target.closest(".ui-select-trigger"))) {
        N.closeSelectMenu();
      }
    });

    // 拖到树空白处：移动到根层级末尾。
    const treeRoot = N.$("#treeRoot");
    treeRoot.addEventListener("dragover", function (e) {
      if (!N.state.dragPageId) return;
      e.preventDefault();
      treeRoot.classList.add("drop-root");
    });
    treeRoot.addEventListener("dragleave", function (e) {
      if (!e.relatedTarget || !treeRoot.contains(e.relatedTarget)) treeRoot.classList.remove("drop-root");
    });
    treeRoot.addEventListener("drop", function (e) {
      treeRoot.classList.remove("drop-root");
      const dragId = N.state.dragPageId;
      N.state.dragPageId = "";
      if (!dragId) return;
      e.preventDefault();
      N.movePageTo(dragId, "", -1);
    });

    document.addEventListener("dragend", function () {
      if (N.clearPageDropMarks) N.clearPageDropMarks();
      N.state.dragPageId = "";
      N.$("#treeRoot").classList.remove("drop-root");
    });

    window.addEventListener("pagehide", function () {
      if (N.state.dirty && N.state.page) {
        try {
          fetch(N.API_BASE + "/pages/" + N.state.page.id, {
            method: "PUT",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({
              title: N.state.page.title,
              icon: N.state.page.icon,
              cover: N.state.page.cover,
              folder: !!N.state.page.folder,
              blocks: N.state.page.blocks,
            }),
            keepalive: true,
          });
        } catch (_) { /* 忽略 */ }
      }
    });
  }

  let sidebarFrame = 0;
  function scheduleSidebar() {
    if (sidebarFrame) return;
    sidebarFrame = requestAnimationFrame(function () {
      sidebarFrame = 0;
      N.renderSidebar();
    });
  }

  /* ---------------- 启动 ---------------- */
  async function boot() {
    bindStatic();
    N.bindEditorEvents();
    N.I18N.apply(document);
    document.title = T("notes.app.name");
    syncMenuBtn();
    N.state.saveKind = "saved";
    N.setSaveState("saved");
    N.I18N.onChange(function () {
      document.title = T("notes.app.name");
      N.I18N.apply(document);
      if (N.state.view === "page" && N.state.page) N.renderPageView();
      else if (N.state.view === "trash") N.renderTrashView();
      N.setSaveState(N.state.saveKind);
    });
    try {
      await N.refreshWorkspace();
      if (N.state.pages.length === 0) {
        await N.createPage({
          title: T("notes.welcome.title"),
          icon: "👋",
          cover: "gradient:g6",
          blocks: welcomeBlocks(),
        });
        return;
      }
      let lastId = "";
      try { lastId = localStorage.getItem("devos.notes.last") || ""; } catch (_) { /* 忽略 */ }
      const last = lastId ? N.summaryOf(lastId) : null;
      const target = last && !last.trashed ? last : N.pageTreeOrder(N.childrenOf(""))[0];
      if (target) await N.openPage(target.id);
      else N.showEmptyState();
    } catch (err) {
      N.toast(T("notes.toast.loadFailed", { error: err.message }), true);
      N.showEmptyState();
    }
  }

  if (document.readyState === "loading") document.addEventListener("DOMContentLoaded", boot);
  else boot();
})();
