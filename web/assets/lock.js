/* DevOS 锁屏：输入应用密码解锁，支持在锁屏界面切换语言。 */
(function () {
  if (window.DevOSLock) return;

  var screen = document.getElementById("lockScreen");
  var form = document.getElementById("lockForm");
  var passwordInput = document.getElementById("lockPassword");
  var submitBtn = document.getElementById("lockSubmit");
  var errorEl = document.getElementById("lockError");
  var localesEl = document.getElementById("lockLocales");
  var timeEl = screen ? screen.querySelector(".lock-time") : null;
  var dateEl = screen ? screen.querySelector(".lock-date") : null;

  var clockTimer = null;
  var busy = false;
  var onUnlock = null;

  function t(key, params) {
    return window.DevOSI18n ? window.DevOSI18n.t(key, params) : key;
  }

  function esc(value) {
    return String(value == null ? "" : value).replace(/[&<>"']/g, function (ch) {
      return { "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[ch];
    });
  }

  function request(url, body) {
    return fetch(url, {
      method: "POST",
      headers: { "Content-Type": "application/json", "X-DevOS-Request": "1" },
      body: JSON.stringify(body || {}),
    }).then(function (resp) {
      return resp.json().catch(function () { return {}; }).then(function (data) {
        if (!resp.ok) {
          var err = new Error(data.error || "HTTP " + resp.status);
          err.status = resp.status;
          err.data = data;
          throw err;
        }
        return data;
      });
    });
  }

  function bcp47() {
    return window.DevOSI18n ? window.DevOSI18n.bcp47() : "zh-CN";
  }

  function renderClock() {
    if (!timeEl || !dateEl) return;
    var now = new Date();
    timeEl.textContent = now.toLocaleTimeString(bcp47(), { hour: "2-digit", minute: "2-digit", hour12: false });
    dateEl.textContent = now.toLocaleDateString(bcp47(), { month: "long", day: "numeric", weekday: "long" });
  }

  function renderLocales() {
    if (!localesEl || !window.DevOSI18n) return;
    var current = window.DevOSI18n.getLocale();
    localesEl.innerHTML = window.DevOSI18n.supported.map(function (code) {
      var active = code === current ? " is-active" : "";
      return '<button type="button" class="lock-locale' + active + '" data-locale="' + code + '">' +
        esc(window.DevOSI18n.t("locale.name." + code)) + "</button>";
    }).join("");
    localesEl.querySelectorAll("[data-locale]").forEach(function (btn) {
      btn.addEventListener("click", function () {
        var code = btn.getAttribute("data-locale");
        window.DevOSI18n.setLocale(code);
        renderLocales();
        renderClock();
        request("/api/security/locale", { locale: code }).catch(function () { /* 忽略离线错误 */ });
      });
    });
  }

  function showError(message) {
    if (!errorEl) return;
    errorEl.textContent = message;
    errorEl.classList.remove("hidden");
  }

  function hideError() {
    if (!errorEl) return;
    errorEl.classList.add("hidden");
    errorEl.textContent = "";
  }

  function show(options) {
    if (!screen) return;
    onUnlock = options && options.onUnlock;
    var desktop = document.getElementById("desktop");
    var taskbar = document.getElementById("taskbar");
    var startMenu = document.getElementById("startMenu");
    if (desktop) desktop.classList.add("hidden");
    if (taskbar) taskbar.classList.add("hidden");
    if (startMenu) startMenu.classList.add("hidden");

    screen.classList.remove("hidden");
    hideError();
    passwordInput.value = "";
    submitBtn.disabled = false;
    submitBtn.textContent = t("lock.unlock");
    renderLocales();
    renderClock();
    if (clockTimer) clearInterval(clockTimer);
    clockTimer = setInterval(renderClock, 1000);
    setTimeout(function () { passwordInput.focus(); }, 50);
  }

  function hide() {
    if (!screen) return;
    screen.classList.add("hidden");
    if (clockTimer) { clearInterval(clockTimer); clockTimer = null; }
    var desktop = document.getElementById("desktop");
    var taskbar = document.getElementById("taskbar");
    if (desktop) desktop.classList.remove("hidden");
    if (taskbar) taskbar.classList.remove("hidden");
  }

  function submit(event) {
    event.preventDefault();
    if (busy) return;
    var password = passwordInput.value;
    if (!password) {
      showError(t("lock.wrong"));
      passwordInput.focus();
      return;
    }
    busy = true;
    hideError();
    submitBtn.disabled = true;
    submitBtn.textContent = t("lock.verifying");
    request("/api/security/unlock", { password: password })
      .then(function () {
        hide();
        if (typeof onUnlock === "function") onUnlock();
      })
      .catch(function (err) {
        passwordInput.value = "";
        if (err.status === 429) {
          var seconds = (err.data && err.data.retryAfter) || 30;
          showError(t("lock.throttled", { seconds: seconds }));
        } else {
          showError(t("lock.wrong"));
        }
        passwordInput.focus();
      })
      .then(function () {
        busy = false;
        submitBtn.disabled = false;
        submitBtn.textContent = t("lock.unlock");
      });
  }

  if (form) form.addEventListener("submit", submit);
  if (window.DevOSI18n) {
    window.DevOSI18n.onChange(function () {
      if (screen && !screen.classList.contains("hidden")) {
        renderLocales();
        renderClock();
      }
    });
  }

  window.DevOSLock = {
    show: show,
    hide: hide,
    isVisible: function () { return !!screen && !screen.classList.contains("hidden"); },
  };
})();
