# Spikes (M1)

Status of the open questions from the plan. "Open" items need a real tenant (see issues labelled `needs-human`).

| # | Question | Status | Result / fallback |
|---|---|---|---|
| 1 | Subscription on `/users/{id}/chats/getAllMessages` with delegated `Chat.ReadWrite`: v1.0? billing? per-user limits? | **Open** (needs tenant) | Docs list delegated `Chat.Read`/`Chat.ReadWrite` for this resource; the example uses `beta`. Implemented against v1.0 (`internal/subs`). Fallback if v1.0 refuses: switch `graph.Client.BaseURL` to beta for subscription calls, or one subscription per chat on `/chats/{id}/messages` (delegated `Chat.Read`). |
| 2 | ACS Teams-user token exchange with the multi-tenant app from a **second** tenant (consent, licensing) | **Open** (needs tenant B) | Docs: `Teams.ManageCalls`/`Teams.ManageChats` need no admin consent by default, but tenants can enforce it (admin consent URL in `docs/setup.md`). Users need a Teams license. Relay side implemented and tested (`relay/acs.go`). |
| 3 | ACS Web Calling SDK in Electron: works? idle RAM of the hidden helper? Linux? | **Partly done** | Microsoft lists Electron as supported on Windows and macOS; Linux is best effort. On macOS the helper starts, completes the handshake, loads the SDK (v2.0.0) and rejects an invalid token cleanly (`go test -tags electron ./internal/video/`). Idle RAM below target (see below). Real calls need a tenant. |
| 4 | License of `@azure/communication-calling` | **Done** | *Microsoft Software License Terms* (proprietary, not OSS). Object code in the "distributables" list may be redistributed inside our app, provided end users agree to terms that protect Microsoft at least as much, and we indemnify Microsoft for our app. Consequences: (a) the SDK is **only an npm dependency**, never committed to this repo; (b) installers (M11) must show a notice/EULA covering the SDK terms; (c) the SDK's license comments are kept (`dist/call.bundle.js.LEGAL.txt`). Our own code stays Apache-2.0. |
| 5 | Quote/reply in 1:1/group chats via `messageReference` attachment: v1.0? | **Open** (needs tenant) | Implemented (`graph.MessageReference`, composer "Reply with quote"). Fallback: send the quote as `<blockquote>` HTML only. |

## Helper idle RAM

Measured 2026-11-27 on macOS (Apple Silicon), Electron 45.5, ACS Calling SDK 2.0.0: hidden window, SDK bundle loaded, no call agent yet.

| | Processes | Sum of RSS | Physical footprint (`top` MEM) |
|---|---|---|---|
| GPU enabled | 4 | 341 MB | **108 MB** |
| `ACAT_VIDEO_NO_GPU=1` | 4 | 303 MB | 105 MB |

Summing RSS counts the shared Electron framework once per process, so it overstates usage. The physical footprint is the meaningful number: it is well below the 200 MB target. GPU stays enabled (video decoding) since disabling it saves almost nothing at idle. The client measured ~100 MB RSS in demo mode, so the total is ≈ 210 MB against the 300 MB goal. Re-measure after a call agent is created (needs a tenant) and on Windows.
