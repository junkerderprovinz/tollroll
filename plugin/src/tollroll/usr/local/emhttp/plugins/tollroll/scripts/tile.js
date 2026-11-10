/* Fills the TollRoll tile from the same-origin proxy every 15 seconds while
 * the dashboard is visible. Everything from the apps goes in as text, never
 * as HTML. */
(function () {
  'use strict';
  var C = window.TollRollCore, L = window.TRL_T || {}, lang = window.TRL_LANG || 'en';
  var root = document.getElementById('trl');
  if (!C || !root) return;

  var rowsEl = document.getElementById('trl-rows');
  var chipsEl = document.getElementById('trl-chips');
  var logEl = document.getElementById('trl-log');
  var subEl = document.getElementById('trl-sub');
  var openEl = document.getElementById('trl-open');
  var STORE = 'tollroll.filter';
  var GLYPH = {
    info: '<svg viewBox="0 0 16 16" aria-hidden="true"><path d="M3 8.5l3.2 3L13 4.5" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round"/></svg>',
    warn: '<svg viewBox="0 0 16 16" aria-hidden="true"><path d="M8 1.8L15 14H1z" fill="currentColor"/><rect x="7.1" y="6" width="1.8" height="4.2" rx=".9" fill="#161616"/><circle cx="8" cy="12" r="1" fill="#161616"/></svg>',
    error: '<svg viewBox="0 0 16 16" aria-hidden="true"><path d="M3.5 3.5l9 9M12.5 3.5l-9 9" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round"/></svg>'
  };

  var view = null, filter = 'all', follow = true, timer = null;
  // Logos by the ID a row carries. They come in a request of their own, made
  // only when a row names one not seen yet, so a poll stays small.
  var icons = {}, iconsAsked = {};
  try { filter = localStorage.getItem(STORE) || 'all'; } catch (e) { /* private window */ }

  function el(tag, cls, text) {
    var n = document.createElement(tag);
    if (cls) n.className = cls;
    if (text != null) n.textContent = text;
    return n;
  }

  // A logo goes in as an img only, never into the DOM as markup, so nothing
  // in it can run.
  function mark(app, small) {
    var src = C.safeIcon(icons[app.icon]);
    var m = el('span', 'trl-mark' + (small ? ' trl-small' : '') + (src ? ' trl-logo' : ''), src ? null : C.mark(app.name));
    m.setAttribute('aria-hidden', 'true');
    if (src) {
      var img = el('img');
      img.alt = '';
      img.src = src;
      img.width = img.height = small ? 14 : 20;
      img.decoding = 'async';
      img.draggable = false;
      m.appendChild(img);
      return m;
    }
    var colors = C.markColors(app.color);
    if (colors) {
      m.style.background = colors.bg;
      m.style.color = colors.fg;
    } else {
      m.style.setProperty('--h', String(C.hue(app.key)));
    }
    return m;
  }

  function loadIcons() {
    var missing = view.apps.some(function (a) { return a.icon && !(a.icon in icons) && !iconsAsked[a.icon]; });
    if (!missing) return;
    view.apps.forEach(function (a) { if (a.icon) iconsAsked[a.icon] = true; });
    fetch('/plugins/tollroll/server/api.php?path=icons', { cache: 'no-store', headers: { Accept: 'application/json' } })
      .then(function (r) { if (!r.ok) throw new Error(String(r.status)); return r.json(); })
      .then(function (v) {
        icons = {};
        Object.keys(v.icons || {}).forEach(function (id) {
          if (C.safeIcon(v.icons[id])) icons[id] = v.icons[id];
        });
        draw();
      })
      .catch(function () { /* the letters stay */ });
  }

  function byKey() {
    var out = {};
    view.apps.forEach(function (a) { out[a.key] = a; });
    return out;
  }

  function setFilter(key) {
    filter = key;
    try { localStorage.setItem(STORE, key); } catch (e) { /* private window */ }
    follow = true;
    draw();
  }

  function drawRows() {
    rowsEl.textContent = '';
    view.apps.forEach(function (app) {
      var st = C.rowStatus(app, L, lang);
      var on = filter === app.key;
      var li = el('li', 'trl-row' + (filter !== 'all' && !on ? ' trl-dim' : ''));
      li.setAttribute('role', 'button');
      li.tabIndex = 0;
      li.setAttribute('aria-pressed', String(on));
      li.setAttribute('aria-label', app.name + ', ' + st.text + '. ' + (on ? L.t_row_all : C.fmt(L.t_row_only, { name: app.name })));
      li.dataset.focus = 'row:' + app.key;
      li.appendChild(mark(app));
      li.appendChild(el('span', 'trl-name', app.name));
      var state = el('span', 'trl-state trl-' + st.tone);
      state.appendChild(el('span', 'trl-dot'));
      state.appendChild(el('span', '', st.text));
      state.title = st.text;
      li.appendChild(state);
      li.appendChild(el('span', 'trl-ver', app.version || ''));
      li.addEventListener('click', function () { setFilter(on ? 'all' : app.key); });
      li.addEventListener('keydown', function (e) {
        if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); setFilter(on ? 'all' : app.key); }
      });
      rowsEl.appendChild(li);
    });
  }

  // A single app needs no filter, so the chips start at two apps with lines.
  function drawChips() {
    chipsEl.textContent = '';
    var list = C.chips(view);
    if (list.length < 3) return;
    var apps = byKey();
    list.forEach(function (c) {
      var b = el('button', 'trl-chip');
      b.type = 'button';
      b.setAttribute('aria-pressed', String(filter === c.key));
      b.dataset.focus = 'chip:' + c.key;
      if (c.name) b.appendChild(mark(apps[c.key], true));
      b.appendChild(el('span', '', c.name || L.t_all));
      b.appendChild(el('span', 'trl-n', String(c.count)));
      b.addEventListener('click', function () { setFilter(c.key); });
      chipsEl.appendChild(b);
    });
  }

  function note(text, cls) {
    logEl.textContent = '';
    var n = el('div', 'trl-note' + (cls ? ' ' + cls : ''), text);
    logEl.appendChild(n);
    return n;
  }

  function drawLog() {
    if (!view.apps.length) {
      var n = note(L.t_empty);
      var a = el('a', '', L.t_empty_link);
      a.href = '/Settings/tollroll';
      n.appendChild(document.createTextNode(' '));
      n.appendChild(a);
      return;
    }
    var lines = C.filterLog(view.log, filter);
    if (!lines.length) { note(L.t_nolog); return; }
    var apps = byKey();
    var now = new Date();
    logEl.classList.toggle('trl-dated', lines.some(function (l) { return new Date(l.at).toDateString() !== now.toDateString(); }));
    var frag = document.createDocumentFragment();
    lines.forEach(function (l) {
      var row = el('div', 'trl-line');
      row.appendChild(el('span', 'trl-t', C.timeLabel(l.at, lang, now)));
      var level = Object.prototype.hasOwnProperty.call(GLYPH, l.level) ? l.level : 'info';
      var g = el('span', 'trl-g trl-l-' + level);
      g.innerHTML = GLYPH[level];
      row.appendChild(g);
      if (filter === 'all' && apps[l.app]) {
        var who = el('span', 'trl-app');
        who.appendChild(mark(apps[l.app], true));
        who.appendChild(el('span', '', apps[l.app].name));
        row.appendChild(who);
      }
      var m = el('span', 'trl-m', l.text);
      m.title = l.text;
      row.appendChild(m);
      frag.appendChild(row);
    });
    logEl.textContent = '';
    logEl.appendChild(frag);
    var app = apps[filter];
    logEl.setAttribute('aria-label', app ? C.fmt(L.t_log_of, { name: app.name }) : L.t_log);
    if (follow) logEl.scrollTop = logEl.scrollHeight;
  }

  // The open button appears once the filter names an app with a web page.
  function drawOpen() {
    var app = byKey()[filter];
    var href = app ? C.safeLink(app.ui) : '';
    openEl.hidden = !href;
    if (href) {
      openEl.href = href;
      openEl.title = C.fmt(L.t_open, { name: app.name });
      openEl.setAttribute('aria-label', openEl.title);
    }
  }

  // Every poll redraws the tile, so the focused row or chip is found again
  // by its key.
  function draw() {
    if (!view) return;
    var active = document.activeElement;
    var keep = active && root.contains(active) && active.dataset ? active.dataset.focus : null;
    filter = C.validFilter(view, filter);
    subEl.textContent = view.apps.length ? C.subtitle(view, L, lang) : '';
    drawRows();
    drawChips();
    drawLog();
    drawOpen();
    if (keep) {
      var all = root.querySelectorAll('[data-focus]');
      for (var i = 0; i < all.length; i++) {
        if (all[i].dataset.focus === keep) { all[i].focus(); break; }
      }
    }
  }

  logEl.addEventListener('scroll', function () {
    follow = logEl.scrollHeight - logEl.scrollTop - logEl.clientHeight < 24;
  });

  function load() {
    fetch('/plugins/tollroll/server/api.php?path=board&lang=' + encodeURIComponent(lang), { cache: 'no-store', headers: { Accept: 'application/json' } })
      .then(function (r) { if (!r.ok) throw new Error(String(r.status)); return r.json(); })
      .then(function (v) {
        view = { apps: v.apps || [], log: v.log || [] };
        draw();
        loadIcons();
      })
      .catch(function () {
        if (!view) note(L.t_engine, 'trl-l-error');
        else subEl.textContent = L.t_engine;
      });
  }

  function schedule() {
    clearInterval(timer);
    timer = null;
    if (document.hidden) return;
    load();
    timer = setInterval(load, 15000);
  }
  document.addEventListener('visibilitychange', schedule);
  schedule();
})();
