// acat-video renderer: ACS Calling SDK as a Teams user.
// Receives commands from the acat client (via preload bridge) and emits
// events. Never logs tokens.
import { CallClient, LocalVideoStream, VideoStreamRenderer } from '@azure/communication-calling';
import { AzureCommunicationTokenCredential } from '@azure/communication-common';
import proto from '../protocol.js';

const { Commands, Events, teamsUser } = proto;
const bridge = window.acat;

let callClient = null;
let agent = null;
let deviceManager = null;
let pendingRefresh = null; // resolve fn while waiting for refreshToken
let selectedCamera = null;
let current = null; // active call
const incoming = new Map(); // id → TeamsIncomingCall
const renderers = new Map(); // stream → { renderer, el }

const $ = (id) => document.getElementById(id);
const emit = (ev) => bridge.emit(ev);
const setStatus = (text) => {
  $('status').textContent = text || '';
};

// --- token handling ----------------------------------------------------
function tokenRefresher() {
  emit({ type: Events.tokenExpired });
  return new Promise((resolve) => {
    pendingRefresh = resolve;
  });
}

async function init(cmd) {
  if (agent) return emit({ type: Events.initialized });
  callClient = new CallClient();
  const credential = new AzureCommunicationTokenCredential({
    token: cmd.token,
    tokenRefresher,
    refreshProactively: true,
  });
  agent = await callClient.createTeamsCallAgent(credential);
  deviceManager = await callClient.getDeviceManager();
  agent.on('incomingCall', ({ incomingCall }) => {
    incoming.set(incomingCall.id, incomingCall);
    const info = incomingCall.callerInfo || {};
    const id = info.identifier ? info.identifier.microsoftTeamsUserId || info.identifier.rawId || '' : '';
    incomingCall.on('callEnded', () => incoming.delete(incomingCall.id));
    emit({ type: Events.incomingCall, callId: incomingCall.id, caller: { id, displayName: info.displayName || '' } });
  });
  emit({ type: Events.initialized });
}

function refreshToken(cmd) {
  if (pendingRefresh) {
    pendingRefresh(cmd.token);
    pendingRefresh = null;
  }
}

// --- devices -------------------------------------------------------------
async function camera() {
  if (selectedCamera) return selectedCamera;
  const cams = await deviceManager.getCameras();
  return cams[0] || null;
}

async function videoOptions(video) {
  if (!video) return {};
  const cam = await camera();
  return cam ? { videoOptions: { localVideoStreams: [new LocalVideoStream(cam)] } } : {};
}

async function listDevices() {
  await deviceManager.askDevicePermission({ audio: true, video: true });
  const map = (list) => list.map((d) => ({ id: d.id, name: d.name }));
  const [mics, cams, spk] = await Promise.all([
    deviceManager.getMicrophones(),
    deviceManager.getCameras(),
    deviceManager.isSpeakerSelectionAvailable ? deviceManager.getSpeakers() : Promise.resolve([]),
  ]);
  emit({ type: Events.devices, microphones: map(mics), cameras: map(cams), speakers: map(spk) });
}

async function setDevice(cmd) {
  const find = (list) => list.find((d) => d.id === cmd.deviceId);
  if (cmd.kind === 'microphone') {
    const d = find(await deviceManager.getMicrophones());
    if (d) await deviceManager.selectMicrophone(d);
  } else if (cmd.kind === 'speaker') {
    const d = find(await deviceManager.getSpeakers());
    if (d) await deviceManager.selectSpeaker(d);
  } else if (cmd.kind === 'camera') {
    selectedCamera = find(await deviceManager.getCameras()) || null;
  }
}

// --- video rendering -----------------------------------------------------
async function renderStream(stream, container) {
  if (renderers.has(stream)) return;
  const renderer = new VideoStreamRenderer(stream);
  const view = await renderer.createView({ scalingMode: 'Fit' });
  const el = document.createElement('div');
  el.appendChild(view.target);
  container.appendChild(el);
  renderers.set(stream, { renderer, el });
}

function disposeStream(stream) {
  const r = renderers.get(stream);
  if (!r) return;
  r.renderer.dispose();
  r.el.remove();
  renderers.delete(stream);
}

function watchRemoteStream(stream) {
  const update = () => (stream.isAvailable ? renderStream(stream, $('remote')).catch(() => {}) : disposeStream(stream));
  stream.on('isAvailableChanged', update);
  update();
}

function watchParticipant(p) {
  p.videoStreams.forEach(watchRemoteStream);
  p.on('videoStreamsUpdated', (e) => {
    e.added.forEach(watchRemoteStream);
    e.removed.forEach(disposeStream);
  });
}

function syncLocalVideo(call) {
  for (const [stream] of renderers) {
    if (stream instanceof LocalVideoStream) disposeStream(stream);
  }
  call.localVideoStreams
    .filter((s) => s.mediaStreamType === 'Video')
    .forEach((s) => renderStream(s, $('local')).catch(() => {}));
}

// --- calls -----------------------------------------------------------------
function attach(call) {
  current = call;
  const state = () => {
    const ev = { type: Events.callState, callId: call.id, state: call.state };
    if (call.state === 'Disconnected' && call.callEndReason) {
      ev.reason = { code: call.callEndReason.code, subCode: call.callEndReason.subCode };
    }
    emit(ev);
    setStatus(call.state === 'Connected' ? '' : call.state);
    if (call.state === 'Disconnected') detach(call);
    updateButtons();
  };
  call.on('stateChanged', state);
  call.on('idChanged', state);
  call.on('isMutedChanged', updateButtons);
  call.on('isScreenSharingOnChanged', updateButtons);
  call.on('localVideoStreamsUpdated', () => {
    syncLocalVideo(call);
    updateButtons();
  });
  call.remoteParticipants.forEach(watchParticipant);
  call.on('remoteParticipantsUpdated', (e) => e.added.forEach(watchParticipant));
  syncLocalVideo(call);
  state();
}

function detach(call) {
  if (current === call) current = null;
  for (const [stream] of renderers) disposeStream(stream);
}

function updateButtons() {
  const c = current;
  const on = !!c && c.state !== 'Disconnected';
  for (const id of ['btn-mute', 'btn-camera', 'btn-share', 'btn-hangup']) $(id).disabled = !on;
  $('btn-mute').setAttribute('aria-pressed', String(!!(c && c.isMuted)));
  $('btn-camera').setAttribute('aria-pressed', String(!!(c && c.localVideoStreams.some((s) => s.mediaStreamType === 'Video'))));
  $('btn-share').setAttribute('aria-pressed', String(!!(c && c.isScreenSharingOn)));
}

function callById(id) {
  if (current && (!id || current.id === id)) return current;
  return agent ? agent.calls.find((c) => c.id === id) || null : null;
}

async function setMute(call, on) {
  if (on) await call.mute();
  else await call.unmute();
}

async function setCamera(call, on) {
  const local = call.localVideoStreams.find((s) => s.mediaStreamType === 'Video');
  if (on && !local) {
    const cam = await camera();
    if (cam) await call.startVideo(new LocalVideoStream(cam));
  } else if (!on && local) {
    await call.stopVideo(local);
  }
}

async function setShare(call, on) {
  if (on && !call.isScreenSharingOn) await call.startScreenSharing();
  else if (!on && call.isScreenSharingOn) await call.stopScreenSharing();
}

function requireAgent() {
  if (!agent) throw new Error('not initialized');
}

async function handle(cmd) {
  switch (cmd.type) {
    case Commands.init:
      return init(cmd);
    case Commands.refreshToken:
      return refreshToken(cmd);
    case Commands.call: {
      requireAgent();
      const ids = (cmd.participants || []).map((p) => teamsUser(p.teamsUserId));
      if (ids.length === 0) throw new Error('no participants');
      const opts = await videoOptions(cmd.video);
      return attach(ids.length === 1 ? agent.startCall(ids[0], opts) : agent.startCall(ids, opts));
    }
    case Commands.join:
      requireAgent();
      return attach(agent.join({ meetingLink: cmd.meetingLink }, await videoOptions(cmd.video)));
    case Commands.accept: {
      const inc = incoming.get(cmd.callId);
      if (!inc) throw new Error('unknown incoming call');
      incoming.delete(cmd.callId);
      return attach(await inc.accept(await videoOptions(cmd.video)));
    }
    case Commands.reject: {
      const inc = incoming.get(cmd.callId);
      incoming.delete(cmd.callId);
      return inc && inc.reject();
    }
    case Commands.hangup: {
      const c = callById(cmd.callId);
      return c && c.hangUp();
    }
    case Commands.mute: {
      const c = callById(cmd.callId);
      return c && setMute(c, !!cmd.on);
    }
    case Commands.camera: {
      const c = callById(cmd.callId);
      return c && setCamera(c, !!cmd.on);
    }
    case Commands.screenshare: {
      const c = callById(cmd.callId);
      return c && setShare(c, !!cmd.on);
    }
    case Commands.devices:
      requireAgent();
      return listDevices();
    case Commands.setDevice:
      requireAgent();
      return setDevice(cmd);
    default:
      return undefined;
  }
}

bridge.onCommand((cmd) => {
  Promise.resolve()
    .then(() => handle(cmd))
    .catch((err) => emit({ type: Events.error, command: cmd.type, message: String((err && err.message) || err) }));
});

// In-window controls act on the current call.
const act = (fn) => () => {
  if (current) fn(current).catch((err) => emit({ type: Events.error, message: String(err.message || err) }));
};
$('btn-mute').addEventListener('click', act((c) => setMute(c, !c.isMuted)));
$('btn-camera').addEventListener('click', act((c) => setCamera(c, !c.localVideoStreams.some((s) => s.mediaStreamType === 'Video'))));
$('btn-share').addEventListener('click', act((c) => setShare(c, !c.isScreenSharingOn)));
$('btn-hangup').addEventListener('click', act((c) => c.hangUp()));
updateButtons();

bridge.ready();
