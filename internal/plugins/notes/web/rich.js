/* DevOS 笔记：代码高亮、LaTeX 公式与 Mermaid 流程图。CDN 未就绪时降级显示源码并自动重试。 */
(function () {
  "use strict";
  const N = window.Notes;
  const T = N.t;

  // 代码语言到 highlight.js 语言标识的别名映射。
  const LANG_ALIAS = { text: "plaintext", shell: "bash", "c++": "cpp" };
  let retryTimer = 0;
  let retries = 0;
  let mermaidReady = false;
  let mermaidSeq = 0;
  let equationTimer = 0;

  function katexLib() { return window.katex || null; }
  function hljsLib() { return window.hljs || null; }

  function ensureMermaid() {
    if (mermaidReady) return window.mermaid;
    const lib = window.mermaid;
    if (!lib || typeof lib.render !== "function") return null;
    try {
      lib.initialize({
        startOnLoad: false,
        securityLevel: "strict",
        theme: "default",
        fontFamily: "inherit",
        flowchart: { useMaxWidth: true },
      });
      mermaidReady = true;
      return lib;
    } catch (_) {
      return null;
    }
  }

  function sourcePre(src) {
    const pre = document.createElement("pre");
    pre.className = "rich-source";
    pre.textContent = src;
    return pre;
  }

  function showError(view, err, src) {
    view.textContent = "";
    const msg = err && err.message ? err.message : String(err || "");
    view.appendChild(N.el("div", "rich-error", T("notes.rich.failed") + (msg ? " · " + msg : "")));
    if (src) view.appendChild(sourcePre(src));
  }

  /* ---------------- 代码高亮 ---------------- */
  N.highlightCode = function (codeEl, language, source) {
    if (!codeEl) return;
    const text = source == null ? codeEl.textContent || "" : String(source);
    const lib = hljsLib();
    const lang = LANG_ALIAS[language] || language || "plaintext";
    if (lib && lang !== "plaintext" && typeof lib.getLanguage === "function" && lib.getLanguage(lang)) {
      try {
        codeEl.innerHTML = lib.highlight(text, { language: lang, ignoreIllegals: true }).value;
        return;
      } catch (_) { /* 回退为纯文本 */ }
    }
    codeEl.textContent = text;
  };

  function scrollSync(wrap) {
    if (!wrap) return;
    const input = wrap.querySelector(".code-input");
    const view = wrap.querySelector(".code-view");
    if (!input || !view) return;
    view.scrollTop = input.scrollTop;
    view.scrollLeft = input.scrollLeft;
  }

  N.autosizeCode = function (input) {
    if (!input) return;
    input.style.height = "auto";
    input.style.height = Math.max(Math.min(input.scrollHeight, 480), 34) + "px";
  };

  N.refreshCode = function (blockId) {
    const wrapper = N.blockEl(blockId);
    const block = N.findBlock(blockId);
    if (!wrapper || !block) return;
    const input = wrapper.querySelector(".code-input");
    const code = wrapper.querySelector(".code-view code");
    if (!input || !code) return;
    N.autosizeCode(input);
    N.highlightCode(code, block.props && block.props.language, input.value);
    scrollSync(wrapper.querySelector(".code-wrap"));
  };

  /* ---------------- 公式 / 流程图 ---------------- */
  function renderEquation(view, block) {
    if (!view) return;
    view.textContent = "";
    const src = String(block.content || "").trim();
    if (!src) {
      view.appendChild(N.el("div", "rich-empty", T("notes.equation.empty")));
      return;
    }
    const lib = katexLib();
    if (lib && typeof lib.render === "function") {
      try {
        lib.render(src, view, { displayMode: true, throwOnError: false, strict: false });
        return;
      } catch (err) {
        showError(view, err, src);
        return;
      }
    }
    view.appendChild(sourcePre(src));
  }

  function renderDiagram(view, block) {
    if (!view) return;
    view.textContent = "";
    const src = String(block.content || "").trim();
    if (!src) {
      view.appendChild(N.el("div", "rich-empty", T("notes.diagram.empty")));
      return;
    }
    const lib = ensureMermaid();
    if (!lib) {
      view.appendChild(sourcePre(src));
      return;
    }
    const id = "notes-mmd-" + (++mermaidSeq);
    const pageID = N.state.page ? N.state.page.id : "";
    try {
      Promise.resolve(lib.render(id, src)).then(function (out) {
        if (!N.state.page || N.state.page.id !== pageID) return;
        if (!view.isConnected) return;
        view.innerHTML = out && out.svg ? out.svg : "";
      }).catch(function (err) {
        if (!view.isConnected) return;
        showError(view, err, src);
      });
    } catch (err) {
      showError(view, err, src);
    }
  }

  function renderView(wrapper, block) {
    if (!wrapper || !block) return;
    if (block.type === "equation") renderEquation(wrapper.querySelector(".equation-view"), block);
    else if (block.type === "diagram") renderDiagram(wrapper.querySelector(".diagram-view"), block);
  }

  function scheduleEquation(view, block) {
    clearTimeout(equationTimer);
    equationTimer = setTimeout(function () { renderEquation(view, block); }, 180);
  }

  function richShell(block, opts) {
    const wrap = N.el("div", "blk-" + block.type);
    wrap.dataset.rich = block.type;

    const view = N.el("div", opts.viewClass);
    view.title = T("notes.rich.editHint");
    view.addEventListener("click", function (e) {
      e.stopPropagation();
      N.startRichEdit(block.id);
    });

    const edit = N.el("div", "rich-edit hidden");
    const input = document.createElement("textarea");
    input.className = opts.inputClass;
    input.spellcheck = false;
    input.value = block.content || "";
    input.placeholder = T(opts.placeholderKey);
    edit.appendChild(input);

    const hint = N.el("div", "rich-hint");
    hint.appendChild(N.el("span", "", T(opts.hintKey)));
    const done = N.el("button", "pill", T("notes.rich.done"));
    done.type = "button";
    done.addEventListener("click", function (e) {
      e.stopPropagation();
      N.finishRichEdit(block.id);
    });
    hint.appendChild(done);
    edit.appendChild(hint);

    const actions = N.el("div", "rich-actions");
    const editBtn = N.el("button", "pill", T("notes.rich.edit"));
    editBtn.type = "button";
    editBtn.appendChild(N.svg(opts.icon));
    editBtn.addEventListener("click", function (e) {
      e.stopPropagation();
      N.startRichEdit(block.id);
    });
    actions.appendChild(editBtn);

    input.addEventListener("input", function () {
      const current = N.findBlock(block.id);
      if (!current) return;
      current.content = input.value;
      N.state.dirty = true;
      N.scheduleSave();
      if (block.type === "equation") scheduleEquation(view, current);
    });
    input.addEventListener("keydown", function (e) {
      if (e.key === "Escape" || (e.key === "Enter" && (e.ctrlKey || e.metaKey))) {
        e.preventDefault();
        N.finishRichEdit(block.id);
      }
    });
    input.addEventListener("blur", function () {
      const wrapper = N.blockEl(block.id);
      if (wrapper && wrapper.classList.contains("is-editing")) N.finishRichEdit(block.id);
    });

    wrap.appendChild(view);
    wrap.appendChild(edit);
    wrap.appendChild(actions);
    return wrap;
  }

  N.buildEquation = function (block) {
    return richShell(block, {
      viewClass: "equation-view",
      inputClass: "equation-input",
      placeholderKey: "notes.editor.placeholder.equation",
      hintKey: "notes.equation.hint",
      icon: "equation",
    });
  };

  N.buildDiagram = function (block) {
    return richShell(block, {
      viewClass: "diagram-view",
      inputClass: "diagram-input",
      placeholderKey: "notes.editor.placeholder.diagram",
      hintKey: "notes.diagram.hint",
      icon: "diagram",
    });
  };

  N.startRichEdit = function (id) {
    const wrapper = N.blockEl(id);
    const block = N.findBlock(id);
    if (!wrapper || !block) return;
    const edit = wrapper.querySelector(".rich-edit");
    const input = edit && edit.querySelector("textarea");
    if (!edit || !input) return;
    if (!wrapper.classList.contains("is-editing")) N.pushHistory();
    edit.classList.remove("hidden");
    wrapper.classList.add("is-editing");
    input.focus();
    const end = input.value.length;
    try { input.setSelectionRange(end, end); } catch (_) { /* 忽略 */ }
  };

  N.finishRichEdit = function (id) {
    const wrapper = N.blockEl(id);
    const block = N.findBlock(id);
    if (!wrapper) return;
    if (!wrapper.classList.contains("is-editing")) return;
    const edit = wrapper.querySelector(".rich-edit");
    const input = edit && edit.querySelector("textarea");
    if (input && block) {
      block.content = input.value;
      N.state.dirty = true;
      N.scheduleSave();
    }
    if (edit) edit.classList.add("hidden");
    wrapper.classList.remove("is-editing");
    renderView(wrapper, block);
  };

  /* ---------------- 统一渲染入口 ---------------- */
  function libsMissing() {
    return !window.katex || !window.mermaid || !window.hljs;
  }

  function scheduleRetry() {
    if (retryTimer) return;
    if (retries > 60) return;
    retryTimer = setTimeout(function () {
      retryTimer = 0;
      retries++;
      if (N.state.page) N.renderRichAll();
    }, 500);
  }

  N.renderRichAll = function () {
    const editor = N.$("#editor");
    if (!editor) return;
    editor.querySelectorAll('.block[data-type="equation"]').forEach(function (wrapper) {
      const block = N.findBlock(wrapper.dataset.id);
      if (block) renderEquation(wrapper.querySelector(".equation-view"), block);
    });
    editor.querySelectorAll('.block[data-type="diagram"]').forEach(function (wrapper) {
      if (wrapper.classList.contains("is-editing")) return;
      const block = N.findBlock(wrapper.dataset.id);
      if (block) renderDiagram(wrapper.querySelector(".diagram-view"), block);
    });
    editor.querySelectorAll('.block[data-type="code"]').forEach(function (wrapper) {
      const block = N.findBlock(wrapper.dataset.id);
      const code = wrapper.querySelector(".code-view code");
      const input = wrapper.querySelector(".code-input");
      if (!block || !code || !input) return;
      N.autosizeCode(input);
      N.highlightCode(code, block.props && block.props.language, input.value);
      scrollSync(wrapper.querySelector(".code-wrap"));
    });
    if (libsMissing()) scheduleRetry(); else retries = 0;
  };
})();
