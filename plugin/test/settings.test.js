'use strict';
const test = require('node:test');
const assert = require('node:assert/strict');
const path = require('node:path');
const fs = require('node:fs');
const vm = require('node:vm');

const PLUGIN = path.join(__dirname, '..', 'src', 'tollroll', 'usr', 'local', 'emhttp', 'plugins', 'tollroll');
const core = require(path.join(PLUGIN, 'scripts', 'tollroll-core.js'));
const en = JSON.parse(fs.readFileSync(path.join(PLUGIN, 'lang', 'en.json'), 'utf8'));

const TABS = ['general', 'notify', 'fortress', 'info'];

// Runs settings.js with just enough of a page for the tabs, the General and
// notification cards, and a fortress card that only keeps what it was given.
function page(answers, stored) {
  const doc = { activeElement: null };
  class Node {
    constructor() { this.listeners = {}; this.attrs = {}; this.value = ''; this.className = ''; this.text = ''; this.hidden = false; }
    addEventListener(type, fn) { (this.listeners[type] = this.listeners[type] || []).push(fn); }
    fire(type, e) { for (const fn of this.listeners[type] || []) fn(Object.assign({ preventDefault() {} }, e)); }
    appendChild(c) { return c; }
    get textContent() { return this.text; }
    set textContent(v) { this.text = String(v); }
    setAttribute(k, v) { this.attrs[k] = String(v); }
    getAttribute(k) { return k in this.attrs ? this.attrs[k] : null; }
    removeAttribute(k) { delete this.attrs[k]; }
    querySelector() { return this.label; }
    focus() { doc.activeElement = this; }
  }
  const byId = {};
  const node = (id) => byId[id] || (byId[id] = new Node());
  const tabs = TABS.map((name, i) => {
    const t = node('trs-tab-' + name);
    Object.assign(t.attrs, { 'data-trs-tab': name, 'aria-controls': 'trs-pane-' + name, 'aria-selected': String(i === 0) });
    t.label = new Node();
    t.label.text = name;
    node('trs-pane-' + name).hidden = i > 0;
    return t;
  });
  const document = {
    getElementById: node,
    createElement: () => new Node(),
    createTextNode: () => new Node(),
    querySelectorAll: (sel) => (sel.includes('trs-tab') ? tabs : [])
  };
  const store = Object.assign({}, stored);
  const localStorage = { getItem: (k) => (k in store ? store[k] : null), setItem: (k, v) => { store[k] = String(v); } };
  const gets = [];
  const posts = [];
  const reply = (body) => Promise.resolve({ json: () => Promise.resolve(body) });
  function fetch(url, opt) {
    if (opt && opt.method === 'POST') {
      const p = Object.fromEntries(opt.body);
      posts.push({ path: p.path, data: JSON.parse(p.data) });
      return reply(answers[p.path] || {});
    }
    gets.push(url);
    return reply(answers[/path=(\w+)/.exec(url)[1]] || {});
  }
  const mounted = {};
  const GlimFortressCard = {
    mount(el, adapter, opts) {
      Object.assign(mounted, { el, adapter, opts });
      return { refresh() {}, destroy() {} };
    }
  };
  const location = {};
  const matchMedia = () => ({ matches: false, addEventListener() {} });
  const window = { TollRollCore: core, TRS_T: en, TRL_LANG: 'en', TollRollGlyphs: {}, csrf_token: 't', fetch, GlimFortressCard, matchMedia };
  const ctx = vm.createContext({ window, document, fetch, URLSearchParams, location, localStorage, setTimeout, clearTimeout });
  vm.runInContext(fs.readFileSync(path.join(PLUGIN, 'scripts', 'settings.js'), 'utf8'), ctx);
  return { card: mounted, fortEl: byId['trs-fort'], byId, tabs, store, gets, posts, location, focused: () => doc.activeElement };
}

// The script builds its objects in its own realm.
const plain = (v) => JSON.parse(JSON.stringify(v));
const settle = async () => { for (let i = 0; i < 20; i++) await new Promise((r) => setImmediate(r)); };

const ICON = 'data:image/svg+xml;base64,' + Buffer.from('<svg xmlns="http://www.w3.org/2000/svg"/>').toString('base64');
const members = [
  { id: 'k1', app: 'knightloader', kind: 'instance', name: 'KnightLoader', version: '2.1.0', device: 'cellar', deployment: 'container', online: true, icon: ICON },
  { id: 's1', app: 'shiplog', kind: 'instance', name: 'ShipLog', version: '1.4.0', device: 'attic', deployment: 'plugin', online: true, relay: true, icon: 'javascript:alert(1)' },
  { id: 'v1', app: 'bombvault', kind: 'viewer', name: 'BombVault', device: 'phone', online: true },
  { id: 'f1', app: 'firesquire', kind: 'instance', name: 'FireSquire', version: '1.0.0', device: 'attic', deployment: 'plugin', online: false, lastSeen: '2026-11-12T08:00:00Z' }
];
const apps = [
  { key: 'knightloader', name: 'KnightLoader', state: 'ok', instances: 1 },
  { key: 'shiplog', name: 'ShipLog', state: 'warning', instances: 1 }
];
const joined = (over) => Object.assign({ joined: true, device: 'tower', members }, over);
const out = { joined: false, members: [] };

test('the card sits in its own element, in the page language, for TollRoll', () => {
  const p = page({});
  assert.equal(p.card.el, p.fortEl);
  assert.equal(p.card.opts.lang, 'en');
  assert.deepEqual({ ...p.card.opts.app }, { id: 'tollroll', name: 'TollRoll' });
});

test('the card gets the instances of other apps with their form, logo and way in', async () => {
  const p = page({ fortress: joined({ removed: [{ app: 'lantern', name: 'lantern', device: 'attic' }] }) });
  const s = await p.card.adapter.load();
  assert.equal(s.joined, true);
  assert.equal(s.device, 'tower');
  assert.deepEqual(s.removed.map((r) => r.name), ['lantern']);
  assert.deepEqual(s.members.map((m) => m.id), ['k1', 's1', 'f1'], 'a viewer is no app instance');
  const [k, sl, f] = s.members;
  assert.deepEqual([k.form, k.version, k.icon, k.relay], ['container', '2.1.0', ICON, false]);
  assert.deepEqual([sl.form, sl.icon, sl.relay], ['plugin', '', true], 'a logo that is no SVG data URI stays off');
  assert.deepEqual([f.online, f.lastSeen], [false, '2026-11-12T08:00:00Z']);
});

test('a service that does not answer hides the card', async () => {
  const p = page({ fortress: { error: 'engine' } });
  assert.equal(await p.card.adapter.load(), null);
});

test('a phrase the tile could not follow, and a removal by another app, reach the card', async () => {
  let s = await page({ fortress: joined({ phraseChanged: true }) }).card.adapter.load();
  assert.equal(s.phraseChanged, true);
  s = await page({ fortress: { joined: false, removedHere: true, members: [] } }).card.adapter.load();
  assert.deepEqual([s.joined, s.removedHere], [false, true]);
});

test('in a fortress a new phrase TollRoll did not pick up can still be entered', async () => {
  const words = 'one two three four five six seven eight nine ten eleven twelve'.split(' ');
  const p = page({ fortress: joined(), join: joined({ device: 'tower' }) });
  const s = await p.card.adapter.load();
  assert.deepEqual(plain(s.hints), [{ text: en.s_rejoin_hint, tone: 'quiet', action: { label: en.s_rejoin, join: true } }]);
  assert.equal((await p.card.adapter.join(words)).joined, true);
  assert.deepEqual(p.posts, [{ path: 'join', data: { phrase: words.join(' ') } }]);
});

test('the line for a new phrase gives way to the card while it asks for one itself', async () => {
  assert.deepEqual(plain((await page({ fortress: joined({ phraseChanged: true }) }).card.adapter.load()).hints), []);
  assert.deepEqual(plain((await page({ fortress: out }).card.adapter.load()).hints), []);
});

test('the twelve words join, and words that open no fortress are said so', async () => {
  const words = 'one two three four five six seven eight nine ten eleven twelve'.split(' ');
  let p = page({ join: joined() });
  const s = await p.card.adapter.join(words);
  assert.deepEqual(p.posts[0], { path: 'join', data: { phrase: words.join(' ') } });
  assert.equal(s.joined, true);
  p = page({ join: { error: 'phrase_invalid' } });
  await assert.rejects(p.card.adapter.join(words), (e) => e.code === 'phrase');
  p = page({ join: { error: 'join_failed' } });
  await assert.rejects(p.card.adapter.join(words), (e) => e.code === 'join_failed');
});

test('leaving and removing answer with the new state, a member already gone closes quietly', async () => {
  let p = page({ leave: out });
  assert.equal((await p.card.adapter.leave()).joined, false);
  assert.equal(p.posts[0].path, 'leave');
  p = page({ remove: joined({ members: members.slice(1), removed: [{ app: 'knightloader', name: 'KnightLoader', device: 'cellar' }] }) });
  const s = await p.card.adapter.remove('k1');
  assert.deepEqual(p.posts[0], { path: 'remove', data: { id: 'k1' } });
  assert.equal(s.removed.length, 1);
  p = page({ remove: { error: 'no_member' } });
  await assert.rejects(p.card.adapter.remove('k1'), (e) => e.code === 'noMember');
});

test("a member's status comes with its times written for the reader", async () => {
  const p = page({ status: {
    report: { state: 'warning', needsYou: [{ severity: 'warn', text: 'Low on space' }], work: [{ title: 'Copying', progress: 0.5 }], next: [{ title: 'Check', at: '2026-11-12T22:00:00Z' }] },
    log: [{ at: '2026-11-12T10:05:00Z', level: 'warn', text: 'Slow disk' }]
  } });
  const r = await p.card.adapter.status('s1');
  assert.match(p.gets[p.gets.length - 1], /path=status&id=s1&lang=en$/);
  assert.equal(r.report.state, 'warning');
  assert.deepEqual(r.report.needsYou, [{ severity: 'warn', text: 'Low on space' }]);
  assert.deepEqual(plain(r.report.next), [{ title: 'Check', at: core.timeLabel('2026-11-12T22:00:00Z', 'en') }]);
  assert.deepEqual(plain(r.log), [{ at: core.timeLabel('2026-11-12T10:05:00Z', 'en'), level: 'warn', text: 'Slow disk' }]);
  await assert.rejects(page({ status: { error: 'unreachable' } }).card.adapter.status('s1'));
});

test('General says what the tile shows and leads to its map', async () => {
  const p = page({ fortress: joined(), board: { apps }, tile: { view: 'rows', hidden: ['shiplog'] } });
  await p.card.adapter.load();
  await settle();
  assert.equal(p.byId['trs-tile-line'].textContent, core.subtitle({ apps }, en, 'en', core.mapDevices(joined(), apps).length), 'the line says what the tile says');
  p.byId['trs-map'].fire('click');
  await settle();
  assert.deepEqual(p.posts, [{ path: 'tile', data: { view: 'map', hidden: ['shiplog'] } }]);
  assert.equal(p.location.href, '/Dashboard');
});

test('outside a fortress General says the tile asks for the phrase, and the card has no row of TollRoll', async () => {
  const p = page({ fortress: out, board: { apps: [] } });
  await p.card.adapter.load();
  await settle();
  assert.equal(p.byId['trs-tile-line'].textContent, en.t_join_title);
  assert.ok(!(p.card.opts.rows || []).length);
});

const shown = (p) => TABS.filter((t) => !p.byId['trs-pane-' + t].hidden);
const selected = (p) => p.tabs.filter((t) => t.getAttribute('aria-selected') === 'true').map((t) => t.getAttribute('data-trs-tab'));

test('a tab opens its panel and is kept for the next visit', () => {
  const p = page({});
  p.tabs[2].fire('click');
  assert.deepEqual(shown(p), ['fortress']);
  assert.deepEqual(selected(p), ['fortress']);
  assert.deepEqual(p.tabs.map((t) => t.tabIndex), [-1, -1, 0, -1]);
  assert.equal(p.store['tollroll.settings.tab'], 'fortress');
});

test('the arrow keys move between the tabs and wrap around', () => {
  const p = page({});
  p.tabs[0].fire('keydown', { key: 'ArrowUp' });
  assert.deepEqual(selected(p), ['info']);
  assert.equal(p.focused(), p.tabs[3]);
  p.tabs[3].fire('keydown', { key: 'ArrowDown' });
  assert.deepEqual(selected(p), ['general']);
  p.tabs[0].fire('keydown', { key: 'End' });
  assert.deepEqual(shown(p), ['info']);
  p.tabs[3].fire('keydown', { key: 'Home' });
  assert.deepEqual(shown(p), ['general']);
});
