# Privacy & threat model

## What the relay sees
- **Never**: Graph access tokens, message content, chat names.
- **Transiently, not stored or logged**: Graph change notifications (resource IDs, subscription IDs, `clientState`). For 1:1 chats the chat ID contains both participants' Entra object IDs.
- **Transiently during `/acs/token`**: an Entra token scoped to ACS (not to Graph) and the resulting ACS token. The exchange needs the ACS access key, so it cannot happen on the client.
- **In memory per connection**: `routingToken → WebSocket`. Dropped on disconnect.

## What the relay stores
- The TLS certificate cache (autocert mode only).
- Its ACS connection string (configuration).

## Logging
Counters and aggregate metrics only. No bodies, IDs, tokens, IPs at info level, or query strings. Tests enforce this from M5.

## Trust
If you do not trust a hosted relay, run your own; see [self-hosting.md](self-hosting.md).

## On the client
- **Tokens**: the MSAL cache is AES-256-GCM encrypted on disk (`0600`); the key lives in the OS keychain (macOS Keychain, Windows Credential Manager, Secret Service). Signing out of the last account wipes the cache. Access tokens are held in memory only.
- **Messages** are kept in memory only; nothing is written to disk except settings (`config.json`: client ID, relay URL).
- **Video helper**: a sandboxed Electron renderer (context isolation, no Node, CSP `script-src 'self'`, no navigation/new windows), in-memory session partition, stderr discarded by the client so SDK logs cannot leak tokens. It talks to the client only over its stdin/stdout.
- **Single instance**: a `0600` Unix socket in the config directory accepts only the word `show`.

## Threat model

| Attacker | Can | Cannot | Mitigation |
|---|---|---|---|
| Relay operator (or attacker on the relay) | See notification metadata (which chat changed, when), see ACS tokens during exchange, drop or delay notifications | Read messages, call Graph as the user (no Graph token), forge messages (client fetches content from Graph itself) | Zero-knowledge design; self-host; ACS tokens for Teams users expire with the Entra token they were exchanged from (about 1 h) |
| Internet attacker hitting `/hook` | Send forged notifications to a guessed routing token | Guess a token (192 bit); learn whether a token exists (always `202`); make the client show fake content (client re-reads from Graph and checks `clientState` in constant time) | Rate limits per token/IP, body size limit, fuzzing |
| Attacker flooding the relay | Exhaust connections | Exceed per-IP caps / connect rate | `ACAT_MAX_CONNS_PER_IP`, rate limits, slow consumers are kicked; load-tested with 10k connections |
| Malicious tenant/user in a chat | Send crafted HTML | Run script, load remote content, open non-http(s) links | HTML is parsed into a small block/inline model (`internal/render`), links restricted to `http(s)`/`mailto`, fuzzed nightly |
| Local malware as the same user | Read the keychain and token cache | — | Out of scope (same as every desktop client) |
| Compromised npm dependency in the helper | Run code in the build or in the sandboxed renderer | Access the filesystem from the renderer | Pinned versions (`--save-exact`, lockfile), `npm audit` in CI, SDK bundled at build time and not loaded remotely |

## Resilience
- Network changes and half-open TCP: the client pings the relay every 30 s and reconnects without a pong within 10 s.
- Sleep/wake: detected by comparing wall-clock and monotonic time; the relay connection is rebuilt at once, subscriptions are re-pointed or recreated, and chats are re-synced from Graph (missed notifications are not replayed).
- Expired tokens: silent refresh via MSAL; if interaction is required the UI asks to sign in again. A tenant revoking consent surfaces as a sign-in error for that account only.
