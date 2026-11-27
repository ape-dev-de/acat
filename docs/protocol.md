# Protocols

Types and version constants live in [`pkg/protocol`](../pkg/protocol). They are the single source of truth for both client and relay.

## Client ↔ relay (WebSocket `/ws`)

1. Client opens the WebSocket and sends `{"type":"hello","proto":<max>,"client":"acat/<version>"}`.
2. Relay answers `{"type":"welcome","proto":<negotiated>,"routingToken":"…","features":[…]}` or closes with an `error` frame if no common version exists.
3. Relay pushes `{"type":"notification"|"lifecycle","payload":<Graph webhook body, unchanged>}` frames. Subscriptions are created without resource data, so payloads contain IDs only.
4. In v1 the client sends **nothing** after `hello`; any data frame closes the connection. Keepalive uses WebSocket pings (every 30 s).

Features currently defined: `acs-token` (the relay offers `POST /acs/token`).

## ACS token exchange (`POST /acs/token`)

Request `{"token":"<Entra access token, audience ACS, scopes Teams.ManageCalls + Teams.ManageChats>"}`; response `{"token":"<ACS token>","expiresOn":"…"}` with `Cache-Control: no-store`. The relay reads `oid`, `tid` and `appid`/`azp` from the token **without** verifying it (ACS verifies it), applies `ACAT_ALLOWED_APP_IDS` / `ACAT_ALLOWED_TENANTS`, signs the ACS request with HMAC-SHA256 (`teamsUser/:exchangeAccessToken?api-version=2023-10-01`) and passes the result through. Errors: 400 malformed token, 403 not allowed or rejected by ACS, 429 rate limited, 501 not configured, 502 ACS unavailable.

Close codes: `1001 going away` (relay shutdown, reconnect), `1008 policy violation` (no/invalid hello, unsupported version, slow consumer), `1013 try again later` (too many connections from the address). Hello must arrive within 10 s.

The routing token (192 bit, base64url) is valid only for this connection. After a reconnect the client gets a new one and must re-point its Graph subscriptions (`PATCH notificationUrl`).

### Compatibility rules
- The relay supports protocol version **N and N-1** (`RelayMinVersion >= RelayVersion-1`, enforced by a unit test).
- Additive changes do **not** bump the version; they are announced as a string in `features`. Clients must ignore unknown fields and features.
- A breaking change bumps N. Support for N-1 is dropped no earlier than one minor release later.
- CI runs the current client against the relay from the previous release tag, and vice versa.
- A client that gets an unsupported-version error tells the user "relay too old" or "client too old".

## Client ↔ video helper (stdin/stdout, JSON lines)

Same envelope idea. Client and helper always ship together, so the version must match exactly (`VideoVersion`). On a mismatch the helper is not started. Message set: see plan M9.
