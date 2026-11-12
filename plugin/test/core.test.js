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

test('an app on several devices says how many', () => {
  assert.deepEqual(core.rowStatus(app({ instances: 2 }), en, 'en'), { tone: 'ok', text: 'running on 2 devices' });
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

const svg = 'data:image/svg+xml;base64,' + Buffer.from('<svg xmlns="http://www.w3.org/2000/svg"/>').toString('base64');

test('only an SVG data URI within the size limit passes as an icon', () => {
  assert.equal(core.safeIcon(svg), svg);
  assert.equal(core.safeIcon('data:image/svg+xml;base64,' + 'A'.repeat(21848)), 'data:image/svg+xml;base64,' + 'A'.repeat(21848));
  for (const bad of ['', undefined, 'https://a.lan/i.svg', 'data:text/html;base64,PHA+', 'data:image/svg+xml,<svg onload=alert(1)>',
    'data:image/svg+xml;base64,PHN2Zz4=" onerror="x', 'data:image/svg+xml;base64,' + 'A'.repeat(21852), 'javascript:alert(1)']) {
    assert.equal(core.safeIcon(bad), '', String(bad).slice(0, 40));
  }
});

test('an app colour gets letters that stay readable on it', () => {
  assert.deepEqual(core.markColors('#2e7d32'), { bg: '#2e7d32', fg: '#ffffff' });
  assert.deepEqual(core.markColors('#F5F5F5'), { bg: '#F5F5F5', fg: '#161616' });
  assert.equal(core.markColors('red'), null);
  assert.equal(core.markColors('#2e7d32;background:url(x)'), null);
  assert.equal(core.markColors(undefined), null);
});

const M = '********';

test('the notification form reads the three channels it edits', () => {
  const form = core.notifyForm([
    { id: 'unraid', kind: 'unraid', enabled: true, on: 'failure' },
    { id: 'apprise', kind: 'apprise', enabled: true, on: 'failure', apprise: { url: 'http://apprise.lan:8000/notify/k' } },
    { id: 'matrix', kind: 'matrix', enabled: true, on: 'failure', matrix: { homeserver: 'https://m.org', room: '!r:m.org', token: M } },
    { id: 'hook', kind: 'webhook', enabled: true, webhook: { url: 'https://h.org' } },
  ]);
  assert.deepEqual(form, { unraid: true, apprise: 'http://apprise.lan:8000/notify/k', hs: 'https://m.org', room: '!r:m.org', token: '', tokenSaved: true, on: 'failure' });
  assert.deepEqual(core.notifyForm([]), { unraid: false, apprise: '', hs: '', room: '', token: '', tokenSaved: false, on: 'always' });
});

test('saving the form keeps every other channel and a saved token', () => {
  const hook = { id: 'hook', kind: 'webhook', enabled: true, webhook: { url: 'https://h.org', headers: { Authorization: M } } };
  const before = [hook, { id: 'matrix', kind: 'matrix', enabled: true, on: 'always', matrix: { homeserver: 'https://m.org', room: '!r:m.org', token: M } }];
  const after = core.notifyChannels(before, { unraid: true, apprise: '', hs: 'https://m.org', room: '!s:m.org', token: '', tokenSaved: true, on: 'failure' });
  assert.deepEqual(after, [
    hook,
    { id: 'matrix', kind: 'matrix', enabled: true, on: 'failure', matrix: { homeserver: 'https://m.org', room: '!s:m.org', token: M } },
    { id: 'unraid', kind: 'unraid', enabled: true, on: 'failure' },
  ]);
});

test('a typed token replaces the saved one, a cleared channel goes', () => {
  const before = [{ id: 'matrix', kind: 'matrix', enabled: true, on: 'always', matrix: { homeserver: 'https://m.org', room: '!r:m.org', token: M } },
    { id: 'apprise', kind: 'apprise', enabled: true, on: 'always', apprise: { url: 'http://a.lan/notify/k', tags: 'ops' } }];
  const after = core.notifyChannels(before, { unraid: false, apprise: 'http://b.lan/notify/k', hs: 'https://m.org', room: '!r:m.org', token: 'syt_new', tokenSaved: true, on: 'always' });
  assert.equal(after.find((c) => c.id === 'matrix').matrix.token, 'syt_new');
  assert.deepEqual(after.find((c) => c.id === 'apprise').apprise, { url: 'http://b.lan/notify/k', tags: 'ops' });
  assert.equal(after.some((c) => c.id === 'unraid'), false);
  const cleared = core.notifyChannels(before, { unraid: false, apprise: '', hs: '', room: '', token: '', tokenSaved: true, on: 'always' });
  assert.deepEqual(cleared, []);
});

test('a half filled channel stays, switched off, so nothing typed is lost', () => {
  const after = core.notifyChannels([], { unraid: false, apprise: 'not a url', hs: 'https://m.org', room: '', token: '', tokenSaved: false, on: 'always' });
  assert.deepEqual(after.map((c) => [c.id, c.enabled]), [['apprise', false], ['matrix', false]]);
});

test('a switched off unraid channel stays in the list', () => {
  const after = core.notifyChannels([{ id: 'unraid', kind: 'unraid', enabled: true, on: 'always' }], { unraid: false, apprise: '', hs: '', room: '', token: '', tokenSaved: false, on: 'always' });
  assert.deepEqual(after, [{ id: 'unraid', kind: 'unraid', enabled: false, on: 'always' }]);
});

test('in a fortress the subtitle counts the devices too', () => {
  const view = { apps: [app(), app({ key: 'b', state: 'failed' })] };
  assert.equal(core.subtitle(view, en, 'en', 3), '2 apps on 3 devices, 1 needs you');
  assert.equal(core.subtitle(view, de, 'de', 1), '2 Apps auf 1 Gerät, 1 braucht dich');
});

test('the apps left out leave the log', () => {
  const apps = [app({ key: 'a' }), app({ key: 'b' }), app({ key: 'c' })];
  assert.deepEqual(core.shown(apps, ['b', 'gone']).map((a) => a.key), ['a', 'c']);
  assert.deepEqual(core.shown(apps, []).map((a) => a.key), ['a', 'b', 'c']);
});

test('the app list button says how many apps are chosen', () => {
  assert.equal(core.selLabel(en, 'en', 6, 6), 'All apps');
  assert.equal(core.selLabel(en, 'en', 3, 6), '3 of 6 apps');
  assert.equal(core.selLabel(en, 'en', 0, 6), 'No app');
  assert.equal(core.selLabel(de, 'de', 1, 2), '1 von 2 Apps');
});

test('the map groups the members by device, this server first', () => {
  const map = { joined: true, device: 'tower', members: [
    { id: 'm1', app: 'alpha', name: 'Alpha', version: '1', device: 'cellar', online: true },
    { id: 'm2', app: 'squire', name: 'Squire', version: '2', device: 'tower', online: true },
    { id: 'm3', app: 'alpha', name: 'Alpha', version: '1', device: 'attic', online: false, lastSeen: '2026-11-10T08:00:00Z' },
    { id: 'm4', app: 'bravo', name: 'Bravo', version: '3', device: 'attic', online: false, lastSeen: '2026-11-10T09:00:00Z' },
  ] };
  const devs = core.mapDevices(map, [app({ key: 'alpha', name: 'Alpha' }), app({ key: 'bravo', name: 'Bravo' })]);
  assert.deepEqual(devs.map((d) => [d.name, d.here, d.online]), [['tower', true, true], ['attic', false, false], ['cellar', false, true]]);
  assert.equal(devs[1].seen, '2026-11-10T09:00:00Z');
  assert.deepEqual(devs[0].members.map((m) => [m.name, m.quiet]), [['Squire', true]]);
  assert.equal(devs[2].members[0].row.key, 'alpha');
});

test('an app that joins only while it runs is calm on the map, not unreachable', () => {
  const map = { joined: true, device: 'tower', members: [
    { id: 'm1', app: 'firesquire', name: 'FireSquire', device: 'tower', online: false, onDemand: true },
    { id: 'm2', app: 'firesquire', name: 'FireSquire', device: 'cellar', online: false, onDemand: true, lastSeen: '2026-11-10T08:00:00Z' },
    { id: 'm3', app: 'alpha', name: 'Alpha', device: 'attic', online: false, onDemand: false },
    { id: 'm4', app: 'firesquire', name: 'FireSquire', device: 'attic', online: false, onDemand: true },
  ] };
  const devs = core.mapDevices(map, []);
  assert.deepEqual(devs[0].members.map((m) => [m.name, m.onDemand, m.quiet]), [['FireSquire', true, false]]);
  const by = Object.fromEntries(devs.map((d) => [d.name, d]));
  assert.equal(by.cellar.calm, true, 'a device with only such apps is calm');
  assert.equal(by.attic.calm, false, 'another app away still shows the device away');
  assert.equal(by.attic.online, false);
});

test('a time in the past reads as how long ago', () => {
  const now = new Date('2026-11-10T12:00:00Z');
  assert.equal(core.ago('2026-11-10T09:00:00Z', 'en', now), '3 hours ago');
  assert.equal(core.ago('2026-11-10T11:58:00Z', 'de', now), 'vor 2 Minuten');
});

test('a member is named by its app and its device', () => {
  assert.equal(core.memberName(en, { app: 'bombvault', name: 'BombVault', device: 'cellar' }), 'BombVault on cellar');
  assert.equal(core.memberName(de, { app: 'bombvault', name: 'BombVault', device: 'cellar' }), 'BombVault auf cellar');
  assert.equal(core.memberName(en, { app: 'lantern', device: 'attic' }), 'lantern on attic');
});

test('a new phrase is made in an app that keeps the phrase, never in a plugin', () => {
  const shiplog = { id: 'p', app: 'shiplog', kind: 'instance', name: 'ShipLog', device: 'tower', deployment: 'plugin', online: true };
  const phone = { id: 'f', app: 'bombvault', kind: 'viewer', name: 'BombVault', device: 'phone', online: true };
  const away = { id: 'k', app: 'knightloader', kind: 'instance', name: 'KnightLoader', device: 'cellar', deployment: 'container', online: false };
  const bombvault = { id: 'b', app: 'bombvault', kind: 'instance', name: 'BombVault', device: 'cellar', deployment: 'container', online: true };
  const before = Object.assign({}, bombvault, { id: 'b0', online: false });
  assert.equal(core.changeWhere(en, { members: [shiplog, phone, bombvault] }, 'en'), 'You change the phrase in BombVault on cellar.');
  assert.equal(core.changeWhere(en, { members: [shiplog, away, before, bombvault] }, 'en'), 'You change the phrase in BombVault on cellar or KnightLoader on cellar.',
    'the apps in reach come first, and an app known from an earlier install is named once');
  assert.equal(core.changeWhere(de, { members: [bombvault, away] }, 'de'), 'Die Phrase änderst du in BombVault auf cellar oder KnightLoader auf cellar.');
  assert.equal(core.changeWhere(en, { members: [shiplog, phone] }, 'en'), en.s_change_where);
  assert.equal(core.changeWhere(en, null, 'en'), en.s_change_where);
});
