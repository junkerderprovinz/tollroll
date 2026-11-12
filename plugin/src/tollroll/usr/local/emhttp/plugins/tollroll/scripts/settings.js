/* The settings page: the fortress and the notification channels. Every
 * change is saved the moment it is made, with a tick when it worked and a
 * plain line when it did not. Only joining has a button, since twelve words
 * are typed before they mean anything. A token field stays empty: typing in
 * it replaces the saved token, leaving it empty keeps that one. */
(function () {
  'use strict';
  var C = window.TollRollCore, L = window.TRS_T || {}, lang = window.TRL_LANG || 'en';
  var API = '/plugins/tollroll/server/api.php';
  var GLYPH = window.TollRollGlyphs || {};
  var fortEl = document.getElementById('trs-fort');
  var dlgEl = document.getElementById('trs-dlg');

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

  function button(name, text, primary) {
    var b = el('button', 'trs-btn' + (primary ? ' trs-primary' : ''));
    b.type = 'button';
    b.appendChild(glyph(name));
    b.appendChild(el('span', '', text));
    return b;
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

  /* The fortress card: the twelve words while the tile is in no fortress or
   * could not follow a changed phrase, what it sees once it is. */
  var fort = null, board = null, rejoin = false, words = [];
  // landing takes the focus once the button that opened a window is gone.
  var landing = null;

  function drawFort() {
    fortEl.textContent = '';
    if (fort && fort.joined && !rejoin && !fort.phraseChanged) drawJoined(); else drawJoin();
  }

  function heading(sub) {
    var h = el('h3', '', L.s_fortress + ' ');
    h.appendChild(el('small', '', sub));
    fortEl.appendChild(h);
  }

  function drawJoin(problem) {
    var changed = !!(fort && fort.phraseChanged), again = rejoin || changed;
    heading(again ? L.s_rejoin_sub : L.s_fortress_sub);
    if (fort && (fort.removedHere || changed) && !rejoin) {
      var n = el('div', 'trs-note');
      n.appendChild(glyph('IconInfo'));
      n.appendChild(el('span', '', changed ? L.s_changed_note : L.s_removed_note));
      fortEl.appendChild(n);
    }
    fortEl.appendChild(el('p', 'trs-hint', again ? L.s_rejoin_hint : L.s_join_hint));
    var grid = el('div', 'trs-words' + (problem === 'phrase_invalid' ? ' trs-bad' : ''));
    var inputs = [];
    for (var i = 0; i < 12; i++) {
      var lab = el('label', 'trs-word');
      lab.appendChild(el('span', '', String(i + 1)));
      var inp = el('input', 'trs-in');
      inp.autocomplete = 'off';
      inp.spellcheck = false;
      inp.setAttribute('autocapitalize', 'off');
      inp.setAttribute('aria-label', C.fmt(L.s_word, { n: i + 1 }));
      inp.value = words[i] || '';
      lab.appendChild(inp);
      grid.appendChild(lab);
      inputs.push(inp);
    }
    // A pasted phrase spreads over the fields from the one it went into.
    inputs.forEach(function (inp, i) {
      inp.addEventListener('input', function () {
        var parts = inp.value.trim().split(/\s+/);
        if (parts.length > 1) {
          parts.slice(0, 12 - i).forEach(function (w, k) { inputs[i + k].value = w; });
          inputs[Math.min(11, i + parts.length - 1)].focus();
        }
        words = inputs.map(function (x) { return x.value.trim(); });
      });
    });
    fortEl.appendChild(grid);
    fortEl.appendChild(el('p', 'trs-hint', L.s_words_hint));
    var tools = el('div', 'trs-tools');
    var join = button('IconSignIn', L.s_join, true);
    var res = el('span', 'trs-res');
    res.setAttribute('role', 'status');
    if (problem) {
      res.appendChild(el('span', 'err', problem === 'phrase_invalid' ? L.s_phrase_bad : errorText(problem)));
    }
    join.addEventListener('click', function () {
      words = inputs.map(function (x) { return x.value.trim(); });
      join.disabled = true;
      res.textContent = '';
      res.appendChild(el('span', 'run', L.s_joining));
      post('join', { phrase: words.join(' ') }).then(function (r) {
        if (r.error) {
          fortEl.textContent = '';
          drawJoin(r.error === 'phrase_invalid' || r.error === 'bad_request' ? 'phrase_invalid' : r.error === 'engine' ? 'engine' : 'join_failed');
          return;
        }
        fort = r;
        rejoin = false;
        words = [];
        drawFort();
        loadBoard();
      });
    });
    tools.appendChild(join);
    if (rejoin) {
      var cancel = button('IconClose', L.s_cancel);
      cancel.addEventListener('click', function () { rejoin = false; words = []; drawFort(); });
      tools.appendChild(cancel);
    }
    tools.appendChild(res);
    fortEl.appendChild(tools);
  }

  function drawJoined() {
    heading(L.s_fortress_sub);
    var devices = C.mapDevices(fort, board ? board.apps : []);
    var others = devices.filter(function (d) { return !d.here; });
    var on = others.filter(function (d) { return d.online; });
    var off = others.filter(function (d) { return !d.online && !d.calm; }).map(function (d) { return d.name; });
    var conn = !others.length ? L.s_conn_alone : C.plural(L, 's_conn', on.length, lang);
    if (off.length) conn += ', ' + C.fmt(L.s_conn_off, { names: off.join(', ') });

    var dl = el('dl', 'trs-facts');
    function fact(term, value, dot) {
      dl.appendChild(el('dt', '', term));
      var dd = el('dd');
      if (dot) dd.appendChild(el('span', 'trs-dot ' + (off.length && !on.length ? 'err' : 'ok')));
      dd.appendChild(document.createTextNode(value));
      dl.appendChild(dd);
    }
    fact(L.s_this_server, C.fmt(L.s_in_fortress, { device: fort.device }));
    fact(L.s_connection, conn, true);
    if (board) fact(L.s_in_tile, C.subtitle(board, L, lang, devices.length));
    var peers = fort.members || [];
    if (peers.length) {
      dl.appendChild(el('dt', '', L.s_members));
      var dd = el('dd');
      var list = el('ul', 'trs-members');
      peers.forEach(function (m, i) {
        var li = el('li', 'trs-member');
        li.appendChild(el('span', 'trs-dot ' + (m.online ? 'ok' : 'trs-away')));
        var name = el('span', 'trs-member-name', C.memberName(L, m));
        name.id = 'trs-member-' + i;
        li.appendChild(name);
        var rm = button('IconTrash', L.s_remove);
        rm.classList.add('trs-quiet');
        rm.setAttribute('aria-describedby', name.id);
        rm.addEventListener('click', function () { confirmRemove(m, rm); });
        li.appendChild(rm);
        list.appendChild(li);
      });
      dd.appendChild(list);
      dl.appendChild(dd);
    }
    fortEl.appendChild(dl);

    var map = button('IconFleet', L.s_map);
    landing = map;
    var gone = fort.removed || [];
    if (gone.length) {
      var note = el('div', 'trs-removed');
      note.appendChild(glyph('IconInfo'));
      var names = gone.map(function (r) { return C.memberName(L, r); }).join(', ');
      note.appendChild(el('span', '', C.fmt(L.s_removed_list, { devices: names })));
      var page = C.phrasePage(fort);
      if (page) {
        landing = phraseLink(page);
        note.appendChild(landing);
      } else {
        note.appendChild(el('span', 'trs-removed-where', L.s_change_where));
      }
      fortEl.appendChild(note);
    }

    var tools = el('div', 'trs-tools');
    map.addEventListener('click', function () {
      get('tile').then(function (p) {
        return post('tile', { view: 'map', hidden: (p && p.hidden) || [] });
      }).then(function () { location.href = '/Dashboard'; });
    });
    var again = button('IconKey', L.s_rejoin);
    again.addEventListener('click', function () { rejoin = true; words = []; drawFort(); });
    var leave = button('IconSignOut', L.s_leave);
    leave.classList.add('trs-end');
    leave.addEventListener('click', function () { confirmLeave(leave); });
    tools.appendChild(map);
    tools.appendChild(again);
    tools.appendChild(leave);
    fortEl.appendChild(tools);
  }

  // The button to change the phrase opens an app that keeps it, since
  // TollRoll has only keys.
  function phraseLink(page, primary) {
    var a = el('a', 'trs-btn ' + (primary ? 'trs-primary' : 'trs-quiet'));
    a.href = page.href;
    a.target = '_blank';
    a.rel = 'noopener noreferrer';
    a.title = C.fmt(L.t_open, { name: page.name });
    a.appendChild(glyph('IconExternalLink'));
    a.appendChild(el('span', '', L.s_change));
    return a;
  }

  // dialog opens a window over the page. Each step replaces what it shows
  // and returns the row for its buttons. Escape and a click beside it close
  // it. The focus then goes back to the button that opened it, or where
  // backTo says once that button is gone.
  function dialog(back) {
    var shade = el('div', 'trs-dlg-back');
    var box = el('div', 'trs-dlg');
    box.setAttribute('role', 'dialog');
    box.setAttribute('aria-modal', 'true');
    box.setAttribute('aria-labelledby', 'trs-dlg-h');
    function close() {
      dlgEl.textContent = '';
      document.removeEventListener('keydown', onKey);
      if (back) back.focus();
    }
    function onKey(e) { if (e.key === 'Escape') close(); }
    shade.addEventListener('click', function (e) { if (e.target === shade) close(); });
    shade.appendChild(box);
    dlgEl.appendChild(shade);
    document.addEventListener('keydown', onKey);
    return {
      close: close,
      backTo: function (n) { back = n; },
      step: function (title, nodes) {
        box.textContent = '';
        var h = el('h4', '', title);
        h.id = 'trs-dlg-h';
        box.appendChild(h);
        nodes.forEach(function (n) { box.appendChild(n); });
        var tools = el('div', 'trs-tools trs-dlg-tools');
        box.appendChild(tools);
        return tools;
      }
    };
  }

  function statusLine() {
    var res = el('p', 'trs-res');
    res.setAttribute('role', 'status');
    return res;
  }

  function failed(res, text) {
    res.textContent = '';
    res.appendChild(el('span', 'err', text));
  }

  function confirmLeave(opener) {
    var d = dialog(opener);
    var res = statusLine();
    var tools = d.step(L.s_leave_q, [el('p', '', L.s_leave_text), res]);
    var cancel = button('IconClose', L.s_cancel);
    var yes = button('IconSignOut', L.s_leave_yes, true);
    cancel.addEventListener('click', d.close);
    yes.addEventListener('click', function () {
      yes.disabled = true;
      post('leave').then(function (r) {
        if (r.error) {
          yes.disabled = false;
          failed(res, errorText(r.error === 'engine' ? 'engine' : 'leave_failed'));
          return;
        }
        fort = r;
        d.close();
        drawFort();
      });
    });
    tools.appendChild(cancel);
    tools.appendChild(yes);
    cancel.focus();
  }

  // confirmRemove asks before member m is taken out, then offers to change
  // the phrase, which would still let it back in.
  function confirmRemove(m, opener) {
    var d = dialog(opener);
    var name = C.memberName(L, m);
    var res = statusLine();
    var tools = d.step(C.fmt(L.s_remove_q, { device: name }), [el('p', '', C.fmt(L.s_remove_text, { device: name })), res]);
    var cancel = button('IconClose', L.s_cancel);
    var yes = button('IconTrash', L.s_remove, true);
    cancel.addEventListener('click', d.close);
    yes.addEventListener('click', function () {
      yes.disabled = true;
      post('remove', { id: m.id }).then(function (r) {
        if (r.error) {
          yes.disabled = false;
          failed(res, errorText(/^(engine|no_member|not_joined)$/.test(r.error) ? r.error : 'remove_failed'));
          return;
        }
        fort = r;
        drawFort();
        d.backTo(landing);
        offer(d, name);
      });
    });
    tools.appendChild(cancel);
    tools.appendChild(yes);
    yes.focus();
  }

  function offer(d, name) {
    var page = C.phrasePage(fort);
    var tools = d.step(L.s_removed_q, [el('p', '', C.fmt(L.s_removed_text, { device: name })), el('p', 'trs-hint', L.s_change_where)]);
    var later = button('IconClose', L.s_later);
    later.addEventListener('click', d.close);
    tools.appendChild(later);
    if (!page) {
      later.focus();
      return;
    }
    var change = phraseLink(page, true);
    change.addEventListener('click', d.close);
    tools.appendChild(change);
    change.focus();
  }

  function loadFort() {
    get('fortress').then(function (r) {
      if (r.error) {
        fortEl.textContent = '';
        heading(L.s_fortress_sub);
        fortEl.appendChild(el('p', 'trs-res err', errorText(r.error)));
        return;
      }
      fort = r;
      drawFort();
      if (fort.joined) loadBoard();
    });
  }

  function loadBoard() {
    get('board&lang=' + encodeURIComponent(lang)).then(function (r) {
      if (r.apps) { board = { apps: r.apps, log: [] }; if (fort && fort.joined && !rejoin && !fort.phraseChanged) drawFort(); }
    });
  }

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
  loadFort();
  loadNotify();
})();
