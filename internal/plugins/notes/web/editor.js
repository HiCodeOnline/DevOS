/* 块渲染与块级菜单 */
(function () {
  "use strict";
  const N = window.Notes;
  const T = N.t;

  N.TEXT_TYPES = ["paragraph", "heading1", "heading2", "heading3", "bulleted", "numbered", "todo", "toggle", "quote", "callout"];
  N.SLASH_ITEMS = [
    { type: "paragraph", icon: "text", labelKey: "notes.block.paragraph", keywords: "text paragraph p 文本 段落 正文" },
    { type: "heading1", icon: "h1", labelKey: "notes.block.heading1", keywords: "heading1 h1 title 标题一 大标题" },
    { type: "heading2", icon: "h2", labelKey: "notes.block.heading2", keywords: "heading2 h2 subtitle 标题二 中标题" },
    { type: "heading3", icon: "h3", labelKey: "notes.block.heading3", keywords: "heading3 h3 标题三 小标题" },
    { type: "bulleted", icon: "list", labelKey: "notes.block.bulleted", keywords: "bulleted list ul 无序列表 项目符号" },
    { type: "numbered", icon: "numbered", labelKey: "notes.block.numbered", keywords: "numbered list ol 有序列表 数字" },
    { type: "todo", icon: "todo", labelKey: "notes.block.todo", keywords: "todo task checkbox 待办 任务 清单" },
    { type: "toggle", icon: "toggle", labelKey: "notes.block.toggle", keywords: "toggle 折叠 展开" },
    { type: "quote", icon: "quote", labelKey: "notes.block.quote", keywords: "quote blockquote 引用" },
    { type: "callout", icon: "callout", labelKey: "notes.block.callout", keywords: "callout tip note 提示 高亮 卡片" },
    { type: "code", icon: "code", labelKey: "notes.block.code", keywords: "code snippet 代码 片段" },
    { type: "equation", icon: "equation", labelKey: "notes.block.equation", keywords: "equation math latex formula katex 公式 数学 方程" },
    { type: "diagram", icon: "diagram", labelKey: "notes.block.diagram", keywords: "diagram mermaid flowchart graph 流程图 流程 图表" },
    { type: "table", icon: "table", labelKey: "notes.block.table", keywords: "table grid 表格" },
    { type: "divider", icon: "divider", labelKey: "notes.block.divider", keywords: "divider line hr 分割线 分隔线" },
    { type: "image", icon: "image", labelKey: "notes.block.image", keywords: "image picture photo 图片 照片" },
    { type: "audio", icon: "audio", labelKey: "notes.block.audio", keywords: "audio music sound mp3 wav 音频 音乐 语音" },
    { type: "video", icon: "video", labelKey: "notes.block.video", keywords: "video movie mp4 webm 视频 影片" },
    { type: "file", icon: "file", labelKey: "notes.block.file", keywords: "file attachment pdf zip doc 附件 文件 文档" },
    { type: "bookmark", icon: "bookmark", labelKey: "notes.block.bookmark", keywords: "bookmark link url 书签 链接 网址" },
  ];

  N.mergeable = function (type) { return N.TEXT_TYPES.indexOf(type) >= 0; };

  N.placeholderKey = function (type) {
    const map = {
      paragraph: "paragraph", heading1: "heading1", heading2: "heading2", heading3: "heading3",
      bulleted: "bulleted", numbered: "numbered", todo: "todo", toggle: "toggle",
      quote: "quote", callout: "callout", equation: "equation", diagram: "diagram",
    };
    return "notes.editor.placeholder." + (map[type] || "paragraph");
  };

  N.defaultRows = function () { return [["", "", ""], ["", "", ""], ["", "", ""]]; };

  function inputFor(block, extraClass) {
    const div = document.createElement("div");
    div.className = "block-input" + (extraClass ? " " + extraClass : "");
    div.contentEditable = "true";
    div.spellcheck = false;
    div.setAttribute("data-placeholder", T(N.placeholderKey(block.type)));
    div.innerHTML = N.safeHTML(block.content || "");
    return div;
  }

  function sideButtons(block) {
    const side = N.el("div", "block-side");
    const add = N.el("button", "");
    add.type = "button";
    add.appendChild(N.svg("plus"));
    add.title = T("notes.editor.addBlock");
    add.addEventListener("click", function (e) {
      e.stopPropagation();
      N.addBlockAfter(block.id, "paragraph", "", true);
    });
    const grip = N.el("button", "block-handle");
    grip.type = "button";
    grip.title = T("notes.editor.blockMenu");
    grip.draggable = true;
    grip.dataset.dragBlock = block.id;
    grip.appendChild(N.svg("grip"));
    grip.addEventListener("click", function (e) {
      e.stopPropagation();
      N.openBlockMenu(block.id, grip);
    });
    side.appendChild(add);
    side.appendChild(grip);
    return side;
  }

  function numberedInfo(index) {
    const blocks = N.state.page ? N.state.page.blocks : [];
    let count = 0;
    for (let i = index; i >= 0; i--) {
      if (blocks[i].type !== "numbered") break;
      count++;
    }
    return count || 1;
  }

  N.renderEditor = function (focus) {
    const editor = N.$("#editor");
    if (!editor || !N.state.page) return;
    N.clear(editor);
    const blocks = N.state.page.blocks || [];
    if (blocks.length === 0) {
      blocks.push(N.newBlock("paragraph"));
    }
    blocks.forEach(function (block, index) {
      editor.appendChild(N.buildBlock(block, index, numberedInfo(index)));
    });
    if (N.renderRichAll) N.renderRichAll();
    if (focus) {
      N.focusBlock(focus.id, focus.where || "end", focus.offset);
    }
    N.updatePageMeta();
  };

  N.buildBlock = function (block, index, number) {
    const wrapper = N.el("div", "block");
    wrapper.dataset.id = block.id;
    wrapper.dataset.type = block.type;
    wrapper.appendChild(sideButtons(block));
    const body = N.el("div", "block-body");
    wrapper.appendChild(body);

    switch (block.type) {
      case "heading1":
      case "heading2":
      case "heading3":
        body.appendChild(inputFor(block, "blk-" + block.type));
        break;
      case "bulleted": {
        const marker = N.el("span", "marker", "•");
        body.appendChild(marker);
        body.appendChild(inputFor(block));
        break;
      }
      case "numbered": {
        const marker = N.el("span", "marker num", String(number || 1) + ".");
        body.appendChild(marker);
        body.appendChild(inputFor(block));
        break;
      }
      case "todo": {
        const row = N.el("div", "blk-todo" + (block.props.checked ? " is-checked" : ""));
        const box = document.createElement("input");
        box.type = "checkbox";
        box.checked = !!block.props.checked;
        box.addEventListener("change", function () {
          N.pushHistory();
          block.props.checked = box.checked;
          row.classList.toggle("is-checked", box.checked);
          N.scheduleSave();
        });
        const input = inputFor(block);
        row.appendChild(box);
        row.appendChild(input);
        body.appendChild(row);
        break;
      }
      case "toggle": {
        const arrow = N.el("button", "toggle-arrow" + (block.props.collapsed ? "" : " is-open"));
        arrow.type = "button";
        arrow.appendChild(N.svg("chevron"));
        arrow.addEventListener("click", function (e) {
          e.stopPropagation();
          block.props.collapsed = !block.props.collapsed;
          wrapper.classList.toggle("is-collapsed", !!block.props.collapsed);
          arrow.classList.toggle("is-open", !block.props.collapsed);
          N.scheduleSave();
        });
        body.appendChild(arrow);
        body.appendChild(inputFor(block));
        if (block.props.collapsed) wrapper.classList.add("is-collapsed");
        break;
      }
      case "quote":
        body.appendChild(inputFor(block, "blk-quote"));
        break;
      case "callout": {
        const color = block.props.color && block.props.color !== "default" ? " callout-" + block.props.color : "";
        const box = N.el("div", "blk-callout" + color);
        const icon = N.el("button", "callout-icon", block.props.icon || "💡");
        icon.type = "button";
        icon.title = T("notes.editor.changeIcon");
        icon.addEventListener("click", function (e) {
          e.stopPropagation();
          N.openCalloutIconMenu(block, icon);
        });
        box.appendChild(icon);
        box.appendChild(inputFor(block));
        body.appendChild(box);
        break;
      }
      case "code": {
        const box = N.el("div", "blk-code");
        const head = N.el("div", "code-head");
        const select = N.el("button", "ui-select-trigger");
        select.type = "button";
        select.appendChild(N.el("span", "code-lang", (N.CODE_LANGS.find(function (l) { return l.value === block.props.language; }) || N.CODE_LANGS[0]).label));
        const caret = N.svg("chevron");
        caret.classList.add("ui-select-caret");
        select.appendChild(caret);
        select.addEventListener("click", function (e) {
          e.stopPropagation();
          N.openSelectMenu(select, N.CODE_LANGS, block.props.language || "text", function (value) {
            block.props.language = value;
            select.querySelector(".code-lang").textContent = (N.CODE_LANGS.find(function (l) { return l.value === value; }) || N.CODE_LANGS[0]).label;
            N.scheduleSave();
          });
        });
        const copy = N.el("button", "code-copy");
        copy.type = "button";
        copy.appendChild(N.svg("copy"));
        copy.appendChild(N.el("span", "", T("notes.code.copy")));
        copy.addEventListener("click", function (e) {
          e.stopPropagation();
          N.copyText(block.content || "");
        });
        head.appendChild(select);
        head.appendChild(copy);
        // 编辑层用 textarea，下层 pre 负责语法高亮，两者字体与内边距完全一致。
        const wrap = N.el("div", "code-wrap");
        const view = document.createElement("pre");
        view.className = "code-view";
        view.setAttribute("aria-hidden", "true");
        const code = document.createElement("code");
        code.className = "hljs";
        view.appendChild(code);
        const input = document.createElement("textarea");
        input.className = "code-input";
        input.spellcheck = false;
        input.placeholder = T("notes.editor.placeholder.code");
        input.value = block.content || "";
        input.addEventListener("scroll", function () {
          view.scrollTop = input.scrollTop;
          view.scrollLeft = input.scrollLeft;
        });
        wrap.appendChild(view);
        wrap.appendChild(input);
        box.appendChild(head);
        box.appendChild(wrap);
        body.appendChild(box);
        break;
      }
      case "equation":
        body.appendChild(N.buildEquation(block));
        break;
      case "diagram":
        body.appendChild(N.buildDiagram(block));
        break;
      case "divider": {
        const box = N.el("div", "blk-divider");
        box.appendChild(document.createElement("hr"));
        body.appendChild(box);
        break;
      }
      case "image":
        body.appendChild(N.buildImage(block));
        break;
      case "audio":
      case "video":
        body.appendChild(N.buildMedia(block));
        break;
      case "file":
        body.appendChild(N.buildFile(block));
        break;
      case "bookmark":
        body.appendChild(N.buildBookmark(block));
        break;
      case "table":
        body.appendChild(N.buildTable(block));
        break;
      default:
        body.appendChild(inputFor(block));
    }
    return wrapper;
  };

  N.buildImage = function (block) {
    const wrap = N.el("div", "blk-image");
    if (block.props.url) {
      const img = document.createElement("img");
      img.src = N.assetURL(block.props.url);
      img.alt = block.props.caption || "";
      img.loading = "lazy";
      wrap.appendChild(img);
      const actions = N.el("div", "image-actions");
      const change = N.el("button", "pill", T("notes.image.change"));
      change.type = "button";
      change.addEventListener("click", function (e) { e.stopPropagation(); N.uploadImageFor(block.id); });
      const link = N.el("button", "pill", T("notes.image.link"));
      link.type = "button";
      link.addEventListener("click", function (e) { e.stopPropagation(); N.editImageURL(block.id); });
      actions.appendChild(change);
      actions.appendChild(link);
      wrap.appendChild(actions);
      const caption = document.createElement("input");
      caption.className = "image-caption";
      caption.value = block.props.caption || "";
      caption.placeholder = T("notes.image.caption");
      caption.dataset.field = "caption";
      wrap.appendChild(caption);
    } else {
      const empty = N.el("div", "image-empty");
      empty.appendChild(N.svg("image"));
      empty.appendChild(N.el("span", "", T("notes.image.empty")));
      const row = N.el("div", "row");
      const up = N.el("button", "btn-plain", T("notes.image.upload"));
      up.type = "button";
      up.addEventListener("click", function (e) { e.stopPropagation(); N.uploadImageFor(block.id); });
      const byLink = N.el("button", "btn-plain", T("notes.image.byLink"));
      byLink.type = "button";
      byLink.addEventListener("click", function (e) { e.stopPropagation(); N.editImageURL(block.id); });
      row.appendChild(up);
      row.appendChild(byLink);
      empty.appendChild(row);
      wrap.appendChild(empty);
    }
    return wrap;
  };

  N.buildMedia = function (block) {
    const isAudio = block.type === "audio";
    const wrap = N.el("div", "blk-media" + (isAudio ? " is-audio" : ""));
    if (!block.props.url) {
      const empty = N.el("div", "image-empty");
      empty.appendChild(N.svg(isAudio ? "audio" : "video"));
      empty.appendChild(N.el("span", "", T(isAudio ? "notes.media.audioEmpty" : "notes.media.videoEmpty")));
      const row = N.el("div", "row");
      const up = N.el("button", "btn-plain", T(isAudio ? "notes.media.uploadAudio" : "notes.media.uploadVideo"));
      up.type = "button";
      up.addEventListener("click", function (e) { e.stopPropagation(); N.pickUpload(block.id, block.type); });
      const byLink = N.el("button", "btn-plain", T("notes.media.byLink"));
      byLink.type = "button";
      byLink.addEventListener("click", function (e) { e.stopPropagation(); N.editMediaURL(block.id); });
      row.appendChild(up);
      row.appendChild(byLink);
      empty.appendChild(row);
      wrap.appendChild(empty);
      return wrap;
    }
    const media = document.createElement(isAudio ? "audio" : "video");
    media.controls = true;
    media.preload = "metadata";
    media.src = N.assetURL(block.props.url);
    wrap.appendChild(media);
    const actions = N.el("div", "image-actions");
    const change = N.el("button", "pill", T("notes.media.change"));
    change.type = "button";
    change.addEventListener("click", function (e) { e.stopPropagation(); N.pickUpload(block.id, block.type); });
    const link = N.el("button", "pill", T("notes.media.link"));
    link.type = "button";
    link.addEventListener("click", function (e) { e.stopPropagation(); N.editMediaURL(block.id); });
    actions.appendChild(change);
    actions.appendChild(link);
    wrap.appendChild(actions);
    const caption = document.createElement("input");
    caption.className = "image-caption";
    caption.value = block.props.caption || "";
    caption.placeholder = T("notes.media.caption");
    caption.dataset.field = "caption";
    wrap.appendChild(caption);
    return wrap;
  };

  N.buildFile = function (block) {
    const wrap = N.el("div", "bookmark-wrap");
    if (!block.props.url) {
      const empty = N.el("div", "image-empty");
      empty.appendChild(N.svg("file"));
      empty.appendChild(N.el("span", "", T("notes.media.fileEmpty")));
      const row = N.el("div", "row");
      const up = N.el("button", "btn-plain", T("notes.media.uploadFile"));
      up.type = "button";
      up.addEventListener("click", function (e) { e.stopPropagation(); N.pickUpload(block.id, "file"); });
      const byLink = N.el("button", "btn-plain", T("notes.media.byLink"));
      byLink.type = "button";
      byLink.addEventListener("click", function (e) { e.stopPropagation(); N.editMediaURL(block.id); });
      row.appendChild(up);
      row.appendChild(byLink);
      empty.appendChild(row);
      wrap.appendChild(empty);
      return wrap;
    }
    const link = document.createElement("a");
    link.className = "blk-bookmark";
    link.href = N.assetURL(block.props.url);
    link.target = "_blank";
    link.rel = "noopener noreferrer";
    const icon = N.el("span", "bookmark-icon");
    icon.appendChild(N.svg("file"));
    const main = N.el("div", "bookmark-main");
    main.appendChild(N.el("div", "bookmark-title", block.props.caption || T("notes.media.fileName")));
    main.appendChild(N.el("div", "bookmark-url", block.props.url));
    const edit = N.el("span", "bookmark-edit", T("notes.common.edit"));
    edit.addEventListener("click", function (e) {
      e.preventDefault();
      e.stopPropagation();
      N.editMediaURL(block.id);
    });
    link.appendChild(icon);
    link.appendChild(main);
    link.appendChild(edit);
    wrap.appendChild(link);
    return wrap;
  };

  N.buildBookmark = function (block) {
    const wrap = N.el("div", "bookmark-wrap");
    if (!block.props.url) {
      const empty = N.el("div", "image-empty");
      empty.appendChild(N.svg("bookmark"));
      empty.appendChild(N.el("span", "", T("notes.bookmark.empty")));
      const btn = N.el("button", "btn-plain", T("notes.bookmark.add"));
      btn.type = "button";
      btn.addEventListener("click", function (e) { e.stopPropagation(); N.editBookmark(block.id); });
      empty.appendChild(btn);
      wrap.appendChild(empty);
      return wrap;
    }
    const link = document.createElement("a");
    link.className = "blk-bookmark";
    link.href = block.props.url;
    link.target = "_blank";
    link.rel = "noopener noreferrer";
    const icon = N.el("span", "bookmark-icon");
    icon.appendChild(N.svg("link"));
    const main = N.el("div", "bookmark-main");
    main.appendChild(N.el("div", "bookmark-title", block.props.caption || block.props.url));
    main.appendChild(N.el("div", "bookmark-url", block.props.url));
    const edit = N.el("span", "bookmark-edit", T("notes.common.edit"));
    edit.addEventListener("click", function (e) {
      e.preventDefault();
      e.stopPropagation();
      N.editBookmark(block.id);
    });
    link.appendChild(icon);
    link.appendChild(main);
    link.appendChild(edit);
    wrap.appendChild(link);
    return wrap;
  };

  N.buildTable = function (block) {
    const rows = block.props.rows && block.props.rows.length ? block.props.rows : N.defaultRows();
    block.props.rows = rows;
    const wrap = N.el("div", "blk-table");
    const table = document.createElement("table");
    const tbody = document.createElement("tbody");
    rows.forEach(function (row, r) {
      const tr = document.createElement("tr");
      row.forEach(function (cell, c) {
        const td = document.createElement(block.props.hasHeader && r === 0 ? "th" : "td");
        const input = N.el("div", "cell-input");
        input.contentEditable = "true";
        input.spellcheck = false;
        input.dataset.row = r;
        input.dataset.col = c;
        input.textContent = cell;
        td.appendChild(input);
        tr.appendChild(td);
      });
      tbody.appendChild(tr);
    });
    table.appendChild(tbody);
    wrap.appendChild(table);
    const tools = N.el("div", "table-tools");
    function tool(label, handler) {
      const btn = N.el("button", "", label);
      btn.type = "button";
      btn.addEventListener("click", function (e) { e.stopPropagation(); N.pushHistory(); handler(); N.renderEditor(); N.scheduleSave(); });
      tools.appendChild(btn);
    }
    tool(T("notes.table.addRow"), function () {
      rows.push(new Array(rows[0].length).fill(""));
    });
    tool(T("notes.table.addCol"), function () {
      rows.forEach(function (row) { row.push(""); });
    });
    tool(block.props.hasHeader ? T("notes.table.headerOff") : T("notes.table.headerOn"), function () {
      block.props.hasHeader = !block.props.hasHeader;
    });
    tool(T("notes.table.delRow"), function () {
      if (rows.length > 1) rows.pop();
    });
    tool(T("notes.table.delCol"), function () {
      if (rows[0].length > 1) rows.forEach(function (row) { row.pop(); });
    });
    wrap.appendChild(tools);
    return wrap;
  };

  /* ---------------- 块操作 ---------------- */
  N.findBlock = function (id) {
    if (!N.state.page) return null;
    return (N.state.page.blocks || []).find(function (b) { return b.id === id; }) || null;
  };
  N.blockIndex = function (id) {
    if (!N.state.page) return -1;
    return (N.state.page.blocks || []).findIndex(function (b) { return b.id === id; });
  };
  N.blockEl = function (id) {
    const editor = N.$("#editor");
    if (!editor) return null;
    return editor.querySelector('.block[data-id="' + id + '"]');
  };
  N.inputEl = function (id) {
    const wrapper = N.blockEl(id);
    return wrapper ? wrapper.querySelector(".block-input, .code-input") : null;
  };

  function isTextarea(el) { return !!el && el.tagName === "TEXTAREA"; }

  N.focusBlock = function (id, where, offset) {
    const el = N.inputEl(id);
    if (!el) return;
    el.focus();
    if (isTextarea(el)) {
      let pos = where === "end" ? el.value.length : 0;
      if (typeof offset === "number") pos = Math.min(offset, el.value.length);
      try { el.setSelectionRange(pos, pos); } catch (_) { /* 忽略 */ }
      N.scrollBlockIntoView(id);
      return;
    }
    const sel = window.getSelection();
    if (!sel) return;
    const range = document.createRange();
    if (typeof offset === "number") {
      N.setCaretOffset(el, offset);
      return;
    }
    range.selectNodeContents(el);
    range.collapse(where !== "end");
    sel.removeAllRanges();
    sel.addRange(range);
    N.scrollBlockIntoView(id);
  };

  N.setCaretOffset = function (el, offset) {
    if (isTextarea(el)) {
      const pos = Math.max(0, Math.min(offset, el.value.length));
      try { el.setSelectionRange(pos, pos); } catch (_) { /* 忽略 */ }
      return;
    }
    const sel = window.getSelection();
    if (!sel) return;
    const walker = document.createTreeWalker(el, NodeFilter.SHOW_TEXT, null);
    let remaining = offset;
    let node = null;
    while ((node = walker.nextNode())) {
      if (remaining <= node.nodeValue.length) {
        const range = document.createRange();
        range.setStart(node, remaining);
        range.collapse(true);
        sel.removeAllRanges();
        sel.addRange(range);
        return;
      }
      remaining -= node.nodeValue.length;
    }
    const range = document.createRange();
    range.selectNodeContents(el);
    range.collapse(false);
    sel.removeAllRanges();
    sel.addRange(range);
  };

  N.scrollBlockIntoView = function (id) {
    const wrapper = N.blockEl(id);
    if (!wrapper) return;
    const scroll = N.$("#pageScroll");
    if (!scroll) return;
    const rect = wrapper.getBoundingClientRect();
    const view = scroll.getBoundingClientRect();
    if (rect.bottom > view.bottom - 80) {
      scroll.scrollTop += rect.bottom - view.bottom + 120;
    } else if (rect.top < view.top + 60) {
      scroll.scrollTop -= view.top + 60 - rect.top;
    }
  };

  N.addBlockAfter = function (id, type, content, focus) {
    N.pushHistory();
    const blocks = N.state.page.blocks;
    const index = N.blockIndex(id);
    const block = N.newBlock(type || "paragraph", content || "", type === "table" ? { rows: N.defaultRows() } : type === "callout" ? { icon: "💡", color: "default" } : {});
    if (index < 0) blocks.push(block);
    else blocks.splice(index + 1, 0, block);
    N.renderEditor(focus ? { id: block.id, where: "end" } : null);
    N.scheduleSave();
    return block;
  };

  N.appendBlock = function (type) {
    const blocks = N.state.page.blocks;
    const last = blocks[blocks.length - 1];
    return N.addBlockAfter(last ? last.id : "", type || "paragraph", "", true);
  };

  N.removeBlock = function (id, focusPrev) {
    if (!N.state.page) return;
    const blocks = N.state.page.blocks;
    if (blocks.length <= 1) {
      const block = blocks[0];
      if (block) {
        N.pushHistory();
        block.content = "";
        block.props = {};
        block.type = "paragraph";
        N.renderEditor({ id: block.id, where: "start" });
        N.scheduleSave();
      }
      return;
    }
    N.pushHistory();
    const index = N.blockIndex(id);
    const prev = index > 0 ? blocks[index - 1] : null;
    const prevLen = prev ? N.plainText(prev.content || "").length : 0;
    blocks.splice(index, 1);
    N.renderEditor(focusPrev && prev ? { id: prev.id, where: "end", offset: prevLen } : null);
    N.scheduleSave();
  };

  N.duplicateBlock = function (id) {
    N.pushHistory();
    const blocks = N.state.page.blocks;
    const index = N.blockIndex(id);
    if (index < 0) return;
    const clone = JSON.parse(JSON.stringify(blocks[index]));
    clone.id = N.uid();
    blocks.splice(index + 1, 0, clone);
    N.renderEditor({ id: clone.id, where: "end" });
    N.scheduleSave();
  };

  N.moveBlockTo = function (dragId, targetId, after) {
    if (dragId === targetId) return;
    N.pushHistory();
    const blocks = N.state.page.blocks;
    const from = N.blockIndex(dragId);
    if (from < 0) return;
    const moving = blocks.splice(from, 1)[0];
    let target = N.blockIndex(targetId);
    if (target < 0) target = blocks.length - 1;
    blocks.splice(after ? target + 1 : target, 0, moving);
    N.renderEditor({ id: moving.id, where: "end" });
    N.scheduleSave();
  };

  N.turnBlock = function (id, type) {
    const block = N.findBlock(id);
    if (!block) return;
    N.pushHistory();
    if (block.type !== type) {
      if (type === "divider") block.content = "";
      if (type === "image") {
        block.content = "";
        block.props.url = "";
        block.props.caption = "";
      }
      if (type === "table") {
        block.content = "";
        block.props.rows = N.defaultRows();
        block.props.hasHeader = false;
      }
      if (type === "bookmark") block.content = "";
      // 转为公式/流程图时保留纯文本，避免把行内标签当成语法源码。
      if (type === "equation" || type === "diagram") block.content = N.plainText(block.content || "");
      if (type === "code") block.props.language = block.props.language || "text";
      if (type === "callout") {
        block.props.icon = block.props.icon || "💡";
        block.props.color = block.props.color || "default";
      }
      if (type === "todo") block.props.checked = false;
      if (type === "toggle") block.props.collapsed = false;
    }
    block.type = type;
    N.renderEditor({ id: block.id, where: "end" });
    N.scheduleSave();
  };

  /* ---------------- 块菜单 ---------------- */
  N.openBlockMenu = function (id, anchor) {
    const block = N.findBlock(id);
    if (!block) return;
    const menu = N.el("div", "block-menu");
    menu.appendChild(N.el("div", "menu-label", T("notes.editor.turnInto")));
    N.SLASH_ITEMS.forEach(function (item) {
      const btn = N.el("button", "menu-item");
      btn.type = "button";
      const icon = N.el("span", "mi-icon");
      icon.appendChild(N.svg(item.icon));
      btn.appendChild(icon);
      btn.appendChild(N.el("span", "mi-label", T(item.labelKey)));
      if (block.type === item.type) btn.appendChild(N.el("span", "mi-hint", T("notes.common.current")));
      btn.addEventListener("click", function () {
        N.hidePopover();
        N.turnBlock(id, item.type);
      });
      menu.appendChild(btn);
    });
    menu.appendChild(N.el("div", "menu-sep"));
    const actions = [
      { label: T("notes.editor.duplicate"), icon: "copy", run: function () { N.duplicateBlock(id); } },
      { label: T("notes.editor.moveUp"), icon: "arrowUp", run: function () { N.moveBlockBy(id, -1); } },
      { label: T("notes.editor.moveDown"), icon: "arrowDown", run: function () { N.moveBlockBy(id, 1); } },
      { label: T("notes.editor.delete"), icon: "trash", danger: true, run: function () { N.removeBlock(id, true); } },
    ];
    actions.forEach(function (action) {
      const btn = N.el("button", "menu-item" + (action.danger ? " is-danger" : ""));
      btn.type = "button";
      const icon = N.el("span", "mi-icon");
      icon.appendChild(N.svg(action.icon));
      btn.appendChild(icon);
      btn.appendChild(N.el("span", "mi-label", action.label));
      btn.addEventListener("click", function () {
        N.hidePopover();
        action.run();
      });
      menu.appendChild(btn);
    });
    const rect = anchor.getBoundingClientRect();
    N.showPopover(menu, { left: rect.left, right: rect.right, top: rect.top, bottom: rect.bottom });
  };

  N.moveBlockBy = function (id, delta) {
    const blocks = N.state.page.blocks;
    const index = N.blockIndex(id);
    const next = index + delta;
    if (index < 0 || next < 0 || next >= blocks.length) return;
    N.pushHistory();
    const item = blocks.splice(index, 1)[0];
    blocks.splice(next, 0, item);
    N.renderEditor({ id: item.id, where: "end" });
    N.scheduleSave();
  };

  N.openCalloutIconMenu = function (block, anchor) {
    const menu = N.el("div", "pop-emoji");
    const grid = N.el("div", "emoji-grid");
    N.EMOJIS.slice(0, 64).forEach(function (emoji) {
      const btn = N.el("button", "emoji-cell", emoji);
      btn.type = "button";
      btn.addEventListener("click", function () {
        N.pushHistory();
        block.props.icon = emoji;
        N.hidePopover();
        N.renderEditor();
        N.scheduleSave();
      });
      grid.appendChild(btn);
    });
    menu.appendChild(grid);
    menu.appendChild(N.el("div", "menu-sep"));
    const colors = N.el("div", "callout-colors");
    ["default", "gray", "brown", "orange", "yellow", "green", "blue", "purple", "pink", "red"].forEach(function (color) {
      const btn = N.el("button", "color-dot" + (color === "default" ? " is-default" : " color-" + color));
      btn.type = "button";
      btn.title = color;
      btn.addEventListener("click", function () {
        N.pushHistory();
        block.props.color = color;
        N.hidePopover();
        N.renderEditor();
        N.scheduleSave();
      });
      colors.appendChild(btn);
    });
    menu.appendChild(colors);
    const rect = anchor.getBoundingClientRect();
    N.showPopover(menu, { left: rect.left, right: rect.right, top: rect.top, bottom: rect.bottom });
  };

  N.copyText = async function (text) {
    try {
      if (navigator.clipboard && navigator.clipboard.writeText) await navigator.clipboard.writeText(text);
      else {
        const area = document.createElement("textarea");
        area.value = text;
        document.body.appendChild(area);
        area.select();
        document.execCommand("copy");
        document.body.removeChild(area);
      }
      N.toast(T("notes.toast.copied"));
    } catch (_) {
      N.toast(T("notes.toast.copyFailed"), true);
    }
  };

  /* ---------------- 附件上传 ---------------- */
  const UPLOAD_LIMITS = { image: 10 << 20, audio: 40 << 20, video: 60 << 20, file: 40 << 20 };

  N.fileKind = function (file) {
    const mime = (file.type || "").toLowerCase();
    if (mime.indexOf("image/") === 0) return "image";
    if (mime.indexOf("audio/") === 0) return "audio";
    if (mime.indexOf("video/") === 0) return "video";
    const ext = (file.name || "").toLowerCase().split(".").pop();
    if (["png", "jpg", "jpeg", "gif", "webp", "bmp"].indexOf(ext) >= 0) return "image";
    if (["mp3", "wav", "ogg", "m4a", "aac", "flac", "opus"].indexOf(ext) >= 0) return "audio";
    if (["mp4", "webm", "ogv", "mov", "m4v"].indexOf(ext) >= 0) return "video";
    return "file";
  };

  N.uploadFile = async function (file, forceKind) {
    const kind = forceKind || N.fileKind(file);
    if (file.size > (UPLOAD_LIMITS[kind] || UPLOAD_LIMITS.file)) {
      throw new Error(T("notes.toast.fileTooLarge", { name: file.name || "" }));
    }
    const form = new FormData();
    form.append("file", file);
    form.append("kind", kind);
    const res = await fetch(N.API_BASE + "/upload", { method: "POST", body: form });
    if (!res.ok) {
      let message = "";
      try {
        const data = await res.json();
        message = data.message || data.error || "";
      } catch (_) { /* 非 JSON 错误响应 */ }
      throw new Error(message || ("HTTP " + res.status));
    }
    return res.json();
  };

  function blockTypeForKind(kind) {
    if (kind === "audio" || kind === "video" || kind === "file") return kind;
    return "image";
  }

  // 把一批文件依次上传并插入到锚点块之后，返回成功插入的数量。
  N.insertFilesAt = async function (anchorId, files) {
    if (!N.state.page || !files || files.length === 0) return 0;
    N.pushHistory();
    const blocks = N.state.page.blocks;
    let anchor = anchorId;
    let count = 0;
    for (let i = 0; i < files.length; i++) {
      const file = files[i];
      try {
        const data = await N.uploadFile(file);
        const type = blockTypeForKind(data.kind);
        const block = N.newBlock(type, "", {
          url: data.url,
          caption: type === "file" ? (data.name || "") : "",
        });
        let at = N.blockIndex(anchor);
        if (at < 0) at = blocks.length - 1;
        blocks.splice(at + 1, 0, block);
        anchor = block.id;
        count++;
      } catch (err) {
        N.toast(err.message, true);
      }
    }
    if (count) {
      N.renderEditor();
      N.scheduleSave();
      N.toast(T("notes.toast.uploadedCount", { count: count }));
    }
    return count;
  };

  N.pickUpload = function (blockId, kind) {
    const input = N.$("#" + ({
      image: "imageInput",
      audio: "audioInput",
      video: "videoInput",
      file: "fileInput",
    }[kind] || "imageInput"));
    if (!input) return;
    N.state.pendingUpload = { kind: "block", blockId: blockId, blockKind: kind };
    input.click();
  };
  N.uploadImageFor = function (blockId) { N.pickUpload(blockId, "image"); };

  N.fillBlockWithFile = async function (blockId, file) {
    const block = N.findBlock(blockId);
    if (!block) return;
    const kind = block.type === "audio" || block.type === "video" || block.type === "file" ? block.type : "image";
    try {
      const data = await N.uploadFile(file, kind);
      N.pushHistory();
      block.props.url = data.url;
      if (kind === "file") block.props.caption = data.name || "";
      N.renderEditor();
      N.scheduleSave();
      N.toast(T("notes.toast.uploadedCount", { count: 1 }));
    } catch (err) {
      N.toast(err.message, true);
    }
  };

  N.editImageURL = async function (blockId) {
    const block = N.findBlock(blockId);
    if (!block) return;
    const url = await N.askText({
      title: T("notes.image.linkTitle"),
      placeholder: "https://example.com/image.png",
      value: block.props.url || "",
      hint: T("notes.image.linkHint"),
    });
    if (url === null) return;
    if (!/^https?:\/\//i.test(url)) {
      N.toast(T("notes.image.linkInvalid"), true);
      return;
    }
    N.pushHistory();
    block.props.url = url;
    N.renderEditor();
    N.scheduleSave();
  };

  N.editMediaURL = async function (blockId) {
    const block = N.findBlock(blockId);
    if (!block) return;
    const key = block.type === "audio" ? "audio" : block.type === "video" ? "video" : "file";
    const url = await N.askText({
      title: T("notes.media." + key + "LinkTitle"),
      placeholder: "https://example.com/" + (key === "audio" ? "audio.mp3" : key === "video" ? "video.mp4" : "file.pdf"),
      value: block.props.url || "",
      hint: T("notes.media.linkHint"),
    });
    if (url === null) return;
    if (!/^https?:\/\//i.test(url)) {
      N.toast(T("notes.image.linkInvalid"), true);
      return;
    }
    N.pushHistory();
    block.props.url = url;
    if (block.type === "file" && !block.props.caption) {
      block.props.caption = url.split("/").pop().split("?")[0];
    }
    N.renderEditor();
    N.scheduleSave();
  };
  N.editBookmark = async function (blockId) {
    const block = N.findBlock(blockId);
    if (!block) return;
    const url = await N.askText({
      title: T("notes.bookmark.title"),
      placeholder: "https://example.com",
      value: block.props.url || "",
      hint: T("notes.bookmark.hint"),
    });
    if (url === null) return;
    if (!/^https?:\/\//i.test(url)) {
      N.toast(T("notes.bookmark.invalid"), true);
      return;
    }
    const caption = await N.askText({
      title: T("notes.bookmark.nameTitle"),
      placeholder: T("notes.bookmark.namePlaceholder"),
      value: block.props.caption || url,
    });
    if (caption === null) return;
    N.pushHistory();
    block.props.url = url;
    block.props.caption = caption || url;
    N.renderEditor();
    N.scheduleSave();
  };
})();
