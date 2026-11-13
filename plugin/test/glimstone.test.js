'use strict';
const test = require('node:test');
const assert = require('node:assert/strict');
const path = require('node:path');
const fs = require('node:fs');

const REPO = path.join(__dirname, '..', '..');
const card = JSON.parse(fs.readFileSync(path.join(REPO, 'glimstone.json'), 'utf8'))
  .targets.find((t) => t.mode === 'copy' && t.name === 'fortress card');

// A release branch stands in for its tag until GlimStone tags the release.
async function glimstoneFile(version, file) {
  for (const ref of [`v${version}`, `rel/${version}`]) {
    const url = `https://raw.githubusercontent.com/junkerderprovinz/glimstone/${ref}/${file}`;
    const res = await fetch(url);
    if (res.status === 404) continue;
    assert.ok(res.ok, `${url} answered ${res.status}`);
    return { ref, body: Buffer.from(await res.arrayBuffer()) };
  }
  assert.fail(`GlimStone has ${file} neither at v${version} nor on rel/${version}`);
}

test("the fortress card is GlimStone's own, unchanged", async () => {
  assert.ok(card, 'glimstone.json has no fortress card copy target');
  assert.equal(card.files.length, 2);
  for (const { from, to } of card.files) {
    const want = await glimstoneFile(card.version, from);
    const have = fs.readFileSync(path.join(REPO, to));
    assert.ok(have.equals(want.body), `${to} is not ${from} of GlimStone ${want.ref}`);
  }
});
