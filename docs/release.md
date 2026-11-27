# Releasing acat

Pushing a tag `vX.Y.Z` runs `.github/workflows/release.yml`:

| Job | Output |
|---|---|
| `bundle` (macOS arm64, Windows x64, Linux x64; native runners) | `acat_<ver>_darwin_arm64.dmg`, `acat_<ver>_windows_amd64.zip`, `acat_<ver>_linux_amd64.tar.gz`. Each contains the client and the video helper (`scripts/package.sh`). |
| `relay` | `acat-relay_<ver>_linux_{amd64,arm64}`, SBOM `acat_<tag>.spdx.json` |
| `publish` | GitHub release with all files + `SHA256SUMS` (`v0.*` and `-rc` tags are marked pre-release) |

The relay container image is published separately by `relay-image.yml` (`ghcr.io/ape-dev-de/acat-relay:<ver>`).

A test build without a release: *Actions → Release → Run workflow* with a version string; bundles are attached to the run as artifacts.

## Defaults baked into builds

Repository **variables** (not secrets, they are public anyway):

- `ACAT_DEFAULT_CLIENT_ID`: Entra app (client) ID
- `ACAT_DEFAULT_RELAY_URL`: e.g. `https://relay.example.org`

Users can override both (see `docs/setup.md`).

## Signing (optional, needed for a smooth install)

Without these secrets, builds are unsigned. macOS bundles are then ad-hoc signed: Gatekeeper requires right-click → Open the first time, and Windows SmartScreen warns.

macOS (Apple Developer ID):

| Secret | Content |
|---|---|
| `MACOS_CERT_P12` | base64 of the *Developer ID Application* certificate + key (.p12) |
| `MACOS_CERT_PASSWORD` | password of the .p12 |
| `MACOS_SIGN_IDENTITY` | e.g. `Developer ID Application: Example GmbH (TEAMID)` |
| `APPLE_ID`, `APPLE_TEAM_ID`, `APPLE_APP_PASSWORD` | notarization (app-specific password) |

Windows code signing is not wired up yet (follow-up issue): the plan is Azure Trusted Signing or a cloud HSM-backed certificate, applied to `acat.exe` and `acat-video.exe` before zipping.

## Bundle layout

- macOS: `acat.app/Contents/MacOS/acat`, helper at `acat.app/Contents/Frameworks/acat-video.app`
- Windows/Linux: `acat/acat(.exe)` with the helper in `acat/acat-video/`

The client finds the helper at these paths (`cmd/acat/calls.go`). The helper is an Electron app built with electron-builder (`video/package.json` → `build`): ASAR, only `en-US`/`de` locales, the ACS SDK is bundled by esbuild and not shipped as `node_modules`.

## Licensing notice

Bundles contain the proprietary Azure Communication Services Calling SDK. `docs/THIRD_PARTY.md` is shipped in every bundle and explains the terms. See `docs/spikes.md` #4.
