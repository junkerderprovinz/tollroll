'use strict';
const test = require('node:test');
const assert = require('node:assert/strict');
const path = require('node:path');
const fs = require('node:fs');

const ROOT = path.join(__dirname, '..');
const PLUGIN = path.join(ROOT, 'src', 'tollroll', 'usr', 'local', 'emhttp', 'plugins', 'tollroll');
const read = (...p) => fs.readFileSync(path.join(PLUGIN, ...p), 'utf8');
const LANGS = ['ar', 'cs', 'da', 'de', 'el', 'en', 'es', 'fi', 'fr', 'he', 'hu', 'it', 'ja', 'ko', 'nl', 'no', 'pl', 'pt', 'ro', 'ru', 'sv', 'th', 'tr', 'uk', 'vi', 'zh'];
const PLURALS = ['t_apps', 't_attn', 't_inst', 't_dev', 't_map', 't_lines', 't_sel'];

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
  for (const route of ['/api/board', '/api/icons', '/api/notify', '/api/notify/test',
    '/api/fortress', '/api/fortress/join', '/api/fortress/leave', '/api/fortress/remove', '/api/fortress/status', '/api/tile']) {
    assert.ok(proxy.includes(`'${route}'`), route);
  }
  assert.doesNotMatch(proxy, /\/api\/(sources|discover|test)'/, 'apps come only through the fortress');
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

test('the newest changes block matches its release notes', () => {
  const plg = fs.readFileSync(path.join(ROOT, 'tollroll.plg'), 'utf8');
  const changes = /<CHANGES>([\s\S]*?)<\/CHANGES>/.exec(plg)[1];
  const [, version, block] = /^### (\S+)\n([\s\S]*?)(?=^### |(?![\s\S]))/m.exec(changes);
  const labels = ['✨ **Added**', '🎨 **Design**', '⚡ **Improved**', '🐛 **Fixed**', '🌐 **Translations**'];
  for (const line of block.match(/^\S+ \*\*.+$/gm)) assert.ok(labels.includes(line), line);
  // The release workflow turns the labels into headings for the release page.
  const notes = fs.readFileSync(path.join(ROOT, '..', '.github', 'release-notes', `v${version}.md`), 'utf8');
  assert.equal(notes.trim(), block.trim().replace(/^(\S+) \*\*(.+)\*\*$/gm, '## $1 $2'));
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

test('a logo reaches the tile only as the source of an img', () => {
  const tile = read('scripts', 'tile.js');
  for (const m of tile.match(/\.innerHTML\s*=[^;]+/g) || []) {
    assert.match(m, /=\s*GLYPH\[/, 'innerHTML takes something else than a glyph: ' + m);
  }
  assert.match(tile, /img\.src = src/);
  assert.match(tile, /var src = C\.safeIcon\(/);
  assert.doesNotMatch(tile, /insertAdjacentHTML|outerHTML|DOMParser|<object|<iframe|<embed/);
});

test('the tile gets no settings or notification texts', () => {
  const page = read('tollroll.Dashboard.page');
  assert.match(page, /strncmp\(\$trl_k, 's_', 2\) !== 0 && strncmp\(\$trl_k, 'n_', 2\) !== 0/);
});

test('a removed TollRoll points at the current phrase, since the one it knew may have changed', () => {
  const en = JSON.parse(read('lang', 'en.json'));
  const de = JSON.parse(read('lang', 'de.json'));
  assert.match(en.t_removed_text, /current phrase/);
  assert.match(de.t_removed_text, /aktuellen Phrase/);
  for (const k of ['t_removed_title', 't_removed_text']) {
    assert.doesNotMatch(de[k], /entfernt/, `de.${k} says herausgenommen, as the apps do`);
  }
});

test('the settings tabs run General, Notifications, Fortress and Info, each over its panel', () => {
  const page = read('tollroll.page');
  const attr = (tag, a) => (new RegExp(` ${a}="([^"]*)"`).exec(tag) || [])[1];
  const tabs = page.match(/<button [^>]*class="trs-tab"[^>]*>/g);
  assert.deepEqual(tabs.map((t) => attr(t, 'data-trs-tab')), ['general', 'notify', 'fortress', 'info'], 'the Fortress sits just above Info');
  tabs.forEach((t, i) => {
    assert.equal(attr(t, 'aria-selected'), String(i === 0));
    assert.equal(attr(t, 'tabindex'), i ? '-1' : undefined);
    const pane = page.match(new RegExp(`<div class="trs-pane" role="tabpanel" id="${attr(t, 'aria-controls')}"[^>]*>`));
    assert.ok(pane, attr(t, 'aria-controls'));
    assert.equal(attr(pane[0], 'aria-labelledby'), attr(t, 'id'));
    assert.equal(/ hidden>$/.test(pane[0]), i > 0);
  });
  const at = (s) => page.indexOf(s);
  assert.ok(at('id="trs-pane-general"') < at('id="trs-tile-line"') && at('id="trs-tile-line"') < at('id="trs-pane-notify"'), 'General holds the tile line');
  assert.ok(at('id="trs-pane-fortress"') < at('id="trs-fort"') && at('id="trs-fort"') < at('id="trs-pane-info"'), 'the Fortress panel holds the card');
});

test('the tab chosen last opens before the first paint', () => {
  const page = read('tollroll.page');
  assert.match(page, /localStorage\.getItem\('tollroll\.settings\.tab'\)/);
  assert.ok(page.indexOf("getItem('tollroll.settings.tab')") > page.indexOf('id="trs-pane-info"'), 'the panels exist before the script picks one');
  assert.match(read('scripts', 'settings.js'), /localStorage\.setItem\(TAB_KEY/);
});

test('Info names the GlimStone version the plugin is built on and links each version to its release', () => {
  const page = read('tollroll.page');
  const { targets } = JSON.parse(fs.readFileSync(path.join(ROOT, '..', 'glimstone.json'), 'utf8'));
  assert.ok(page.includes(`$trs_glimstone = '${targets[0].version}';`), `Info does not name GlimStone ${targets[0].version}`);
  assert.match(page, /\/releases\/tag\/v/);
  assert.doesNotMatch(page, /class="button[^"]*trs-ver/, 'a version is a plain link');
});
