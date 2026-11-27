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
