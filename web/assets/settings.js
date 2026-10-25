/* DevOS 设置面板：壁纸、语言与应用密码。 */
(function () {
  if (window.DevOSSettings) return;

  var PRESETS = [
    { id: "aurora", swatch: "wallpaper-swatch-aurora" },
    { id: "dusk", swatch: "wallpaper-swatch-dusk" },
    { id: "mint", swatch: "wallpaper-swatch-mint" },
    { id: "sand", swatch: "wallpaper-swatch-sand" },
    { id: "midnight", swatch: "wallpaper-swatch-midnight" },
  ];
  var LOCALE_NAMES = { "zh-CN": "简体中文", "en": "English", "zh-TW": "繁體中文" };

  var overlay = null;
  var status = null;
  var tab = "appearance";
  var busy = false;

  function t(key, params) {
    return window.DevOSI18n ? window.DevOSI18n.t(key, params) : key;
  }

  function esc(value) {
    return String(value == null ? "" : value).replace(/[&<>"']/g, function (ch) {
      return { "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[ch];
    });
  }

  function request(url, options) {
    var opts = options || {};
    var headers = { "X-DevOS-Request": "1" };
    if (opts.body && !(opts.body instanceof FormData)) headers["Content-Type"] = "application/json";
    return fetch(url, {
      method: opts.method || "POST",
      headers: headers,
      body: opts.body instanceof FormData ? opts.body : (opts.body ? JSON.stringify(opts.body) : undefined),
    }).then(function (resp) {
      return resp.json().catch(function () { return {}; }).then(function (data) {
        if (!resp.ok) {
          var err = new Error(data.error || "HTTP " + resp.status);
          err.status = resp.status;
          throw err;
        }
        return data;
      });
    });
  }

  function notify(message) {
    if (window.DevOSDesktop && typeof window.DevOSDesktop.notify === "function") {
      window.DevOSDesktop.notify(message);
    }
  }

  function wallpaperURL(wallpaper) {
    if (!wallpaper || wallpaper.kind !== "custom" || !wallpaper.file) return "";
    return "/api/security/wallpaper/file/" + encodeURIComponent(wallpaper.file);
  }

  function ensureOverlay() {
    if (overlay) return overlay;
    overlay = document.createElement("div");
    overlay.className = "settings-overlay hidden";
    overlay.addEventListener("mousedown", function (event) {
      if (event.target === overlay) close();
    });
    document.body.appendChild(overlay);
    return overlay;
  }

  function wallpaperSection() {
    var current = (status && status.wallpaper) || { kind: "preset", preset: "aurora" };
    var customActive = current.kind === "custom";
    var tiles = PRESETS.map(function (preset) {
      var active = !customActive && current.preset === preset.id ? " is-active" : "";
      return '<button type="button" class="wallpaper-item ' + preset.swatch + active + '" data-preset="' + preset.id + '">' +
        '<span class="wallpaper-name">' + esc(t("settings.wallpaper.preset." + preset.id)) + "</span></button>";
    }).join("");
    var preview = customActive
      ? '<img src="' + esc(wallpaperURL(current)) + '" alt="" />'
      : '<svg class="h-4 w-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7"><rect x="3.5" y="5" width="17" height="14" rx="2.5"/><circle cx="9" cy="10" r="1.6"/><path d="m5 17 4.5-4.5 3 3L16 12l3.5 5"/></svg>';
    return '<div class="settings-label" data-i18n="settings.wallpaper.title"></div>' +
      '<p class="settings-desc" data-i18n="settings.wallpaper.desc"></p>' +
      '<div class="wallpaper-grid">' + tiles + "</div>" +
      '<div class="settings-upload">' +
      '<div class="settings-upload-preview" id="wallpaperPreview">' + preview + "</div>" +
      '<div class="min-w-0"><label class="btn-ghost inline-flex cursor-pointer items-center gap-2">' +
      '<input id="wallpaperFile" type="file" accept="image/png,image/jpeg" class="hidden" />' +
      '<span data-i18n="settings.wallpaper.upload"></span></label>' +
      '<p class="settings-desc" data-i18n="settings.wallpaper.hint"></p></div>' +
      "</div>";
  }

  function languageSection() {
    var current = (window.DevOSI18n && window.DevOSI18n.getLocale()) || "zh-CN";
    var items = (window.DevOSI18n ? window.DevOSI18n.supported : ["zh-CN", "en", "zh-TW"]).map(function (code) {
      var active = code === current ? " is-active" : "";
      return '<button type="button" class="lang-item' + active + '" data-locale="' + code + '">' +
        '<span class="lang-item-name">' + esc(LOCALE_NAMES[code] || code) + "</span>" +
        '<span class="lang-item-code">' + code + "</span>" +
        '<span class="lang-item-check"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3.4" stroke-linecap="round" stroke-linejoin="round"><path d="m5 13 4 4 10-10"/></svg></span>' +
        "</button>";
    }).join("");
    return '<div class="settings-label" data-i18n="settings.language.title"></div>' +
      '<p class="settings-desc" data-i18n="settings.language.desc"></p>' +
      '<div class="lang-list">' + items + "</div>";
  }

  function securitySection() {
    var hasPassword = !!(status && status.hasPassword);
    var oldField = hasPassword
      ? '<div class="settings-field"><label data-i18n="settings.security.current"></label>' +
        '<input id="secOld" type="password" class="settings-input" autocomplete="current-password" data-i18n-placeholder="settings.security.oldPlaceholder" /></div>'
      : "";
    return '<div class="settings-label" data-i18n="settings.security.title"></div>' +
      '<p class="settings-desc" data-i18n="settings.security.desc"></p>' +
      '<span class="security-status' + (hasPassword ? " is-on" : "") + '"><span class="dot"></span>' +
      esc(t(hasPassword ? "settings.security.enabled" : "settings.security.disabled")) + "</span>" +
      '<form class="settings-form" id="securityForm">' + oldField +
      '<div class="settings-field"><label data-i18n="settings.security.new"></label>' +
      '<input id="secNew" type="password" class="settings-input" autocomplete="new-password" data-i18n-placeholder="settings.security.newPlaceholder" /></div>' +
      '<div class="settings-field"><label data-i18n="settings.security.confirm"></label>' +
      '<input id="secConfirm" type="password" class="settings-input" autocomplete="new-password" data-i18n-placeholder="settings.security.confirmPlaceholder" /></div>' +
      '<p class="settings-error hidden" id="secError"></p>' +
      '<div class="settings-actions">' +
      (hasPassword ? '<button type="button" id="secRemove" class="btn-ghost btn-danger" data-i18n="settings.security.remove"></button>' : "") +
      '<button type="submit" id="secSubmit" class="btn-primary">' +
      esc(t(hasPassword ? "settings.security.save" : "settings.security.set")) + "</button></div>" +
      "</form>" +
      '<p class="settings-note" data-i18n="settings.security.workHint"></p>';
  }

  function render() {
    if (!overlay) return;
    overlay.innerHTML =
      '<div class="settings-panel" role="dialog" aria-modal="true">' +
      '<div class="settings-head">' +
      '<span class="settings-head-icon"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="3.2"/><path d="M12 3v2.2M12 18.8V21M3 12h2.2M18.8 12H21M5.6 5.6l1.6 1.6M16.8 16.8l1.6 1.6M18.4 5.6l-1.6 1.6M7.2 16.8l-1.6 1.6"/></svg></span>' +
      '<div><div class="settings-title" data-i18n="settings.title"></div>' +
      '<div class="settings-subtitle">DevOS</div></div>' +
      '<button type="button" class="settings-close" id="settingsClose" data-i18n-title="settings.close">' +
      '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M6 6l12 12M18 6L6 18"/></svg></button>' +
      "</div>" +
      '<div class="settings-tabs">' +
      '<button type="button" class="settings-tab" data-tab="appearance" data-i18n="settings.tab.appearance"></button>' +
      '<button type="button" class="settings-tab" data-tab="language" data-i18n="settings.tab.language"></button>' +
      '<button type="button" class="settings-tab" data-tab="security" data-i18n="settings.tab.security"></button>' +
      "</div>" +
      '<div class="settings-body">' +
      '<section class="settings-section" data-section="appearance">' + wallpaperSection() + "</section>" +
      '<section class="settings-section" data-section="language">' + languageSection() + "</section>" +
      '<section class="settings-section" data-section="security">' + securitySection() + "</section>" +
      "</div></div>";

    if (window.DevOSI18n) window.DevOSI18n.apply(overlay);
    bind();
    selectTab(tab);
  }

  function selectTab(name) {
    tab = name;
    if (!overlay) return;
    overlay.querySelectorAll(".settings-tab").forEach(function (btn) {
      btn.classList.toggle("is-active", btn.getAttribute("data-tab") === name);
    });
    overlay.querySelectorAll(".settings-section").forEach(function (section) {
      section.classList.toggle("is-active", section.getAttribute("data-section") === name);
    });
  }

  function bind() {
    overlay.querySelector("#settingsClose").addEventListener("click", close);
    overlay.querySelectorAll(".settings-tab").forEach(function (btn) {
      btn.addEventListener("click", function () { selectTab(btn.getAttribute("data-tab")); });
    });

    overlay.querySelectorAll("[data-preset]").forEach(function (btn) {
      btn.addEventListener("click", function () {
        if (busy) return;
        var preset = btn.getAttribute("data-preset");
        busy = true;
        request("/api/security/settings", { body: { wallpaper: { kind: "preset", preset: preset } } })
          .then(function (data) {
            status = data;
            applyWallpaper(data.wallpaper);
            overlay.querySelectorAll("[data-preset]").forEach(function (item) {
              item.classList.toggle("is-active", item.getAttribute("data-preset") === preset);
            });
          })
          .catch(function (err) { notify(t("settings.wallpaper.failed", { error: err.message })); })
          .then(function () { busy = false; });
      });
    });

    var fileInput = overlay.querySelector("#wallpaperFile");
    if (fileInput) {
      fileInput.addEventListener("change", function () {
        var file = fileInput.files && fileInput.files[0];
        if (!file || busy) return;
        var form = new FormData();
        form.append("file", file);
        busy = true;
        notify(t("settings.wallpaper.uploading"));
        request("/api/security/wallpaper", { body: form })
          .then(function (data) {
            status = Object.assign({}, status, { wallpaper: data.wallpaper });
            applyWallpaper(data.wallpaper);
            render();
            notify(t("settings.wallpaper.uploaded"));
          })
          .catch(function (err) { notify(t("settings.wallpaper.failed", { error: err.message })); })
          .then(function () { busy = false; });
      });
    }

    overlay.querySelectorAll(".lang-item").forEach(function (btn) {
      btn.addEventListener("click", function () {
        var locale = btn.getAttribute("data-locale");
        if (window.DevOSI18n) window.DevOSI18n.setLocale(locale);
        request("/api/security/settings", { body: { locale: locale } })
          .then(function () { notify(t("settings.language.saved")); render(); })
          .catch(function (err) { notify(t("settings.language.failed", { error: err.message })); });
      });
    });

    var form = overlay.querySelector("#securityForm");
    if (form) form.addEventListener("submit", submitPassword);
    var removeBtn = overlay.querySelector("#secRemove");
    if (removeBtn) removeBtn.addEventListener("click", removePassword);
  }

  function formError(message) {
    var error = overlay.querySelector("#secError");
    if (!error) return;
    error.textContent = message;
    error.classList.remove("hidden");
  }

  function clearFormError() {
    var error = overlay.querySelector("#secError");
    if (!error) return;
    error.classList.add("hidden");
    error.textContent = "";
  }

  function submitPassword(event) {
    event.preventDefault();
    if (busy) return;
    var hasPassword = !!(status && status.hasPassword);
    var oldInput = overlay.querySelector("#secOld");
    var newInput = overlay.querySelector("#secNew");
    var confirmInput = overlay.querySelector("#secConfirm");
    var oldPassword = oldInput ? oldInput.value : "";
    var newPassword = newInput.value;
    var confirm = confirmInput.value;

    clearFormError();
    if (hasPassword && !oldPassword) return formError(t("settings.security.needOld"));
    if (Array.from(newPassword).length < 6) return formError(t("settings.security.tooShort"));
    if (!hasPassword && !newPassword.trim()) return formError(t("settings.security.needNew"));
    if (newPassword !== confirm) return formError(t("settings.security.mismatch"));

    busy = true;
    request("/api/security/password", { body: { oldPassword: oldPassword, newPassword: newPassword } })
      .then(function (data) {
        status = Object.assign({}, status, { hasPassword: data.hasPassword });
        render();
        notify(t("settings.security.saved"));
      })
      .catch(function (err) {
        formError(err.status === 401 ? t("settings.security.wrongOld") : t("settings.security.failed", { error: err.message }));
      })
      .then(function () { busy = false; });
  }

  function removePassword() {
    if (busy) return;
    var oldInput = overlay.querySelector("#secOld");
    var oldPassword = oldInput ? oldInput.value : "";
    clearFormError();
    if (!oldPassword) return formError(t("settings.security.needOld"));
    busy = true;
    request("/api/security/password", { body: { oldPassword: oldPassword, newPassword: "" } })
      .then(function () {
        status = Object.assign({}, status, { hasPassword: false, locked: false });
        render();
        notify(t("settings.security.removed"));
      })
      .catch(function (err) {
        formError(err.status === 401 ? t("settings.security.wrongOld") : t("settings.security.failed", { error: err.message }));
      })
      .then(function () { busy = false; });
  }

  function applyWallpaper(wallpaper) {
    if (window.DevOSDesktop && typeof window.DevOSDesktop.applyWallpaper === "function") {
      window.DevOSDesktop.applyWallpaper(wallpaper);
    }
  }

  function open() {
    ensureOverlay();
    overlay.classList.remove("hidden");
    request("/api/security/status", { method: "GET" })
      .then(function (data) { status = data; })
      .catch(function () { /* 保留上次状态 */ })
      .then(function () { render(); });
  }

  function close() {
    if (overlay) overlay.classList.add("hidden");
  }

  if (window.DevOSI18n) {
    window.DevOSI18n.onChange(function () {
      if (overlay && !overlay.classList.contains("hidden")) render();
    });
  }
  document.addEventListener("keydown", function (event) {
    if (event.key === "Escape" && overlay && !overlay.classList.contains("hidden")) close();
  });

  window.DevOSSettings = { open: open, close: close };
})();
