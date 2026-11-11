/* What the pages show, worked out without the DOM: row texts and tones, the
 * log filter, the chips above the log, the apps chosen for the apps and log
 * views, the map of the fortress, the labels, an app's mark and the
 * notification form. tile.js and settings.js draw it, plugin/test/core.test.js
 * checks it under node. */
(function (root, factory) {
  if (typeof module === 'object' && module.exports) module.exports = factory();
  else root.TollRollCore = factory();
})(typeof self !== 'undefined' ? self : this, function () {
  'use strict';

  function fmt(s, vars) {
    return String(s).replace(/\{(\w+)\}/g, function (m, k) {
      return Object.prototype.hasOwnProperty.call(vars, k) ? String(vars[k]) : m;
    });
  }

  // plural picks the form for n and fills in vars, n among them unless vars
  // names it otherwise.
  function plural(L, base, n, lang, vars) {
    var cat = 'other';
    try { cat = new Intl.PluralRules(lang).select(n); } catch (e) { /* unknown tag, keep other */ }
    var s = L[base + '_' + cat] || L[base + '_other'] || '';
    return fmt(s, Object.assign({ n: n }, vars));
  }

  var STATES = {
    failed: ['fail', 't_st_failed'],
    overdue: ['warn', 't_st_overdue'],
    warning: ['warn', 't_st_warning'],
    starting: ['idle', 't_st_starting'],
    off: ['off', 't_st_off'],
    unreachable: ['off', 't_st_unreachable'],
  };

  function percent(p, lang) {
    try { return new Intl.NumberFormat(lang, { style: 'percent', maximumFractionDigits: 0 }).format(p); }
    catch (e) { return Math.round(p * 100) + '%'; }
  }

  /* The app's own words win: its most urgent need, then its running work.
   * Only a quiet app gets the tile's word for its state. */
  function rowStatus(app, L, lang) {
    if (app.state !== 'unreachable' && app.need) {
      return { tone: app.need.severity === 'error' ? 'fail' : 'warn', text: app.need.text };
    }
    if (app.state === 'ok') {
      if (app.work) {
        var t = app.work.title;
        if (typeof app.work.progress === 'number') t += ', ' + percent(app.work.progress, lang);
        return { tone: 'ok', text: t };
      }
      if (app.instances > 1) return { tone: 'ok', text: plural(L, 't_inst', app.instances, lang) };
      return { tone: 'ok', text: L.t_st_ok };
    }
    var s = STATES[app.state] || STATES.unreachable;
    return { tone: s[0], text: L[s[1]] };
  }

  function filterLog(log, key) {
    if (key === 'all') return log.slice();
    return log.filter(function (l) { return l.app === key; });
  }

  function chips(view) {
    var counts = {};
    (view.log || []).forEach(function (l) { counts[l.app] = (counts[l.app] || 0) + 1; });
    var out = [{ key: 'all', name: null, count: (view.log || []).length }];
    (view.apps || []).forEach(function (a) {
      if (counts[a.key]) out.push({ key: a.key, name: a.name, count: counts[a.key] });
    });
    return out;
  }

  function validFilter(view, key) {
    return (view.apps || []).some(function (a) { return a.key === key; }) ? key : 'all';
  }

  function safeLink(u) {
    if (typeof u !== 'string') return '';
    return /^https?:\/\/[^\s/]/i.test(u) ? u : '';
  }

  function hue(key) {
    var h = 2166136261;
    for (var i = 0; i < key.length; i++) {
      h ^= key.charCodeAt(i);
      h = Math.imul(h, 16777619);
    }
    return (h >>> 0) % 360;
  }

  function mark(name) {
    var s = String(name || '').trim();
    return s ? Array.from(s)[0].toUpperCase() : '?';
  }

  /* The service checks every icon against the status contract already. This
   * second look keeps anything but an SVG data URI of at most 16 KiB out of
   * an img src. */
  var ICON = /^data:image\/svg\+xml;base64,[A-Za-z0-9+\/]+={0,2}$/;
  var MAX_ICON = 26 + 21848;

  function safeIcon(u) {
    return typeof u === 'string' && u.length <= MAX_ICON && ICON.test(u) ? u : '';
  }

  // The letter on an app's own colour is white or near black, whichever reads
  // better by the colour's luminance.
  function markColors(c) {
    if (typeof c !== 'string' || !/^#[0-9a-fA-F]{6}$/.test(c)) return null;
    var lin = [1, 3, 5].map(function (i) {
      var v = parseInt(c.substr(i, 2), 16) / 255;
      return v <= 0.03928 ? v / 12.92 : Math.pow((v + 0.055) / 1.055, 2.4);
    });
    var y = 0.2126 * lin[0] + 0.7152 * lin[1] + 0.0722 * lin[2];
    return { bg: c, fg: y > 0.18 ? '#161616' : '#ffffff' };
  }

  /* The settings page edits three channels of notify.json by their ids and
   * leaves every other channel as it is. The service sends saved secrets back
   * masked, and a mask sent back keeps the saved one. */
  var MASK = '********';

  function byId(list, id) {
    for (var i = 0; i < list.length; i++) if (list[i] && list[i].id === id) return list[i];
    return null;
  }

  function notifyForm(list) {
    var u = byId(list, 'unraid'), a = byId(list, 'apprise'), m = byId(list, 'matrix');
    var mx = (m && m.matrix) || {};
    var first = u || a || m;
    return {
      unraid: !!(u && u.enabled),
      apprise: (a && a.apprise && a.apprise.url) || '',
      hs: mx.homeserver || '',
      room: mx.room || '',
      token: '',
      tokenSaved: !!mx.token,
      on: first && first.on === 'failure' ? 'failure' : 'always'
    };
  }

  function notifyChannels(list, f) {
    var out = list.slice();
    function base(id, kind) {
      var old = byId(out, id);
      var c = old ? JSON.parse(JSON.stringify(old)) : { id: id, kind: kind, enabled: false };
      c.on = f.on;
      return c;
    }
    function set(id, c) {
      var i = out.indexOf(byId(out, id));
      if (c === null) { if (i >= 0) out.splice(i, 1); return; }
      if (i >= 0) out[i] = c; else out.push(c);
    }

    var url = f.apprise.trim();
    var a = null;
    if (url) {
      a = base('apprise', 'apprise');
      a.enabled = /^https?:\/\/[^\/\s]+/i.test(url);
      a.apprise = Object.assign({}, a.apprise, { url: url });
    }
    set('apprise', a);

    var hs = f.hs.trim(), room = f.room.trim(), typed = f.token.trim();
    var m = null;
    if (hs || room || typed) {
      var token = typed || (f.tokenSaved ? MASK : '');
      m = base('matrix', 'matrix');
      m.enabled = !!(hs && room && token);
      m.matrix = { homeserver: hs, room: room, token: token };
    }
    set('matrix', m);

    if (byId(out, 'unraid') || f.unraid) {
      var u = base('unraid', 'unraid');
      u.enabled = !!f.unraid;
      set('unraid', u);
    }
    return out;
  }

  function timeLabel(iso, lang, now) {
    var d = new Date(iso);
    now = now || new Date();
    var time = { hour: '2-digit', minute: '2-digit' };
    try {
      if (d.toDateString() === now.toDateString()) return d.toLocaleTimeString(lang, time);
      return d.toLocaleString(lang, { day: 'numeric', month: 'numeric', hour: '2-digit', minute: '2-digit' });
    } catch (e) {
      return d.toISOString().slice(11, 16);
    }
  }

  function needsYou(app) {
    return app.state === 'unreachable' || !!app.need || ['failed', 'overdue', 'warning'].indexOf(app.state) >= 0;
  }

  function subtitle(view, L, lang, devices) {
    var apps = view.apps || [];
    var s = plural(L, 't_apps', apps.length, lang);
    if (devices) s = fmt(L.t_sub_in, { apps: s, devices: plural(L, 't_dev', devices, lang) });
    var n = apps.filter(needsYou).length;
    return n ? s + ', ' + plural(L, 't_attn', n, lang) : s;
  }

  function shown(apps, hidden) {
    return (apps || []).filter(function (a) { return (hidden || []).indexOf(a.key) < 0; });
  }

  function selLabel(L, lang, n, total) {
    if (n === total) return L.t_all_apps;
    if (!n) return L.t_sel_none;
    return plural(L, 't_sel', total, lang, { n: n, total: total });
  }

  /* The map shows each device of the fortress with its members, this server
   * first. A member whose app has no row on the board reports no status. A
   * device counts as connected while any of its members is. */
  function mapDevices(map, apps) {
    var rows = {}, by = {}, order = [];
    (apps || []).forEach(function (a) { rows[a.key] = a; });
    function device(name) {
      if (!by[name]) {
        by[name] = { name: name, here: name === map.device, online: name === map.device, seen: '', members: [] };
        order.push(name);
      }
      return by[name];
    }
    device(map.device);
    (map.members || []).forEach(function (m) {
      var d = device(m.device || '?');
      if (m.online) d.online = true;
      if (m.lastSeen && m.lastSeen > d.seen) d.seen = m.lastSeen;
      var row = rows[m.app] || null;
      d.members.push({ id: m.id, app: m.app, name: row ? row.name : (m.name || m.app), version: m.version || '', online: !!m.online, quiet: !row, row: row });
    });
    return order.map(function (n) { return by[n]; }).sort(function (a, b) {
      return (b.here - a.here) || a.name.localeCompare(b.name);
    });
  }

  function ago(iso, lang, now) {
    var s = Math.round((new Date(iso) - (now || new Date())) / 1000);
    var steps = [[60, 'second'], [3600, 'minute'], [86400, 'hour'], [Infinity, 'day']];
    var size = [1, 60, 3600, 86400];
    for (var i = 0; i < steps.length; i++) {
      if (Math.abs(s) < steps[i][0]) {
        try { return new Intl.RelativeTimeFormat(lang, { numeric: 'auto' }).format(Math.round(s / size[i]), steps[i][1]); }
        catch (e) { return new Date(iso).toLocaleString(); }
      }
    }
    return '';
  }

  function lang(locale) {
    var m = /^([a-z]{2,3})(?:[_-]|$)/i.exec(String(locale || ''));
    return m ? m[1].toLowerCase() : 'en';
  }

  return { fmt: fmt, plural: plural, rowStatus: rowStatus, filterLog: filterLog, chips: chips, validFilter: validFilter,
    safeLink: safeLink, hue: hue, mark: mark, timeLabel: timeLabel, subtitle: subtitle, needsYou: needsYou, lang: lang,
    safeIcon: safeIcon, markColors: markColors, notifyForm: notifyForm, notifyChannels: notifyChannels,
    shown: shown, selLabel: selLabel, mapDevices: mapDevices, ago: ago };
});
