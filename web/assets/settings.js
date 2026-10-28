/* DevOS 设置面板：壁纸、语言、应用密码与数据备份。 */
(function () {
  if (window.DevOSSettings) return;

  var PRESETS = [
    { id: "aurora", swatch: "wallpaper-swatch-aurora" },
    { id: "dusk", swatch: "wallpaper-swatch-dusk" },
    { id: "mint", swatch: "wallpaper-swatch-mint" },
    { id: "sand", swatch: "wallpaper-swatch-sand" },
    { id: "midnight", swatch: "wallpaper-swatch-midnight" },
  ];
  var LOCALE_NAMES = {
    "zh-CN": "简体中文", "zh-TW": "繁體中文", "en": "English",
    "ja": "日本語", "ko": "한국어", "fr": "Français", "de": "Deutsch", "ru": "Русский", "hi": "हिन्दी",
  };

  var overlay = null;
  var status = null;
  var customs = [];
  var storage = null;
  var migrateDraft = "";
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
          err.code = data.code || "";
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

  // 自定义壁纸以“月-日 时:分”作为角标，便于区分多张上传图片。
  function wallpaperStamp(iso) {
    var date = new Date(iso);
    if (isNaN(date.getTime())) return "";
    var pad = function (value) { return (value < 10 ? "0" : "") + value; };
    return pad(date.getMonth() + 1) + "-" + pad(date.getDate()) + " " + pad(date.getHours()) + ":" + pad(date.getMinutes());
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
    // 自定义壁纸与内置壁纸同网格展示：点击切换，右上角按钮删除。
    var customTiles = customs.map(function (item) {
      var active = customActive && current.file === item.file ? " is-active" : "";
      return '<div class="wallpaper-item is-custom' + active + '" data-file="' + esc(item.file) + '"' +
        ' style="background-image:url(' + esc(item.url) + ')">' +
        '<button type="button" class="wallpaper-pick" data-custom-file="' + esc(item.file) + '" data-i18n-title="settings.wallpaper.switch">' +
        '<span class="wallpaper-name">' + esc(wallpaperStamp(item.modTime)) + "</span></button>" +
        '<button type="button" class="wallpaper-delete" data-custom-delete="' + esc(item.file) + '" data-i18n-title="settings.wallpaper.delete">' +
        '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round"><path d="M6 6l12 12M18 6L6 18"/></svg>' +
        "</button></div>";
    }).join("");
    // 网格末尾追加与壁纸卡片同尺寸的上传区域，点击选择本地图片。
    var addTile = '<label class="wallpaper-item is-add" data-i18n-title="settings.wallpaper.upload">' +
      '<input id="wallpaperFile" type="file" accept="image/png,image/jpeg" class="hidden" />' +
      '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round"><path d="M12 6v12M6 12h12"/></svg>' +
      '<span data-i18n="settings.wallpaper.upload"></span></label>';
    return '<div class="settings-label" data-i18n="settings.wallpaper.title"></div>' +
      '<p class="settings-desc" data-i18n="settings.wallpaper.desc"></p>' +
      '<div class="wallpaper-grid">' + tiles + customTiles + addTile + "</div>" +
      '<p class="settings-desc" data-i18n="settings.wallpaper.hint"></p>';
  }

  function languageSection() {
    var current = (window.DevOSI18n && window.DevOSI18n.getLocale()) || "zh-CN";
    var items = (window.DevOSI18n ? window.DevOSI18n.supported : Object.keys(LOCALE_NAMES)).map(function (code) {
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

  function formatBytes(bytes) {
    var value = Number(bytes) || 0;
    if (value < 1024) return value + " B";
    var units = ["KB", "MB", "GB", "TB"];
    var index = -1;
    do {
      value = value / 1024;
      index++;
    } while (value >= 1024 && index < units.length - 1);
    return (value >= 10 ? value.toFixed(0) : value.toFixed(1)) + " " + units[index];
  }

  function copyIcon() {
    return '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round">' +
      '<rect x="9" y="9" width="11" height="11" rx="2"/><path d="M5 15V6a2 2 0 0 1 2-2h9"/></svg>';
  }

  function storageSection() {
    var info = storage || {};
    var dataDir = info.dataDir || "";
    var pendingRestore = !!info.pendingRestore;
    var pendingMigrate = info.pendingMigrate || "";
    var hasPending = pendingRestore || !!pendingMigrate;
    var pendingHtml = "";
    if (pendingRestore) {
      pendingHtml += '<span class="storage-badge is-warn" data-i18n="settings.storage.pendingRestore"></span>';
    }
    if (pendingMigrate) {
      pendingHtml += '<span class="storage-badge is-warn">' +
        esc(t("settings.storage.pendingMigrate", { dir: pendingMigrate })) + "</span>";
    }
    return '<div class="settings-label" data-i18n="settings.storage.title"></div>' +
      '<p class="settings-desc" data-i18n="settings.storage.desc"></p>' +
      '<div class="storage-path-row">' +
      '<div class="storage-path-label" data-i18n="settings.storage.current"></div>' +
      '<div class="storage-path"><code>' + esc(dataDir || "—") + "</code>" +
      '<button type="button" class="storage-copy" data-copy-path="' + esc(dataDir) + '" data-i18n-title="settings.storage.copy">' +
      copyIcon() + "</button></div>" +
      '<div class="storage-meta">' + esc(t("settings.storage.files", { files: info.files || 0, size: formatBytes(info.bytes) })) + "</div>" +
      "</div>" +
      '<div class="storage-block">' +
      '<div class="settings-label" data-i18n="settings.storage.backup"></div>' +
      '<p class="settings-desc" data-i18n="settings.storage.backupDesc"></p>' +
      '<div class="settings-actions storage-actions"><button type="button" id="storageBackupBtn" class="btn-primary" data-i18n="settings.storage.backupBtn"></button></div>' +
      "</div>" +
      '<div class="storage-block">' +
      '<div class="settings-label" data-i18n="settings.storage.restore"></div>' +
      '<p class="settings-desc" data-i18n="settings.storage.restoreDesc"></p>' +
      '<div class="settings-actions storage-actions">' +
      '<button type="button" id="storageRestoreBtn" class="btn-ghost" data-i18n="settings.storage.restoreBtn"></button>' +
      '<input id="storageRestoreFile" type="file" accept=".zip,application/zip" class="hidden" />' +
      "</div>" + pendingHtml +
      "</div>" +
      '<div class="storage-block">' +
      '<div class="settings-label" data-i18n="settings.storage.migrate"></div>' +
      '<p class="settings-desc" data-i18n="settings.storage.migrateDesc"></p>' +
      '<div class="storage-row">' +
      '<input id="storageMigrateDir" class="settings-input" type="text" value="' + esc(migrateDraft) + '" data-i18n-placeholder="settings.storage.migratePlaceholder" />' +
      '<button type="button" id="storageBrowseBtn" class="btn-ghost" data-i18n="settings.storage.browse"></button>' +
      "</div>" +
      '<div class="settings-actions storage-actions"><button type="button" id="storageMigrateBtn" class="btn-primary" data-i18n="settings.storage.migrateBtn"></button></div>' +
      "</div>" +
      (hasPending
        ? '<div class="settings-actions storage-actions"><button type="button" id="storageCancelBtn" class="btn-ghost btn-danger" data-i18n="settings.storage.cancel"></button></div>' +
          '<p class="settings-note" data-i18n="settings.storage.restartHint"></p>'
        : "") +
      '<p class="settings-note" data-i18n="settings.storage.note"></p>';
  }

  function render() {
    if (!overlay) return;
    overlay.innerHTML =
      '<div class="settings-panel" role="dialog" aria-modal="true">' +
      '<div class="settings-head">' +
      '<span class="settings-head-icon"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="3"/><path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 0 1 0 2.83 2 2 0 0 1-2.83 0l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-2 2 2 2 0 0 1-2-2v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 0 1-2.83 0 2 2 0 0 1 0-2.83l.06-.06a1.65 1.65 0 0 0 .33-1.82 1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1-2-2 2 2 0 0 1 2-2h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 0 1 0-2.83 2 2 0 0 1 2.83 0l.06.06a1.65 1.65 0 0 0 1.82.33H9a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 2-2 2 2 0 0 1 2 2v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 0 1 2.83 0 2 2 0 0 1 0 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82V9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 0 1 2 2 2 2 0 0 1-2 2h-.09a1.65 1.65 0 0 0-1.51 1z"/></svg></span>' +
      '<div><div class="settings-title" data-i18n="settings.title"></div>' +
      '<div class="settings-subtitle">DevOS</div></div>' +
      '<button type="button" class="settings-close" id="settingsClose" data-i18n-title="settings.close">' +
      '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M6 6l12 12M18 6L6 18"/></svg></button>' +
      "</div>" +
      '<div class="settings-tabs">' +
      '<button type="button" class="settings-tab" data-tab="appearance" data-i18n="settings.tab.appearance"></button>' +
      '<button type="button" class="settings-tab" data-tab="language" data-i18n="settings.tab.language"></button>' +
      '<button type="button" class="settings-tab" data-tab="security" data-i18n="settings.tab.security"></button>' +
      '<button type="button" class="settings-tab" data-tab="storage" data-i18n="settings.tab.storage"></button>' +
      "</div>" +
      '<div class="settings-body">' +
      '<section class="settings-section" data-section="appearance">' + wallpaperSection() + "</section>" +
      '<section class="settings-section" data-section="language">' + languageSection() + "</section>" +
      '<section class="settings-section" data-section="security">' + securitySection() + "</section>" +
      '<section class="settings-section" data-section="storage">' + storageSection() + "</section>" +
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
    // 首次进入数据页时按需加载目录信息。
    if (name === "storage" && !storage) {
      loadStorageInfo().then(function () {
        if (tab === "storage" && overlay && !overlay.classList.contains("hidden")) render();
      });
    }
  }

  function loadStorageInfo() {
    return request("/api/storage/info", { method: "GET" })
      .then(function (data) {
        storage = data || {};
        return storage;
      })
      .catch(function () {
        storage = storage || {};
        return storage;
      });
  }

  function refreshStorage() {
    return loadStorageInfo().then(function () {
      if (overlay) render();
    });
  }

  function downloadBackup() {
    if (busy) return;
    busy = true;
    notify(t("settings.storage.backingUp"));
    fetch("/api/storage/backup", { headers: { "X-DevOS-Request": "1" } })
      .then(function (resp) {
        if (resp.ok) return resp.blob();
        return resp.json().catch(function () { return {}; }).then(function (data) {
          throw new Error(data.error || "HTTP " + resp.status);
        });
      })
      .then(function (blob) {
        var url = URL.createObjectURL(blob);
        var link = document.createElement("a");
        var now = new Date();
        var stamp = now.getFullYear() + String(now.getMonth() + 1).padStart(2, "0") + String(now.getDate()).padStart(2, "0") +
          "-" + String(now.getHours()).padStart(2, "0") + String(now.getMinutes()).padStart(2, "0") + String(now.getSeconds()).padStart(2, "0");
        link.href = url;
        link.download = "devos-backup-" + stamp + ".zip";
        document.body.appendChild(link);
        link.click();
        link.remove();
        window.setTimeout(function () { URL.revokeObjectURL(url); }, 5000);
        notify(t("settings.storage.backupDone"));
      })
      .catch(function (err) {
        notify(t("settings.storage.backupFailed", { error: err.message }));
      })
      .then(function () { busy = false; });
  }

  function restoreFromFile(file) {
    if (!file || busy) return;
    if (!window.confirm(t("settings.storage.restoreConfirm"))) return;
    var form = new FormData();
    form.append("file", file);
    busy = true;
    request("/api/storage/restore", { body: form })
      .then(function () {
        notify(t("settings.storage.restoreQueued"));
        return refreshStorage();
      })
      .catch(function (err) {
        notify(t("settings.storage.restoreFailed", { error: err.message }));
      })
      .then(function () { busy = false; });
  }

  function requestMigrate(dir, overwrite) {
    return request("/api/storage/migrate", { body: { dir: dir, overwrite: !!overwrite } })
      .then(function () {
        notify(t("settings.storage.migrateQueued"));
        return refreshStorage();
      });
  }

  function submitMigrate() {
    if (busy) return;
    var input = overlay.querySelector("#storageMigrateDir");
    var dir = input ? input.value.trim() : "";
    if (!dir) return notify(t("settings.storage.migratePlaceholder"));
    if (!window.confirm(t("settings.storage.migrateConfirm", { dir: dir }))) return;
    busy = true;
    requestMigrate(dir, false)
      .catch(function (err) {
        if (err.code === "not_empty" && window.confirm(t("settings.storage.overwriteConfirm", { dir: dir }))) {
          return requestMigrate(dir, true);
        }
        throw err;
      })
      .catch(function (err) {
        notify(t("settings.storage.migrateFailed", { error: err.message }));
      })
      .then(function () { busy = false; });
  }

  function browseMigrateDir() {
    if (busy) return;
    busy = true;
    request("/api/storage/browse", { body: {} })
      .then(function (data) {
        migrateDraft = data.dir || migrateDraft;
        var input = overlay.querySelector("#storageMigrateDir");
        if (input) input.value = migrateDraft;
      })
      .catch(function (err) {
        notify(t("settings.storage.browseFailed", { error: err.message }));
      })
      .then(function () { busy = false; });
  }

  function cancelPending() {
    if (busy) return;
    busy = true;
    request("/api/storage/pending/cancel", { body: {} })
      .then(function () {
        notify(t("settings.storage.canceled"));
        return refreshStorage();
      })
      .catch(function (err) {
        notify(t("settings.storage.migrateFailed", { error: err.message }));
      })
      .then(function () { busy = false; });
  }

  // 按当前壁纸同步内置与自定义壁纸的选中态。
  function syncActive() {
    var current = (status && status.wallpaper) || {};
    overlay.querySelectorAll(".wallpaper-item").forEach(function (item) {
      var preset = item.getAttribute("data-preset");
      var file = item.getAttribute("data-file");
      var active = current.kind === "custom"
        ? !!file && file === current.file
        : !!preset && preset === current.preset;
      item.classList.toggle("is-active", active);
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
            syncActive();
          })
          .catch(function (err) { notify(t("settings.wallpaper.failed", { error: err.message })); })
          .then(function () { busy = false; });
      });
    });

    overlay.querySelectorAll("[data-custom-file]").forEach(function (btn) {
      btn.addEventListener("click", function () {
        if (busy) return;
        var file = btn.getAttribute("data-custom-file");
        busy = true;
        request("/api/security/settings", { body: { wallpaper: { kind: "custom", file: file } } })
          .then(function (data) {
            status = data;
            applyWallpaper(data.wallpaper);
            syncActive();
          })
          .catch(function (err) { notify(t("settings.wallpaper.failed", { error: err.message })); })
          .then(function () { busy = false; });
      });
    });

    overlay.querySelectorAll("[data-custom-delete]").forEach(function (btn) {
      btn.addEventListener("click", function () {
        if (busy) return;
        var file = btn.getAttribute("data-custom-delete");
        if (!window.confirm(t("settings.wallpaper.deleteConfirm"))) return;
        busy = true;
        request("/api/security/wallpaper/delete", { body: { file: file } })
          .then(function (data) {
            customs = data.wallpapers || [];
            status = Object.assign({}, status, { wallpaper: data.wallpaper });
            applyWallpaper(data.wallpaper);
            render();
            notify(t("settings.wallpaper.deleted"));
          })
          .catch(function (err) { notify(t("settings.wallpaper.deleteFailed", { error: err.message })); })
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
            customs = data.wallpapers || customs;
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

    var backupBtn = overlay.querySelector("#storageBackupBtn");
    if (backupBtn) backupBtn.addEventListener("click", downloadBackup);
    var restoreBtn = overlay.querySelector("#storageRestoreBtn");
    var restoreInput = overlay.querySelector("#storageRestoreFile");
    if (restoreBtn && restoreInput) {
      restoreBtn.addEventListener("click", function () { restoreInput.click(); });
      restoreInput.addEventListener("change", function () {
        var file = restoreInput.files && restoreInput.files[0];
        restoreFromFile(file);
        restoreInput.value = "";
      });
    }
    var migrateInput = overlay.querySelector("#storageMigrateDir");
    if (migrateInput) {
      migrateInput.addEventListener("input", function () { migrateDraft = migrateInput.value; });
    }
    var browseBtn = overlay.querySelector("#storageBrowseBtn");
    if (browseBtn) browseBtn.addEventListener("click", browseMigrateDir);
    var migrateBtn = overlay.querySelector("#storageMigrateBtn");
    if (migrateBtn) migrateBtn.addEventListener("click", submitMigrate);
    var cancelBtn = overlay.querySelector("#storageCancelBtn");
    if (cancelBtn) cancelBtn.addEventListener("click", cancelPending);
    overlay.querySelectorAll("[data-copy-path]").forEach(function (btn) {
      btn.addEventListener("click", function () {
        var text = btn.getAttribute("data-copy-path") || "";
        if (!text) return;
        navigator.clipboard.writeText(text)
          .then(function () { notify(t("settings.storage.copied")); })
          .catch(function (err) { notify(t("settings.storage.copyFailed", { error: err.message })); });
      });
    });
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
    // 状态与自定义壁纸库并行加载，保证壁纸网格与当前选中态一致。
    Promise.all([
      request("/api/security/status", { method: "GET" }).catch(function () { return null; }),
      request("/api/security/wallpapers", { method: "GET" }).catch(function () { return null; }),
      loadStorageInfo(),
    ])
      .then(function (results) {
        if (results[0]) status = results[0];
        if (results[1] && results[1].wallpapers) customs = results[1].wallpapers;
        if (results[1] && results[1].wallpaper) {
          status = Object.assign({}, status, { wallpaper: results[1].wallpaper });
        }
      })
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
