# Setup

acat needs three things: an Entra app registration (multi-tenant public client), an Azure Communication Services (ACS) resource, and a running `acat-relay`. The canonical desired state lives in [`/infra`](../infra/README.md); this page explains it for humans.

## 1. Entra app registration

| Setting | Value |
|---|---|
| Supported account types | Accounts in any organizational directory (multi-tenant) |
| Platform | Mobile and desktop applications |
| Redirect URI | `http://localhost` (MSAL picks a free port at runtime) |
| Allow public client flows | Yes |
| Secrets / certificates | none |

### Delegated permissions

| API | Permission | Why | Admin consent |
|---|---|---|---|
| Microsoft Graph | `User.Read` | Own profile, `/me` | no |
| Microsoft Graph | `User.ReadBasic.All` | People search for new chats | no |
| Microsoft Graph | `Chat.ReadWrite` | Read chats, subscriptions, mark read, edit/delete own messages | no |
| Microsoft Graph | `ChatMessage.Send` | Send messages | no |
| Microsoft Graph | `Presence.Read.All` | Chat partners' presence | no* |
| Microsoft Graph | `Presence.ReadWrite` | Set own presence | no* |
| Microsoft Graph | `offline_access` | Refresh tokens | no |
| Azure Communication Services | `Teams.ManageCalls` | Calls via ACS as Teams user | no** |
| Azure Communication Services | `Teams.ManageChats` | Required by the ACS token exchange | no** |

\* Verify in your tenant; some tenants restrict user consent entirely.
\** Per Microsoft docs not required by default, but tenants can enforce it.

### Admin consent for another tenant

If a tenant blocks user consent, its admin opens:

```
https://login.microsoftonline.com/<tenant-id>/adminconsent?client_id=<ACAT_CLIENT_ID>
```

## 2. ACS resource

Create one Communication Services resource (data location Europe), e.g. with [`infra/acs.bicep`](../infra/acs.bicep). The relay needs its connection string (`ACAT_ACS_CONNECTION_STRING`). Teams users calling through acat need a Teams license in their own tenant.

## 3. Relay

See [self-hosting.md](self-hosting.md).

## 4. Client configuration

| Variable / config key | Meaning |
|---|---|
| `ACAT_CLIENT_ID` / `client_id` | appId from step 1 |
| `ACAT_RELAY_URL` / `relay_url` | e.g. `wss://relay.example.org` |

Defaults can be compiled in for your own builds; users can override them.

## 5. Video helper (development)

Calls run in a separate helper (`video/`, Electron + ACS Calling SDK). Packaged builds ship it next to the binary. For a dev checkout:

```sh
make video                      # npm ci + bundle
export ACAT_VIDEO_HELPER="$PWD/video/node_modules/electron/dist/Electron.app/Contents/MacOS/Electron"  # macOS
# Linux: $PWD/video/node_modules/electron/dist/electron   Windows: …\electron.exe
export ACAT_VIDEO_APP="$PWD/video"
go run ./cmd/acat
```

Without a helper (or without a relay offering `acs-token`), the call buttons stay disabled. The first call asks for consent to the Teams calling permissions (a separate resource).

## Test tenants (development)

Integration tests (`make test-integration`) read `ACAT_TEST_TENANT_A`, `ACAT_TEST_TENANT_B` and `ACAT_CLIENT_ID` from the environment. Never commit them.
