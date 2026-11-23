/* The settings page in its tabs: General with what the tile shows, the
 * notification channels, the fortress and Info. The fortress is GlimStone's
 * card, the same in every plugin. Every channel change is saved the moment it
 * is made, with a tick when it worked and a plain line when it did not. A
 * token field stays empty: typing in it replaces the saved token, leaving it
 * empty keeps that one. */
(function () {
  'use strict';
  var C = window.TollRollCore, L = window.TRS_T || {}, lang = window.TRL_LANG || 'en';
  var API = '/plugins/tollroll/server/api.php';
  var GLYPH = window.TollRollGlyphs || {};

  function el(tag, cls, text) {
    var n = document.createElement(tag);
    if (cls) n.className = cls;
    if (text != null) n.textContent = text;
    return n;
  }

  function glyph(name) {
    var s = el('span', 'trs-gl');
    s.innerHTML = GLYPH[name] || '';
    return s;
  }

  function errorText(code) {
    return L['e_' + code] || L.e_generic;
  }

  function get(path) {
    return fetch(API + '?path=' + path, { cache: 'no-store', headers: { Accept: 'application/json' } })
      .then(function (r) { return r.json().catch(function () { return { error: 'engine' }; }); });
  }

  // Unraid checks csrf_token on every POST to a plugin script.
  function post(path, data) {
    var body = new URLSearchParams();
    body.set('path', path);
    body.set('data', JSON.stringify(data || {}));
    body.set('csrf_token', window.csrf_token || '');
    return fetch(API, { method: 'POST', body: body, headers: { Accept: 'application/json' } })
      .then(function (r) { return r.json().catch(function () { return { error: 'engine' }; }); });
  }

  /* The tabs: a vertical tab list, the arrows move between the tabs, and the
   * choice is kept for the next visit. */
  var TAB_KEY = 'tollroll.settings.tab';
  var tabs = Array.prototype.slice.call(document.querySelectorAll('#trs-cfg .trs-tab'));

  function openTab(tab, focus) {
    tabs.forEach(function (t) {
      var on = t === tab;
      t.setAttribute('aria-selected', on ? 'true' : 'false');
      t.tabIndex = on ? 0 : -1;
      document.getElementById(t.getAttribute('aria-controls')).hidden = !on;
    });
    try { localStorage.setItem(TAB_KEY, tab.getAttribute('data-trs-tab')); } catch (e) { /* kept until the page reloads */ }
    if (tab.getAttribute('data-trs-tab') === 'info') infoOpened();
    if (focus) tab.focus();
  }

  tabs.forEach(function (t, i) {
    t.addEventListener('click', function () { openTab(t, false); });
    t.addEventListener('keydown', function (e) {
      var n = { ArrowDown: i + 1, ArrowRight: i + 1, ArrowUp: i - 1, ArrowLeft: i - 1, Home: 0, End: tabs.length - 1 }[e.key];
      if (n === undefined) return;
      e.preventDefault();
      openTab(tabs[(n + tabs.length) % tabs.length], true);
    });
  });

  // Below 1024px the tabs show their glyphs only, and the name moves into the title.
  var narrow = window.matchMedia('(max-width: 1023px)');
  function tabTitles() {
    tabs.forEach(function (t) {
      if (narrow.matches) t.setAttribute('title', t.querySelector('.trs-tablbl').textContent);
      else t.removeAttribute('title');
    });
  }
  narrow.addEventListener('change', tabTitles);
  tabTitles();

  /* General: what the tile shows, from the board once it answers, and the way
   * to the tile's map. */
  var tileLine = document.getElementById('trs-tile-line');
  var mapBtn = document.getElementById('trs-map');
  var fort = null, reading = false;

  mapBtn.addEventListener('click', function () {
    mapBtn.disabled = true;
    get('tile').then(function (p) {
      return post('tile', { view: 'map', hidden: (p && p.hidden) || [] });
    }).then(function (r) {
      if (r.error) throw r;
      location.href = '/Dashboard';
    }).catch(function () { mapBtn.disabled = false; });
  });

  function readBoard() {
    if (reading) return;
    reading = true;
    get('board&lang=' + encodeURIComponent(lang)).then(function (r) {
      reading = false;
      if (r.apps && fort && fort.joined) tileLine.textContent = C.subtitle({ apps: r.apps }, L, lang, C.mapDevices(fort, r.apps).length);
    }, function () { reading = false; });
  }

  /* The advisor's suggestions, each with a way to its app and one to leave
   * it out from now on. */
  var adviceEl = document.getElementById('trs-advice');

  function drawAdvice(list) {
    adviceEl.textContent = '';
    if (!list.length) { adviceEl.appendChild(el('p', 'trs-prose', L.a_none)); return; }
    list.forEach(function (s) {
      var row = el('div', 'trs-row');
      var lbl = el('div', 'trs-lbl');
      lbl.appendChild(el('span', 'trs-name', s.name));
      lbl.appendChild(el('span', 'trs-sub', L[s.text] || ''));
      row.appendChild(lbl);
      var ctl = el('div', 'trs-ctl');
      if (/^https:\/\/github\.com\/junkerderprovinz\/[a-z]+$/.test(s.link)) {
        var a = el('a', 'trs-btn');
        a.href = s.link;
        a.target = '_blank';
        a.rel = 'noopener noreferrer';
        a.appendChild(glyph('IconExternalLink'));
        a.appendChild(el('span', '', L.a_more));
        ctl.appendChild(a);
      }
      var b = el('button', 'trs-btn');
      b.type = 'button';
      b.appendChild(glyph('IconClose'));
      b.appendChild(el('span', '', L.a_dismiss));
      b.addEventListener('click', function () {
        b.disabled = true;
        post('advice_dismiss', { id: s.id }).then(function (r) {
          if (r.suggestions) drawAdvice(r.suggestions); else b.disabled = false;
        }, function () { b.disabled = false; });
      });
      ctl.appendChild(b);
      row.appendChild(ctl);
      adviceEl.appendChild(row);
    });
  }
  get('advice').then(function (r) { if (r.suggestions) drawAdvice(r.suggestions); });

  var routerEl = document.getElementById('trs-router');
  get('router').then(function (r) {
    if (r.error) return;
    C.routerStatus(r, L, lang).forEach(function (t) { routerEl.appendChild(el('p', 'trs-prose', t)); });
  });

  /* The card gets the fortress from the service and the steps go back to it. */
  var JOIN = { phrase_invalid: 'phrase', bad_request: 'phrase' };
  var REMOVE = { no_member: 'noMember', not_joined: 'noMember' };

  // cardState keeps the map for General's line and gives the card the
  // instances of other apps, as it draws them.
  function cardState(f) {
    fort = f;
    if (f.joined) readBoard();
    else tileLine.textContent = L.t_join_title;
    return {
      joined: !!f.joined, device: f.device || '', removedHere: !!f.removedHere, phraseChanged: !!f.phraseChanged,
      removed: f.removed || [], hints: rejoin(f),
      members: (f.members || []).filter(function (m) { return m.kind === 'instance'; }).map(function (m) {
        return { id: m.id, app: m.app, name: m.name, device: m.device, form: m.deployment, version: m.version,
          icon: C.safeIcon(m.icon), online: !!m.online, relay: !!m.relay, lastSeen: m.lastSeen || '' };
      })
    };
  }

  // TollRoll notices a new phrase only when a member answers on the old one,
  // so the new words can be entered before that. Once it has noticed, the
  // card asks for them itself.
  function rejoin(f) {
    if (!f.joined || f.phraseChanged) return [];
    return [{ text: L.s_rejoin_hint, tone: 'quiet', action: { label: L.s_rejoin, join: true } }];
  }

  function answer(codes) {
    return function (r) {
      if (r.error) throw { code: codes[r.error] || r.error };
      return cardState(r);
    };
  }

  function report(r) {
    return {
      state: r.state, needsYou: r.needsYou || [], work: r.work || [],
      next: (r.next || []).map(function (n) { return { title: n.title, at: C.timeLabel(n.at, lang) }; })
    };
  }

  var adapter = {
    // A service that does not answer has no fortress to show.
    load: function () {
      return get('fortress').then(function (r) {
        if (r.error === 'engine') return null;
        return answer({})(r);
      });
    },
    join: function (words) { return post('join', { phrase: words.join(' ') }).then(answer(JOIN)); },
    leave: function () { return post('leave').then(answer({})); },
    remove: function (id) { return post('remove', { id: id }).then(answer(REMOVE)); },
    status: function (id) {
      return get('status&id=' + encodeURIComponent(id) + '&lang=' + encodeURIComponent(lang)).then(function (r) {
        if (r.error) throw { code: r.error };
        return {
          report: report(r.report || {}),
          log: (r.log || []).map(function (l) { return { at: C.timeLabel(l.at, lang), level: l.level, text: l.text }; })
        };
      });
    }
  };

  window.GlimFortressCard.mount(document.getElementById('trs-fort'), adapter, { lang: lang, app: { id: 'tollroll', name: 'TollRoll' } });

  var savedEl = document.getElementById('trs-n-saved'), fade = 0;

  function saved(ok, text) {
    clearTimeout(fade);
    savedEl.textContent = '';
    savedEl.className = 'trs-saved ' + (ok ? 'trs-saved-ok' : 'trs-saved-err');
    if (ok) savedEl.appendChild(glyph('IconCheck'));
    savedEl.appendChild(document.createTextNode(text));
    if (ok) fade = setTimeout(function () { savedEl.className = 'trs-saved'; }, 2200);
  }

  /* The notification channels, in the card every plugin of the house shares.
   * The service answers a save with the channels as kept, secrets masked,
   * and refuses a list that does not validate with the problems in it. */
  var KINDS = ['unraid', 'discord', 'slack', 'ntfy', 'gotify', 'matrix', 'apprise', 'smtp', 'webhook'];
  window.GlimNotifyCard.mount(document.getElementById('trs-notify'), {
    load: function () {
      return get('notify').then(function (r) {
        if (!r.channels) throw { code: r.error || 'engine' };
        return r.channels;
      });
    },
    save: function (list) {
      return post('notify', { channels: list }).then(function (r) {
        if (!r.channels) throw { code: r.error || 'engine', problems: r.problems };
        return r.channels;
      });
    },
    test: function (channel) {
      return post('notify_test', { channel: channel }).then(function (r) { return { ok: !!r.ok, code: r.error }; });
    }
  }, { t: L, lang: lang, kinds: KINDS, marks: window.GlimNotifyMarks, classes: { button: 'trs-btn' }, saved: saved });

  /* The hub: quiet hours, a mute until a time, the daily summary and all
   * quiet, taking over the apps' notifications and the level of each app,
   * all saved at once. */
  var H = {
    quiet: document.getElementById('trs-q-on'), from: document.getElementById('trs-q-from'), to: document.getElementById('trs-q-to'),
    digest: document.getElementById('trs-d-on'), at: document.getElementById('trs-d-at'), take: document.getElementById('trs-take'),
    mute: document.getElementById('trs-m-on'), until: document.getElementById('trs-m-at'), calm: document.getElementById('trs-calm')
  };
  var levelsEl = document.getElementById('trs-levels');
  var hub = null;
  [[H.from, 's_quiet_from'], [H.to, 's_quiet_to'], [H.at, 's_digest_at'], [H.until, 's_mute']].forEach(function (f) {
    window.GlimTimeField.enhance(f[0], { label: L[f[1]] });
  });

  function hubForm() {
    H.quiet.checked = hub.quiet.on;
    H.from.value = hub.quiet.from;
    H.to.value = hub.quiet.to;
    H.digest.checked = hub.digest.on;
    H.at.value = hub.digest.at;
    H.take.checked = hub.takeOver;
    var m = C.muteState(hub, new Date());
    H.mute.checked = m.on;
    H.until.value = m.at;
    H.calm.checked = !!hub.calm;
  }

  function saveHub() {
    if (!hub) return;
    var next = C.hubSettings(hub, {
      quiet: H.quiet.checked, from: H.from.value, to: H.to.value, digest: H.digest.checked, at: H.at.value, take: H.take.checked,
      mute: H.mute.checked, until: H.until.value, calm: H.calm.checked
    }, new Date());
    post('hub', next).then(function (r) {
      if (!r.quiet) { saved(false, L.s_not_saved + ': ' + errorText(r.error)); return; }
      hub = r;
      hub.apps = hub.apps || {};
      saved(true, L.s_saved);
    }, function () { saved(false, L.s_not_saved + ': ' + errorText('engine')); });
  }

  function drawLevels(apps) {
    levelsEl.textContent = '';
    var list = C.levelApps(apps, hub.apps);
    if (!list.length) { levelsEl.appendChild(el('p', 'trs-sub', L.s_levels_none)); return; }
    list.forEach(function (a) {
      var lbl = el('label', 'trs-name', a.name);
      lbl.htmlFor = 'trs-level-' + a.key.replace(/[^a-z0-9_-]/gi, '_');
      var sel = el('select', 'trs-in trs-select');
      sel.id = lbl.htmlFor;
      ['info', 'warning', 'failure', 'critical'].forEach(function (lv) {
        var o = el('option', '', L['s_level_' + lv]);
        o.value = lv;
        sel.appendChild(o);
      });
      sel.value = hub.apps[a.key] || 'info';
      sel.addEventListener('change', function () {
        if (sel.value === 'info') delete hub.apps[a.key]; else hub.apps[a.key] = sel.value;
        saveHub();
      });
      var row = el('div', 'trs-row'), start = el('div', 'trs-lbl'), ctl = el('div', 'trs-ctl');
      start.appendChild(lbl);
      ctl.appendChild(sel);
      row.appendChild(start);
      row.appendChild(ctl);
      levelsEl.appendChild(row);
    });
  }

  [H.quiet, H.digest, H.take, H.from, H.to, H.at, H.mute, H.until, H.calm].forEach(function (n) { n.addEventListener('change', saveHub); });
  get('hub').then(function (r) {
    if (!r.quiet) return;
    hub = r;
    hub.apps = hub.apps || {};
    hubForm();
    get('board&lang=' + encodeURIComponent(lang)).then(function (b) { drawLevels(b.apps || []); });
  });

  /* The Version card. Where TollRoll's updates stand comes from Unraid's
   * plugin manager, and Check for updates runs the check its Plugins tab runs.
   * Until Unraid has checked once since boot, opening Info runs that check. */
  var upd = document.getElementById('trs-update'), updCheck = document.getElementById('trs-check');
  var updState = JSON.parse(upd.getAttribute('data-trs-state') || '{}');
  function ago(at) {
    var f = new Intl.RelativeTimeFormat(lang, { numeric: 'auto' });
    var v = Math.round(at - Date.now() / 1000);
    var steps = [['second', 60], ['minute', 60], ['hour', 24], ['day', Infinity]];
    for (var i = 0; i < steps.length; i++) {
      if (Math.abs(v) < steps[i][1]) return f.format(steps[i][0] === 'second' ? 0 : v, steps[i][0]);
      v = Math.round(v / steps[i][1]);
    }
  }
  function showUpdate(s) {
    updState = s;
    var line = L.s_vc_checking, tone = 'neutral', note = '';
    // Up to date is a finding, so it needs a check behind it.
    if (s.kind === 'unchecked' || (s.kind === 'current' && !s.checkedAt)) {
      line = L.s_vc_unchecked;
    } else if (s.kind === 'current') {
      line = L.s_vc_current; tone = 'ok';
      note = L.s_vc_checked.replace('{when}', ago(s.checkedAt));
    } else if (s.kind === 'available') {
      line = L.s_vc_available.replace('{version}', s.version); tone = 'warn';
    } else if (s.kind === 'failed') {
      line = L.s_vc_failed; tone = 'fail'; note = s.reason || '';
    }
    document.getElementById('trs-vstate').setAttribute('data-tone', tone);
    document.getElementById('trs-vline').textContent = line;
    document.getElementById('trs-vnote').textContent = note;
    var busy = s.kind === 'checking';
    updCheck.hidden = s.kind === 'available';
    updCheck.disabled = busy;
    if (busy) updCheck.setAttribute('aria-busy', 'true'); else updCheck.removeAttribute('aria-busy');
    document.getElementById('trs-plugins').hidden = s.kind !== 'available';
  }
  function checkUpdate() {
    showUpdate({ kind: 'checking' });
    var body = new URLSearchParams({ csrf_token: window.csrf_token || '' });
    fetch('/plugins/tollroll/server/update-check.php', { method: 'POST', body: body, headers: { Accept: 'application/json' } })
      .then(function (r) { return r.json(); })
      .then(showUpdate, function (e) { showUpdate({ kind: 'failed', reason: String(e) }); });
  }
  function infoOpened() { if (updState.kind === 'unchecked') checkUpdate(); }
  updCheck.addEventListener('click', checkUpdate);
  var news = document.getElementById('trs-whatsnew');
  news.addEventListener('click', openNotes);
  document.getElementById('trs-plugins').addEventListener('click', function () { location.href = '/Plugins'; });
  showUpdate(updState);
  if (!document.getElementById('trs-pane-info').hidden) infoOpened();

  /* What's new opens the notes of the running version, or of the new one
   * while an update waits, as the plg on the server carries them, in the
   * window form of the channel picker. Escape and the scrim close it, and the
   * focus goes back to What's new. */
  var RELEASES = 'https://github.com/junkerderprovinz/tollroll/releases';
  function isRelease(v) { return /^\d+(\.\d+)+$/.test(v || ''); }
  function winButton(cls, glyphName, text) {
    var b = el('button', 'glim-nc-win-btn ' + cls);
    b.type = 'button';
    var g = el('span', 'glim-nc-g');
    g.setAttribute('aria-hidden', 'true');
    g.innerHTML = GLYPH[glyphName] || '';
    b.appendChild(g);
    b.appendChild(el('span', 'glim-nc-l', text));
    return b;
  }
  function openNotes() {
    var page = RELEASES;
    var title = el('h2', 'glim-nc-win-title');
    title.id = 'trs-notes-t';
    var notes = el('div', 'trs-notes');
    notes.tabIndex = 0;
    var body = el('div', 'glim-nc-win-body');
    body.appendChild(notes);
    var link = winButton('trs-win-link', 'IconExternalLink', L.s_notes_github);
    var shut = winButton('trs-win-close', 'IconClose', L.nc_close);
    var act = el('div', 'glim-nc-win-act');
    act.appendChild(link);
    act.appendChild(shut);
    var box = el('div', 'glim-nc-win trs-win-notes');
    box.setAttribute('role', 'dialog');
    box.setAttribute('aria-modal', 'true');
    box.setAttribute('aria-labelledby', title.id);
    box.tabIndex = -1;
    [title, body, act].forEach(function (n) { box.appendChild(n); });
    var scrim = el('div', 'glim-nc-scrim');
    var wins = el('div', 'glim-nc-wins');
    wins.appendChild(scrim);
    wins.appendChild(box);
    document.body.appendChild(wins);

    function named(v) {
      title.textContent = isRelease(v) ? L.s_notes_title.replace('{version}', v) : L.s_vc_whats_new;
      page = isRelease(v) ? RELEASES + '/tag/v' + v : RELEASES;
    }
    function say(text) {
      notes.textContent = '';
      notes.appendChild(el('p', 'trs-win-text', text));
    }
    function close() {
      if (!wins.parentNode) return;
      wins.parentNode.removeChild(wins);
      document.removeEventListener('keydown', onKey, true);
      news.focus();
    }
    function onKey(e) {
      if (e.key === 'Escape') { e.preventDefault(); e.stopPropagation(); close(); return; }
      if (e.key !== 'Tab') return;
      var stops = [notes, link, shut], first = stops[0], last = stops[stops.length - 1];
      if (!box.contains(document.activeElement)) { e.preventDefault(); (e.shiftKey ? last : first).focus(); }
      else if (e.shiftKey && document.activeElement === first) { e.preventDefault(); last.focus(); }
      else if (!e.shiftKey && document.activeElement === last) { e.preventDefault(); first.focus(); }
    }
    link.addEventListener('click', function () { window.open(page, '_blank', 'noopener'); });
    shut.addEventListener('click', close);
    scrim.addEventListener('click', close);
    document.addEventListener('keydown', onKey, true);

    named(updState.kind === 'available' ? updState.version : upd.getAttribute('data-trs-version'));
    say(L.s_notes_loading);
    notes.focus();
    fetch('/plugins/tollroll/server/release-notes.php', { headers: { Accept: 'application/json' } })
      .then(function (r) { if (!r.ok) throw new Error('HTTP ' + r.status); return r.json(); })
      .then(function (n) {
        if (!wins.parentNode) return;
        if (isRelease(n.version)) named(n.version);
        if (!String(n.notes || '').trim()) return say(L.s_notes_none);
        notes.textContent = '';
        window.TollRollNotes.render(n.notes, notes);
      })
      .catch(function () { if (wins.parentNode) say(L.s_notes_failed); });
  }

  document.querySelectorAll('[data-glyph]').forEach(function (n) { n.innerHTML = GLYPH[n.dataset.glyph] || ''; });
})();
