'use strict';
const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const proto = require('../src/protocol');

test('version matches Go (pkg/protocol)', () => {
  const goSrc = fs.readFileSync(path.join(__dirname, '../../pkg/protocol/protocol.go'), 'utf8');
  const m = goSrc.match(/const VideoVersion = (\d+)/);
  assert.ok(m, 'VideoVersion not found in Go source');
  assert.equal(proto.VERSION, Number(m[1]));
});

test('command and event names match Go constants', () => {
  const goSrc = fs.readFileSync(path.join(__dirname, '../../pkg/protocol/video.go'), 'utf8');
  const goValues = new Set([...goSrc.matchAll(/^\s+Video\w+\s+= "(\w+)"/gm)].map((m) => m[1]));
  for (const v of [...Object.values(proto.Commands), ...Object.values(proto.Events)]) {
    assert.ok(goValues.has(v), `"${v}" missing in pkg/protocol/video.go`);
  }
});

test('parseCommand accepts known commands only', () => {
  assert.deepEqual(proto.parseCommand('{"type":"init","token":"t"}'), { type: 'init', token: 't' });
  for (const bad of ['', 'not json', '[]', 'null', '{"type":"rm -rf"}', '{"x":1}', '"init"']) {
    assert.equal(proto.parseCommand(bad), null, bad);
  }
  assert.equal(proto.parseCommand('{"type":"init","p":"' + 'x'.repeat(1 << 20) + '"}'), null);
});

test('LineSplitter handles partial and CRLF lines', () => {
  const s = new proto.LineSplitter();
  assert.deepEqual(s.push('{"type":"sh'), []);
  assert.deepEqual(s.push('ow"}\r\n{"type":"hide"}\n\n{"ty'), ['{"type":"show"}', '{"type":"hide"}']);
  assert.deepEqual(s.push('pe":"quit"}\n'), ['{"type":"quit"}']);
});

test('events are validated and line-formatted', () => {
  assert.ok(proto.isValidEvent({ type: 'ready', proto: 1 }));
  assert.ok(!proto.isValidEvent({ type: 'evil' }));
  assert.ok(!proto.isValidEvent(null));
  assert.equal(proto.formatEvent({ type: 'ready' }), '{"type":"ready"}\n');
});

test('teamsUser builds an ACS identifier', () => {
  assert.deepEqual(proto.teamsUser('oid-1'), { microsoftTeamsUserId: 'oid-1' });
});
