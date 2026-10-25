/* DevOS 国际化：支持简体中文、英文、繁体中文，新增语言只需补充一个词典文件。 */
(function () {
  if (window.DevOSI18n) return;

  var FALLBACK = "zh-CN";
  var STORAGE_KEY = "devos.locale";
  var SUPPORTED = ["zh-CN", "en", "zh-TW"];
  var BCP47 = { "zh-CN": "zh-CN", "en": "en-US", "zh-TW": "zh-TW" };
  var dicts = Object.create(null);
  var listeners = [];

  function pickInitial() {
    var saved = "";
    try { saved = localStorage.getItem(STORAGE_KEY) || ""; } catch (_) { saved = ""; }
    if (SUPPORTED.indexOf(saved) >= 0) return saved;
    var nav = String(navigator.language || "").toLowerCase();
    if (nav.indexOf("zh") === 0) return /tw|hk|hant/.test(nav) ? "zh-TW" : "zh-CN";
    if (nav.indexOf("en") === 0) return "en";
    return FALLBACK;
  }

  var locale = pickInitial();

  function register(code, dict) {
    if (!code || !dict) return;
    dicts[code] = Object.assign(dicts[code] || {}, dict);
  }

  function lookup(key) {
    var chain = [locale, FALLBACK];
    for (var i = 0; i < chain.length; i++) {
      var dict = dicts[chain[i]];
      if (dict && Object.prototype.hasOwnProperty.call(dict, key)) return dict[key];
    }
    return null;
  }

  function t(key, params) {
    var text = lookup(key);
    if (text == null) return key;
    if (params) {
      text = String(text).replace(/\{(\w+)\}/g, function (match, name) {
        return Object.prototype.hasOwnProperty.call(params, name) ? String(params[name]) : match;
      });
    }
    return text;
  }

  function apply(root) {
    var scope = root || document;
    if (!scope || !scope.querySelectorAll) return;
    scope.querySelectorAll("[data-i18n]").forEach(function (node) {
      node.textContent = t(node.getAttribute("data-i18n"));
    });
    scope.querySelectorAll("[data-i18n-placeholder]").forEach(function (node) {
      node.setAttribute("placeholder", t(node.getAttribute("data-i18n-placeholder")));
    });
    scope.querySelectorAll("[data-i18n-title]").forEach(function (node) {
      node.setAttribute("title", t(node.getAttribute("data-i18n-title")));
    });
  }

  function setLocale(code, options) {
    var next = SUPPORTED.indexOf(code) >= 0 ? code : FALLBACK;
    locale = next;
    try { localStorage.setItem(STORAGE_KEY, next); } catch (_) { /* 忽略隐私模式限制 */ }
    document.documentElement.lang = next;
    apply(document);
    if (!(options && options.silent)) {
      listeners.forEach(function (fn) { fn(next); });
      document.dispatchEvent(new CustomEvent("devos:locale-changed", { detail: { locale: next } }));
    }
    return next;
  }

  function onChange(fn) { if (typeof fn === "function") listeners.push(fn); }

  function bcp47() { return BCP47[locale] || "zh-CN"; }

  window.DevOSI18n = {
    supported: SUPPORTED.slice(),
    register: register,
    t: t,
    apply: apply,
    setLocale: setLocale,
    getLocale: function () { return locale; },
    bcp47: bcp47,
    onChange: onChange,
  };

  document.addEventListener("DOMContentLoaded", function () {
    document.documentElement.lang = locale;
    apply(document);
  });
})();
