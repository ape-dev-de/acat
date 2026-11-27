'use strict';
// acat-video main process: one hidden, hardened window running the ACS
// Calling SDK. Controlled by the acat client over stdin/stdout.

const { app, BrowserWindow, Menu, desktopCapturer, ipcMain, session } = require('electron');
const path = require('node:path');
const proto = require('./protocol');

// --- Chromium diet (must happen before 'ready') ------------------------
app.commandLine.appendSwitch(
  'disable-features',
  [
    'Autofill',
    'AutofillServerCommunication',
    'Translate',
    'SpellCheck',
    'MediaRouter',
    'DialMediaRouteProvider',
    'OptimizationHints',
    'HardwareMediaKeyHandling',
    'BackForwardCache',
    'InterestFeedContentSuggestions',
    'CalculateNativeWinOcclusion',
  ].join(','),
);
for (const sw of [
  'disable-extensions',
  'disable-background-networking',
  'disable-sync',
  'disable-component-update',
  'disable-default-apps',
  'disable-domain-reliability',
  'disable-breakpad',
  'no-pings',
  'no-default-browser-check',
]) {
  app.commandLine.appendSwitch(sw);
}
if (process.env.ACAT_VIDEO_NO_GPU === '1') app.disableHardwareAcceleration();

const PARTITION = 'acat-video'; // no "persist:" → nothing written to disk
const devTools = !app.isPackaged && process.env.ACAT_VIDEO_DEVTOOLS === '1';
const allowedPermissions = new Set(['media', 'display-capture', 'speaker-selection', 'fullscreen']);

let win = null;
let quitting = false;

function emit(ev) {
  if (!proto.isValidEvent(ev)) return;
  try {
    process.stdout.write(proto.formatEvent(ev));
  } catch {
    // stdout closed: the client is gone
    quit();
  }
}

function quit() {
  quitting = true;
  app.quit();
}

// Every web contents: no new windows, no navigation, no webviews.
app.on('web-contents-created', (_e, wc) => {
  wc.setWindowOpenHandler(() => ({ action: 'deny' }));
  wc.on('will-navigate', (e) => e.preventDefault());
  wc.on('will-redirect', (e) => e.preventDefault());
  wc.on('will-attach-webview', (e) => e.preventDefault());
});

function hardenSession(ses) {
  ses.setPermissionRequestHandler((_wc, permission, callback) => callback(allowedPermissions.has(permission)));
  ses.setPermissionCheckHandler((_wc, permission) => allowedPermissions.has(permission));
  // Screen sharing: share the primary screen (a picker can come later).
  ses.setDisplayMediaRequestHandler(async (_req, callback) => {
    try {
      const sources = await desktopCapturer.getSources({ types: ['screen'] });
      callback(sources.length ? { video: sources[0] } : {});
    } catch {
      callback({});
    }
  });
}

function createWindow() {
  win = new BrowserWindow({
    show: false,
    width: 1000,
    height: 640,
    title: 'acat call',
    autoHideMenuBar: true,
    webPreferences: {
      preload: path.join(__dirname, 'preload.js'),
      contextIsolation: true,
      nodeIntegration: false,
      sandbox: true,
      webSecurity: true,
      spellcheck: false,
      devTools,
      partition: PARTITION,
      backgroundThrottling: false, // keep receiving calls while hidden
    },
  });
  win.on('close', (e) => {
    if (!quitting) {
      e.preventDefault();
      win.hide();
    }
  });
  win.loadFile(path.join(__dirname, '..', 'dist', 'call.html'));
}

function handleCommand(cmd) {
  switch (cmd.type) {
    case proto.Commands.show:
      if (win) {
        win.show();
        win.focus();
      }
      return;
    case proto.Commands.hide:
      if (win) win.hide();
      return;
    case proto.Commands.quit:
      quit();
      return;
    default:
      if (win) win.webContents.send('cmd', cmd);
  }
}

function readStdin() {
  const splitter = new proto.LineSplitter();
  process.stdin.setEncoding('utf8');
  process.stdin.on('data', (chunk) => {
    for (const line of splitter.push(chunk)) {
      const cmd = proto.parseCommand(line);
      if (cmd) handleCommand(cmd);
      else emit({ type: proto.Events.error, message: 'invalid command' });
    }
  });
  process.stdin.on('end', quit); // client exited
}

app.whenReady().then(() => {
  Menu.setApplicationMenu(null);
  hardenSession(session.fromPartition(PARTITION));

  ipcMain.on('evt', (e, ev) => {
    if (!win || e.sender !== win.webContents) return;
    emit(ev);
  });
  ipcMain.once('renderer-ready', (e) => {
    if (!win || e.sender !== win.webContents) return;
    emit({ type: proto.Events.ready, proto: proto.VERSION, version: app.getVersion() });
  });

  createWindow();
  readStdin();
});

app.on('window-all-closed', quit);
