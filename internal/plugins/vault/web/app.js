/* 密钥库前端：解锁、分组、条目、标签筛选与会话管理。 */
(function () {
  'use strict';

  var API = '/api/apps/vault';
  var IDLE_MS = 15 * 60 * 1000;
  var PALETTE = ['#6366f1', '#0ea5e9', '#10b981', '#f59e0b', '#f43f5e', '#8b5cf6', '#14b8a6', '#64748b'];
  var ICONS = {
    edit: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M4 20h4l10-10-4-4L4 16v4Z"/><path d="M13.5 6.5l4 4"/></svg>',
    copy: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><rect x="9" y="9" width="11" height="11" rx="2"/><path d="M15 5H6a1 1 0 0 0-1 1v9"/></svg>',
    eye: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M2 12s3.6-6 10-6 10 6 10 6-3.6 6-10 6S2 12 2 12Z"/><circle cx="12" cy="12" r="2.6"/></svg>',
    eyeOff: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M4 4l16 16"/><path d="M9.9 5.4A10.6 10.6 0 0 1 12 5c6.4 0 10 7 10 7a17 17 0 0 1-3 4.1"/><path d="M6.3 6.7A16.4 16.4 0 0 0 2 12s3.6 7 10 7c1.6 0 3-.4 4.2-1"/><path d="M9.6 9.7a3.2 3.2 0 0 0 4.6 4.5"/></svg>',
    close: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round"><path d="M6 6l12 12M18 6 6 18"/></svg>',
    clock: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="8.5"/><path d="M12 7.5V12l3 2"/></svg>',
    all: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M4 7.5 12 4l8 3.5-8 3.5-8-3.5Z"/><path d="M4 12l8 3.5 8-3.5"/><path d="M4 16.5 12 20l8-3.5"/></svg>',
    lock: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><rect x="4" y="10" width="16" height="10" rx="2"/><path d="M8 10V7a4 4 0 0 1 8 0v3"/></svg>',
    key: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><circle cx="8" cy="8" r="4"/><path d="M11 11l8.5 8.5M16 16l2-2M19 19l2-2"/></svg>'
  };

  var i18n = window.DevOSAppI18n;
  function t(key, params) { return i18n.t(key, params); }
  function $(id) { return document.getElementById(id); }
  function el(tag, cls, text) {
    var node = document.createElement(tag);
    if (cls) node.className = cls;
    if (text != null) node.textContent = text;
    return node;
  }
  function iconEl(name) {
    var span = document.createElement('span');
    span.className = 'ico';
    span.innerHTML = ICONS[name] || '';
    return span;
  }
  function iconHtml(name) { return ICONS[name] || ''; }

  var state = {
    mode: 'unlock',
    initialized: false,
    token: '',
    groups: [],
    secrets: [],
    tags: [],
    groupId: '',
    tagFilter: [],
    query: '',
    revealed: {},
    editingId: '',
    editorTags: [],
    editingGroupId: '',
    groupColor: PALETTE[0],
    pendingConfirm: null,
    idleTimer: null,
    toastTimer: null
  };

  // ---------- 请求 ----------

  async function request(path, options) {
    options = options || {};
    var headers = { 'Content-Type': 'application/json' };
    if (state.token) headers['X-Vault-Token'] = state.token;
    var res = await fetch(API + path, {
      method: options.method || 'GET',
      headers: headers,
      body: options.body === undefined ? undefined : JSON.stringify(options.body)
    });
    var text = await res.text();
    var data = null;
    if (text) {
      try { data = JSON.parse(text); } catch (_) { data = null; }
    }
    var publicCall = path === '/unlock' || path === '/setup' || path === '/state';
    if (res.status === 401 && !publicCall) {
      handleExpired();
      var expired = new Error('locked');
      expired.status = 401;
      throw expired;
    }
    if (!res.ok) {
      var err = new Error((data && data.error) || ('HTTP ' + res.status));
      err.status = res.status;
      throw err;
    }
    return data || {};
  }

  function applySnapshot(data) {
    state.groups = (data && data.groups) || [];
    state.secrets = (data && data.secrets) || [];
    state.tags = (data && data.tags) || [];
  }

  // ---------- 生命周期 ----------

  function init() {
    bindEvents();
    i18n.onChange(function () {
      if (state.token) {
        renderAll();
      } else {
        updateLockCopy();
      }
    });
    request('/state').then(function (data) {
      state.initialized = !!data.initialized;
      showLockView(state.initialized ? 'unlock' : 'setup');
    }).catch(function () {
      showLockView('setup');
    });
  }

  function bindEvents() {
    $('lockForm').addEventListener('submit', submitLock);
    Array.prototype.forEach.call(document.querySelectorAll('[data-eye]'), function (btn) {
      btn.addEventListener('click', function () {
        var input = $(btn.getAttribute('data-eye'));
        if (!input) return;
        input.type = input.type === 'password' ? 'text' : 'password';
        btn.innerHTML = iconHtml(input.type === 'password' ? 'eye' : 'eyeOff');
      });
    });
    Array.prototype.forEach.call(document.querySelectorAll('[data-close]'), function (btn) {
      btn.addEventListener('click', function () { closeOverlay(btn.getAttribute('data-close')); });
    });

    $('lockBtn').addEventListener('click', function () { lockVault('manual'); });
    $('menuBtn').addEventListener('click', function (event) {
      event.stopPropagation();
      toggleMenu();
    });
    $('addSecretBtn').addEventListener('click', function () { openEntry(null); });
    $('emptyAction').addEventListener('click', function () {
      if ($('emptyAction').getAttribute('data-mode') === 'clear') {
        clearFilters();
      } else {
        openEntry(null);
      }
    });
    $('addGroupBtn').addEventListener('click', function () { openGroup(null); });
    $('clearTagsBtn').addEventListener('click', function () {
      state.tagFilter = [];
      renderAll();
    });
    $('searchInput').addEventListener('input', function () {
      state.query = this.value;
      renderList();
      renderViewTitle();
    });

    $('entrySave').addEventListener('click', saveEntry);
    $('entryDelete').addEventListener('click', deleteSecret);
    $('entryCopy').addEventListener('click', function () { copyText($('entryValue').value); });
    $('entryName').addEventListener('keydown', function (event) {
      if (event.key === 'Enter') { event.preventDefault(); saveEntry(); }
    });
    $('entryTagInput').addEventListener('keydown', function (event) {
      if (event.key === 'Enter' || event.key === ',' || event.key === '，') {
        event.preventDefault();
        addEditorTag(this.value);
        this.value = '';
      } else if (event.key === 'Backspace' && this.value === '' && state.editorTags.length) {
        state.editorTags.pop();
        renderEditorTags();
      }
    });
    $('entryTagInput').addEventListener('blur', function () {
      if (this.value.trim()) {
        addEditorTag(this.value);
        this.value = '';
      }
    });

    $('groupSave').addEventListener('click', saveGroup);
    $('groupDelete').addEventListener('click', deleteGroup);
    $('groupName').addEventListener('keydown', function (event) {
      if (event.key === 'Enter') { event.preventDefault(); saveGroup(); }
    });

    $('pwSave').addEventListener('click', savePassword);
    $('pwConfirm').addEventListener('keydown', function (event) {
      if (event.key === 'Enter') { event.preventDefault(); savePassword(); }
    });
    $('confirmOk').addEventListener('click', function () {
      if (state.pendingConfirm) state.pendingConfirm(true);
    });

    Array.prototype.forEach.call(document.querySelectorAll('.overlay'), function (overlay) {
      overlay.addEventListener('mousedown', function (event) {
        if (event.target === overlay && overlay.id !== 'entryOverlay') closeOverlay(overlay.id);
      });
    });
    document.addEventListener('click', function () { closePopover(); });
    document.addEventListener('keydown', function (event) {
      if (event.key === 'Escape') {
        closePopover();
        var open = document.querySelector('.overlay:not(.hidden)');
        if (open) { closeOverlay(open.id); return; }
      }
      if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === 'k' && state.token) {
        event.preventDefault();
        $('searchInput').focus();
        $('searchInput').select();
      }
    });

    ['mousemove', 'keydown', 'click', 'touchstart'].forEach(function (name) {
      document.addEventListener(name, resetIdle, { passive: true });
    });
  }

  // ---------- 解锁 ----------

  function showLockView(mode) {
    state.mode = mode;
    closePopover();
    state.token = '';
    state.secrets = [];
    state.groups = [];
    state.tags = [];
    state.revealed = {};
    $('appView').classList.add('hidden');
    $('lockView').classList.remove('hidden');
    $('lockPassword').value = '';
    $('lockConfirm').value = '';
    $('lockPassword').type = 'password';
    $('lockConfirm').type = 'password';
    $('lockError').classList.add('hidden');
    var setup = mode === 'setup';
    $('lockConfirmWrap').classList.toggle('hidden', !setup);
    $('lockHint').classList.toggle('hidden', !setup);
    updateLockCopy();
    setTimeout(function () { $('lockPassword').focus(); }, 40);
  }

  function updateLockCopy() {
    var setup = state.mode === 'setup';
    document.title = t('vault.app.name');
    $('lockTitle').textContent = t(setup ? 'vault.lock.setupTitle' : 'vault.lock.unlockTitle');
    $('lockDesc').textContent = t(setup ? 'vault.lock.setupDesc' : 'vault.lock.unlockDesc');
    $('lockPassword').setAttribute('placeholder', t(setup ? 'vault.lock.newPlaceholder' : 'vault.lock.passwordPlaceholder'));
    $('lockSubmit').textContent = t(setup ? 'vault.lock.setupAction' : 'vault.lock.unlockAction');
  }

  function showLockError(message) {
    var box = $('lockError');
    box.textContent = message;
    box.classList.remove('hidden');
    $('lockForm').classList.remove('shake');
    void $('lockForm').offsetWidth;
    $('lockForm').classList.add('shake');
  }

  async function submitLock(event) {
    event.preventDefault();
    var password = $('lockPassword').value;
    if (state.mode === 'setup') {
      if (password.length < 8) return showLockError(t('vault.lock.errShort'));
      if (password !== $('lockConfirm').value) return showLockError(t('vault.lock.errMismatch'));
    } else if (!password) {
      return showLockError(t('vault.lock.errEmpty'));
    }
    var button = $('lockSubmit');
    button.disabled = true;
    $('lockError').classList.add('hidden');
    try {
      var data = state.mode === 'setup'
        ? await request('/setup', { method: 'POST', body: { password: password, groupName: t('vault.group.default') } })
        : await request('/unlock', { method: 'POST', body: { password: password } });
      state.token = data.token;
      state.initialized = true;
      applySnapshot(data.snapshot);
      enterApp();
    } catch (error) {
      if (error.status === 401) {
        showLockError(t('vault.lock.errWrong'));
      } else if (error.status === 409) {
        showLockError(t('vault.error.generic'));
      } else if (error.status === 400) {
        showLockError(t('vault.lock.errShort'));
      } else {
        showLockError(t('vault.error.network'));
      }
    } finally {
      button.disabled = false;
    }
  }

  function enterApp() {
    $('lockView').classList.add('hidden');
    $('appView').classList.remove('hidden');
    state.groupId = '';
    state.tagFilter = [];
    state.query = '';
    $('searchInput').value = '';
    renderAll();
    resetIdle();
  }

  async function lockVault(reason) {
    var token = state.token;
    state.token = '';
    clearTimeout(state.idleTimer);
    if (token) {
      try { await request('/lock', { method: 'POST' }); } catch (_) { /* 已失效时忽略 */ }
    }
    showLockView(state.initialized ? 'unlock' : 'setup');
    toast(t(reason === 'idle' ? 'vault.toast.idleLocked' : 'vault.toast.locked'));
  }

  function handleExpired() {
    if (!state.token) return;
    state.token = '';
    clearTimeout(state.idleTimer);
    showLockView('unlock');
    toast(t('vault.toast.sessionExpired'), true);
  }

  function resetIdle() {
    clearTimeout(state.idleTimer);
    if (!state.token) return;
    state.idleTimer = setTimeout(function () { lockVault('idle'); }, IDLE_MS);
  }

  // ---------- 渲染 ----------

  function renderAll() {
    renderGroups();
    renderTags();
    renderFilterBar();
    renderViewTitle();
    renderList();
  }

  function countInGroup(groupId) {
    if (!groupId) return state.secrets.length;
    return state.secrets.filter(function (item) { return item.groupId === groupId; }).length;
  }

  function findGroup(id) {
    for (var i = 0; i < state.groups.length; i++) {
      if (state.groups[i].id === id) return state.groups[i];
    }
    return null;
  }

  function findSecret(id) {
    for (var i = 0; i < state.secrets.length; i++) {
      if (state.secrets[i].id === id) return state.secrets[i];
    }
    return null;
  }

  function renderGroups() {
    var list = $('groupList');
    list.innerHTML = '';
    list.appendChild(groupRow(null, t('vault.view.all'), countInGroup(''), true));
    state.groups.forEach(function (group) {
      list.appendChild(groupRow(group, group.name, countInGroup(group.id), false));
    });
  }

  function groupRow(group, name, count, isAll) {
    var row = el('div', 'group-row' + (state.groupId === (group ? group.id : '') ? ' is-active' : ''));
    var main = el('button', 'group-main');
    main.type = 'button';
    var dot = el('span', 'group-dot');
    dot.style.background = group ? group.color : 'linear-gradient(135deg,#818cf8,#6366f1)';
    main.appendChild(dot);
    main.appendChild(el('span', 'group-name', name));
    main.appendChild(el('span', 'group-count', String(count)));
    main.addEventListener('click', function () {
      state.groupId = group ? group.id : '';
      renderAll();
    });
    row.appendChild(main);
    if (!isAll) {
      var edit = el('button', 'group-edit');
      edit.type = 'button';
      edit.title = t('vault.group.edit');
      edit.innerHTML = iconHtml('edit');
      edit.addEventListener('click', function (event) {
        event.stopPropagation();
        openGroup(group);
      });
      row.appendChild(edit);
    }
    return row;
  }

  function renderTags() {
    var box = $('tagList');
    box.innerHTML = '';
    state.tags.forEach(function (tag) {
      var active = state.tagFilter.some(function (item) { return item.toLowerCase() === tag.name.toLowerCase(); });
      var chip = el('button', 'tag-chip' + (active ? ' is-active' : ''));
      chip.type = 'button';
      chip.appendChild(el('span', '', tag.name));
      chip.appendChild(el('span', 'tag-n', String(tag.count)));
      chip.addEventListener('click', function () {
        var key = tag.name.toLowerCase();
        var index = state.tagFilter.findIndex(function (item) { return item.toLowerCase() === key; });
        if (index >= 0) state.tagFilter.splice(index, 1);
        else state.tagFilter.push(tag.name);
        renderAll();
      });
      box.appendChild(chip);
    });
    $('tagEmpty').classList.toggle('hidden', state.tags.length > 0);
    $('clearTagsBtn').classList.toggle('hidden', state.tagFilter.length === 0);
  }

  function renderFilterBar() {
    var bar = $('filterBar');
    bar.innerHTML = '';
    if (!state.tagFilter.length) {
      bar.classList.add('hidden');
      return;
    }
    bar.classList.remove('hidden');
    bar.appendChild(el('span', 'filter-label', t('vault.filter.label')));
    state.tagFilter.forEach(function (tag) {
      var chip = el('span', 'filter-chip');
      chip.appendChild(el('span', '', tag));
      var remove = el('button');
      remove.type = 'button';
      remove.innerHTML = iconHtml('close');
      remove.title = t('vault.tags.remove');
      remove.addEventListener('click', function () {
        state.tagFilter = state.tagFilter.filter(function (item) { return item.toLowerCase() !== tag.toLowerCase(); });
        renderAll();
      });
      chip.appendChild(remove);
      bar.appendChild(chip);
    });
    var clear = el('button', 'tag-chip', t('vault.tags.clear'));
    clear.type = 'button';
    clear.addEventListener('click', function () {
      state.tagFilter = [];
      renderAll();
    });
    bar.appendChild(clear);
  }

  function visibleSecrets() {
    var query = state.query.trim().toLowerCase();
    var tags = state.tagFilter.map(function (item) { return item.toLowerCase(); });
    return state.secrets.filter(function (item) {
      if (state.groupId && item.groupId !== state.groupId) return false;
      if (tags.length) {
        var own = (item.tags || []).map(function (tag) { return tag.toLowerCase(); });
        for (var i = 0; i < tags.length; i++) {
          if (own.indexOf(tags[i]) < 0) return false;
        }
      }
      if (!query) return true;
      var haystack = [item.name, item.key, item.note].concat(item.tags || []).join(' ').toLowerCase();
      return haystack.indexOf(query) >= 0;
    });
  }

  function renderViewTitle() {
    var group = state.groupId ? findGroup(state.groupId) : null;
    $('viewTitle').textContent = group ? group.name : t('vault.view.all');
    $('viewCount').textContent = t('vault.view.count', { n: visibleSecrets().length });
  }

  function renderList() {
    var list = visibleSecrets();
    var grid = $('secretGrid');
    grid.innerHTML = '';
    grid.classList.toggle('hidden', list.length === 0);
    var empty = $('emptyState');
    empty.classList.toggle('hidden', list.length > 0);
    list.forEach(function (secret) { grid.appendChild(secretCard(secret)); });

    var filtered = state.tagFilter.length > 0 || !!state.query.trim() || !!state.groupId;
    if (list.length === 0) {
      $('emptyTitle').textContent = t(filtered ? 'vault.empty.filteredTitle' : 'vault.empty.title');
      $('emptyDesc').textContent = t(filtered ? 'vault.empty.filteredDesc' : 'vault.empty.desc');
      var action = $('emptyAction');
      if (filtered) {
        action.setAttribute('data-mode', 'clear');
        action.textContent = t('vault.empty.clear');
      } else {
        action.setAttribute('data-mode', 'new');
        action.textContent = t('vault.secret.new');
      }
    }
    $('searchInput').value = state.query;
  }

  function secretCard(secret) {
    var group = findGroup(secret.groupId);
    var card = el('div', 'secret-card');
    card.tabIndex = 0;
    card.addEventListener('click', function () { openEntry(secret); });
    card.addEventListener('keydown', function (event) {
      if (event.key === 'Enter' || event.key === ' ') {
        event.preventDefault();
        openEntry(secret);
      }
    });

    var top = el('div', 'card-top');
    var dot = el('span', 'card-dot');
    dot.style.background = group ? group.color : '#94a3b8';
    top.appendChild(dot);
    top.appendChild(el('span', 'card-name', secret.name));
    var copyBtn = el('button', 'card-action');
    copyBtn.type = 'button';
    copyBtn.title = t('vault.secret.copy');
    copyBtn.innerHTML = iconHtml('copy');
    copyBtn.addEventListener('click', function (event) {
      event.stopPropagation();
      copyText(secret.value);
    });
    top.appendChild(copyBtn);
    card.appendChild(top);

    card.appendChild(el('div', 'card-key', secret.key || t('vault.secret.noKey')));

    var valueBox = el('div', 'card-value');
    var revealed = !!state.revealed[secret.id];
    var valueText = el('span', 'val-text' + (revealed ? '' : ' is-marked'), secret.value ? (revealed ? secret.value : '••••••••') : t('vault.secret.emptyValue'));
    valueBox.appendChild(valueText);
    var eyeBtn = el('button');
    eyeBtn.type = 'button';
    eyeBtn.title = t(revealed ? 'vault.secret.hide' : 'vault.secret.reveal');
    eyeBtn.innerHTML = iconHtml(revealed ? 'eyeOff' : 'eye');
    eyeBtn.addEventListener('click', function (event) {
      event.stopPropagation();
      if (state.revealed[secret.id]) delete state.revealed[secret.id];
      else state.revealed[secret.id] = true;
      renderList();
    });
    valueBox.appendChild(eyeBtn);
    card.appendChild(valueBox);

    if ((secret.tags || []).length) {
      var tags = el('div', 'card-tags');
      secret.tags.forEach(function (tag) { tags.appendChild(el('span', 'mini-tag', tag)); });
      card.appendChild(tags);
    }

    var foot = el('div', 'card-foot');
    var clock = el('span');
    clock.innerHTML = iconHtml('clock');
    foot.appendChild(clock);
    foot.appendChild(el('span', '', relativeTime(secret.updatedAt)));
    card.appendChild(foot);
    return card;
  }

  function relativeTime(iso) {
    var date = new Date(iso);
    if (!iso || isNaN(date.getTime())) return '';
    var seconds = Math.max(0, (Date.now() - date.getTime()) / 1000);
    if (seconds < 60) return t('vault.time.justNow');
    if (seconds < 3600) return t('vault.time.minutes', { n: Math.floor(seconds / 60) });
    if (seconds < 86400) return t('vault.time.hours', { n: Math.floor(seconds / 3600) });
    if (seconds < 604800) return t('vault.time.days', { n: Math.floor(seconds / 86400) });
    try {
      return new Intl.DateTimeFormat(i18n.bcp47(), { year: 'numeric', month: 'short', day: 'numeric' }).format(date);
    } catch (_) {
      return date.toLocaleDateString();
    }
  }

  // ---------- 条目编辑 ----------

  function openEntry(secret) {
    closePopover();
    state.editingId = secret ? secret.id : '';
    state.editorTags = secret ? (secret.tags || []).slice() : [];
    $('entryTitle').textContent = t(secret ? 'vault.secret.edit' : 'vault.secret.new');
    $('entryName').value = secret ? secret.name : '';
    $('entryKey').value = secret ? secret.key : '';
    $('entryValue').value = secret ? secret.value : '';
    $('entryNote').value = secret ? secret.note : '';
    $('entryDelete').classList.toggle('hidden', !secret);
    $('entryError').classList.add('hidden');

    var select = $('entryGroup');
    select.innerHTML = '';
    var preferred = secret ? secret.groupId : (state.groupId || (state.groups[0] && state.groups[0].id));
    state.groups.forEach(function (group) {
      var option = document.createElement('option');
      option.value = group.id;
      option.textContent = group.name;
      if (group.id === preferred) option.selected = true;
      select.appendChild(option);
    });
    $('entryTagInput').value = '';
    renderEditorTags();
    showOverlay('entryOverlay');
    setTimeout(function () { $('entryName').focus(); }, 40);
  }

  function renderEditorTags() {
    var box = $('entryTags');
    Array.prototype.forEach.call(box.querySelectorAll('.editor-tag'), function (node) { node.remove(); });
    var input = $('entryTagInput');
    state.editorTags.forEach(function (tag) {
      var chip = el('span', 'editor-tag');
      chip.appendChild(el('span', '', tag));
      var remove = el('button');
      remove.type = 'button';
      remove.innerHTML = iconHtml('close');
      remove.addEventListener('click', function () {
        state.editorTags = state.editorTags.filter(function (item) { return item !== tag; });
        renderEditorTags();
      });
      chip.appendChild(remove);
      box.insertBefore(chip, input);
    });
  }

  function addEditorTag(raw) {
    var tag = String(raw || '').trim().slice(0, 24);
    if (!tag) return;
    if (state.editorTags.some(function (item) { return item.toLowerCase() === tag.toLowerCase(); })) return;
    if (state.editorTags.length >= 12) {
      toast(t('vault.tags.limit'), true);
      return;
    }
    state.editorTags.push(tag);
    renderEditorTags();
  }

  async function saveEntry() {
    var name = $('entryName').value.trim();
    if (!name) {
      showEntryError(t('vault.secret.errName'));
      return;
    }
    if ($('entryTagInput').value.trim()) {
      addEditorTag($('entryTagInput').value);
      $('entryTagInput').value = '';
    }
    var body = {
      groupId: $('entryGroup').value,
      name: name,
      key: $('entryKey').value,
      value: $('entryValue').value,
      note: $('entryNote').value,
      tags: state.editorTags.slice()
    };
    var button = $('entrySave');
    button.disabled = true;
    $('entryError').classList.add('hidden');
    try {
      var data = state.editingId
        ? await request('/secrets/' + state.editingId, { method: 'PUT', body: body })
        : await request('/secrets', { method: 'POST', body: body });
      applySnapshot(data);
      state.editingId = '';
      closeOverlay('entryOverlay');
      renderAll();
      toast(t('vault.toast.saved'));
    } catch (error) {
      if (error.status !== 401) showEntryError(errorMessage(error));
    } finally {
      button.disabled = false;
    }
  }

  function showEntryError(message) {
    var box = $('entryError');
    box.textContent = message;
    box.classList.remove('hidden');
  }

  async function deleteSecret() {
    var secret = findSecret(state.editingId);
    if (!secret) return;
    var ok = await askConfirm({
      title: t('vault.secret.deleteTitle'),
      body: t('vault.secret.deleteBody', { name: secret.name }),
      ok: t('vault.common.delete')
    });
    if (!ok) return;
    try {
      var data = await request('/secrets/' + secret.id, { method: 'DELETE' });
      applySnapshot(data);
      state.editingId = '';
      closeOverlay('entryOverlay');
      renderAll();
      toast(t('vault.toast.deleted'));
    } catch (error) {
      if (error.status !== 401) showEntryError(errorMessage(error));
    }
  }

  // ---------- 分组 ----------

  function openGroup(group) {
    state.editingGroupId = group ? group.id : '';
    state.groupColor = group ? group.color : PALETTE[0];
    $('groupTitle').textContent = t(group ? 'vault.group.edit' : 'vault.group.new');
    $('groupName').value = group ? group.name : '';
    $('groupDelete').classList.toggle('hidden', !group);
    $('groupError').classList.add('hidden');
    renderSwatches();
    showOverlay('groupOverlay');
    setTimeout(function () { $('groupName').focus(); }, 40);
  }

  function renderSwatches() {
    var box = $('groupColors');
    box.innerHTML = '';
    PALETTE.forEach(function (color) {
      var swatch = el('button', 'color-swatch' + (state.groupColor === color ? ' is-active' : ''));
      swatch.type = 'button';
      swatch.style.background = color;
      swatch.title = color;
      swatch.addEventListener('click', function () {
        state.groupColor = color;
        renderSwatches();
      });
      box.appendChild(swatch);
    });
  }

  async function saveGroup() {
    var name = $('groupName').value.trim();
    if (!name) {
      showGroupError(t('vault.group.errName'));
      return;
    }
    var body = { name: name, color: state.groupColor };
    var button = $('groupSave');
    button.disabled = true;
    $('groupError').classList.add('hidden');
    try {
      var data = state.editingGroupId
        ? await request('/groups/' + state.editingGroupId, { method: 'PUT', body: body })
        : await request('/groups', { method: 'POST', body: body });
      applySnapshot(data);
      state.editingGroupId = '';
      closeOverlay('groupOverlay');
      renderAll();
      toast(t('vault.toast.groupSaved'));
    } catch (error) {
      if (error.status !== 401) showGroupError(errorMessage(error));
    } finally {
      button.disabled = false;
    }
  }

  function showGroupError(message) {
    var box = $('groupError');
    box.textContent = message;
    box.classList.remove('hidden');
  }

  async function deleteGroup() {
    var group = state.editingGroupId ? findGroup(state.editingGroupId) : null;
    if (!group) return;
    if (state.groups.length <= 1) {
      showGroupError(t('vault.group.errLast'));
      return;
    }
    var count = countInGroup(group.id);
    var ok = await askConfirm({
      title: t('vault.group.deleteTitle'),
      body: t('vault.group.deleteBody', { name: group.name, n: count }),
      ok: t('vault.common.delete')
    });
    if (!ok) return;
    try {
      var data = await request('/groups/' + group.id, { method: 'DELETE' });
      applySnapshot(data);
      if (state.groupId === group.id) state.groupId = '';
      state.editingGroupId = '';
      closeOverlay('groupOverlay');
      renderAll();
      toast(t('vault.toast.groupDeleted'));
    } catch (error) {
      if (error.status !== 401) showGroupError(errorMessage(error));
    }
  }

  // ---------- 修改密码 ----------

  function openPassword() {
    $('pwOld').value = '';
    $('pwNew').value = '';
    $('pwConfirm').value = '';
    $('pwError').classList.add('hidden');
    showOverlay('passwordOverlay');
    setTimeout(function () { $('pwOld').focus(); }, 40);
  }

  async function savePassword() {
    var oldPassword = $('pwOld').value;
    var newPassword = $('pwNew').value;
    if (!oldPassword) return showPwError(t('vault.password.errOldEmpty'));
    if (newPassword.length < 8) return showPwError(t('vault.password.errShort'));
    if (newPassword !== $('pwConfirm').value) return showPwError(t('vault.password.errMismatch'));
    var button = $('pwSave');
    button.disabled = true;
    $('pwError').classList.add('hidden');
    try {
      var data = await request('/password', { method: 'POST', body: { oldPassword: oldPassword, newPassword: newPassword } });
      state.token = data.token;
      applySnapshot(data.snapshot);
      closeOverlay('passwordOverlay');
      renderAll();
      resetIdle();
      toast(t('vault.password.success'));
    } catch (error) {
      if (error.status === 401) showPwError(t('vault.password.errOld'));
      else if (error.status !== 401) showPwError(errorMessage(error));
    } finally {
      button.disabled = false;
    }
  }

  function showPwError(message) {
    var box = $('pwError');
    box.textContent = message;
    box.classList.remove('hidden');
  }

  // ---------- 菜单 / 浮层 / 提示 ----------

  function toggleMenu() {
    var popover = $('popover');
    if (!popover.classList.contains('hidden')) {
      closePopover();
      return;
    }
    popover.innerHTML = '';
    var change = menuItem('lock', t('vault.menu.changePassword'), function () {
      closePopover();
      openPassword();
    });
    var lock = menuItem('key', t('vault.menu.lock'), function () {
      closePopover();
      lockVault('manual');
    });
    popover.appendChild(change);
    popover.appendChild(lock);
    popover.appendChild(el('div', 'menu-sep'));
    popover.appendChild(el('div', 'menu-hint', t('vault.menu.hint')));
    popover.classList.remove('hidden');
    var rect = $('menuBtn').getBoundingClientRect();
    popover.style.left = Math.round(rect.left) + 'px';
    popover.style.top = 'auto';
    popover.style.bottom = Math.round(window.innerHeight - rect.top + 6) + 'px';
    popover.addEventListener('click', function (event) { event.stopPropagation(); });
  }

  function menuItem(icon, label, handler) {
    var item = el('button', 'menu-item');
    item.type = 'button';
    var box = el('span', 'mi-icon');
    box.innerHTML = iconHtml(icon);
    item.appendChild(box);
    item.appendChild(el('span', 'mi-label', label));
    item.addEventListener('click', handler);
    return item;
  }

  function closePopover() {
    $('popover').classList.add('hidden');
  }

  function showOverlay(id) {
    $('popover').classList.add('hidden');
    $(id).classList.remove('hidden');
  }

  function closeOverlay(id) {
    $(id).classList.add('hidden');
    if (id === 'confirmOverlay' && state.pendingConfirm) {
      state.pendingConfirm(false);
    }
  }

  function askConfirm(options) {
    return new Promise(function (resolve) {
      $('confirmTitle').textContent = options.title;
      $('confirmBody').textContent = options.body;
      $('confirmOk').textContent = options.ok || t('vault.common.delete');
      $('confirmOverlay').classList.remove('hidden');
      var done = false;
      state.pendingConfirm = function (ok) {
        if (done) return;
        done = true;
        state.pendingConfirm = null;
        $('confirmOverlay').classList.add('hidden');
        resolve(ok);
      };
    });
  }

  function toast(message, isError) {
    var box = $('toast');
    box.textContent = message;
    box.classList.toggle('is-error', !!isError);
    box.classList.remove('hidden');
    clearTimeout(state.toastTimer);
    state.toastTimer = setTimeout(function () { box.classList.add('hidden'); }, 2400);
  }

  function clearFilters() {
    state.tagFilter = [];
    state.query = '';
    state.groupId = '';
    state.revealed = {};
    renderAll();
  }

  async function copyText(text) {
    if (!text) {
      toast(t('vault.toast.nothingToCopy'), true);
      return;
    }
    try {
      await navigator.clipboard.writeText(text);
      toast(t('vault.toast.copied'));
      return;
    } catch (_) {
      /* 回退到 execCommand */
    }
    var area = document.createElement('textarea');
    area.value = text;
    area.style.position = 'fixed';
    area.style.opacity = '0';
    document.body.appendChild(area);
    area.select();
    try {
      document.execCommand('copy');
      toast(t('vault.toast.copied'));
    } catch (error) {
      toast(t('vault.toast.copyFailed'), true);
    }
    document.body.removeChild(area);
  }

  function errorMessage(error) {
    if (!error) return t('vault.error.generic');
    if (error.status === 404) return t('vault.error.notFound');
    return t('vault.error.generic');
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', init);
  } else {
    init();
  }
})();
