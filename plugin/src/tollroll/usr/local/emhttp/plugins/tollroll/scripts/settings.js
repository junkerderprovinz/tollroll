/* The notification channels on the settings page, read from the service and
 * written back as a whole on Apply. A token field stays empty: typing in it
 * replaces the saved token, leaving it empty keeps that one. */
(function () {
  'use strict';
  var C = window.TollRollCore, L = window.TRS_T || {};
  var API = '/plugins/tollroll/server/api.php';

  function el(tag, cls, text) {
    var n = document.createElement(tag);
    if (cls) n.className = cls;
    if (text != null) n.textContent = text;
    return n;
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
    body.set('data', JSON.stringify(data));
    body.set('csrf_token', window.csrf_token || '');
    return fetch(API, { method: 'POST', body: body, headers: { Accept: 'application/json' } })
      .then(function (r) { return r.json().catch(function () { return { error: 'engine' }; }); });
  }

  function apply() {
    var res = document.getElementById('trs-apply-res');
    res.className = 'trs-res run';
    res.textContent = '';
    function failed(text) {
      res.className = 'trs-res err';
      res.textContent = '✗ ' + text;
    }
    post('notify', { channels: notifyList() }).then(function (n) {
      if (!n.channels) { failed(notifyError(n.error)); return; }
      showNotify(n.channels);
      res.className = 'trs-res ok';
      res.textContent = L.s_saved;
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

  document.getElementById('trs-apply').addEventListener('click', apply);
  document.getElementById('trs-n-test').addEventListener('click', testNotify);
  N.hs.addEventListener('input', tokenHint);
  loadNotify();
})();
