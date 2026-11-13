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

  /* The notification channels. Switches and choices save at once, text a
   * moment after the last key, so a half typed address is not sent. */
  var N = {
    unraid: document.getElementById('trs-n-unraid'),
    apprise: document.getElementById('trs-n-apprise'),
    hs: document.getElementById('trs-n-hs'),
    room: document.getElementById('trs-n-room'),
    token: document.getElementById('trs-n-token'),
    on: document.getElementById('trs-n-on')
  };
  var savedEl = document.getElementById('trs-n-saved');
  var appriseErr = document.getElementById('trs-n-apprise-err');
  var channels = [], savedHs = '', tokenSaved = false, typing = 0, fade = 0;
  var NAMES = { unraid: 'Unraid', apprise: 'Apprise', matrix: 'Matrix' };

  // A saved token stays with its homeserver: once that changes, the field
  // asks for a token again.
  function tokenKept() {
    return tokenSaved && N.hs.value.trim() === savedHs;
  }

  function tokenHint() {
    N.token.placeholder = tokenKept() ? L.s_token_saved : L.s_n_token;
  }

  function showNotify(list) {
    channels = list || [];
    var f = C.notifyForm(channels);
    N.unraid.checked = f.unraid;
    N.apprise.value = f.apprise;
    N.hs.value = f.hs;
    N.room.value = f.room;
    N.token.value = '';
    N.on.value = f.on;
    savedHs = f.hs;
    tokenSaved = f.tokenSaved;
    tokenHint();
  }

  function notifyList() {
    return C.notifyChannels(channels, {
      unraid: N.unraid.checked, apprise: N.apprise.value, hs: N.hs.value, room: N.room.value,
      token: N.token.value, tokenSaved: tokenKept(), on: N.on.value
    });
  }

  function notifyError(code) {
    return L['s_n_err_' + code] || C.fmt(L.s_n_err_send, { code: code || 'engine' });
  }

  function saved(ok, text) {
    clearTimeout(fade);
    savedEl.textContent = '';
    savedEl.className = 'trs-saved ' + (ok ? 'trs-saved-ok' : 'trs-saved-err');
    if (ok) savedEl.appendChild(glyph('IconCheck'));
    savedEl.appendChild(document.createTextNode(text));
    if (ok) fade = setTimeout(function () { savedEl.className = 'trs-saved'; }, 2200);
  }

  // The answer brings the channels back masked; the fields keep what is
  // typed in them, so nothing jumps under the cursor.
  function saveNotify() {
    clearTimeout(typing);
    var url = N.apprise.value.trim();
    if (url && !/^https?:\/\/[^\s]+$/i.test(url)) {
      appriseErr.textContent = L.s_n_apprise_bad;
      saved(false, L.s_not_saved);
      return;
    }
    appriseErr.textContent = '';
    post('notify', { channels: notifyList() }).then(function (r) {
      if (!r.channels) { saved(false, L.s_not_saved + ': ' + notifyError(r.error)); return; }
      channels = r.channels;
      savedHs = N.hs.value.trim();
      if (N.token.value.trim()) tokenSaved = true;
      tokenHint();
      saved(true, L.s_saved);
    }, function () { saved(false, L.s_not_saved + ': ' + errorText('engine')); });
  }

  [N.unraid, N.on].forEach(function (n) { n.addEventListener('change', saveNotify); });
  [N.apprise, N.hs, N.room, N.token].forEach(function (n) {
    n.addEventListener('input', function () {
      clearTimeout(typing);
      typing = setTimeout(saveNotify, 700);
    });
    n.addEventListener('change', saveNotify);
  });
  N.hs.addEventListener('input', tokenHint);

  // Each switched on channel gets the test message, as the form has it.
  function testNotify() {
    var res = document.getElementById('trs-n-res');
    var list = notifyList().filter(function (c) { return NAMES[c.id] && c.enabled; });
    res.textContent = '';
    if (!list.length) { res.appendChild(el('span', '', L.s_n_test_none)); return; }
    list.reduce(function (done, c) {
      return done.then(function () {
        var item = res.appendChild(el('span', 'run', NAMES[c.id] + ': ' + L.s_testing));
        return post('notify_test', { channel: c }).then(function (r) {
          item.className = r.ok ? 'ok' : 'err';
          item.textContent = (r.ok ? '✓ ' : '✗ ') + NAMES[c.id] + ': ' + (r.ok ? L.s_n_sent : notifyError(r.error));
        }, function () {
          item.className = 'err';
          item.textContent = '✗ ' + NAMES[c.id] + ': ' + errorText('engine');
        });
      });
    }, Promise.resolve());
  }

  function loadNotify() {
    get('notify').then(function (r) {
      if (r.channels) showNotify(r.channels);
      else saved(false, errorText(r.error || 'engine'));
    });
  }

  document.querySelectorAll('[data-glyph]').forEach(function (n) { n.innerHTML = GLYPH[n.dataset.glyph] || ''; });
  document.getElementById('trs-n-test').addEventListener('click', testNotify);
  loadNotify();
})();
