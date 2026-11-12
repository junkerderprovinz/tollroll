'use strict';
const test = require('node:test');
const assert = require('node:assert/strict');
const path = require('node:path');
const fs = require('node:fs');
const vm = require('node:vm');

const SCRIPTS = path.join(__dirname, '..', 'src', 'tollroll', 'usr', 'local', 'emhttp', 'plugins', 'tollroll', 'scripts');
const core = require(path.join(SCRIPTS, 'tollroll-core.js'));

// Just enough of a DOM for tile.js to build its frame and ask for data.
function element(tag, id) {
  return {
    tagName: tag.toUpperCase(), id, children: [], dataset: {}, parent: null,
    appendChild(c) { c.parent = this; this.children.push(c); return c; },
    setAttribute() {},
    addEventListener() {},
    closest(sel) {
      for (let n = this; n; n = n.parent) if (n.tagName === sel.toUpperCase()) return n;
      return null;
    }
  };
}

function dashboard() {
  const byId = {};
  const listeners = {};
  const fetched = [];
  const document = {
    readyState: 'loading',
    hidden: false,
    getElementById: (id) => byId[id] || null,
    createElement: (tag) => element(tag),
    querySelectorAll: () => [],
    addEventListener(type, fn) { (listeners[type] = listeners[type] || []).push(fn); }
  };
  const window = {
    TollRollCore: core,
    TRL_T: {},
    fetch(url) { fetched.push(url); return new Promise(() => {}); }
  };
  const ctx = vm.createContext({ window, document, fetch: window.fetch, localStorage: { getItem: () => null } });
  return {
    fetched,
    run(file) { vm.runInContext(fs.readFileSync(path.join(SCRIPTS, file), 'utf8'), ctx); },
    printTile() {
      const tbody = element('tbody');
      for (const id of ['trl', 'trl-sub', 'trl-open', 'trl-hs']) byId[id] = tbody.appendChild(element('span', id));
      return byId.trl;
    },
    finishParsing() {
      document.readyState = 'interactive';
      for (const fn of listeners.DOMContentLoaded || []) fn();
    }
  };
}

test('the tile fills when its script runs before the dashboard prints the tile', () => {
  const page = dashboard();
  page.run('tile.js');
  const root = page.printTile();
  page.finishParsing();
  assert.ok(root.children.length > 0, 'the tile stays empty');
  assert.ok(page.fetched.some((u) => u.endsWith('?path=tile')), 'the tile never asks for its data');
});

test('the tile fills at once when the page has already been parsed', () => {
  const page = dashboard();
  const root = page.printTile();
  page.finishParsing();
  page.run('tile.js');
  assert.ok(root.children.length > 0, 'the tile stays empty');
});
