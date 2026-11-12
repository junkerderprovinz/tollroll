'use strict';
const test = require('node:test');
const assert = require('node:assert/strict');
const path = require('node:path');
const fs = require('node:fs');

const REPO = path.join(__dirname, '..', '..');
const { vanilla } = JSON.parse(fs.readFileSync(path.join(REPO, 'glimstone.json'), 'utf8'));

test("the fortress card is GlimStone's own, unchanged", async () => {
  for (const [copy, source] of Object.entries(vanilla.files)) {
    const url = `https://raw.githubusercontent.com/junkerderprovinz/glimstone/${vanilla.ref}/${source}`;
    const res = await fetch(url);
    assert.ok(res.ok, `${url} answered ${res.status}`);
    const want = Buffer.from(await res.arrayBuffer());
    const have = fs.readFileSync(path.join(REPO, copy));
    assert.ok(have.equals(want), `${copy} is not ${source} of GlimStone ${vanilla.ref}`);
  }
});
