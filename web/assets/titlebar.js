/* 自绘标题栏交互：读取窗口状态、绑定最小化 / 最大化 / 关闭，并按平台切换样式 */
(function () {
  'use strict';

  var bar = document.getElementById('appTitlebar');
  if (!bar) {
    return;
  }

  var body = document.body;
  var btnMax = bar.querySelector('.tb-max');
  var refreshTimer = null;

  function setMaximised(maximised) {
    bar.dataset.maximised = maximised ? 'true' : 'false';
    if (btnMax) {
      btnMax.title = maximised ? '还原' : '最大化';
      btnMax.setAttribute('aria-label', btnMax.title);
    }
  }

  // apply 根据窗口状态切换平台样式与可用性。
  function apply(state) {
    var platform = state.platform || 'windows';
    bar.dataset.platform = platform;
    body.classList.toggle('no-desktop', !state.desktop);
    setMaximised(!!state.maximised);
    // 非原生窗口（例如回退到浏览器）时不提供窗口操作按钮。
    bar.querySelectorAll('[data-window-action]').forEach(function (btn) {
      btn.disabled = !state.desktop;
    });
  }

  function refresh() {
    fetch('/api/window/state', { cache: 'no-store' })
      .then(function (res) {
        if (!res.ok) {
          throw new Error('state');
        }
        return res.json();
      })
      .then(apply)
      .catch(function () {
        apply({ platform: 'windows', desktop: false, maximised: false });
      });
  }

  function command(action) {
    fetch('/api/window/' + action, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'X-DevOS-Request': '1' },
      body: '{}'
    })
      .then(function (res) {
        if (!res.ok) {
          throw new Error('command');
        }
        return res.json();
      })
      .then(function (data) {
        if (typeof data.maximised === 'boolean') {
          setMaximised(data.maximised);
        }
      })
      .catch(function () {});
  }

  bar.addEventListener('click', function (event) {
    var btn = event.target.closest('[data-window-action]');
    if (!btn || btn.disabled) {
      return;
    }
    command(btn.dataset.windowAction);
  });

  // 双击标题栏空白区域切换最大化。Windows 由系统标题栏行为原生处理，其余平台在此补齐。
  var drag = bar.querySelector('.titlebar-drag');
  if (drag) {
    drag.addEventListener('dblclick', function () {
      if (bar.dataset.platform !== 'windows') {
        command('maximize');
      }
    });
  }

  // 窗口尺寸变化（最大化、贴边吸附、还原）后同步按钮图标。
  window.addEventListener('resize', function () {
    clearTimeout(refreshTimer);
    refreshTimer = setTimeout(refresh, 180);
  });

  refresh();
})();
