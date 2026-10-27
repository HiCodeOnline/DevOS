/* 编辑器交互：输入、快捷键、斜杠菜单、行内工具栏、拖拽 */
(function () {
  "use strict";
  const N = window.Notes;
  const T = N.t;
  let bound = false;
  let toolbar = null;
  let savedRange = null;

  function caretInfo() {
    const sel = window.getSelection();
    if (!sel || !sel.rangeCount || !sel.isCollapsed) return null;
    const range = sel.getRangeAt(0);
    const anchor = range.startContainer.nodeType === 1 ? range.startContainer : range.startContainer.parentElement;
    const el = anchor && anchor.closest && anchor.closest(".block-input, .code-input, .cell-input");
    if (!el) return null;
    const editor = N.$("#editor");
    if (!editor || !editor.contains(el)) return null;
    const pre = range.cloneRange();
    pre.selectNodeContents(el);
    pre.setEnd(range.startContainer, range.startOffset);
    const atStart = pre.toString().length === 0;
    const post = document.createRange();
    post.selectNodeContents(el);
    post.setStart(range.endContainer, range.endOffset);
    const atEnd = post.toString().length === 0;
    return { el: el, range: range, atStart: atStart, atEnd: atEnd, text: el.textContent || "" };
  }

  function activeBlock(el) {
    const wrapper = el.closest(".block");
    if (!wrapper) return null;
    return N.findBlock(wrapper.dataset.id);
  }

  function syncFromEl(el, block, pushDebounced) {
    if (!block) return;
    if (el.classList.contains("block-input")) block.content = el.innerHTML;
    else if (el.classList.contains("code-input")) block.content = el.value != null ? el.value : el.textContent;
    N.state.dirty = true;
    N.scheduleSave();
    if (pushDebounced) {
      clearTimeout(N.state.typingTimer);
      N.state.typingTimer = setTimeout(function () { N.pushHistory(); }, 1000);
    }
  }

  function splitAtCaret(el) {
    const sel = window.getSelection();
    if (!sel || !sel.rangeCount) return { left: el.innerHTML, right: "" };
    const range = sel.getRangeAt(0);
    const before = document.createRange();
    before.selectNodeContents(el);
    before.setEnd(range.startContainer, range.startOffset);
    const after = document.createRange();
    after.selectNodeContents(el);
    after.setStart(range.endContainer, range.endOffset);
    const left = document.createElement("div");
    left.appendChild(before.cloneContents());
    const right = document.createElement("div");
    right.appendChild(after.cloneContents());
    function clean(html) {
      return html === "<br>" ? "" : html;
    }
    return { left: clean(left.innerHTML), right: clean(right.innerHTML) };
  }

  /* ---------------- 斜杠菜单 ---------------- */
  N.slash = { open: false, blockId: "", query: "", index: 0, items: [] };

  function closeSlash() {
    if (!N.slash.open) return;
    N.slash.open = false;
    N.slash.items = [];
    N.hidePopover();
  }

  function slashCandidates(query) {
    const q = (query || "").trim().toLowerCase();
    return N.SLASH_ITEMS.filter(function (item) {
      if (!q) return true;
      const label = T(item.labelKey).toLowerCase();
      return label.indexOf(q) >= 0 || item.keywords.toLowerCase().indexOf(q) >= 0 || item.type.indexOf(q) >= 0;
    });
  }

  function renderSlash() {
    const items = N.slash.items;
    const menu = N.el("div", "slash-menu");
    if (items.length === 0) {
      menu.appendChild(N.el("div", "menu-label", T("notes.slash.empty")));
    } else {
      items.forEach(function (item, index) {
        const btn = N.el("button", "menu-item" + (index === N.slash.index ? " is-highlight" : ""));
        btn.type = "button";
        const icon = N.el("span", "mi-icon");
        icon.appendChild(N.svg(item.icon));
        btn.appendChild(icon);
        btn.appendChild(N.el("span", "mi-label", T(item.labelKey)));
        btn.dataset.index = index;
        btn.addEventListener("mousedown", function (e) { e.preventDefault(); });
        btn.addEventListener("click", function () {
          N.slash.index = index;
          applySlash();
        });
        menu.appendChild(btn);
      });
    }
    menu.appendChild(N.el("div", "menu-sep"));
    menu.appendChild(N.el("div", "menu-label", T("notes.slash.hint")));
    N.showPopover(menu, N.caretRect());
  }

  function openSlash(blockId, query) {
    N.slash.open = true;
    N.slash.blockId = blockId;
    N.slash.query = query;
    N.slash.index = 0;
    N.slash.items = slashCandidates(query);
    renderSlash();
  }

  function updateSlash(query) {
    const items = slashCandidates(query);
    N.slash.query = query;
    N.slash.items = items;
    if (N.slash.index >= items.length) N.slash.index = Math.max(0, items.length - 1);
    renderSlash();
  }

  function applySlash() {
    const block = N.findBlock(N.slash.blockId);
    if (!block || !N.slash.items.length) { closeSlash(); return; }
    const type = N.slash.items[N.slash.index].type;
    closeSlash();
    N.pushHistory();
    block.content = "";
    block.type = type;
    if (type === "table") {
      block.props.rows = N.defaultRows();
      block.props.hasHeader = false;
    }
    if (type === "callout") {
      block.props.icon = "💡";
      block.props.color = "default";
    }
    if (type === "code") block.props.language = block.props.language || "text";
    if (type === "todo") block.props.checked = false;
    if (type === "bookmark") {
      block.props.url = "";
      block.props.caption = "";
    }
    if (type === "image") {
      block.props.url = "";
      block.props.caption = "";
    }
    if (type === "divider") block.content = "";
    if (type === "equation" || type === "diagram") block.content = "";
    N.renderEditor(type === "divider" ? null : { id: block.id, where: "end" });
    N.scheduleSave();
    if (type === "divider") {
      N.addBlockAfter(block.id, "paragraph", "", true);
    } else if (type === "bookmark") {
      N.editBookmark(block.id);
    } else if ((type === "equation" || type === "diagram") && N.startRichEdit) {
      N.startRichEdit(block.id);
    }
  }

  /* ---------------- Markdown 快捷输入 ---------------- */
  const SHORTCUTS = [
    { re: /^(#{3})\s+/, type: "heading3" },
    { re: /^(#{2})\s+/, type: "heading2" },
    { re: /^(#)\s+/, type: "heading1" },
    { re: /^[-*+]\s+/, type: "bulleted" },
    { re: /^\d+[.)]\s+/, type: "numbered" },
    { re: /^\[([ xX]?)\]\s+/, type: "todo", checked: true },
    { re: /^>\s+/, type: "quote" },
    { re: /^```$/, type: "code" },
    { re: /^---+$/, type: "divider" },
  ];

  function tryShortcut(block, el) {
    if (block.type !== "paragraph") return false;
    const text = el.textContent || "";
    const sel = window.getSelection();
    if (!sel || !sel.rangeCount || !sel.isCollapsed) return false;
    for (let i = 0; i < SHORTCUTS.length; i++) {
      const item = SHORTCUTS[i];
      const match = text.match(item.re);
      if (!match) continue;
      if (item.re.source.indexOf("$") >= 0) continue;
      const rest = text.slice(match[0].length);
      N.pushHistory();
      block.content = rest;
      block.type = item.type;
      if (item.type === "todo") {
        block.props.checked = item.checked && match[1] && match[1].toLowerCase() === "x";
      }
      if (item.type === "code") block.props.language = block.props.language || "text";
      N.renderEditor(item.type === "divider" ? null : { id: block.id, where: "end" });
      N.scheduleSave();
      if (item.type === "divider") N.addBlockAfter(block.id, "paragraph", "", true);
      return true;
    }
    return false;
  }

  /* ---------------- 键盘 ---------------- */
  function handleEnter(e, info, block, el) {
    e.preventDefault();
    if (block.type === "divider" || block.type === "image" || block.type === "bookmark" || block.type === "table") {
      N.addBlockAfter(block.id, "paragraph", "", true);
      return;
    }
    const text = el.textContent || "";
    const isList = block.type === "bulleted" || block.type === "numbered" || block.type === "todo";
    if (info.atEnd && text === "" && isList) {
      N.pushHistory();
      block.type = "paragraph";
      block.content = "";
      N.renderEditor({ id: block.id, where: "start" });
      N.scheduleSave();
      return;
    }
    const split = splitAtCaret(el);
    N.pushHistory();
    let nextType = block.type;
    if (block.type === "heading1" || block.type === "heading2" || block.type === "heading3" || block.type === "quote" || block.type === "callout") nextType = "paragraph";
    if (block.type === "toggle") nextType = "toggle";
    const next = N.newBlock(nextType, split.right, block.type === "callout" ? { icon: "💡", color: "default" } : {});
    block.content = split.left;
    const blocks = N.state.page.blocks;
    blocks.splice(N.blockIndex(block.id) + 1, 0, next);
    N.renderEditor({ id: next.id, where: "start" });
    N.scheduleSave();
  }

  function handleBackspace(e, info, block) {
    const blocks = N.state.page.blocks;
    const index = N.blockIndex(block.id);
    const isEmpty = (info.el.textContent || "") === "";
    if (!info.atStart) return;
    e.preventDefault();
    if (block.type !== "paragraph" && block.type !== "divider") {
      N.pushHistory();
      block.type = "paragraph";
      if (block.props) {
        block.props.checked = false;
        block.props.collapsed = false;
      }
      N.renderEditor({ id: block.id, where: "start" });
      N.scheduleSave();
      return;
    }
    if (index <= 0) {
      if (block.type !== "paragraph") return;
      if (!isEmpty) return;
      return;
    }
    const prev = blocks[index - 1];
    if (!prev || !N.mergeable(prev.type)) {
      if (isEmpty && block.type === "paragraph") {
        N.removeBlock(block.id, true);
      }
      return;
    }
    N.pushHistory();
    const prevLen = N.plainText(prev.content || "").length;
    prev.content = (prev.content || "") + (block.content || "");
    blocks.splice(index, 1);
    N.renderEditor({ id: prev.id, where: "end", offset: prevLen });
    N.scheduleSave();
  }

  function handleDeleteForward(info, block) {
    const blocks = N.state.page.blocks;
    const index = N.blockIndex(block.id);
    const next = blocks[index + 1];
    if (!next || !N.mergeable(next.type)) return false;
    N.pushHistory();
    const len = N.plainText(block.content || "").length;
    block.content = (block.content || "") + (next.content || "");
    blocks.splice(index + 1, 1);
    N.renderEditor({ id: block.id, where: "end", offset: len });
    N.scheduleSave();
    return true;
  }

  function execFormat(command, value) {
    const sel = window.getSelection();
    if (!sel || !sel.rangeCount) return;
    const anchor = sel.anchorNode && (sel.anchorNode.nodeType === 1 ? sel.anchorNode : sel.anchorNode.parentElement);
    const input = anchor && anchor.closest && anchor.closest(".block-input");
    if (!input) return;
    const block = activeBlock(input);
    document.execCommand(command, false, value || null);
    syncFromEl(input, block, false);
  }

  function normalizeURL(value) {
    const url = String(value || "").trim();
    if (!url) return "";
    if (/^(https?:|mailto:)/i.test(url)) return url;
    return "https://" + url.replace(/^\/+/, "");
  }

  function onEditorKeydown(e) {
    if (!N.state.page) return;
    const target = e.target;
    const isBlockInput = target.classList && target.classList.contains("block-input");
    const isCode = target.classList && target.classList.contains("code-input");
    const isCell = target.classList && target.classList.contains("cell-input");
    const k = (e.key || "").toLowerCase();
    const mod = e.ctrlKey || e.metaKey;

    if (N.slash.open && isBlockInput) {
      if (e.key === "ArrowDown" || e.key === "ArrowUp") {
        e.preventDefault();
        const count = N.slash.items.length;
        if (count === 0) return;
        N.slash.index = (N.slash.index + (e.key === "ArrowDown" ? 1 : count - 1)) % count;
        const menu = N.$("#popover");
        const rows = menu ? menu.querySelectorAll(".menu-item") : [];
        rows.forEach(function (row, index) {
          row.classList.toggle("is-highlight", Number(row.dataset.index) === N.slash.index);
        });
        return;
      }
      if (e.key === "Enter" || e.key === "Tab") {
        e.preventDefault();
        applySlash();
        return;
      }
      if (e.key === "Escape") {
        e.preventDefault();
        closeSlash();
        return;
      }
    }

    if (mod) {
      if (k === "s") { e.preventDefault(); N.flushSave(); return; }
      if (k === "z") {
        e.preventDefault();
        if (e.shiftKey) N.redo(); else N.undo();
        return;
      }
      if (k === "y") { e.preventDefault(); N.redo(); return; }
      if (!isCell) {
        if (k === "b") { e.preventDefault(); execFormat("bold"); return; }
        if (k === "i") { e.preventDefault(); execFormat("italic"); return; }
        if (k === "u") { e.preventDefault(); execFormat("underline"); return; }
        if (e.shiftKey && k === "x") { e.preventDefault(); execFormat("strikeThrough"); return; }
        if (e.shiftKey && k === "e") {
          e.preventDefault();
          const sel = window.getSelection();
          if (sel && !sel.isCollapsed) {
            execFormat("insertHTML", "<code>" + N.escapeHTML(sel.toString()) + "</code>");
          }
          return;
        }
        if (e.altKey && (k === "1" || k === "2" || k === "3")) {
          e.preventDefault();
          const block = activeBlock(target);
          if (block) N.turnBlock(block.id, "heading" + k);
          return;
        }
        if (e.shiftKey && k === "7") { e.preventDefault(); const b = activeBlock(target); if (b) N.turnBlock(b.id, "numbered"); return; }
        if (e.shiftKey && k === "8") { e.preventDefault(); const b = activeBlock(target); if (b) N.turnBlock(b.id, "bulleted"); return; }
        if (e.shiftKey && k === "9") { e.preventDefault(); const b = activeBlock(target); if (b) N.turnBlock(b.id, "todo"); return; }
      }
    }

    if (isCode) {
      if (e.key === "Tab") {
        e.preventDefault();
        const start = target.selectionStart == null ? 0 : target.selectionStart;
        const end = target.selectionEnd == null ? start : target.selectionEnd;
        target.setRangeText("  ", start, end, "end");
        target.dispatchEvent(new Event("input", { bubbles: true }));
        return;
      }
      if (e.key === "Backspace" && (target.value || "") === "") {
        const block = activeBlock(target);
        if (block) {
          e.preventDefault();
          N.pushHistory();
          block.type = "paragraph";
          block.content = "";
          N.renderEditor({ id: block.id, where: "start" });
          N.scheduleSave();
        }
        return;
      }
      if (!e.shiftKey && e.key === "ArrowUp" && target.selectionStart === 0) {
        const block = activeBlock(target);
        const blocks = N.state.page ? N.state.page.blocks : [];
        const prev = block ? blocks[N.blockIndex(block.id) - 1] : null;
        if (prev) {
          e.preventDefault();
          N.focusBlock(prev.id, "end");
        }
        return;
      }
      if (!e.shiftKey && e.key === "ArrowDown" && target.selectionStart === (target.value || "").length) {
        const block = activeBlock(target);
        const blocks = N.state.page ? N.state.page.blocks : [];
        const next = block ? blocks[N.blockIndex(block.id) + 1] : null;
        if (next) {
          e.preventDefault();
          N.focusBlock(next.id, "start");
        }
      }
      return;
    }
    if (isCell) return;

    if (!isBlockInput) return;
    const block = activeBlock(target);
    if (!block) return;
    const info = caretInfo();

    if (e.key === "Enter" && !e.shiftKey && !mod && !e.altKey) {
      if (!info) return;
      handleEnter(e, info, block, target);
      return;
    }
    if (e.key === "Enter" && e.shiftKey) {
      e.preventDefault();
      document.execCommand("insertLineBreak");
      syncFromEl(target, block, false);
      return;
    }
    if (e.key === "Backspace") {
      if (!info) return;
      handleBackspace(e, info, block);
      return;
    }
    if (e.key === "Delete") {
      if (!info || !info.atEnd) return;
      if (handleDeleteForward(info, block)) e.preventDefault();
      return;
    }
    if (e.key === "ArrowUp" && info && info.atStart) {
      const blocks = N.state.page.blocks;
      const index = N.blockIndex(block.id);
      const prev = blocks[index - 1];
      if (prev) {
        e.preventDefault();
        N.focusBlock(prev.id, "end");
      }
      return;
    }
    if (e.key === "ArrowDown" && info && info.atEnd) {
      const blocks = N.state.page.blocks;
      const index = N.blockIndex(block.id);
      const next = blocks[index + 1];
      if (next) {
        e.preventDefault();
        N.focusBlock(next.id, "start");
      }
      return;
    }
    if (e.key === "Tab") {
      e.preventDefault();
      return;
    }
    if (e.key === "Enter" && mod && block.type === "todo") {
      e.preventDefault();
      const wrapper = N.blockEl(block.id);
      const box = wrapper && wrapper.querySelector('input[type="checkbox"]');
      if (box) {
        box.checked = !box.checked;
        box.dispatchEvent(new Event("change"));
      }
    }
  }

  /* ---------------- 输入 ---------------- */
  function onEditorInput(e) {
    const target = e.target;
    if (!target.classList) return;
    if (target.classList.contains("image-caption")) {
      const block = activeBlock(target);
      if (block) {
        block.props.caption = target.value;
        N.scheduleSave();
      }
      return;
    }
    if (target.classList.contains("cell-input")) {
      const block = activeBlock(target);
      if (!block || !block.props.rows) return;
      const row = Number(target.dataset.row);
      const col = Number(target.dataset.col);
      if (block.props.rows[row] && typeof block.props.rows[row][col] === "string") {
        block.props.rows[row][col] = target.textContent || "";
        N.state.dirty = true;
        N.scheduleSave();
      }
      return;
    }
    if (target.classList.contains("code-input")) {
      const codeBlock = activeBlock(target);
      if (!codeBlock) return;
      syncFromEl(target, codeBlock, true);
      if (N.refreshCode) N.refreshCode(codeBlock.id);
      return;
    }
    const block = activeBlock(target);
    if (!block) return;
    syncFromEl(target, block, true);

    if (target.classList.contains("block-input")) {
      const text = target.textContent || "";
      if (text.charAt(0) === "/" && (block.type === "paragraph" || block.type === "heading1" || block.type === "heading2" || block.type === "heading3" || block.type === "bulleted" || block.type === "numbered")) {
        const sel = window.getSelection();
        if (sel && sel.rangeCount && sel.getRangeAt(0).collapsed) {
          if (!N.slash.open) openSlash(block.id, text.slice(1));
          else updateSlash(text.slice(1));
        }
      } else if (N.slash.open && N.slash.blockId === block.id) {
        closeSlash();
      }
      if (!N.slash.open) tryShortcut(block, target);
    }
  }

  function onEditorPaste(e) {
    const target = e.target;
    if (!target.classList) return;
    if (target.classList.contains("cell-input")) {
      const text = (e.clipboardData || window.clipboardData).getData("text/plain");
      if (text == null) return;
      e.preventDefault();
      document.execCommand("insertText", false, text.replace(/\r?\n/g, " "));
      return;
    }
    if (target.classList.contains("code-input")) return;
    if (!target.classList.contains("block-input")) return;
    const block = activeBlock(target);
    if (!block) return;
    const text = (e.clipboardData || window.clipboardData).getData("text/plain");
    if (text == null || text === "") return;
    e.preventDefault();
    const normalized = text.replace(/\r\n?/g, "\n");
    if (normalized.indexOf("\n") < 0) {
      document.execCommand("insertText", false, normalized);
      syncFromEl(target, block, false);
      return;
    }
    const lines = normalized.split("\n");
    while (lines.length > 1 && lines[lines.length - 1].trim() === "") lines.pop();
    N.pushHistory();
    document.execCommand("insertText", false, lines[0]);
    syncFromEl(target, block, false);
    const keepType = block.type === "bulleted" || block.type === "numbered" || block.type === "todo" ? block.type : "paragraph";
    let anchorId = block.id;
    for (let i = 1; i < lines.length; i++) {
      const created = N.newBlock(keepType, N.escapeHTML(lines[i]));
      const blocks = N.state.page.blocks;
      blocks.splice(N.blockIndex(anchorId) + 1, 0, created);
      anchorId = created.id;
    }
    const last = N.state.page.blocks[N.blockIndex(anchorId)];
    N.renderEditor({ id: last.id, where: "end" });
    N.scheduleSave();
  }

  /* ---------------- 行内工具栏 ---------------- */
  function ensureToolbar() {
    if (toolbar) return toolbar;
    toolbar = N.el("div", "inline-toolbar hidden");
    const buttons = [
      { cmd: "bold", label: "B", cls: "tb-bold" },
      { cmd: "italic", label: "I", cls: "tb-italic" },
      { cmd: "underline", label: "U", cls: "tb-underline" },
      { cmd: "strikeThrough", label: "S", cls: "tb-strike" },
      { sep: true },
      { cmd: "code", label: "</>" },
      { cmd: "link", label: "link" },
      { sep: true },
      { cmd: "clear", label: "clear" },
    ];
    buttons.forEach(function (item) {
      if (item.sep) {
        toolbar.appendChild(N.el("span", "tb-sep"));
        return;
      }
      const btn = N.el("button", item.cls || "");
      btn.type = "button";
      if (item.cmd === "bold") btn.innerHTML = "<b>B</b>";
      else if (item.cmd === "italic") btn.innerHTML = "<i>I</i>";
      else if (item.cmd === "underline") btn.innerHTML = "<u>U</u>";
      else if (item.cmd === "strikeThrough") btn.innerHTML = "<s>S</s>";
      else if (item.cmd === "code") btn.textContent = "</>";
      else if (item.cmd === "link") btn.appendChild(N.svg("link"));
      else if (item.cmd === "clear") btn.textContent = "Tx";
      btn.title = item.cmd;
      btn.addEventListener("mousedown", function (e) {
        e.preventDefault();
        if (item.cmd === "link") { openLinkDialog(); return; }
        if (item.cmd === "code") {
          const sel = window.getSelection();
          if (sel && sel.rangeCount && !sel.isCollapsed) {
            execFormat("insertHTML", "<code>" + N.escapeHTML(sel.toString()) + "</code>");
          }
          updateToolbar();
          return;
        }
        execFormat(item.cmd === "clear" ? "removeFormat" : item.cmd);
        updateToolbar();
      });
      toolbar.appendChild(btn);
    });
    document.body.appendChild(toolbar);
    return toolbar;
  }

  async function openLinkDialog() {
    const sel = window.getSelection();
    if (!sel || !sel.rangeCount) return;
    const range = sel.getRangeAt(0).cloneRange();
    const anchor = sel.anchorNode && (sel.anchorNode.nodeType === 1 ? sel.anchorNode : sel.anchorNode.parentElement);
    const input = anchor && anchor.closest && anchor.closest(".block-input");
    if (!input) return;
    const block = activeBlock(input);
    const text = sel.toString();
    const url = await N.askText({
      title: T("notes.link.title"),
      placeholder: "https://example.com",
      hint: T("notes.link.hint"),
    });
    if (url === null || !url) return;
    const safe = normalizeURL(url);
    input.focus();
    sel.removeAllRanges();
    sel.addRange(range);
    if (text) {
      document.execCommand("createLink", false, safe);
    } else {
      document.execCommand("insertHTML", false, '<a href="' + N.escapeHTML(safe) + '">' + N.escapeHTML(safe) + "</a>");
    }
    syncFromEl(input, block, false);
    N.pushHistory();
    updateToolbar();
  }

  function updateToolbar() {
    const bar = ensureToolbar();
    const sel = window.getSelection();
    const editor = N.$("#editor");
    if (!sel || !sel.rangeCount || sel.isCollapsed || !editor) {
      bar.classList.add("hidden");
      return;
    }
    const anchor = sel.anchorNode && (sel.anchorNode.nodeType === 1 ? sel.anchorNode : sel.anchorNode.parentElement);
    if (!anchor || !editor.contains(anchor)) {
      bar.classList.add("hidden");
      return;
    }
    const range = sel.getRangeAt(0);
    const rect = range.getBoundingClientRect();
    if (!rect || (!rect.width && !rect.height)) {
      bar.classList.add("hidden");
      return;
    }
    bar.classList.remove("hidden");
    const width = bar.offsetWidth;
    const height = bar.offsetHeight;
    let left = rect.left + rect.width / 2 - width / 2;
    left = Math.max(8, Math.min(left, window.innerWidth - width - 8));
    let top = rect.top - height - 8;
    if (top < 8) top = rect.bottom + 8;
    bar.style.left = left + "px";
    bar.style.top = top + "px";
    savedRange = range.cloneRange();
  }

  /* ---------------- 拖拽 ---------------- */
  function clearDropMarks() {
    const editor = N.$("#editor");
    if (!editor) return;
    editor.querySelectorAll(".block.drop-before, .block.drop-after").forEach(function (node) {
      node.classList.remove("drop-before", "drop-after");
    });
  }

  function bindDrag() {
    const editor = N.$("#editor");
    if (!editor) return;
    editor.addEventListener("dragstart", function (e) {
      const handle = e.target.closest && e.target.closest(".block-handle");
      if (!handle) return;
      N.state.dragBlockId = handle.dataset.dragBlock;
      try {
        e.dataTransfer.setData("text/plain", N.state.dragBlockId);
        e.dataTransfer.effectAllowed = "move";
      } catch (_) { /* 某些浏览器禁止设置数据 */ }
    });
    editor.addEventListener("dragover", function (e) {
      if (!N.state.dragBlockId) return;
      const wrapper = e.target.closest && e.target.closest(".block");
      if (!wrapper || wrapper.dataset.id === N.state.dragBlockId) return;
      e.preventDefault();
      clearDropMarks();
      const rect = wrapper.getBoundingClientRect();
      const after = e.clientY > rect.top + rect.height / 2;
      wrapper.classList.add(after ? "drop-after" : "drop-before");
    });
    editor.addEventListener("drop", function (e) {
      if (!N.state.dragBlockId) return;
      const wrapper = e.target.closest && e.target.closest(".block");
      if (!wrapper) return;
      e.preventDefault();
      const after = wrapper.classList.contains("drop-after");
      const dragId = N.state.dragBlockId;
      clearDropMarks();
      N.state.dragBlockId = "";
      N.moveBlockTo(dragId, wrapper.dataset.id, after);
    });
    editor.addEventListener("dragend", function () {
      N.state.dragBlockId = "";
      clearDropMarks();
    });
  }

  N.bindEditorEvents = function () {
    if (bound) return;
    bound = true;
    const editor = N.$("#editor");
    ensureToolbar();
    editor.addEventListener("keydown", onEditorKeydown);
    editor.addEventListener("input", onEditorInput);
    editor.addEventListener("paste", onEditorPaste);
    editor.addEventListener("focusout", function (e) {
      const target = e.target;
      if (!target.classList || !target.classList.contains("block-input")) return;
      const block = activeBlock(target);
      if (!block) return;
      const safe = N.safeHTML(target.innerHTML);
      if (target.textContent === "") safe === "" ? (target.innerHTML = "") : (target.innerHTML = safe);
      else if (safe !== target.innerHTML) target.innerHTML = safe;
      block.content = target.innerHTML;
      N.scheduleSave();
    });
    editor.addEventListener("click", function (e) {
      if (e.target === editor) {
        const blocks = N.state.page ? N.state.page.blocks : [];
        if (blocks.length) N.addBlockAfter(blocks[blocks.length - 1].id, "paragraph", "", true);
        return;
      }
      const wrapper = e.target.closest && e.target.closest(".block");
      if (!wrapper) return;
      if (e.target.closest(".block-side") || e.target.closest(".block-body")) return;
      const input = wrapper.querySelector(".block-input, .code-input");
      if (input) {
        e.preventDefault();
        N.focusBlock(wrapper.dataset.id, "end");
      }
    });
    document.addEventListener("selectionchange", function () {
      if (!N.state.page || N.state.view !== "page") {
        if (toolbar) toolbar.classList.add("hidden");
        return;
      }
      const sel = window.getSelection();
      if (!sel || !sel.rangeCount || sel.isCollapsed) {
        if (toolbar) toolbar.classList.add("hidden");
        return;
      }
      const node = sel.anchorNode;
      const anchor = node && (node.nodeType === 1 ? node : node.parentElement);
      if (anchor && anchor.closest && anchor.closest(".block-input")) updateToolbar();
      else if (toolbar) toolbar.classList.add("hidden");
    });
    const scroll = N.$("#pageScroll");
    if (scroll) scroll.addEventListener("scroll", function () {
      if (toolbar) toolbar.classList.add("hidden");
      if (N.slash.open) N.hidePopover();
    });
    document.addEventListener("mousedown", function (e) {
      const pop = N.$("#popover");
      if (!pop || pop.classList.contains("hidden")) return;
      if (pop.contains(e.target)) return;
      if (e.target.closest && e.target.closest(".ui-select-trigger, .block-handle, .callout-icon")) return;
      N.hidePopover();
      closeSlash();
    });
    bindDrag();
  };

  N.closeSlash = closeSlash;
  N.updateToolbar = updateToolbar;
  N.savedRange = function () { return savedRange; };
})();
