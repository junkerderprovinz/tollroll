/* Fills the TollRoll tile from the same-origin proxy every 15 seconds while
 * the dashboard is visible. Everything from the apps goes in as text, never
 * as HTML. The view and the apps chosen for it are kept on the server, so
 * every browser finds the tile the way it was left. */
(function () {
  'use strict';
  var C = window.TollRollCore, L = window.TRL_T || {}, lang = window.TRL_LANG || 'en';
  var root = document.getElementById('trl');
  if (!C || !root) return;

  var API = '/plugins/tollroll/server/api.php';
  var GLYPH = Object.assign({}, window.TollRollGlyphs, {
    info: '<svg viewBox="0 0 16 16" aria-hidden="true"><path d="M3 8.5l3.2 3L13 4.5" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round"/></svg>',
    warn: '<svg viewBox="0 0 16 16" aria-hidden="true"><path d="M8 1.8L15 14H1z" fill="currentColor"/><rect x="7.1" y="6" width="1.8" height="4.2" rx=".9" fill="#161616"/><circle cx="8" cy="12" r="1" fill="#161616"/></svg>',
    error: '<svg viewBox="0 0 16 16" aria-hidden="true"><path d="M3.5 3.5l9 9M12.5 3.5l-9 9" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round"/></svg>',
    caret: '<svg class="trl-caret" viewBox="0 0 10 10" aria-hidden="true"><path d="M2 3.5l3 3 3-3" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round"/></svg>'
  });
  var VIEWS = [['all', 't_v_all', 'IconDashboard'], ['apps', 't_v_apps', 'IconTabApps'], ['log', 't_v_log', 'IconLog'], ['map', 't_v_map', 'IconFleet']];
  var STORE = 'tollroll.filter';

  var tile = root.closest('tbody') || document.body;
  var subEl = document.getElementById('trl-sub');
  var openEl = document.getElementById('trl-open');
  var hsEl = document.getElementById('trl-hs');

  var view = null, fort = null, prefs = { view: 'all', hidden: [] };
  var filter = 'all', follow = true, timer = null, picking = null;
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

  function glyph(name, cls) {
    var s = el('span', 'trl-gl' + (cls ? ' ' + cls : ''));
    s.innerHTML = GLYPH[name] || '';
    return s;
  }

  function get(path) {
    return fetch(API + '?path=' + path, { cache: 'no-store', headers: { Accept: 'application/json' } })
      .then(function (r) { if (!r.ok) throw new Error(String(r.status)); return r.json(); });
  }

  // Unraid checks csrf_token on every POST to a plugin script.
  function post(path, data) {
    var body = new URLSearchParams();
    body.set('path', path);
    body.set('data', JSON.stringify(data));
    body.set('csrf_token', window.csrf_token || '');
    return fetch(API, { method: 'POST', body: body, headers: { Accept: 'application/json' } })
      .then(function (r) { if (!r.ok) throw new Error(String(r.status)); return r.json(); });
  }

  var rowsHead = el('div', 'trl-apphd');
  var rowsEl = el('ul', 'trl-rows');
  var chipsEl = el('div', 'trl-chips');
  chipsEl.setAttribute('role', 'group');
  chipsEl.setAttribute('aria-label', L.t_filter);
  var logBox = el('div', 'trl-logbox');
  var logHead = el('div', 'trl-loghd');
  var logCount = el('span', 'trl-n');
  var logEl = el('div', 'trl-log');
  logEl.setAttribute('role', 'log');
  logEl.tabIndex = 0;
  logBox.appendChild(logHead);
  logBox.appendChild(logEl);
  var mapBox = el('div', 'trl-mapbox');
  var emptyEl = el('div', 'trl-empty');
  [rowsHead, rowsEl, chipsEl, logBox, mapBox, emptyEl].forEach(function (n) { root.appendChild(n); });

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
    get('icons')
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

  // A failed save keeps the choice on this page, and the next change sends
  // it again.
  function save() {
    post('tile', prefs).catch(function () { /* see above */ });
  }

  function setView(v) {
    prefs.view = v;
    picking = null;
    follow = true;
    save();
    draw();
  }

  function setHidden(list) {
    prefs.hidden = list;
    follow = true;
    save();
    draw();
  }

  function setFilter(key) {
    filter = key;
    try { localStorage.setItem(STORE, key); } catch (e) { /* private window */ }
    follow = true;
    draw();
  }

  // A click on an app in the apps view or on the map shows its log alone.
  function showLog(key) {
    setHidden(view.apps.map(function (a) { return a.key; }).filter(function (k) { return k !== key; }));
    setView('log');
  }

  function drawHS() {
    hsEl.hidden = !fort || !fort.joined;
    hsEl.textContent = '';
    VIEWS.forEach(function (v) {
      var on = prefs.view === v[0];
      var b = el('button');
      b.type = 'button';
      b.setAttribute('role', 'radio');
      b.setAttribute('aria-checked', String(on));
      b.tabIndex = on ? 0 : -1;
      b.title = L[v[1]];
      b.setAttribute('aria-label', L[v[1]]);
      b.dataset.focus = 'view:' + v[0];
      b.appendChild(glyph(v[2]));
      b.appendChild(el('span', 'trl-hs-word', L[v[1]]));
      b.addEventListener('click', function () { setView(v[0]); });
      hsEl.appendChild(b);
    });
  }

  hsEl.addEventListener('keydown', function (e) {
    if (e.key !== 'ArrowRight' && e.key !== 'ArrowLeft') return;
    e.preventDefault();
    var i = VIEWS.findIndex(function (v) { return v[0] === prefs.view; });
    var next = VIEWS[(i + (e.key === 'ArrowRight' ? 1 : VIEWS.length - 1)) % VIEWS.length][0];
    setView(next);
    var b = hsEl.querySelector('[data-focus="view:' + next + '"]');
    if (b) b.focus();
  });

  function drawRows(apps, onlyChosen) {
    rowsEl.textContent = '';
    apps.forEach(function (app) {
      var st = C.rowStatus(app, L, lang);
      var on = !onlyChosen && filter === app.key;
      var li = el('li', 'trl-row' + (!onlyChosen && filter !== 'all' && !on ? ' trl-dim' : ''));
      li.setAttribute('role', 'button');
      li.tabIndex = 0;
      if (!onlyChosen) li.setAttribute('aria-pressed', String(on));
      var does = onlyChosen ? C.fmt(L.t_show_log, { name: app.name }) : on ? L.t_row_all : C.fmt(L.t_row_only, { name: app.name });
      li.setAttribute('aria-label', app.name + ', ' + st.text + '. ' + does);
      li.dataset.focus = 'row:' + app.key;
      li.appendChild(mark(app));
      li.appendChild(el('span', 'trl-name', app.name));
      var state = el('span', 'trl-state trl-' + st.tone);
      state.appendChild(el('span', 'trl-dot'));
      state.appendChild(el('span', '', st.text));
      state.title = st.text;
      li.appendChild(state);
      li.appendChild(el('span', 'trl-ver', app.version || ''));
      function act() { if (onlyChosen) showLog(app.key); else setFilter(on ? 'all' : app.key); }
      li.addEventListener('click', act);
      li.addEventListener('keydown', function (e) {
        if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); act(); }
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

  // The app list ticks apps on and off for the apps and log views. It stays
  // open while ticking and closes on Escape or a click elsewhere.
  function drawPicker(into, where) {
    var counts = {};
    view.log.forEach(function (l) { counts[l.app] = (counts[l.app] || 0) + 1; });
    var n = C.shown(view.apps, prefs.hidden).length;
    var b = el('button', 'trl-pick' + (where === 'apps' ? ' trl-pick-light' : ''));
    b.type = 'button';
    b.setAttribute('aria-haspopup', 'true');
    b.setAttribute('aria-expanded', String(picking === where));
    b.title = L.t_pick;
    b.dataset.focus = 'pick:' + where;
    b.appendChild(glyph('IconFilter'));
    b.appendChild(el('span', '', C.selLabel(L, lang, n, view.apps.length)));
    b.appendChild(glyph('caret'));
    b.addEventListener('click', function (e) {
      e.stopPropagation();
      picking = picking === where ? null : where;
      draw();
    });
    into.appendChild(b);
    if (picking !== where) return;

    var list = el('div', 'trl-pick-list');
    list.setAttribute('role', 'group');
    list.setAttribute('aria-label', L.t_pick);
    list.addEventListener('click', function (e) { e.stopPropagation(); });
    var top = el('div', 'trl-pick-top');
    [['IconSelectAll', L.t_all, []], ['IconClearSelection', L.t_none, null]].forEach(function (t) {
      var tb = el('button');
      tb.type = 'button';
      tb.dataset.focus = 'pickall:' + where + ':' + (t[2] ? 'all' : 'none');
      tb.appendChild(glyph(t[0]));
      tb.appendChild(el('span', '', t[1]));
      tb.addEventListener('click', function () {
        setHidden(t[2] || view.apps.map(function (a) { return a.key; }));
      });
      top.appendChild(tb);
    });
    list.appendChild(top);
    view.apps.forEach(function (app) {
      var item = el('label', 'trl-pick-item');
      var box = el('input');
      box.type = 'checkbox';
      box.checked = prefs.hidden.indexOf(app.key) < 0;
      box.dataset.focus = 'pickapp:' + where + ':' + app.key;
      box.addEventListener('change', function () {
        setHidden(box.checked ? prefs.hidden.filter(function (k) { return k !== app.key; }) : prefs.hidden.concat([app.key]));
      });
      item.appendChild(box);
      item.appendChild(glyph('IconCheck', 'trl-check'));
      item.appendChild(mark(app, true));
      item.appendChild(el('span', '', app.name));
      item.appendChild(el('span', 'trl-n', String(counts[app.key] || 0)));
      list.appendChild(item);
    });
    into.appendChild(list);
  }

  document.addEventListener('click', function () {
    if (picking) { picking = null; draw(); }
  });
  document.addEventListener('keydown', function (e) {
    if (e.key === 'Escape' && picking) {
      var where = picking;
      picking = null;
      draw();
      var b = root.querySelector('[data-focus="pick:' + where + '"]');
      if (b) b.focus();
    }
  });

  function note(text, cls) {
    logEl.textContent = '';
    logEl.appendChild(el('div', 'trl-note' + (cls ? ' ' + cls : ''), text));
  }

  function drawLog(lines, one) {
    if (!lines.length) { note(view.apps.length ? L.t_nolog : L.t_empty); return; }
    var apps = byKey();
    var now = new Date();
    logEl.classList.toggle('trl-dated', lines.some(function (l) { return new Date(l.at).toDateString() !== now.toDateString(); }));
    var frag = document.createDocumentFragment();
    lines.forEach(function (l) {
      var row = el('div', 'trl-line');
      row.appendChild(el('span', 'trl-t', C.timeLabel(l.at, lang, now)));
      var level = ['info', 'warn', 'error'].indexOf(l.level) >= 0 ? l.level : 'info';
      var g = el('span', 'trl-g trl-l-' + level);
      g.innerHTML = GLYPH[level];
      row.appendChild(g);
      if (!one && apps[l.app]) {
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
    logEl.setAttribute('aria-label', one ? C.fmt(L.t_log_of, { name: one.name }) : L.t_log);
    if (follow) logEl.scrollTop = logEl.scrollHeight;
  }

  // The log view keeps the height the tile has with everything on it, so the
  // dashboard does not jump when the view changes.
  function fillHeight() {
    drawRows(view.apps, false);
    drawChips();
    var probe = [rowsEl, chipsEl];
    probe.forEach(function (n) { n.hidden = false; n.style.cssText = 'visibility:hidden;position:absolute;width:' + root.clientWidth + 'px'; });
    var extra = rowsEl.offsetHeight + chipsEl.offsetHeight + 12;
    probe.forEach(function (n) { n.hidden = true; n.style.cssText = ''; });
    logEl.style.height = Math.max(260, 260 + extra - logHead.offsetHeight) + 'px';
  }

  function drawMap() {
    mapBox.textContent = '';
    var devices = C.mapDevices(fort, view.apps);
    var map = el('div', 'trl-map');
    var wires = document.createElementNS('http://www.w3.org/2000/svg', 'svg');
    wires.setAttribute('class', 'trl-wires');
    wires.setAttribute('aria-hidden', 'true');
    map.appendChild(wires);
    var here = el('div', 'trl-here'), others = el('div', 'trl-devs');
    devices.forEach(function (d) { (d.here ? here : others).appendChild(device(d)); });
    if (devices.length < 2) others.appendChild(el('p', 'trl-note', L.t_alone));
    map.appendChild(here);
    map.appendChild(others);
    mapBox.appendChild(map);
    var legend = el('div', 'trl-legend');
    [['trl-ok', L.t_connected], ['trl-off', L.t_st_unreachable]].forEach(function (t) {
      var tag = el('span', 'trl-tag ' + t[0]);
      tag.appendChild(el('i'));
      tag.appendChild(document.createTextNode(t[1]));
      legend.appendChild(tag);
    });
    legend.appendChild(el('span', 'trl-legend-hint', L.t_map_hint));
    mapBox.appendChild(legend);
    requestAnimationFrame(drawWires);
  }

  function device(d) {
    var box = el('div', 'trl-dev' + (d.online ? '' : ' trl-gone'));
    box.dataset.link = d.here ? 'here' : d.online ? 'on' : 'off';
    var hd = el('div', 'trl-dev-hd');
    hd.appendChild(el('span', 'trl-dev-name', d.name));
    var tag = el('span', 'trl-tag' + (d.here ? '' : d.online ? ' trl-ok' : ' trl-off'));
    if (!d.here) tag.appendChild(el('i'));
    tag.appendChild(document.createTextNode(d.here ? L.t_here : d.online ? L.t_connected : L.t_st_unreachable));
    hd.appendChild(tag);
    box.appendChild(hd);
    var ul = el('ul');
    d.members.forEach(function (m) {
      var li = el('li', m.quiet ? 'trl-quiet' : '');
      li.appendChild(mark(m.row || { key: m.app, name: m.name }, true));
      var name = el('span', '', m.name);
      if (m.quiet) name.appendChild(el('span', 'trl-quiet-word', ' ' + L.t_quiet));
      li.appendChild(name);
      li.appendChild(el('span', 'trl-ver', m.version));
      if (m.quiet) {
        li.title = L.t_quiet_hint;
      } else {
        // The row shows the worst instance of the app; a member that answers
        // is not the one that does not.
        var tone = !m.online ? 'off' : m.row.state === 'unreachable' ? 'ok' : C.rowStatus(m.row, L, lang).tone;
        var dot = el('span', 'trl-state trl-' + tone);
        dot.appendChild(el('span', 'trl-dot'));
        li.appendChild(dot);
        li.tabIndex = 0;
        li.setAttribute('role', 'button');
        li.title = C.fmt(L.t_show_log, { name: m.name });
        li.addEventListener('click', function () { showLog(m.app); });
        li.addEventListener('keydown', function (e) {
          if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); showLog(m.app); }
        });
      }
      ul.appendChild(li);
    });
    box.appendChild(ul);
    if (!d.online && d.seen) box.appendChild(el('div', 'trl-dev-seen', C.fmt(L.t_seen, { when: C.ago(d.seen, lang) })));
    return box;
  }

  // A wire runs from this server to every other device: solid while it is
  // connected, dotted while it does not answer.
  function drawWires() {
    var map = mapBox.querySelector('.trl-map'), svg = mapBox.querySelector('.trl-wires');
    if (!map || getComputedStyle(svg).display === 'none') return;
    var box = map.getBoundingClientRect();
    function at(n) { var b = n.getBoundingClientRect(); return { l: b.left - box.left, r: b.right - box.left, m: (b.top + b.bottom) / 2 - box.top }; }
    var here = map.querySelector('.trl-here .trl-dev');
    if (!here) return;
    var h = at(here);
    while (svg.firstChild) svg.removeChild(svg.firstChild);
    map.querySelectorAll('.trl-devs .trl-dev').forEach(function (n) {
      var d = at(n), c = (d.l - h.r) / 2;
      var p = document.createElementNS('http://www.w3.org/2000/svg', 'path');
      p.setAttribute('class', n.dataset.link === 'on' ? 'trl-wire-on' : 'trl-wire-off');
      p.setAttribute('d', 'M' + h.r + ',' + h.m + ' C' + (h.r + c) + ',' + h.m + ' ' + (d.l - c) + ',' + d.m + ' ' + d.l + ',' + d.m);
      svg.appendChild(p);
    });
  }
  if (window.ResizeObserver) new ResizeObserver(function () { if (prefs.view === 'map') drawWires(); }).observe(root);

  function drawEmpty() {
    emptyEl.textContent = '';
    var removed = fort && fort.removed;
    emptyEl.appendChild(glyph('IconFortress', 'trl-empty-gl'));
    var text = el('div', 'trl-empty-text');
    text.appendChild(el('b', '', removed ? L.t_removed_title : L.t_join_title));
    text.appendChild(el('span', '', removed ? L.t_removed_text : L.t_join_text));
    emptyEl.appendChild(text);
    var a = el('a', 'trl-btn trl-btn-primary');
    a.href = '/Settings/tollroll';
    a.appendChild(glyph('IconGear'));
    a.appendChild(el('span', '', L.t_empty_link));
    emptyEl.appendChild(a);
  }

  // The open button shows once the log is down to one app with a web page.
  // It keeps its place while hidden, so the selector does not move.
  function drawOpen(v) {
    var chosen = C.shown(view.apps, prefs.hidden);
    var app = v === 'all' ? byKey()[filter] : v === 'log' && chosen.length === 1 ? chosen[0] : null;
    var href = app ? C.safeLink(app.ui) : '';
    openEl.style.visibility = href ? '' : 'hidden';
    openEl.tabIndex = href ? 0 : -1;
    if (href) {
      openEl.href = href;
      openEl.title = C.fmt(L.t_open, { name: app.name });
      openEl.setAttribute('aria-label', openEl.title);
    }
  }

  function show(n, on) { n.hidden = !on; }

  // Every poll redraws the tile, so the focused control is found again by its
  // key.
  function draw() {
    if (!view || !fort) return;
    var active = document.activeElement;
    var keep = active && tile.contains(active) && active.dataset ? active.dataset.focus : null;
    var joined = fort.joined;
    var v = joined ? prefs.view : 'all';
    filter = C.validFilter(view, filter);
    var devices = C.mapDevices(fort, view.apps).length;
    subEl.textContent = !joined ? '' : v === 'map' ? C.plural(L, 't_map', devices, lang) : C.subtitle(view, L, lang, devices);
    drawHS();

    show(emptyEl, !joined);
    show(rowsHead, joined && v === 'apps');
    show(rowsEl, joined && (v === 'all' || v === 'apps'));
    show(chipsEl, joined && v === 'all');
    show(logBox, joined && (v === 'all' || v === 'log'));
    show(logHead, v === 'log');
    show(mapBox, joined && v === 'map');
    logBox.classList.toggle('trl-logbox-full', v === 'log');
    logEl.style.height = '';

    if (!joined) {
      drawEmpty();
    } else if (v === 'map') {
      drawMap();
    } else {
      var chosen = C.shown(view.apps, prefs.hidden);
      if (v === 'all') {
        drawRows(view.apps, false);
        drawChips();
        var apps = byKey();
        drawLog(C.filterLog(view.log, filter), filter === 'all' ? null : apps[filter]);
      } else if (v === 'apps') {
        rowsHead.textContent = '';
        drawPicker(rowsHead, 'apps');
        drawRows(chosen, true);
      } else {
        var keys = chosen.map(function (a) { return a.key; });
        var lines = view.log.filter(function (l) { return keys.indexOf(l.app) >= 0; });
        logHead.textContent = '';
        drawPicker(logHead, 'log');
        logCount.textContent = C.plural(L, 't_lines', lines.length, lang);
        logHead.appendChild(logCount);
        drawLog(lines, chosen.length === 1 ? chosen[0] : null);
        fillHeight();
      }
    }
    drawOpen(v);
    if (keep) {
      var all = document.querySelectorAll('#trl [data-focus], #trl-hs [data-focus]');
      for (var i = 0; i < all.length; i++) {
        if (all[i].dataset.focus === keep) { all[i].focus(); break; }
      }
    }
  }

  logEl.addEventListener('scroll', function () {
    follow = logEl.scrollHeight - logEl.scrollTop - logEl.clientHeight < 24;
  });

  function load() {
    Promise.all([get('board&lang=' + encodeURIComponent(lang)), get('fortress')])
      .then(function (r) {
        view = { apps: r[0].apps || [], log: r[0].log || [] };
        fort = r[1];
        draw();
        loadIcons();
      })
      .catch(function () {
        if (!view) { show(logBox, true); note(L.t_engine, 'trl-l-error'); }
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

  document.querySelectorAll('[data-glyph]').forEach(function (n) { n.innerHTML = GLYPH[n.dataset.glyph] || ''; });
  hsEl.setAttribute('aria-label', L.t_view);
  get('tile').then(function (p) { prefs = { view: p.view || 'all', hidden: p.hidden || [] }; }, function () { /* everything */ })
    .then(function () {
      document.addEventListener('visibilitychange', schedule);
      schedule();
    });
})();
