/* What the tile shows, worked out without the DOM: row texts and tones, the
 * log filter, the chips above the log and the labels. tile.js draws it,
 * plugin/test/core.test.js checks it under node. */
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

  function plural(L, base, n, lang) {
    var cat = 'other';
    try { cat = new Intl.PluralRules(lang).select(n); } catch (e) { /* unknown tag, keep other */ }
    var s = L[base + '_' + cat] || L[base + '_other'] || '';
    return fmt(s, { n: n });
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

  function subtitle(view, L, lang) {
    var apps = view.apps || [];
    var s = plural(L, 't_apps', apps.length, lang);
    var n = apps.filter(needsYou).length;
    return n ? s + ', ' + plural(L, 't_attn', n, lang) : s;
  }

  function lang(locale) {
    var m = /^([a-z]{2,3})(?:[_-]|$)/i.exec(String(locale || ''));
    return m ? m[1].toLowerCase() : 'en';
  }

  return { fmt: fmt, plural: plural, rowStatus: rowStatus, filterLog: filterLog, chips: chips, validFilter: validFilter,
    safeLink: safeLink, hue: hue, mark: mark, timeLabel: timeLabel, subtitle: subtitle, needsYou: needsYou, lang: lang };
});
