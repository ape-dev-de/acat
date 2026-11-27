# Self-hosting acat-relay

The relay is a single stateless Go binary, configured only through environment variables. Run **exactly one instance** per domain: routing tokens live in that process's memory.

Requirements: 1 vCPU, 512 MB RAM, a public DNS name, and HTTPS reachable by Microsoft Graph.

## Option A: mini-VPS with Docker Compose + Caddy

```sh
cp deploy/relay.env.example deploy/relay.env   # fill in secrets
ACAT_DOMAIN=relay.example.org docker compose -f deploy/docker-compose.yml up -d
curl https://relay.example.org/healthz
```

## Option B: behind your own reverse proxy

Run the image `ghcr.io/ape-dev-de/acat-relay` with `ACAT_TLS_MODE=proxy` and route `https://<domain>/` to port 8080. Requirements for the proxy:
- WebSocket upgrade on `/ws`, idle timeout ≥ 120 s.
- Set `ACAT_TRUSTED_PROXIES` to the proxy's CIDR so rate limiting sees real client IPs.
- Do not log query strings: `/hook?r=…` contains routing tokens.

## Option C: relay terminates TLS itself

`ACAT_TLS_MODE=autocert ACAT_DOMAIN=relay.example.org`, ports 80 and 443 open, a volume mounted at `ACAT_CERT_DIR` (default `/certs`). The certificate cache is the only file the relay writes.

## Metrics

Set `ACAT_METRICS_ADDR` (e.g. `:9090`) to serve Prometheus metrics on a **separate** listener; do not expose it publicly. Only aggregate counters exist: open connections, accepted/rejected connections, validations, forwarded and dropped notifications by fixed reason.

## Behaviour worth knowing

- Webhooks are always answered `202`, even for unknown routing tokens (no token oracle). Unknown tokens are counted and dropped.
- A client that cannot keep up (send queue full) is disconnected; it reconnects and resyncs via Graph.
- On `SIGTERM` all WebSockets get a "going away" close, so clients reconnect immediately after a restart. No state survives a restart.

## Environment

See [`deploy/relay.env.example`](../deploy/relay.env.example).
