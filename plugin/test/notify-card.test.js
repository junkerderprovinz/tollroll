'use strict';
const test = require('node:test');
const assert = require('node:assert/strict');
const path = require('node:path');
const fs = require('node:fs');

const PLUGIN = path.join(__dirname, '..', 'src', 'tollroll', 'usr', 'local', 'emhttp', 'plugins', 'tollroll');
const card = require(path.join(PLUGIN, 'scripts', 'notify-card.js'));
const marks = require(path.join(PLUGIN, 'scripts', 'notify-marks.js'));
const en = JSON.parse(fs.readFileSync(path.join(PLUGIN, 'lang', 'en.json'), 'utf8'));
const KINDS = ['unraid', 'discord', 'slack', 'ntfy', 'gotify', 'matrix', 'apprise', 'smtp', 'webhook'];

// Just enough of a document for the card: elements with children, attributes,
// values and events, found again by their class or their kind.
function dom() {
  class El {
    constructor(tag) { this.tagName = tag.toUpperCase(); this.children = []; this.attrs = {}; this.listeners = {}; this.value = ''; this.className = ''; this.text = ''; this.disabled = false; this.checked = false; }
    appendChild(c) { this.children.push(c); c.parent = this; return c; }
    setAttribute(k, v) { this.attrs[k] = String(v); }
    getAttribute(k) { return k in this.attrs ? this.attrs[k] : null; }
    addEventListener(type, fn) { (this.listeners[type] = this.listeners[type] || []).push(fn); }
    fire(type) { for (const fn of this.listeners[type] || []) fn({}); }
    set textContent(v) { this.text = String(v); this.children = []; }
    get textContent() { return this.text + this.children.map((c) => c.textContent).join(''); }
    set innerHTML(v) { this.html = v; }
    all(pred, out = []) { for (const c of this.children) { if (pred(c)) out.push(c); c.all(pred, out); } return out; }
    byClass(name) { return this.all((n) => (' ' + n.className + ' ').includes(' ' + name + ' ')); }
  }
  const doc = { createElement: (tag) => new El(tag) };
  const root = new El('div');
  root.ownerDocument = doc;
  return root;
}

const settle = async () => { for (let i = 0; i < 20; i++) await new Promise((r) => setImmediate(r)); };

function mounted(channels, answers = {}) {
  const el = dom();
  const saves = [], tests = [], saved = [];
  const adapter = {
    load: () => Promise.resolve(JSON.parse(JSON.stringify(channels))),
    save: (list) => { saves.push(list); return answers.save ? answers.save(list) : Promise.resolve(JSON.parse(JSON.stringify(list))); },
    test: (c) => { tests.push(c); return Promise.resolve(answers.test || { ok: true }); }
  };
  const view = card.mount(el, adapter, { t: en, lang: 'en', kinds: KINDS, marks, classes: { input: 'trs-in', button: 'trs-btn', switch: 'trs-switch' }, saved: (ok, text) => saved.push([ok, text]) });
  return { el, view, saves, tests, saved };
}

test('the tiles show the services, then mail and webhooks, each in the order of their names', () => {
  const s = card.sections(KINDS, [], en, 'en');
  assert.deepEqual(s.map((x) => x.title), [en.nc_services, en.nc_standards]);
  assert.deepEqual(s[0].tiles.map((x) => x.name), ['Apprise', 'Discord', 'Gotify', 'Matrix', 'ntfy', 'Slack', 'Unraid']);
  assert.deepEqual(s[1].tiles.map((x) => x.name), [en.nc_smtp, en.nc_webhook]);
  assert.ok(s[0].tiles.every((x) => x.hint), 'every service explains itself');
  assert.equal(card.sections(KINDS, [{ id: 'unraid', kind: 'unraid' }], en, 'en')[0].tiles.find((x) => x.kind === 'unraid').off, true, 'Unraid once');
});

test('every kind the card offers has its mark, and the services their own colours', () => {
  for (const k of KINDS) assert.match(marks.marks[k], /^<svg [^>]*viewBox=/, k);
  for (const k of KINDS.filter((x) => x !== 'smtp' && x !== 'webhook')) assert.match(marks.tiles[k].tile, /^#[0-9a-f]{6}$/i, k);
});

test('a new channel is on, sends everything and takes an id no other channel has', () => {
  const c = card.newChannel('discord', [{ id: 'discord' }, { id: 'discord-2' }]);
  assert.deepEqual(c, { id: 'discord-3', kind: 'discord', enabled: true, on: 'always', discord: { url: '' } });
  assert.deepEqual(card.newChannel('unraid', []), { id: 'unraid', kind: 'unraid', enabled: true, on: 'always' });
  assert.equal(card.newChannel('webhook', []).webhook.method, 'POST');
});

test('a kept secret shows as an empty field, and an empty field keeps it', () => {
  const f = { type: 'secret' };
  assert.equal(card.shown(f, card.MASK), '');
  assert.equal(card.taken(f, '', card.MASK), card.MASK);
  assert.equal(card.taken(f, ' new ', card.MASK), 'new');
  assert.equal(card.taken(f, '', ''), '');
  assert.deepEqual(card.taken({ type: 'headers' }, 'Authorization: Bearer x\nbad line\n X-Y : z', {}), { Authorization: 'Bearer x', 'X-Y': 'z' });
  assert.deepEqual(card.taken({ type: 'words' }, ' a://1  b://2 '), ['a://1', 'b://2']);
});

test('picking a tile adds its channel with its fields and saves the list at once', async () => {
  const p = mounted([]);
  await settle();
  assert.equal(p.el.byClass('glim-nc-tile').length, 0, 'the tiles wait for Add');
  p.el.byClass('glim-nc-add')[0].fire('click');
  const tiles = p.el.byClass('glim-nc-tile');
  assert.equal(tiles.length, KINDS.length);
  assert.match(tiles.find((b) => b.attrs['data-kind'] === 'discord').attrs.style, /--nc-tile:#5865f2/);
  tiles.find((b) => b.attrs['data-kind'] === 'discord').fire('click');
  await settle();
  assert.equal(p.saves.length, 1);
  assert.deepEqual(p.saves[0].map((c) => c.kind), ['discord']);
  const ch = p.el.byClass('glim-nc-ch');
  assert.equal(ch.length, 1);
  assert.ok(ch[0].byClass('glim-nc-in').some((i) => i.attrs.placeholder === 'https://discord.com/api/webhooks/...'));
  assert.equal(p.el.byClass('glim-nc-tile').length, 0, 'the tiles close again');
});

test('a channel switched off keeps its settings, hides its fields and saves', async () => {
  const p = mounted([{ id: 'matrix', kind: 'matrix', enabled: true, on: 'failure', matrix: { homeserver: 'https://m.example', room: '!r:m', token: card.MASK } }]);
  await settle();
  let ch = p.el.byClass('glim-nc-ch')[0];
  const token = ch.byClass('glim-nc-in').find((i) => i.attrs.type === 'password');
  assert.equal(token.value, '');
  assert.equal(token.attrs.placeholder, en.nc_saved);
  ch.byClass('glim-nc-switch')[0].checked = false;
  ch.byClass('glim-nc-switch')[0].fire('change');
  await settle();
  assert.deepEqual(p.saves[0][0].matrix, { homeserver: 'https://m.example', room: '!r:m', token: card.MASK });
  assert.equal(p.saves[0][0].enabled, false);
  ch = p.el.byClass('glim-nc-ch')[0];
  assert.equal(ch.byClass('glim-nc-in').length, 0);
});

test('a field the service refuses is said on its channel, and a test says how it went', async () => {
  const bad = (list) => Promise.reject({ code: 'invalid', problems: [{ index: 0, field: 'url', code: 'invalid' }] });
  const p = mounted([{ id: 'slack', kind: 'slack', enabled: true, on: 'always', slack: { url: '' } }], { save: bad, test: { ok: false, code: 'not_found' } });
  await settle();
  const url = p.el.byClass('glim-nc-in').find((i) => i.attrs.placeholder === 'https://hooks.slack.com/services/...');
  url.value = 'nope';
  url.fire('change');
  await settle();
  assert.equal(p.el.byClass('glim-nc-err')[0].textContent, en.nc_fail_invalid);
  assert.deepEqual(p.saved[p.saved.length - 1], [false, en.nc_not_saved + ': ' + en.nc_fail_invalid]);
  p.el.byClass('glim-nc-btn').find((b) => b.text === en.nc_test).fire('click');
  await settle();
  assert.equal(p.tests[0].slack.url, 'nope');
  assert.equal(p.el.byClass('glim-nc-res')[0].textContent, en.nc_test_failed + ': ' + en.nc_fail_not_found);
});

test('a channel is removed with its button', async () => {
  const p = mounted([{ id: 'unraid', kind: 'unraid', enabled: true, on: 'always' }, { id: 'ntfy', kind: 'ntfy', enabled: true, on: 'always', ntfy: { url: 'https://ntfy.sh/x' } }]);
  await settle();
  p.el.byClass('glim-nc-ch')[0].byClass('glim-nc-btn').find((b) => b.text === en.nc_remove).fire('click');
  await settle();
  assert.deepEqual(p.saves[0].map((c) => c.id), ['ntfy']);
});

test('every string the card asks for is in every language', () => {
  const src = fs.readFileSync(path.join(PLUGIN, 'scripts', 'notify-card.js'), 'utf8');
  const keys = [...new Set(src.match(/nc_[a-z_]+/g))].filter((k) => k !== 'nc_fail_' && k !== 'nc_');
  for (const l of fs.readdirSync(path.join(PLUGIN, 'lang'))) {
    const tx = JSON.parse(fs.readFileSync(path.join(PLUGIN, 'lang', l), 'utf8'));
    for (const k of keys) assert.ok(tx[k], l + ' ' + k);
  }
});
