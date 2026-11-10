'use strict';
const test = require('node:test');
const assert = require('node:assert/strict');
const path = require('node:path');
const fs = require('node:fs');

const ROOT = path.join(__dirname, '..');
const PLUGIN = path.join(ROOT, 'src', 'tollroll', 'usr', 'local', 'emhttp', 'plugins', 'tollroll');
const read = (...p) => fs.readFileSync(path.join(PLUGIN, ...p), 'utf8');
const LANGS = ['ar', 'cs', 'da', 'de', 'el', 'en', 'es', 'fi', 'fr', 'he', 'hu', 'it', 'ja', 'ko', 'nl', 'no', 'pl', 'pt', 'ro', 'ru', 'sv', 'th', 'tr', 'uk', 'vi', 'zh'];
const PLURALS = ['t_apps', 't_attn', 't_inst'];

function walk(dir) {
  return fs.readdirSync(dir, { withFileTypes: true }).flatMap((e) => {
    const p = path.join(dir, e.name);
    return e.isDirectory() ? walk(p) : [p];
  });
}

const base = (k) => PLURALS.find((b) => k.startsWith(b + '_')) || k;

test('every language has every string of the English file', () => {
  const en = JSON.parse(read('lang', 'en.json'));
  const want = [...new Set(Object.keys(en).map(base))].sort();
  for (const l of LANGS) {
    const tr = JSON.parse(read('lang', l + '.json'));
    assert.deepEqual([...new Set(Object.keys(tr).map(base))].sort(), want, l);
    for (const b of PLURALS) {
      const cats = new Intl.PluralRules(l).resolvedOptions().pluralCategories;
      for (const c of cats) assert.ok(tr[b + '_' + c], `${l}: ${b}_${c} is missing`);
    }
    for (const [k, v] of Object.entries(tr)) {
      for (const ph of (en[k] || en[base(k) + '_other'] || '').match(/\{\w+\}/g) || []) {
        assert.ok(v.includes(ph), `${l}.${k} lacks ${ph}`);
      }
    }
  }
});

test('no string carries a dash or a semicolon', () => {
  for (const l of LANGS) {
    for (const [k, v] of Object.entries(JSON.parse(read('lang', l + '.json')))) {
      assert.doesNotMatch(v, /[\u2013\u2014;]/, `${l}.${k}`);
    }
  }
});

test('the service, the proxy and the removal share one socket', () => {
  const rc = read('scripts', 'rc.tollroll');
  const sock = /^SOCKET="([^"]+)"/m.exec(rc);
  assert.ok(sock, 'rc.tollroll sets no SOCKET');
  assert.match(rc, /export[^\n]*\bSOCKET\b/);
  const proxy = read('server', 'api.php');
  assert.ok(proxy.includes('CURLOPT_UNIX_SOCKET_PATH') && proxy.includes(`'${sock[1]}'`), 'api.php does not dial the socket');
  assert.doesNotMatch(proxy, /127\.0\.0\.1|localhost:\d/);
  const plg = fs.readFileSync(path.join(ROOT, 'tollroll.plg'), 'utf8');
  assert.ok(plg.includes(sock[1].replace('tollroll', '&slug;')), 'tollroll.plg does not remove the socket');
});

test('the proxy forwards only what the pages use', () => {
  const proxy = read('server', 'api.php');
  for (const route of ['/api/board', '/api/sources', '/api/discover', '/api/test']) {
    assert.ok(proxy.includes(`'${route}'`), route);
  }
  assert.doesNotMatch(proxy, /\$_(GET|POST)\[[^\]]+\]\s*\.\s*['"]?\//, 'a request value builds a path');
});

test('the pages and scripts ship with LF line endings', () => {
  for (const f of walk(PLUGIN).filter((f) => !f.endsWith('.png'))) {
    assert.ok(!fs.readFileSync(f, 'utf8').includes('\r'), path.relative(ROOT, f));
  }
});

test('the plg is plain XML', () => {
  const plg = fs.readFileSync(path.join(ROOT, 'tollroll.plg'), 'utf8');
  const changes = /<CHANGES>([\s\S]*?)<\/CHANGES>/.exec(plg)[1];
  assert.doesNotMatch(changes, /[<]|&(?!amp;|lt;|gt;|quot;|apos;)/, 'raw < or & in CHANGES');
  for (const c of plg.match(/<!--[\s\S]*?-->/g) || []) {
    assert.ok(!c.slice(4, -3).includes('--'), 'a comment holds --: ' + c.slice(0, 60));
  }
  assert.match(plg, /<PLUGIN name="&name;"/);
  assert.match(plg, /<!ENTITY name\s+"tollroll">/);
});

test('the tile keeps its English title as its identity', () => {
  assert.match(read('tollroll.Dashboard.page'), /<tbody title="TollRoll">/);
  assert.match(read('tollroll.Dashboard.page'), /^Menu="Dashboard:0"$/m);
});

test('no page ever writes a token into the browser', () => {
  for (const f of ['tollroll.page', 'tollroll.Dashboard.page', 'scripts/tile.js', 'scripts/settings.js']) {
    assert.doesNotMatch(read(f), /parse_plugin_cfg|sources\.json/, f);
  }
});
