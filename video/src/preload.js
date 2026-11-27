'use strict';
// Minimal bridge: the renderer can receive commands and emit events, nothing else.
const { contextBridge, ipcRenderer } = require('electron');

contextBridge.exposeInMainWorld('acat', {
  onCommand(fn) {
    ipcRenderer.on('cmd', (_e, cmd) => fn(cmd));
  },
  emit(ev) {
    ipcRenderer.send('evt', ev);
  },
  ready() {
    ipcRenderer.send('renderer-ready');
  },
});
