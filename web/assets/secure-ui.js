/* DevOS 加密导出 / 导入的通用界面组件：展示导出密钥、提示输入导入密钥。 */
(function () {
  if (window.DevOSSecure) return;

  function esc(value) {
    return String(value ?? "").replace(/[&<>"']/g, (ch) => ({
      "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;",
    }[ch]));
  }

  // 优先使用 DevOS 词典；应用页面未加载 i18n 时回退到中文文案。
  function t(key, fallback) {
    if (window.DevOSI18n) {
      const text = window.DevOSI18n.t(key);
      if (text !== key) return text;
    }
    return fallback;
  }

  let overlay = null;
  let activeCleanup = null;

  function close() {
    if (!overlay) return;
    overlay.classList.add("hidden");
    overlay.innerHTML = "";
    if (activeCleanup) {
      const fn = activeCleanup;
      activeCleanup = null;
      fn();
    }
  }

  function mount(html, onReady) {
    if (!overlay) {
      overlay = document.createElement("div");
      overlay.className =
        "fixed inset-0 z-[80] hidden items-center justify-center bg-slate-900/35 px-4 backdrop-blur-sm";
      document.body.appendChild(overlay);
    }
    overlay.innerHTML = html;
    overlay.classList.remove("hidden");
    overlay.querySelectorAll("[data-dismiss]").forEach((node) => {
      node.addEventListener("click", close);
    });
    onReady(overlay);
  }

  function download(filename, content) {
    const blob = new Blob([content], { type: "application/json;charset=utf-8" });
    const url = URL.createObjectURL(blob);
    const link = document.createElement("a");
    link.href = url;
    link.download = filename || "devos-export.json";
    document.body.appendChild(link);
    link.click();
    link.remove();
    setTimeout(() => URL.revokeObjectURL(url), 1500);
  }

  /* 展示一次性导出密钥，并提供下载按钮。 */
  function showExport(data) {
    const key = String(data?.key || "");
    const filename = String(data?.filename || "devos-export.json");
    const content = String(data?.content || "");
    return new Promise((resolve) => {
      mount(
        `
        <div class="w-full max-w-[460px] overflow-hidden rounded-2xl border border-slate-200 bg-white shadow-2xl">
          <div class="flex items-start gap-3 border-b border-slate-100 px-5 py-4">
            <span class="mt-0.5 grid h-9 w-9 flex-none place-items-center rounded-xl bg-emerald-50 text-emerald-600">
              <svg class="h-4 w-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><rect x="4" y="10" width="16" height="10" rx="2"/><path d="M8 10V7a4 4 0 0 1 8 0v3"/></svg>
            </span>
            <div class="min-w-0">
              <div class="text-[13.5px] font-semibold text-slate-900">${esc(t("secure.export.title", "导出密钥"))}</div>
              <div class="mt-1 text-[11px] leading-relaxed text-slate-500">${esc(t("secure.export.desc", "已使用新的随机密钥加密导出内容。该密钥仅展示这一次，导入时必须填写。"))}</div>
            </div>
            <button data-dismiss class="ml-auto grid h-6 w-6 place-items-center rounded-md text-slate-400 hover:bg-slate-100 hover:text-slate-600" title="${esc(t("secure.close", "关闭"))}">
              <svg class="h-3 w-3" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M6 6l12 12M18 6L6 18"/></svg>
            </button>
          </div>
          <div class="px-5 py-4">
            <div class="flex items-center gap-2 rounded-xl border border-slate-200 bg-slate-50 px-3 py-2.5">
              <code class="export-key min-w-0 flex-1 select-all break-all font-mono text-[12.5px] tracking-wide text-slate-800">${esc(key)}</code>
              <button class="copy-key flex-none rounded-lg border border-slate-200 bg-white px-2 py-1 text-[10.5px] text-slate-600 hover:bg-slate-50">${esc(t("secure.export.copy", "复制"))}</button>
            </div>
            <div class="mt-3 rounded-xl bg-amber-50 px-3 py-2 text-[10.5px] leading-relaxed text-amber-700">
              ${esc(t("secure.export.warning", "导出文件包含已加密的凭据。请将密钥与文件分开保存；密钥遗失后无法恢复。"))}
            </div>
          </div>
          <div class="flex items-center justify-end gap-2 border-t border-slate-100 bg-slate-50/60 px-5 py-3">
            <button data-dismiss class="rounded-lg border border-slate-200 bg-white px-3 py-1.5 text-[11.5px] text-slate-600 hover:bg-slate-50">${esc(t("common.done", "完成"))}</button>
            <button class="download-export rounded-lg bg-emerald-500 px-3 py-1.5 text-[11.5px] font-medium text-white hover:bg-emerald-400">${esc(t("secure.export.download", "下载文件"))}</button>
          </div>
        </div>`,
        (root) => {
          const copyBtn = root.querySelector(".copy-key");
          copyBtn.addEventListener("click", () => {
            navigator.clipboard.writeText(key)
              .then(() => { copyBtn.textContent = t("secure.export.copied", "已复制"); })
              .catch(() => {});
          });
          root.querySelector(".download-export").addEventListener("click", () => download(filename, content));
        }
      );
      activeCleanup = () => resolve();
    });
  }

  /* 为加密导出文件请求导入密钥，返回 Promise<string|null>。 */
  function askKey(options) {
    const title = esc(options?.title || t("secure.import.title", "导入加密文件"));
    return new Promise((resolve) => {
      let settled = false;
      const finish = (value) => {
        if (settled) return;
        settled = true;
        resolve(value);
      };
      mount(
        `
        <div class="w-full max-w-[420px] overflow-hidden rounded-2xl border border-slate-200 bg-white shadow-2xl">
          <div class="flex items-start gap-3 border-b border-slate-100 px-5 py-4">
            <span class="mt-0.5 grid h-9 w-9 flex-none place-items-center rounded-xl bg-sky-50 text-sky-600">
              <svg class="h-4 w-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><circle cx="7.5" cy="15.5" r="4.5"/><path d="m11 12 8-8M17 6l2 2M19 4l2 2"/></svg>
            </span>
            <div class="min-w-0">
              <div class="text-[13.5px] font-semibold text-slate-900">${title}</div>
              <div class="mt-1 text-[11px] leading-relaxed text-slate-500">${esc(t("secure.import.desc", "该文件已加密，请输入导出时生成的密钥（形如 XXXX-XXXX-XXXX-XXXX-XXXX-XXXX）。"))}</div>
            </div>
          </div>
          <div class="px-5 py-4">
            <input class="import-key w-full rounded-xl border border-slate-200 bg-white px-3 py-2.5 font-mono text-[12.5px] uppercase tracking-wide text-slate-800 outline-none focus:border-emerald-400 focus:ring-4 focus:ring-emerald-50" placeholder="${esc(t("secure.import.placeholder", "导出时生成的密钥"))}" autocomplete="off" spellcheck="false" />
            <div class="import-error mt-2 hidden text-[10.5px] text-rose-600">${esc(t("secure.import.error", "请输入完整的导出密钥。"))}</div>
          </div>
          <div class="flex items-center justify-end gap-2 border-t border-slate-100 bg-slate-50/60 px-5 py-3">
            <button class="cancel-import rounded-lg border border-slate-200 bg-white px-3 py-1.5 text-[11.5px] text-slate-600 hover:bg-slate-50">${esc(t("common.cancel", "取消"))}</button>
            <button class="confirm-import rounded-lg bg-emerald-500 px-3 py-1.5 text-[11.5px] font-medium text-white hover:bg-emerald-400">${esc(t("secure.import.confirm", "导入"))}</button>
          </div>
        </div>`,
        (root) => {
          const input = root.querySelector(".import-key");
          const error = root.querySelector(".import-error");
          const submit = () => {
            const value = input.value.trim();
            if (value.length < 16) {
              error.classList.remove("hidden");
              input.focus();
              return;
            }
            finish(value);
            close();
          };
          root.querySelector(".confirm-import").addEventListener("click", submit);
          root.querySelector(".cancel-import").addEventListener("click", () => {
            finish(null);
            close();
          });
          input.addEventListener("keydown", (event) => {
            if (event.key === "Enter") {
              event.preventDefault();
              submit();
            }
            if (event.key === "Escape") {
              finish(null);
              close();
            }
          });
          input.addEventListener("input", () => {
            input.value = input.value.toUpperCase().replace(/[^A-Z0-9-]/g, "");
            error.classList.add("hidden");
          });
          setTimeout(() => input.focus(), 30);
        }
      );
      activeCleanup = () => finish(null);
    });
  }

  window.DevOSSecure = { showExport, askKey, download, close, isEncryptedEnvelope: (obj) => !!obj && obj.format === "devos-secure-export" };
})();
