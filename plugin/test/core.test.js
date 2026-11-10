'use strict';
const test = require('node:test');
const assert = require('node:assert/strict');
const path = require('node:path');
const fs = require('node:fs');

const PLUGIN = path.join(__dirname, '..', 'src', 'tollroll', 'usr', 'local', 'emhttp', 'plugins', 'tollroll');
const core = require(path.join(PLUGIN, 'scripts', 'tollroll-core.js'));
const en = JSON.parse(fs.readFileSync(path.join(PLUGIN, 'lang', 'en.json'), 'utf8'));
const de = Object.assign({}, en, JSON.parse(fs.readFileSync(path.join(PLUGIN, 'lang', 'de.json'), 'utf8')));

const app = (over) => Object.assign({ key: 'alpha', name: 'Alpha', version: '1.0.0', state: 'ok', instances: 1 }, over);

test('placeholders are filled by name', () => {
  assert.equal(core.fmt('Open {name} {name}', { name: 'Alpha' }), 'Open Alpha Alpha');
  assert.equal(core.fmt('{n} apps', { n: 3 }), '3 apps');
});

test('the plural form follows the language', () => {
  assert.equal(core.plural(en, 't_apps', 1, 'en'), '1 app');
  assert.equal(core.plural(en, 't_apps', 4, 'en'), '4 apps');
  assert.equal(core.plural(de, 't_attn', 2, 'de'), '2 brauchen dich');
});

test('a missing plural form falls back to other', () => {
  assert.equal(core.plural({ x_other: '{n} things' }, 'x', 1, 'en'), '1 things');
});

test('a running app reads as running', () => {
  assert.deepEqual(core.rowStatus(app(), en, 'en'), { tone: 'ok', text: 'running' });
});

test('an app on several servers says how many', () => {
  assert.deepEqual(core.rowStatus(app({ instances: 2 }), en, 'en'), { tone: 'ok', text: 'running on 2 servers' });
});

test('a need shows its own text, coloured by severity', () => {
  assert.deepEqual(core.rowStatus(app({ state: 'warning', need: { severity: 'warn', text: 'Captcha waits' } }), en, 'en'), { tone: 'warn', text: 'Captcha waits' });
  assert.deepEqual(core.rowStatus(app({ state: 'failed', need: { severity: 'error', text: 'Backup failed' } }), en, 'en'), { tone: 'fail', text: 'Backup failed' });
  assert.deepEqual(core.rowStatus(app({ need: { severity: 'info', text: '2 updates wait' } }), en, 'en'), { tone: 'warn', text: '2 updates wait' });
});

test('running work shows its title and progress', () => {
  assert.deepEqual(core.rowStatus(app({ work: { title: 'Container: web', progress: 0.42 } }), en, 'en'), { tone: 'ok', text: 'Container: web, 42%' });
  assert.deepEqual(core.rowStatus(app({ work: { title: 'Sync' } }), en, 'en'), { tone: 'ok', text: 'Sync' });
});

test('every state has its own word and tone', () => {
  const want = {
    failed: ['fail', 'failed'], overdue: ['warn', 'overdue'], warning: ['warn', 'needs a look'],
    starting: ['idle', 'starting'], off: ['off', 'off'], unreachable: ['off', 'not reachable'],
  };
  for (const [state, [tone, text]] of Object.entries(want)) {
    assert.deepEqual(core.rowStatus(app({ state }), en, 'en'), { tone, text }, state);
  }
});

test('an unknown state reads as not reachable rather than running', () => {
  assert.equal(core.rowStatus(app({ state: 'exploded' }), en, 'en').tone, 'off');
});

test('the log filter keeps the lines of one app', () => {
  const log = [{ app: 'a', text: '1' }, { app: 'b', text: '2' }, { app: 'a', text: '3' }];
  assert.deepEqual(core.filterLog(log, 'all').map((l) => l.text), ['1', '2', '3']);
  assert.deepEqual(core.filterLog(log, 'a').map((l) => l.text), ['1', '3']);
});

test('chips list every app with lines, in row order, with counts', () => {
  const view = { apps: [app({ key: 'a', name: 'A' }), app({ key: 'b', name: 'B' }), app({ key: 'c', name: 'C' })], log: [{ app: 'c' }, { app: 'a' }, { app: 'c' }] };
  assert.deepEqual(core.chips(view), [{ key: 'all', name: null, count: 3 }, { key: 'a', name: 'A', count: 1 }, { key: 'c', name: 'C', count: 2 }]);
});

test('a filter for an app that is gone falls back to all', () => {
  const view = { apps: [app({ key: 'a' })], log: [] };
  assert.equal(core.validFilter(view, 'a'), 'a');
  assert.equal(core.validFilter(view, 'gone'), 'all');
  assert.equal(core.validFilter(view, null), 'all');
});

test('only web links survive', () => {
  assert.equal(core.safeLink('https://tower.lan:3443/'), 'https://tower.lan:3443/');
  assert.equal(core.safeLink('http://10.0.0.2'), 'http://10.0.0.2');
  assert.equal(core.safeLink('javascript:alert(1)'), '');
  assert.equal(core.safeLink(' JaVaScRiPt:alert(1)'), '');
  assert.equal(core.safeLink('//evil.example'), '');
  assert.equal(core.safeLink(undefined), '');
});

test('an app keeps its colour', () => {
  assert.equal(core.hue('alpha'), core.hue('alpha'));
  assert.notEqual(core.hue('alpha'), core.hue('bravo'));
  assert.ok(core.hue('x') >= 0 && core.hue('x') < 360);
});

test('the mark takes the first letter of the name', () => {
  assert.equal(core.mark('Bravo'), 'B');
  assert.equal(core.mark('  élan'), 'É');
  assert.equal(core.mark(''), '?');
});

test('a line from today shows the time, an older one the date too', () => {
  const now = new Date('2026-11-10T12:00:00');
  assert.equal(core.timeLabel(new Date('2026-11-10T09:42:00').toISOString(), 'de', now), '09:42');
  assert.match(core.timeLabel(new Date('2026-11-08T09:42:00').toISOString(), 'de', now), /8\.11\..*09:42/);
});

test('the subtitle counts apps and those that need you', () => {
  const view = { apps: [app(), app({ key: 'b', need: { severity: 'warn', text: 'x' } }), app({ key: 'c', state: 'unreachable' })] };
  assert.equal(core.subtitle(view, en, 'en'), '3 apps, 2 need you');
  assert.equal(core.subtitle({ apps: [app()] }, en, 'en'), '1 app');
});

test('the Unraid locale becomes a language tag', () => {
  assert.equal(core.lang('de_DE'), 'de');
  assert.equal(core.lang('zh_CN'), 'zh');
  assert.equal(core.lang(''), 'en');
  assert.equal(core.lang('<x>'), 'en');
});
