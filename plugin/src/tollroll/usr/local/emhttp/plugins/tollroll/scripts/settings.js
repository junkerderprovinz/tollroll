/* The list of apps and the notification channels on the settings page. Both
 * are read from the service and written back as a whole on Apply. A token
 * field stays empty: typing in it replaces the saved token, leaving it empty
 * keeps that one. */
(function () {
  'use strict';
  var C = window.TollRollCore, L = window.TRS_T || {};
  var API = '/plugins/tollroll/server/api.php';
  var listEl = document.getElementById('trs-list');
  var rows = [];

  function el(tag, cls, text) {
    var n = document.createElement(tag);
    if (cls) n.className = cls;
    if (text != null) n.textContent = text;
    return n;
  }

  function errorText(code) {
    return L['e_' + code] || L.e_generic;
  }

  function isSocket(addr) {
    return addr.trim().charAt(0) === '/';
  }

  function get(path) {
    return fetch(API + '?path=' + path, { cache: 'no-store', headers: { Accept: 'application/json' } })
      .then(function (r) { return r.json().catch(function () { return { error: 'engine' }; }); });
  }

  // Unraid checks csrf_token on every POST to a plugin script.
  function post(path, data) {
    var body = new URLSearchParams();
    body.set('path', path);
    body.set('data', JSON.stringify(data));
    body.set('csrf_token', window.csrf_token || '');
    return fetch(API, { method: 'POST', body: body, headers: { Accept: 'application/json' } })
      .then(function (r) { return r.json().catch(function () { return { error: 'engine' }; }); });
  }

  function fromServer(list) {
    rows = (list || []).map(function (s) {
      return { id: s.id, address: s.address, saved: s.address, hasToken: s.hasToken, pinned: s.pinned, last: s.last || null, token: '', trust: false, msg: null };
    });
    draw();
  }

  function status(row) {
    if (row.msg) return row.msg;
    if (!row.last) return null;
    if (row.last.error) return { ok: false, text: errorText(row.last.error), code: row.last.error };
    return { ok: true, text: C.fmt(L.s_ok, { name: row.last.name || '', version: row.last.version || '' }) };
  }

  function drawRow(row, i) {
    var box = el('div', 'trs-row');

    var addr = el('input', 'trs-in trs-addr');
    addr.type = 'text';
    addr.value = row.address;
    addr.placeholder = 'https://192.168.1.10:3443';
    addr.setAttribute('aria-label', L.s_addr);
    addr.spellcheck = false;
    addr.autocomplete = 'off';

    var tok = el('input', 'trs-in trs-tok');
    tok.type = 'password';
    tok.value = row.token;
    tok.autocomplete = 'new-password';
    tok.setAttribute('aria-label', L.s_token);
    var sockNow = isSocket(row.address);
    tok.disabled = sockNow;
    tok.placeholder = sockNow ? L.s_token_none : (row.hasToken && row.address === row.saved ? L.s_token_saved : L.s_token);

    addr.addEventListener('input', function () {
      row.address = addr.value;
      var s = isSocket(row.address);
      tok.disabled = s;
      tok.placeholder = s ? L.s_token_none : (row.hasToken && row.address.trim() === row.saved ? L.s_token_saved : L.s_token);
    });
    tok.addEventListener('input', function () { row.token = tok.value; });

    var eye = el('button', 'trs-btn trs-small', L.s_show);
    eye.type = 'button';
    eye.addEventListener('click', function () {
      var show = tok.type === 'password';
      tok.type = show ? 'text' : 'password';
      eye.textContent = show ? L.s_hide : L.s_show;
    });

    var test = el('button', 'trs-btn trs-small', L.s_test);
    test.type = 'button';
    test.addEventListener('click', function () { runTest(row); });

    var rm = el('button', 'trs-btn trs-small trs-ghost', L.s_remove);
    rm.type = 'button';
    rm.addEventListener('click', function () {
      rows.splice(i, 1);
      draw();
    });

    var fields = el('div', 'trs-fields');
    fields.appendChild(addr);
    var tokWrap = el('div', 'trs-tokwrap');
    tokWrap.appendChild(tok);
    tokWrap.appendChild(eye);
    fields.appendChild(tokWrap);
    var btns = el('div', 'trs-rowbtns');
    btns.appendChild(test);
    btns.appendChild(rm);
    fields.appendChild(btns);
    box.appendChild(fields);

    var st = status(row);
    if (st) {
      var line = el('div', 'trs-res');
      line.appendChild(el('span', st.ok ? 'ok' : st.run ? 'run' : 'err', (st.run ? '' : st.ok ? '✓ ' : '✗ ') + st.text));
      if (row.pinned && row.address === row.saved && st.code !== 'cert_changed') {
        line.title = L.s_pinned;
      }
      if (st.code === 'cert_changed' && !row.trust) {
        var trust = el('button', 'trs-btn trs-small', L.s_trust);
        trust.type = 'button';
        trust.addEventListener('click', function () {
          row.trust = true;
          apply();
        });
        line.appendChild(trust);
      }
      box.appendChild(line);
    }
    return box;
  }

  function draw() {
    listEl.textContent = '';
    if (!rows.length) {
      listEl.appendChild(el('p', 'trs-empty', L.s_none));
      return;
    }
    rows.forEach(function (row, i) { listEl.appendChild(drawRow(row, i)); });
  }

  function runTest(row) {
    row.msg = { run: true, text: L.s_testing };
    draw();
    var id = row.address.trim() === row.saved ? row.id : '';
    post('test', { id: id || '', address: row.address, token: row.token })
      .then(function (r) {
        row.msg = r.ok ? { ok: true, text: C.fmt(L.s_ok, { name: r.name || '', version: r.version || '' }) }
          : { ok: false, text: errorText(r.error), code: r.error };
        draw();
      })
      .catch(function () { row.msg = { ok: false, text: errorText('engine') }; draw(); });
  }

  function apply() {
    var res = document.getElementById('trs-apply-res');
    res.className = 'trs-res run';
    res.textContent = '';
    var edits = rows.map(function (r) {
      return { id: r.id || '', address: r.address, token: r.token, trust: r.trust };
    });
    function failed(text) {
      res.className = 'trs-res err';
      res.textContent = '✗ ' + text;
    }
    post('sources', { sources: edits }).then(function (r) {
      if (!r.sources) {
        failed(errorText(r.error));
        if (typeof r.index === 'number' && rows[r.index]) {
          rows[r.index].msg = { ok: false, text: errorText(r.error) };
          draw();
        }
        return;
      }
      fromServer(r.sources);
      // The service reads the apps when a board is asked for, so one read
      // fills in how each app answered.
      get('board').then(load, load);
      return post('notify', { channels: notifyList() }).then(function (n) {
        if (!n.channels) { failed(notifyError(n.error)); return; }
        showNotify(n.channels);
        res.className = 'trs-res ok';
        res.textContent = L.s_saved;
      });
    }).catch(function () { failed(errorText('engine')); });
  }

  var N = {
    unraid: document.getElementById('trs-n-unraid'),
    apprise: document.getElementById('trs-n-apprise'),
    hs: document.getElementById('trs-n-hs'),
    room: document.getElementById('trs-n-room'),
    token: document.getElementById('trs-n-token'),
    on: document.getElementById('trs-n-on')
  };
  var channels = [], savedHs = '', tokenSaved = false;
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

  // Each switched on channel of the form gets the test message, saved or not.
  function testNotify() {
    var res = document.getElementById('trs-n-res');
    var list = notifyList().filter(function (c) { return NAMES[c.id] && c.enabled; });
    res.className = 'trs-res';
    res.textContent = '';
    if (!list.length) { res.textContent = L.s_n_test_none; return; }
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
      else {
        var res = document.getElementById('trs-n-res');
        res.className = 'trs-res err';
        res.textContent = '✗ ' + errorText(r.error || 'engine');
      }
    });
  }

  function find() {
    var res = document.getElementById('trs-find-res');
    var box = document.getElementById('trs-found');
    var ul = document.getElementById('trs-found-list');
    res.className = 'trs-res run';
    res.textContent = L.s_finding;
    get('discover').then(function (r) {
      var found = (r.found || []).filter(function (f) {
        return !rows.some(function (row) { return row.address.trim() === f.address; });
      });
      ul.textContent = '';
      res.textContent = r.error ? errorText(r.error) : found.length ? '' : L.s_found_none;
      res.className = r.error ? 'trs-res err' : 'trs-res';
      box.hidden = !found.length;
      found.forEach(function (f) {
        var li = el('li');
        li.appendChild(el('b', '', f.name + ' ' + f.version));
        li.appendChild(el('code', '', f.address));
        var add = el('button', 'trs-btn trs-small', L.s_found_add);
        add.type = 'button';
        add.addEventListener('click', function () {
          rows.push({ id: '', address: f.address, saved: null, hasToken: false, token: '', trust: false, last: null, msg: null });
          li.remove();
          box.hidden = !ul.children.length;
          draw();
        });
        li.appendChild(add);
        ul.appendChild(li);
      });
    }).catch(function () {
      res.className = 'trs-res err';
      res.textContent = errorText('engine');
    });
  }

  function load() {
    get('sources').then(function (r) {
      if (r.sources) fromServer(r.sources);
      else listEl.textContent = '✗ ' + errorText(r.error || 'engine');
    });
  }

  document.getElementById('trs-add').addEventListener('click', function () {
    rows.push({ id: '', address: '', saved: null, hasToken: false, token: '', trust: false, last: null, msg: null });
    draw();
    var inputs = listEl.querySelectorAll('.trs-addr');
    if (inputs.length) inputs[inputs.length - 1].focus();
  });
  document.getElementById('trs-find').addEventListener('click', find);
  document.getElementById('trs-apply').addEventListener('click', apply);
  document.getElementById('trs-n-test').addEventListener('click', testNotify);
  N.hs.addEventListener('input', tokenHint);
  load();
  loadNotify();
})();
