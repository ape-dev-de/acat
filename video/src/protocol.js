'use strict';
// Mirror of pkg/protocol/video.go. Newline-delimited JSON on stdin/stdout.

const VERSION = 1;

const Commands = Object.freeze({
  init: 'init',
  refreshToken: 'refreshToken',
  call: 'call',
  join: 'join',
  accept: 'accept',
  reject: 'reject',
  hangup: 'hangup',
  mute: 'mute',
  camera: 'camera',
  screenshare: 'screenshare',
  devices: 'devices',
  setDevice: 'setDevice',
  show: 'show',
  hide: 'hide',
  quit: 'quit',
});

const Events = Object.freeze({
  ready: 'ready',
  initialized: 'initialized',
  incomingCall: 'incomingCall',
  callState: 'callState',
  devices: 'devices',
  tokenExpired: 'tokenExpired',
  error: 'error',
});

const commandTypes = new Set(Object.values(Commands));
const eventTypes = new Set(Object.values(Events));

// Commands handled by the main process itself; all others go to the renderer.
const mainCommands = new Set([Commands.show, Commands.hide, Commands.quit]);

const MAX_LINE = 1 << 20;

/** Parses one input line. Returns a command object or null if invalid. */
function parseCommand(line) {
  if (typeof line !== 'string' || line.length === 0 || line.length > MAX_LINE) return null;
  let cmd;
  try {
    cmd = JSON.parse(line);
  } catch {
    return null;
  }
  if (!cmd || typeof cmd !== 'object' || Array.isArray(cmd) || !commandTypes.has(cmd.type)) return null;
  return cmd;
}

/** Validates an event from the renderer before it is written to stdout. */
function isValidEvent(ev) {
  return !!ev && typeof ev === 'object' && !Array.isArray(ev) && eventTypes.has(ev.type);
}

/** Serializes an event as one output line. */
function formatEvent(ev) {
  return JSON.stringify(ev) + '\n';
}

/**
 * Splits a byte stream into lines. push(chunk) returns complete lines;
 * overly long partial lines are dropped.
 */
class LineSplitter {
  constructor() {
    this.buf = '';
  }
  push(chunk) {
    this.buf += chunk;
    const parts = this.buf.split('\n');
    this.buf = parts.pop();
    if (this.buf.length > MAX_LINE) this.buf = '';
    return parts.map((l) => l.replace(/\r$/, '')).filter((l) => l.length > 0);
  }
}

/** Converts a Teams user object ID into an ACS identifier. */
function teamsUser(oid) {
  return { microsoftTeamsUserId: String(oid) };
}

module.exports = {
  VERSION,
  Commands,
  Events,
  mainCommands,
  parseCommand,
  isValidEvent,
  formatEvent,
  LineSplitter,
  teamsUser,
};
