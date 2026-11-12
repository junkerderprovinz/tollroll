'use strict';
const test = require('node:test');
const assert = require('node:assert/strict');
const path = require('node:path');
const fs = require('node:fs');
const vm = require('node:vm');

const PLUGIN = path.join(__dirname, '..', 'src', 'tollroll', 'usr', 'local', 'emhttp', 'plugins', 'tollroll');
const core = require(path.join(PLUGIN, 'scripts', 'tollroll-core.js'));
const en = JSON.parse(fs.readFileSync(path.join(PLUGIN, 'lang', 'en.json'), 'utf8'));

// Just enough of a DOM for settings.js to draw the fortress card and its
// windows, and to be clicked through.
function page(answers) {
  const doc = { activeElement: null, listeners: {} };
  class Node {
    constructor(tag) {
      this.tagName = tag.toUpperCase();
      this.children = [];
      this.attrs = {};
      this.listeners = {};
      this.dataset = {};
      this.style = {};
      this.className = '';
      this.ownText = '';
      this.value = '';
      this.classList = { add: (c) => { this.className += ' ' + c; } };
    }
    get textContent() { return this.ownText + this.children.map((c) => c.textContent).join(''); }
    set textContent(v) { this.ownText = String(v); this.children = []; }
    set innerHTML(v) { this.html = v; }
    appendChild(c) { this.children.push(c); return c; }
    setAttribute(k, v) { this.attrs[k] = String(v); }
    getAttribute(k) { return this.attrs[k]; }
    addEventListener(type, fn) { (this.listeners[type] = this.listeners[type] || []).push(fn); }
    removeEventListener() {}
    focus() { doc.activeElement = this; }
    click() { if (!this.disabled) for (const fn of this.listeners.click || []) fn({ target: this }); }
  }
  const byId = {};
  const document = {
    getElementById: (id) => byId[id] || (byId[id] = new Node('div')),
    createElement: (tag) => new Node(tag),
    createTextNode: (text) => { const n = new Node('#text'); n.ownText = String(text); return n; },
    querySelectorAll: () => [],
    addEventListener(type, fn) { (doc.listeners[type] = doc.listeners[type] || []).push(fn); },
    removeEventListener() {},
    get activeElement() { return doc.activeElement; }
  };
  const posts = [];
  const reply = (body) => Promise.resolve({ json: () => Promise.resolve(body) });
  function fetch(url, opt) {
    if (opt && opt.method === 'POST') {
      const p = Object.fromEntries(opt.body);
      posts.push({ path: p.path, data: JSON.parse(p.data) });
      return reply(answers[p.path] || {});
    }
    return reply(answers[/path=(\w+)/.exec(url)[1]] || {});
  }
  const window = { TollRollCore: core, TRS_T: en, TRL_LANG: 'en', TollRollGlyphs: {}, csrf_token: 't', fetch };
  const ctx = vm.createContext({ window, document, fetch, URLSearchParams, location: {}, setTimeout, clearTimeout });
  vm.runInContext(fs.readFileSync(path.join(PLUGIN, 'scripts', 'settings.js'), 'utf8'), ctx);
  return { fort: byId['trs-fort'], dlg: byId['trs-dlg'], posts, focused: () => doc.activeElement };
}

const settle = async () => { for (let i = 0; i < 20; i++) await new Promise((r) => setImmediate(r)); };

function all(n, pred, out = []) {
  if (pred(n)) out.push(n);
  n.children.forEach((c) => all(c, pred, out));
  return out;
}
const buttons = (n, text) => all(n, (x) => (x.tagName === 'BUTTON' || x.tagName === 'A') && x.textContent === text);

const members = [
  { id: 'k1', app: 'knightloader', name: 'KnightLoader', device: 'cellar', deployment: 'container', online: true },
  { id: 'b1', app: 'bombvault', name: 'BombVault', device: 'attic', deployment: 'container', online: true },
  { id: 's1', app: 'shiplog', name: 'ShipLog', device: 'tower', deployment: 'plugin', online: true }
];
const apps = [
  { key: 'knightloader', name: 'KnightLoader', state: 'ok', instances: 1, ui: 'http://cellar:8080' },
  { key: 'bombvault', name: 'BombVault', state: 'ok', instances: 1, ui: 'http://attic:3000' }
];
const joined = (over) => Object.assign({ joined: true, device: 'tower', members }, over);

test('every other member of the fortress has its own Remove', async () => {
  const p = page({ fortress: joined(), board: { apps } });
  await settle();
  assert.equal(buttons(p.fort, en.s_remove).length, 3);
  assert.ok(p.fort.textContent.includes('KnightLoader on cellar'), p.fort.textContent);
  assert.ok(p.fort.textContent.includes('ShipLog on tower'));
});

test('Remove asks first, naming the app and its device, and focuses the button that goes on', async () => {
  const p = page({ fortress: joined(), board: { apps } });
  await settle();
  buttons(p.fort, en.s_remove)[0].click();
  assert.ok(p.dlg.textContent.includes('Remove KnightLoader on cellar?'), p.dlg.textContent);
  assert.ok(p.dlg.textContent.includes('KnightLoader on cellar loses its access right away.'));
  assert.equal(p.focused().textContent, en.s_remove);
  buttons(p.dlg, en.s_cancel)[0].click();
  assert.equal(p.dlg.children.length, 0);
  assert.equal(p.posts.length, 0, 'cancel removed something');
});

test('after a removal the window offers to change the phrase in an app that keeps it', async () => {
  const removed = [{ app: 'knightloader', name: 'KnightLoader', device: 'cellar' }];
  const p = page({ fortress: joined(), board: { apps }, remove: joined({ members: members.slice(1), removed }) });
  await settle();
  buttons(p.fort, en.s_remove)[0].click();
  buttons(p.dlg, en.s_remove)[0].click();
  await settle();
  assert.deepEqual(p.posts, [{ path: 'remove', data: { id: 'k1' } }]);
  assert.ok(p.dlg.textContent.includes(en.s_removed_q), p.dlg.textContent);
  assert.ok(p.dlg.textContent.includes('KnightLoader on cellar is out.'));
  const change = buttons(p.dlg, en.s_change);
  assert.equal(change.length, 1);
  assert.equal(change[0].href, 'http://attic:3000', 'the phrase is changed in BombVault, which is still in');
  assert.equal(change[0].target, '_blank');
  assert.equal(p.focused(), change[0]);

  buttons(p.dlg, en.s_later)[0].click();
  assert.equal(p.dlg.children.length, 0);
  assert.ok(p.fort.textContent.includes('Removed: KnightLoader on cellar. Change the phrase to close the way back in.'), p.fort.textContent);
  assert.equal(buttons(p.fort, en.s_remove).length, 2);
});

test('without an app that keeps the phrase the offer says where it is changed', async () => {
  const removed = [{ app: 'knightloader', name: 'KnightLoader', device: 'cellar' }];
  const p = page({ fortress: joined(), board: { apps: [] }, remove: joined({ members: members.slice(1), removed }) });
  await settle();
  buttons(p.fort, en.s_remove)[0].click();
  buttons(p.dlg, en.s_remove)[0].click();
  await settle();
  assert.ok(p.dlg.textContent.includes(en.s_change_where), p.dlg.textContent);
  assert.equal(buttons(p.dlg, en.s_change).length, 0);
  assert.equal(p.focused().textContent, en.s_later);
});

test('the reminder stays until the phrase changes', async () => {
  const removed = [{ app: 'knightloader', name: 'KnightLoader', device: 'cellar' }, { app: 'lantern', name: 'lantern', device: 'attic' }];
  let p = page({ fortress: joined({ removed }), board: { apps } });
  await settle();
  assert.ok(p.fort.textContent.includes('Removed: KnightLoader on cellar, lantern on attic.'), p.fort.textContent);
  assert.equal(buttons(p.fort, en.s_change)[0].href, 'http://cellar:8080');
  p = page({ fortress: joined(), board: { apps } });
  await settle();
  assert.ok(!p.fort.textContent.includes('Removed:'), p.fort.textContent);
});

test('a removal the service refuses is said in the window', async () => {
  const p = page({ fortress: joined(), board: { apps }, remove: { error: 'no_member' } });
  await settle();
  buttons(p.fort, en.s_remove)[0].click();
  buttons(p.dlg, en.s_remove)[0].click();
  await settle();
  assert.ok(p.dlg.textContent.includes(en.e_no_member), p.dlg.textContent);
  assert.ok(!p.dlg.textContent.includes(en.s_removed_q));
});
