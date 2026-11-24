'use strict';
const test = require('node:test');
const assert = require('node:assert/strict');
const path = require('node:path');
const fs = require('node:fs');
const os = require('node:os');
const { spawnSync } = require('node:child_process');

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
      // Greek writes its question mark as a semicolon.
      const text = l === 'el' ? v.replace(/;(\s|$)/g, '?$1') : v;
      assert.doesNotMatch(text, /[\u2013\u2014;]/, `${l}.${k}`);
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
    '/api/fortress', '/api/fortress/join', '/api/fortress/leave', '/api/fortress/remove', '/api/fortress/status', '/api/tile', '/api/watch']) {
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

test('the newest released changes block matches its release notes', () => {
  const plg = fs.readFileSync(path.join(ROOT, 'tollroll.plg'), 'utf8');
  const changes = /<CHANGES>([\s\S]*?)<\/CHANGES>/.exec(plg)[1];
  const [, version, block] = /^### (\d\S*)\n([\s\S]*?)(?=^### |(?![\s\S]))/m.exec(changes);
  const labels = ['✨ **Added**', '🎨 **Design**', '⚡ **Improved**', '🐛 **Fixed**', '🌐 **Translations**'];
  for (const line of block.match(/^(?!- )\S+ \*\*.+$/gm)) assert.ok(labels.includes(line), line);
  // The release workflow turns the labels into headings for the release page.
  const notes = fs.readFileSync(path.join(ROOT, '..', '.github', 'release-notes', `v${version}.md`), 'utf8');
  assert.equal(notes.trim(), block.trim().replace(/^(?!- )(\S+) \*\*(.+)\*\*$/gm, '## $1 $2'));
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

test('the Help card offers Discord right after GitHub, and every language names it', () => {
  const page = read('tollroll.page');
  const help = page.slice(page.indexOf('id="trs-h-help"'), page.indexOf('id="trs-h-about"'));
  const buttons = [...help.matchAll(/\$trs_rbtn\('(\w+)', '[^']*', '([^']+)'\)/g)].map((m) => [m[1], m[2]]);
  assert.deepEqual(buttons, [
    ['github', 'https://github.com/junkerderprovinz/tollroll/issues'],
    ['discord', 'https://discord.gg/aRu2WjkR59'],
  ]);
  assert.match(read('include', 'marks.php'), /'discord' => '<svg /);
  assert.match(read('styles', 'settings.css'), /\.trs-tile-discord \{ --trs-give: #5865f2;/);
  for (const l of LANGS) {
    assert.match(JSON.parse(read('lang', l + '.json')).s_help_body, /Discord/, l);
  }
});

test("the Help sentence is GlimStone's own", () => {
  assert.equal(JSON.parse(read('lang', 'en.json')).s_help_body, "Problems, wishes or suggestions? You're welcome to open an issue on GitHub, or come say hi on Discord.");
  assert.equal(JSON.parse(read('lang', 'de.json')).s_help_body, 'Probleme, Wünsche oder Verbesserungsvorschläge? Mach gern ein Issue auf GitHub auf, oder schau auf Discord vorbei.');
});

test('every version opens its newest release, since patch tags fold away', () => {
  const page = read('tollroll.page');
  const row = page.slice(page.indexOf('>GlimStone<'), page.indexOf('id="trs-h-help"'));
  assert.match(row, /\$trs_num\(\$trs_glimstone, 'https:\/\/github\.com\/junkerderprovinz\/glimstone'\)/);
  assert.match(page, /\$repo\.'\/releases\/latest'/);
  assert.doesNotMatch(page, /\/releases\/tag\/v/);
});

test("a README button's focus ring is the opaque accent ink, as GlimStone draws it", () => {
  const rule = /#trs-cfg \.trs-rbtn:focus-visible \{([^}]*)\}/.exec(read('styles', 'settings.css'));
  assert.ok(rule, 'no focus rule for the README buttons');
  assert.match(rule[1], /var\(--tt-accent-ink, #FCC419\)/);
  assert.doesNotMatch(rule[1], /color-mix|transparent/);
});

test("a README button's name keeps its descenders inside its clip", () => {
  assert.match(read('styles', 'settings.css'), /#trs-cfg \.trs-rbtn-name \{[^}]*overflow: hidden;[^}]*padding-block: \.15em; margin-block: -\.15em;/);
});

test('Info names the GlimStone version the plugin is built on, and a version never looks like a button', () => {
  const page = read('tollroll.page');
  const { targets } = JSON.parse(fs.readFileSync(path.join(ROOT, '..', 'glimstone.json'), 'utf8'));
  assert.ok(targets.every((t) => t.version === '4.9.0'), 'glimstone.json pins every part to 4.9.0');
  assert.ok(page.includes(`$trs_glimstone = '${targets[0].version}';`), `Info does not name GlimStone ${targets[0].version}`);
  assert.equal((page.match(/<\?=\$trs_num\(/g) || []).length, 2, 'the app and GlimStone show their number through $trs_num');
  assert.doesNotMatch(page, /class="[^"]*(button|trs-btn)[^"]*trs-ver/, 'a version is plain text');
  const css = read('styles', 'settings.css');
  const rest = /\.trs-page a\.trs-ver \{([^}]*)\}/.exec(css);
  assert.ok(rest, 'no rule for a linked version');
  for (const want of ['background: none', 'border: 0', 'color: inherit', 'text-decoration: none']) assert.ok(rest[1].includes(want), want);
  assert.match(css, /#trs-cfg a\.trs-ver \{ color: inherit !important; text-decoration: none !important; \}/);
  assert.match(css, /#trs-cfg a\.trs-ver:focus-visible \{ color: var\(--trs-focus\) !important; \}/);
  assert.match(css, /@media \(hover: hover\) \{[^}]*\}\s*#trs-cfg a\.trs-ver:hover \{ color: var\(--trs-focus\) !important; \}/);
  assert.doesNotMatch(css, /trs-ver[^{]*\{[^}]*underline/);
});

test('a version number is a 24px target that keeps the line at its height', () => {
  const rest = /\.trs-page a\.trs-ver \{([^}]*)\}/.exec(read('styles', 'settings.css'));
  assert.ok(rest, 'no rule for a linked version');
  assert.match(rest[1], /padding: 2px 0;/);
  assert.match(rest[1], /margin: -2px 0;/);
});

test('a long build number drops under the app name, and a component row breaks before its number', () => {
  const css = read('styles', 'settings.css');
  const head = /\.trs-vhead \{([^}]*)\}/.exec(css)[1];
  assert.match(head, /flex-wrap: wrap;/);
  const num = /\.trs-vhead \.trs-ver \{([^}]*)\}/.exec(css);
  assert.ok(num, 'no rule for the number in the head');
  for (const want of ['min-width: 0', 'margin-inline-start: auto', 'overflow-wrap: anywhere']) assert.ok(num[1].includes(want), want);
  const row = /\.trs-vrow \{([^}]*)\}/.exec(css)[1];
  assert.match(row, /flex-wrap: wrap;/);
  assert.match(css, /\.trs-vrow > \.trs-ver \{[^}]*margin-inline-start: auto;/);
});

test('the Info tab holds Version, Components, Help and About, in that order', () => {
  const page = read('tollroll.page');
  const pane = page.slice(page.indexOf('id="trs-pane-info"'), page.indexOf('<script>', page.indexOf('id="trs-pane-info"')));
  const cards = [...pane.matchAll(/<section class="trs-card" aria-labelledby="trs-h-(\w+)">/g)].map((m) => m[1]);
  assert.deepEqual(cards, ['version', 'components', 'help', 'about']);
  assert.equal(JSON.parse(read('lang', 'en.json')).s_about, 'About');
});

test("the Version and Components cards speak every language with GlimStone's words", () => {
  const blanks = { s_vc_hint: '{app}', s_vc_checked: '{when}', s_vc_available: '{version}', s_vc_unreleased: '{version}', s_cc_hint: '{app}', s_notes_title: '{version}' };
  const keys = ['s_vc_title', 's_vc_hint', 's_vc_whats_new', 's_vc_check', 's_vc_checking', 's_vc_unchecked', 's_vc_current', 's_vc_checked',
    's_vc_available', 's_vc_failed', 's_vc_plugins', 's_vc_unreleased', 's_cc_title', 's_cc_hint', 's_cc_design',
    's_notes_title', 's_notes_loading', 's_notes_none', 's_notes_failed', 's_notes_github'];
  for (const l of LANGS) {
    const t = JSON.parse(read('lang', l + '.json'));
    for (const k of keys) {
      assert.ok(t[k], `${l}: ${k}`);
      if (blanks[k]) assert.ok(t[k].includes(blanks[k]), `${l}: ${k} keeps ${blanks[k]}`);
    }
  }
});

test("the update state is what Unraid's Plugins tab shows", (t) => {
  const php = spawnSync('php', ['-v']);
  if (php.error) {
    if (process.env.CI) assert.fail('php is not installed');
    t.skip('php is not installed');
    return;
  }
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'trs-update-'));
  const plg = (name, v) => {
    const p = path.join(dir, name);
    fs.writeFileSync(p, `<!DOCTYPE PLUGIN [
  <!ENTITY version   "${v}">
]>
`);
    return p;
  };
  const installed = plg('installed.plg', '1.2.1');
  const state = (fetched) => {
    const code = `$U = include ${JSON.stringify(path.join(PLUGIN, 'include', 'update.php'))}; echo json_encode($U(${JSON.stringify(installed)}, ${JSON.stringify(fetched)}));`;
    const r = spawnSync('php', ['-r', code], { encoding: 'utf8' });
    assert.equal(r.status, 0, r.stderr);
    return JSON.parse(r.stdout);
  };
  assert.deepEqual(state(path.join(dir, 'missing.plg')), { kind: 'unchecked' });
  assert.deepEqual(state(plg('newer.plg', '1.2.2')), { kind: 'available', version: '1.2.2' });
  const same = state(plg('same.plg', '1.2.1'));
  assert.equal(same.kind, 'current');
  assert.equal(typeof same.checkedAt, 'number');
  // Unraid compares with strcmp, so it offers nothing at the x.9 to x.10 step.
  assert.equal(state(plg('ten.plg', '1.10.0')).kind, 'current');
  fs.rmSync(dir, { recursive: true, force: true });
});

test("Check for updates runs the check of Unraid's Plugins tab, and the action leads there", () => {
  const src = read('server', 'update-check.php');
  assert.ok(src.includes("exec('/usr/local/sbin/plugin check tollroll.plg 2>&1', $out, $rc);"));
  assert.ok(src.includes("if ($_SERVER['REQUEST_METHOD'] !== 'POST') {"), 'a GET is not checked for the CSRF token');
  const js = read('scripts', 'settings.js');
  assert.match(js, /fetch\('\/plugins\/tollroll\/server\/update-check\.php', \{ method: 'POST'/);
  assert.match(js, /getElementById\('trs-plugins'\)\.addEventListener\('click', function \(\) \{ location\.href = '\/Plugins'; \}\);/);
  assert.match(read('tollroll.page'), /<button type="button" class="trs-btn trs-btn-accent tt-fm-primary" id="trs-plugins" hidden>/);
});

test("What's new reads the notes of the running release, or of the waiting one, from the plg", (t) => {
  if (spawnSync('php', ['-v']).error) {
    if (process.env.CI) assert.fail('php is not installed');
    t.skip('php is not installed');
    return;
  }
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'trs-notes-'));
  const plg = (name, v) => {
    const p = path.join(dir, name);
    fs.writeFileSync(p, `<?xml version='1.0' standalone='yes'?>
<!DOCTYPE PLUGIN [
  <!ENTITY name      "tollroll">
  <!ENTITY version   "${v}">
]>
<PLUGIN name="&name;">
<CHANGES>
What TollRoll does.

### 1.2.2
The newer one.

🐛 **Fixed**
- **A fix.** Under Settings &gt; TollRoll.

### 1.2.1
The running one.

✨ **Added**
- **A feature.** With &amp; and \`code\`.
- **Bold alone.**

### 1.2.0
An older one.
</CHANGES>
</PLUGIN>
`);
    return p;
  };
  const notes = (installed, fetched) => {
    const code = `$N = include ${JSON.stringify(path.join(PLUGIN, 'include', 'notes.php'))}; echo json_encode($N(${JSON.stringify(installed)}, ${JSON.stringify(fetched)}));`;
    const r = spawnSync('php', ['-r', code], { encoding: 'utf8' });
    assert.equal(r.status, 0, r.stderr);
    return JSON.parse(r.stdout);
  };
  const missing = path.join(dir, 'missing.plg');
  assert.deepEqual(notes(plg('installed.plg', '1.2.1'), missing),
    { version: '1.2.1', notes: 'The running one.\n\n## ✨ Added\n- **A feature.** With & and `code`.\n- **Bold alone.**' });
  const newer = notes(plg('installed.plg', '1.2.1'), plg('fetched.plg', '1.2.2'));
  assert.equal(newer.version, '1.2.2');
  assert.equal(newer.notes, 'The newer one.\n\n## 🐛 Fixed\n- **A fix.** Under Settings > TollRoll.');
  assert.equal(notes(plg('build.plg', '1.2.1.fix.c7d03cf'), missing).version, '1.2.2', 'a build that is not a release reads the newest block');
  assert.deepEqual(notes(plg('bare.plg', '1.2.3'), missing), { version: '1.2.3', notes: '' });
  fs.rmSync(dir, { recursive: true, force: true });
});

test("What's new opens a window of the plugin with the notes from TollRoll's own server", () => {
  const page = read('tollroll.page');
  assert.match(page, /<button type="button" class="trs-btn" id="trs-whatsnew" aria-haspopup="dialog"><span class="trs-vdot" aria-hidden="true"><\/span>/);
  assert.ok(page.indexOf("scripts/release-notes.js')?>") < page.indexOf("scripts/settings.js')?>"), 'the notes are drawn by a script that loads after the page\'s');
  const js = read('scripts', 'settings.js');
  assert.match(js, /fetch\('\/plugins\/tollroll\/server\/release-notes\.php', \{ headers: \{ Accept: 'application\/json' \} \}\)/);
  assert.doesNotMatch(js, /api\.github\.com|window\.open\(notesFor/, 'What\'s new still leads the browser to GitHub');
  assert.match(js, /news\.addEventListener\('click', openNotes\);/);
  // The foot holds the link to the release page at its start and Close at its end.
  assert.match(js, /act\.appendChild\(link\);\n\s+act\.appendChild\(shut\);/);
  assert.match(js, /var box = el\('div', 'glim-nc-win trs-win-notes'\);\n\s+box\.setAttribute\('role', 'dialog'\);\n\s+box\.setAttribute\('aria-modal', 'true'\);/);
  assert.match(js, /notes\.tabIndex = 0;/, 'long notes scroll, and the keyboard needs a way in');
  const src = read('server', 'release-notes.php');
  assert.ok(src.includes("$notes = include '/usr/local/emhttp/plugins/tollroll/include/notes.php';") && !src.includes('curl'));
  const css = read('styles', 'settings.css');
  assert.ok(css.includes('.glim-nc-win.trs-win-notes { width: min(760px, calc(100vw - 32px)); }'), 'the window is wide enough to read');
  assert.ok(css.includes('.trs-win-notes .glim-nc-win-act { justify-content: space-between; }'), 'the link leans on a margin TowerTorch overrides');
});

test('the dot before What\'s new stays grey, since TollRoll marks no changes', () => {
  const css = read('styles', 'settings.css');
  assert.ok(css.includes('/* The dot before "What\'s new", grey while nothing is unseen. */\n.trs-vdot { flex: none; width: 8px; height: 8px; border-radius: 50%; background: var(--trs-dim); }'));
  assert.doesNotMatch(css, /data-unseen/);
});

test("with TowerTorch every row, view, option and tab lights in its hover", () => {
  const tile = read('styles', 'tile.css');
  assert.match(tile, /html\.tt-popups-on \.trl\{--trl-hover:var\(--tt-hover,/);
  // A control TowerTorch made a badge keeps the badge's own hover.
  assert.match(tile, /html\.tt-popups-on :is\(\.trl-control:not\(\.tt-dash-btn\),\.trl-dlg-btn\):hover\{background:var\(--tt-hover,/);
  assert.match(tile, /html\.tt-popups-on \.trl-hs button:not\(\[aria-checked="true"\]\):hover\{background:var\(--tt-hover,/);
  // The list of apps is dark on every theme.
  assert.match(tile, /html\.tt-popups-on \.trl-pick-item:hover\{background:var\(--tt-hover-dark,/);
  const settings = read('styles', 'settings.css');
  assert.match(settings, /html\.tt-popups-on \.trs-page \.trs-tab:not\(\[aria-selected="true"\]\):hover \{ background: var\(--tt-hover, var\(--trs-tile\)\); box-shadow: none; \}/);
  assert.doesNotMatch(settings, /--tt-hover-wash/);
});

test('every point that needs you stands on a surface of its own, with no line round it', () => {
  const css = read('styles', 'tile.css');
  const rule = (sel) => (new RegExp(sel.replace(/[.[\]]/g, '\\$&') + '\\s*\\{([^}]*)\\}').exec(css) || [])[1] || '';
  const item = rule('.trl-dlg-list li.trl-item');
  assert.match(item, /background:\s*var\(--tt-surface-3,/, 'the item takes the step above the card');
  assert.match(item, /border-radius:\s*var\(--tt-radius-control,/);
  assert.doesNotMatch(item, /border:\s*(?!0|none)/);
  assert.match(rule('.trl-w-btn'), /border:\s*0/, 'a button in an item draws no line');
  assert.match(read('scripts', 'tile.js'), /li\.className = 'trl-item' \+/);
  const settings = read('styles', 'settings.css');
  assert.match(settings, /#trs-advice \.trs-row\s*\{[^}]*background:\s*var\(--trs-field\)[^}]*border-radius:\s*var\(--trs-r-ctl\)/);
});

test('every glyph takes the colour of the text around it', () => {
  const ctx = { window: {} };
  require('node:vm').runInNewContext(read('scripts', 'glyphs.js'), ctx);
  for (const [name, svg] of Object.entries(ctx.window.TollRollGlyphs)) {
    assert.match(/^<svg[^>]*>/.exec(svg)[0], /\sfill="currentColor"/, name);
    assert.doesNotMatch(svg, /fill="(?!currentColor|none)[^"]*"/, name);
  }
});
