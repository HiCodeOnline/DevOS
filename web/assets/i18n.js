/* DevOS 国际化：支持简体中文、英文、繁体中文，新增语言只需补充一个词典文件。 */
(function () {
  if (window.DevOSI18n) return;

  var FALLBACK = "en";
  var SUPPORTED = ["zh-CN", "en", "zh-TW"];
  var BCP47 = { "zh-CN": "zh-CN", "en": "en-US", "zh-TW": "zh-TW" };
  var dicts = Object.create(null);
  var listeners = [];

  // 默认语言跟随系统；用户在设置中的选择由内核持久化后覆盖这里的结果。
  function detectLocale() {
    var tags = navigator.languages && navigator.languages.length ? navigator.languages : [navigator.language || ""];
    for (var i = 0; i < tags.length; i++) {
      var tag = String(tags[i] || "").toLowerCase();
      if (tag.indexOf("zh") === 0) return /(tw|hk|mo|hant)/.test(tag) ? "zh-TW" : "zh-CN";
      if (tag.indexOf("en") === 0) return "en";
    }
    return FALLBACK;
  }

  var locale = detectLocale();

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
    document.documentElement.lang = next;
    apply(document);
    if (!(options && options.silent)) {
      listeners.forEach(function (fn) { fn(next); });
      document.dispatchEvent(new CustomEvent("devos:locale-changed", { detail: { locale: next } }));
    }
    return next;
  }

  function onChange(fn) { if (typeof fn === "function") listeners.push(fn); }

  function bcp47() { return BCP47[locale] || "en-US"; }

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
