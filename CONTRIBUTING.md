# Contributing

1. Read [docs/plan.md](docs/plan.md). Work is organized in milestones; each PR targets one.
2. Tests first, then implementation. Unit tests use mocks/fakes; real Graph/ACS calls only in tests with the `integration` build tag.
3. `make build test lint` must pass. CI runs on Linux, macOS and Windows.
4. Never log tokens, chat/message IDs or message content. Never commit secrets or tenant IDs.
5. Protocol changes: follow the compatibility rules in [docs/protocol.md](docs/protocol.md).

By contributing you agree that your contributions are licensed under the Apache License 2.0.

## Prerequisites

- Go (version in `go.mod`), a C compiler (Fyne needs CGO)
- Linux: `gcc libgl1-mesa-dev xorg-dev libxkbcommon-dev libwayland-dev`
- golangci-lint v2, govulncheck
- Node.js LTS (for `/video`, from M9)
