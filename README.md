# acat – a client about teams

A lean, native desktop client for Microsoft Teams **chat and video calls**. Multi-account and multi-tenant, with OS notifications, and a goal of **< 300 MB RAM** at idle.

> **Status:** early development. See [docs/plan.md](docs/plan.md) for milestones.
>
> acat is an independent open-source project. It is **not affiliated with, endorsed by, or sponsored by Microsoft**. "Microsoft Teams" is a trademark of Microsoft Corporation.

## How it works

- **acat** (Go + Fyne) talks to Microsoft Graph directly with your own delegated token.
- **acat-relay** (Go) receives Graph webhooks and forwards them over WebSocket. It is zero-knowledge by design: no database, no message content, never sees Graph tokens. See [docs/privacy.md](docs/privacy.md).
- **acat-video** (minimal, hardened Electron + Azure Communication Services Calling SDK) runs hidden and only shows up during calls.

## Scope

In scope: 1:1 and group chats, notifications, presence, 1:1/group calls, joining meetings.
Out of scope: channels, files, calendar, apps/bots, whiteboard, recording, mobile.

## Getting started

- Admins / self-hosters: [docs/setup.md](docs/setup.md), [docs/self-hosting.md](docs/self-hosting.md)
- Developers: `make build test lint`, then see [CONTRIBUTING.md](CONTRIBUTING.md)
- Try it: `make demo` (sample data, no sign-in), `make run` (live, against the ape-dev relay; `make video` first for calls)
- macOS: `make install` builds `acat.app` with the video helper and copies it to `/Applications` (unsigned, ad-hoc). Override `CLIENT_ID=` / `RELAY_URL=` for your own deployment.

## License

[Apache License 2.0](LICENSE)
