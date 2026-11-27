# CLAUDE.md

Read `docs/plan.md` first. It is the source of truth for architecture and milestones.

## Rules
- One milestone per PR. Copy its acceptance criteria into the PR body as a checklist.
- [auto] criteria must pass in CI. For each [manuell] criterion open an issue labeled `needs-human` and keep building instead of waiting.
- Tests/mocks first, then implementation. Real Graph/ACS calls only behind the `integration` build tag.
- Zero knowledge: the relay never logs bodies, IDs, tokens or query strings. Keep `docs/privacy.md` accurate.
- No secrets in the repo. Test tenants come from `ACAT_TEST_*` env vars.
- Protocol changes go through `pkg/protocol` and follow `docs/protocol.md` (relay supports N and N-1; additive features over version bumps).
- Keep dependencies minimal. No msgraph-sdk-go. The relay is stdlib + `coder/websocket`.
- Comments and docs in English; the plan stays in German.

## Commands
- `make build test lint` (all must pass), `make vuln`
- `make relay`, `make client`, `make docker-relay`
