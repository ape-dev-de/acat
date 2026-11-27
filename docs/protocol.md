# Protocols

Types and version constants live in [`pkg/protocol`](../pkg/protocol). They are the single source of truth for both client and relay.

## Client ↔ relay (WebSocket `/ws`)

1. Client opens the WebSocket and sends `{"type":"hello","proto":<max>,"client":"acat/<version>"}`.
2. Relay answers `{"type":"welcome","proto":<negotiated>,"routingToken":"…","features":[…]}` or closes with an `error` frame if no common version exists.
3. Relay pushes `notification` and `lifecycle` frames (Graph payloads, IDs only, no content).

### Compatibility rules
- The relay supports protocol version **N and N-1** (`RelayMinVersion >= RelayVersion-1`, enforced by a unit test).
- Additive changes do **not** bump the version; they are announced as a string in `features`. Clients must ignore unknown fields and features.
- A breaking change bumps N. Support for N-1 is dropped no earlier than one minor release later.
- CI runs the current client against the relay from the previous release tag, and vice versa.
- A client that gets an unsupported-version error tells the user "relay too old" or "client too old".

## Client ↔ video helper (stdin/stdout, JSON lines)

Same envelope idea. Client and helper always ship together, so the version must match exactly (`VideoVersion`). On a mismatch the helper is not started. Message set: see plan M9.
