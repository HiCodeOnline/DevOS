/* DevOS 应用页面国际化：跟随桌面外壳语言联动切换，缺失文案回退英语，再回退键名。 */
(function () {
  if (window.DevOSAppI18n) return;

  var SUPPORTED = ["zh-CN", "zh-TW", "en", "ja", "ko", "fr", "de", "ru", "hi"];
  var FALLBACK = "en";
  var BCP47 = {
    "zh-CN": "zh-CN", "zh-TW": "zh-TW", "en": "en-US",
    "ja": "ja-JP", "ko": "ko-KR", "fr": "fr-FR", "de": "de-DE", "ru": "ru-RU", "hi": "hi-IN",
  };
  var dicts = Object.create(null);
  var listeners = [];

  function normalize(code) {
    var tag = String(code || "").toLowerCase();
    if (!tag) return "";
    if (tag.indexOf("zh") === 0) return /(tw|hk|mo|hant)/.test(tag) ? "zh-TW" : "zh-CN";
    if (tag.indexOf("en") === 0) return "en";
    if (tag.indexOf("ja") === 0) return "ja";
    if (tag.indexOf("ko") === 0) return "ko";
    if (tag.indexOf("fr") === 0) return "fr";
    if (tag.indexOf("de") === 0) return "de";
    if (tag.indexOf("ru") === 0) return "ru";
    if (tag.indexOf("hi") === 0) return "hi";
    return "";
  }

  // 独立打开应用时按系统语言初始化；默认回退英语。
  function systemLocale() {
    var tags = navigator.languages && navigator.languages.length ? navigator.languages : [navigator.language || ""];
    for (var i = 0; i < tags.length; i++) {
      var code = normalize(tags[i]);
      if (code) return code;
    }
    return FALLBACK;
  }

  // 在桌面外壳内运行时，直接读取外壳当前语言，保证初次加载即一致。
  function parentLocale() {
    try {
      var parent = window.parent;
      if (!parent || parent === window) return "";
      var shell = parent.DevOSI18n;
      if (!shell || typeof shell.getLocale !== "function") return "";
      var code = normalize(shell.getLocale());
      return SUPPORTED.indexOf(code) >= 0 ? code : "";
    } catch (_) {
      return "";
    }
  }

  var locale = parentLocale() || systemLocale();

  function register(code, dict) {
    if (!code || !dict || SUPPORTED.indexOf(code) < 0) return;
    dicts[code] = Object.assign(dicts[code] || {}, dict);
  }

  // 一次注册全部语言，词典文件使用该入口。
  function registerAll(bundle) {
    if (!bundle) return;
    Object.keys(bundle).forEach(function (code) { register(code, bundle[code]); });
  }

  function lookup(key) {
    var chain = locale === FALLBACK ? [locale] : [locale, FALLBACK];
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
    scope.querySelectorAll("[data-i18n-html]").forEach(function (node) {
      node.innerHTML = t(node.getAttribute("data-i18n-html"));
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
    var changed = next !== locale;
    locale = next;
    document.documentElement.lang = next;
    apply(document);
    if (!(options && options.silent)) {
      listeners.forEach(function (fn) { fn(next, changed); });
      document.dispatchEvent(new CustomEvent("devos:locale-changed", { detail: { locale: next, changed: changed } }));
    }
    return next;
  }

  function onChange(fn) { if (typeof fn === "function") listeners.push(fn); }

  function bcp47() { return BCP47[locale] || "en-US"; }

  window.DevOSAppI18n = {
    supported: SUPPORTED.slice(),
    fallback: FALLBACK,
    register: register,
    registerAll: registerAll,
    t: t,
    apply: apply,
    setLocale: setLocale,
    getLocale: function () { return locale; },
    bcp47: bcp47,
    onChange: onChange,
  };

  // 外壳切换语言时向同源 iframe 广播。
  window.addEventListener("message", function (event) {
    if (event.origin && event.origin !== location.origin) return;
    var data = event.data;
    if (!data || data.type !== "devos:locale") return;
    var code = normalize(data.locale);
    if (SUPPORTED.indexOf(code) < 0) return;
    setLocale(code);
  });

  document.addEventListener("DOMContentLoaded", function () {
    document.documentElement.lang = locale;
    apply(document);
  });
})();
