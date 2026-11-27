'use strict';
// Randomized robustness tests for the stdin protocol parser. Nightly runs
// more iterations via ACAT_FUZZ_ITERATIONS.
const test = require('node:test');
const assert = require('node:assert/strict');
const proto = require('../src/protocol');

const N = Number(process.env.ACAT_FUZZ_ITERATIONS || 5000);
const alphabet = '{}[]":,\\\n\r tnulfaeisx0123456789' + Object.values(proto.Commands).join('');

function rnd(maxLen) {
  const len = Math.floor(Math.random() * maxLen);
  let s = '';
  for (let i = 0; i < len; i++) s += alphabet[Math.floor(Math.random() * alphabet.length)];
  return s;
}

test('parseCommand never throws and only returns known commands', () => {
  for (let i = 0; i < N; i++) {
    const c = proto.parseCommand(rnd(80));
    if (c !== null) assert.ok(Object.values(proto.Commands).includes(c.type));
  }
});

test('LineSplitter never throws and returns no newlines', () => {
  const s = new proto.LineSplitter();
  for (let i = 0; i < N; i++) {
    for (const line of s.push(rnd(120))) {
      assert.ok(!line.includes('\n'));
      assert.ok(line.length > 0);
    }
  }
});
