# acat – a client about teams

Schlanker, nativer Client für Microsoft Teams: **nur Chat + Video**, mandantenfähig, mit Benachrichtigungen.
Go-Client mit nativer UI, Video in einem abgespeckten Electron-Hilfsprozess, Zero-Knowledge-Relay-Server.

> Ziel: < 300 MB RAM im Leerlauf (statt > 2 GB beim Original-Teams).

Plan-Version: **v2 (2026-11-27)**: validiert gegen die MS-Learn-Doku, Milestones neu geschnitten (siehe §7 Änderungsprotokoll).

---

## 1. Architektur

```
┌──────────────────── acat (Go) ─────────────────────┐        ┌──────── acat-relay (Go) ────────┐
│ Native UI (Fyne): Chatliste, Verlauf, Eingabe       │◄─WSS──►│ /hook       Graph-Webhooks       │
│ Graph-REST direkt (delegiertes Token, Keychain)     │        │ /lifecycle  Lifecycle-Events     │
│ Abo-Verwaltung (create/renew/delete)                │        │ /ws         WebSocket-Hub (RAM)  │
│ OS-Notifications + Tray (Fyne-intern)               │        │ /acs/token  Entra→ACS-Tausch     │
│        │ stdin/stdout JSON-Zeilen                   │        │ /healthz, /metrics               │
│        ▼                                            │        │ keine DB, keine Inhalts-Logs     │
│ acat-video (Electron, hidden) + ACS Calling SDK     │        └──────────────┬──────────────────┘
└──────────────┬──────────────────────────────────────┘                       │
               └────────────── Microsoft Graph / Azure Communication Services ┘
```

### Zero-Knowledge-Prinzip
- Der **Client** legt die Graph-Abos mit seinem eigenen Token selbst an. Der Relay sieht **nie** Graph-Tokens.
- `notificationUrl = https://relay/hook?r=<routingToken>`, `lifecycleNotificationUrl = https://relay/lifecycle?r=<routingToken>`.
  Der `routingToken` ist zufällig (≥ 128 bit), gilt pro WS-Verbindung und liegt nur im RAM.
- `clientState` = zufälliges Geheimnis, das **nur der Client** prüft.
- Abos **ohne** `includeResourceData`: Benachrichtigungen enthalten nur IDs. Der Client holt die Nachricht selbst per Graph.
- Der Relay hält im RAM nur `routingToken → WS-Verbindung`. Persistent sind nur das TLS-Zertifikat (nur im autocert-Modus) und der ACS-Connection-String.
- **Bekannte Einschränkungen (offen dokumentieren):**
  - Der Relay sieht IDs (Metadaten). Bei 1:1-Chats stecken die Object-IDs beider Teilnehmer in der Chat-ID.
  - `/acs/token`: Der Relay sieht **kurzzeitig** das Entra-Token mit ACS-Scope (nicht das Graph-Token) und gibt das ACS-Token zurück. Er speichert und loggt beides nicht. Technisch geht es nicht anders, denn der Tausch braucht den ACS-Access-Key.

---

## 2. Tech-Stack

| Bereich | Wahl |
|---|---|
| Client-Sprache | Go ≥ 1.23 (CGO nötig wegen Fyne) |
| UI | Fyne v2 (Notifications + System-Tray sind eingebaut, kein beeep nötig) |
| Auth | `microsoft-authentication-library-for-go` (öffentlicher Client, PKCE, Loopback-Redirect `http://localhost`) |
| Token-Speicher | MSAL-Cache **verschlüsselt als Datei** (AES-GCM), **Schlüssel** in der OS-Keychain (`zalando/go-keyring`). Grund: Der Windows Credential Manager erlaubt nur ca. 2,5 KB pro Eintrag, und ein Multi-Account-MSAL-Cache ist größer. |
| Graph | Eigener schlanker REST-Client (kein msgraph-sdk-go) |
| Relay | Go stdlib `net/http` + `github.com/coder/websocket`, TLS wahlweise `autocert` **oder** hinter Reverse-Proxy |
| Video | Electron (minimal) + `@azure/communication-calling` (Electron offiziell nur für Win/macOS unterstützt) |
| IPC Go↔Electron | Newline-delimited JSON über stdin/stdout, versioniert |
| Lizenz | **Apache-2.0** (enthält eine Patentklausel; alle Go-Abhängigkeiten MIT/BSD/ISC und damit kompatibel) |

### Repo: **Monorepo** (ein GitHub-Repo)
```
/cmd/acat            Client-Einstiegspunkt
/cmd/acat-relay      Relay-Server
/internal/auth       MSAL, verschlüsselter Cache, Multi-Account
/internal/graph      REST-Client, Paging, 429/Retry-After
/internal/subs       Abo-Lifecycle
/internal/relayc     WS-Client zum Relay
/internal/ui         Fyne-Views
/internal/video      Electron-Prozess-Steuerung + IPC
/internal/render     Teams-HTML → Fyne-RichText
/pkg/protocol        Relay↔Client- und Go↔Electron-Protokoll (Typen + Version), von beiden Seiten importiert
/relay               Server-Code (hook, lifecycle, ws, acs)
/video               Electron-App (main.js, preload.js, call.html, call.js)
/deploy              Dockerfile, docker-compose (Relay + Caddy), Beispiel-Env
/infra               Entra-App-Spezifikation + ACS (Bicep) für den Infra-Agent
/docs                setup.md, self-hosting.md, privacy.md, protocol.md
```

### Versionierung & Kompatibilität
- **Ein Release-Tag** `vX.Y.Z` für Client und Relay (gemeinsames Go-Modul, gemeinsame CI).
- **Getrennte Protokollversion** in `/pkg/protocol` (Ganzzahl). Der WS-Handshake ist der erste Frame: `hello{proto, client}` → `welcome{proto, routingToken, features[]}`.
- Regel: **Der Relay ist rückwärtskompatibel** und unterstützt `proto` N und N-1. Neue Funktionen kommen nur als **additive Features** über `features[]`. Ein Breaking Change erhöht N, und N-1 wird erst nach einem Minor-Release-Zyklus entfernt.
- Der Client lehnt einen Relay mit `proto` < Minimum mit einer klaren UI-Meldung ab („Relay zu alt“).
- CI: Kompatibilitätstest „aktueller Client gegen Relay aus dem vorigen Release-Tag“ und umgekehrt. Damit braucht es **keine Versionsmatrix** in der Doku.
- Gleiches Prinzip für Go↔Electron-IPC. Dort ist es einfacher, weil beide immer zusammen ausgeliefert werden: Bei falscher Version wird der Helfer nicht gestartet.

---

## 3. Infrastruktur

### Relay-Hosting (eigene Infra oder Mini-VPS)
- Zwei Betriebsmodi per Env-Variable:
  - `ACAT_TLS_MODE=autocert`: Der Relay terminiert TLS selbst (Mini-VPS, Ports 80/443).
  - `ACAT_TLS_MODE=proxy`: HTTP auf internem Port hinter Traefik/Caddy/nginx. Die Client-IP kommt nur aus `ACAT_TRUSTED_PROXIES` (für das Rate-Limit).
- Ressourcen: 1 vCPU / 512 MB reichen für mehrere tausend WS-Verbindungen. Zustandslos, daher **genau eine Instanz** (sticky routing wäre sonst nötig, weil die Tokens im RAM liegen).
- Öffentlich erreichbar per HTTPS mit gültigem Zertifikat (Graph-Anforderung). Die Validierungsantwort muss in < 10 s kommen.
- Liefergegenstand: `deploy/docker-compose.yml` (Relay + Caddy) und `docs/self-hosting.md`.

### Entra-App & ACS (über den Infra-Agent)
Liegt als maschinenlesbare Spezifikation in `/infra`; der Infra-Agent legt die Ressourcen damit an:
- **App-Registrierung** `acat`: `signInAudience = AzureADMultipleOrgs`, `isFallbackPublicClient = true`, Plattform „Mobile & Desktop“, Redirect `http://localhost`.
- **Delegierte Berechtigungen (Graph):** `User.Read`, `User.ReadBasic.All` (Personensuche), `Chat.ReadWrite`, `ChatMessage.Send`, `Presence.Read.All`, `Presence.ReadWrite` (eigenen Status setzen), `offline_access`.
- **Delegierte Berechtigungen (ACS-Ressource „Azure Communication Services“):** `Teams.ManageCalls`, `Teams.ManageChats`.
  Laut Doku braucht das standardmäßig **keine** Admin-Zustimmung, Mandanten können sie aber erzwingen. Der Admin-Consent-Link kommt in `docs/setup.md`.
- **ACS-Ressource** (Bicep): eine Instanz, Datenstandort EU. Der Connection-String geht als Secret an den Relay (`ACAT_ACS_CONNECTION_STRING`).
- Ergebnis-Ausgabe des Infra-Agents: `ACAT_CLIENT_ID` (geht in den Client-Build/die Config), Relay-Secret.

---

## 4. Milestones

Regeln für die Umsetzung:
- Jeder Milestone = ein PR auf `main`, Akzeptanzkriterien als Checkliste im PR.
- Kriterien sind gekennzeichnet: **[auto]** = in CI/Tests prüfbar und damit eigenständig abschließbar. **[manuell]** = braucht einen Menschen, einen echten Mandanten oder einen echten Teams-Client. Manuelle Punkte werden gesammelt und an **Checkpoints** gemeinsam abgenommen, damit sie den Weiterbau nicht blockieren.
- Erst Tests/Mocks, dann Implementierung. Echte Graph-Aufrufe nur in Tests mit Build-Tag `integration`.

### M0 – Fundament
- Go-Modul, Repo-Layout, Makefile (`build`, `test`, `lint`, `relay`, `video`), golangci-lint, govulncheck.
- CI (GitHub Actions): Lint + Test auf Linux; Build-Matrix auf **nativen** Runnern (ubuntu, macos, windows), weil Fyne CGO braucht.
- `LICENSE` (Apache-2.0), `NOTICE`, `README` (mit Hinweis „nicht mit Microsoft verbunden“), `CONTRIBUTING`, `SECURITY.md`, `CLAUDE.md`, Issue-/PR-Vorlagen, Dependabot.
- `/infra`: Entra-App-Spezifikation + ACS-Bicep (für den Infra-Agent), `docs/setup.md`.
- `/pkg/protocol`: Grundgerüst mit Versionskonstanten.

**Akzeptanz:** [auto] `make build test lint` lokal und in CI auf 3 OS grün. [manuell] Infra-Agent hat App + ACS angelegt, Client-ID liegt vor.

### M1 – Spikes (vor allem anderen klären, Ergebnis in `docs/spikes.md`)
1. Abo `/users/{id}/chats/getAllMessages` mit delegiertem `Chat.ReadWrite`: Laut Doku unterstützt, das Beispiel nutzt aber `beta`. Klären: Geht es auf v1.0? Wird es abgerechnet? Gibt es Limits pro Nutzer?
2. ACS-Token-Tausch mit der Multi-Tenant-App aus einem **zweiten** Mandanten (Consent-Ablauf, nötige Teams-Lizenz).
3. ACS Web Calling SDK in Electron: Läuft es auf macOS/Windows? Wie viel RAM braucht ein versteckter Leerlauf-Helfer? Linux nur Versuch.
4. Lizenz von `@azure/communication-calling` prüfen (vermutlich proprietäre MS-Lizenz). Daraus folgt: nur als npm-Abhängigkeit, nicht ins Repo kopieren; Weitergabe im Installer anhand der Lizenzbedingungen klären.
5. Zitat/Antwort in 1:1-/Gruppenchats per `messageReference`-Anhang: v1.0 oder beta?

**Akzeptanz:** [manuell] Jede Frage beantwortet, und zwar mit Go/No-Go plus Fallback. Bei No-Go wird der betroffene Milestone im Plan angepasst, bevor gebaut wird.

### M2 – Auth & Accounts
- Interaktiver Login (PKCE, Loopback), Authority `organizations`.
- MSAL-Cache verschlüsselt als Datei, Schlüssel in der Keychain. Mehrere Accounts/Mandanten parallel, Account-Wechsel.
- Stille Token-Erneuerung, Logout (Account aus dem Cache entfernen, Schlüssel löschen, wenn es der letzte Account war).
- Konfiguration: `client_id`, `relay_url` aus Config-Datei/Env (Standardwerte zur Build-Zeit einkompiliert, damit Self-Hoster eigene Werte setzen können).

**Akzeptanz:** [auto] Cache-Verschlüsselung + Keychain-Abstraktion mit Fake getestet. [manuell] Login bei 2 Mandanten, Neustart ohne erneuten Login, `GET /me` geht.

### M3 – Graph-Client & Chat lesen
- REST-Client: Paging (`@odata.nextLink`), Retry bei 429/503/504 mit `Retry-After` + Jitter, Kontext-Timeouts, gemeinsamer Token-Provider.
- `GET /me/chats?$expand=members,lastMessagePreview&$orderby=lastMessagePreview/createdDateTime desc`, `GET /chats/{id}/messages` (Nachladen älterer Seiten).
- `/internal/render`: Teams-HTML → Fyne-RichText (Text, Links, Fett/Kursiv, @Mentions, Codeblöcke, Emojis). Unbekanntes wird als Text angezeigt. **Fuzz-Test** des Parsers.
- Cache **nur im RAM**.
- Fyne-UI: Account-Umschalter, Chatliste, Verlauf.

**Akzeptanz:** [auto] Unit-Tests für Paging/Retry gegen Mock-Server, Golden-Tests für das Rendering. [manuell] Chatliste + Verlauf inkl. ältere Nachrichten in der UI.

### M4 – Chat schreiben
- Senden (HTML), @Mention (`mentions[]`), Bearbeiten (`PATCH`), Löschen (`softDelete`). Zitieren je nach Spike-Ergebnis M1.5.
- Neuen 1:1-/Gruppenchat anlegen, Personensuche über `/users?$search` (Header `ConsistencyLevel: eventual`, braucht `User.ReadBasic.All`).
- Gelesen/ungelesen: `viewpoint`, `markChatReadForUser`, `markChatUnreadForUser`.

**Akzeptanz:** [auto] Request-Bodies per Golden-Test gegen die Graph-Doku-Beispiele. [manuell] Round-Trip mit dem echten Teams-Client in beide Richtungen, korrekt formatiert.

### M5 – Relay-Server (Zero Knowledge)
- `GET/POST /hook` und `/lifecycle`: `validationToken` als `text/plain` zurück (< 10 s). Benachrichtigungen per `?r=` an die WS weiterleiten, **sofort `202`**, auch bei unbekanntem Token (verwerfen, damit kein Orakel entsteht).
- `/ws`: `hello`/`welcome`-Handshake (§2 Versionierung), Token ≥ 128 bit aus `crypto/rand`, Ping/Pong, Token beim Trennen verwerfen, Backpressure (langsame Clients werden getrennt).
- Keine Persistenz, keine Logs mit Bodies/IDs/Tokens; nur Zähler unter `/metrics` (Prometheus). Body-Limit (z. B. 64 KB), Rate-Limit pro IP, Limit gleichzeitiger WS pro IP.
- TLS: `autocert` **oder** `proxy` (§3). Konfiguration nur über Env.
- Dockerfile (distroless, non-root), `deploy/docker-compose.yml` mit Caddy.

**Akzeptanz:** [auto] Integrationstest: simulierter Graph-Webhook kommt beim WS-Client an; unbekannter Token wird verworfen; Validierungs-Handshake klappt; nach Neustart sind keine Daten übrig; Logs enthalten nachweislich keine Tokens/IDs (Test auf Log-Ausgabe); Fuzzing von `/hook`. [manuell] Relay deployt, erreichbar unter echter Domain.

### M6 – Live-Updates & Benachrichtigungen
- Client: WS zum Relay → `routingToken`. Abo auf `/users/{id}/chats/getAllMessages` (Fallback: ein Abo pro Chat auf `/chats/{id}/messages`), ohne Resource Data, `clientState` zufällig, `lifecycleNotificationUrl` immer gesetzt.
- Ablaufzeit: **60 min** (Graph erlaubt bis 3 Tage, kurze Laufzeit heißt aber: Abos sterben von selbst, wenn der Client weg ist). Erneuern 15 min vor Ablauf.
- Reconnect → neuer Token → bestehende Abos per `PATCH notificationUrl` umhängen (falls das Patchen der URL nicht geht: löschen + neu anlegen) → Chats seit dem letzten Stand nachladen (Lücke schließen).
- Lifecycle: `reauthorizationRequired` → erneuern; `missed`/`subscriptionRemoved` → neu anlegen + nachladen.
- `clientState` prüfen, Nachricht per Graph holen, UI aktualisieren, OS-Benachrichtigung (nicht bei eigener Nachricht und nicht im gerade offenen Chat, wenn das Fenster im Fokus ist).
- Beim Beenden Abos löschen; Tray-Icon mit Ungelesen-Zähler.

**Akzeptanz:** [auto] Abo-Lifecycle-State-Machine gegen Fake-Graph + Fake-Relay getestet (Ablauf, Reconnect, missed, removed). [manuell] Nachricht aus Teams erscheint in < 5 s in acat + Toast; 2 h Dauerlauf ohne verlorene Abos; Relay-Neustart wird automatisch überbrückt.

> **Checkpoint A (Chat-MVP):** M2–M6 manuell abnehmen → Release `v0.1.0` (nur Chat).

### M7 – Presence
- Status der Chatpartner per `POST /communications/getPresencesByUserId` (Polling alle 60 s nur für sichtbare Chats; ein Presence-Abo lebt max. 60 min und ist für den Anfang nicht nötig).
- Eigenen Status setzen (`setUserPreferredPresence`, `Presence.ReadWrite`).

**Akzeptanz:** [auto] Batching/Polling mit Mock. [manuell] Statuspunkte in der Chatliste, Statuswechsel in < 1 min sichtbar.

### M8 – ACS-Token-Tausch
- Relay `POST /acs/token`: Body = Entra-Token (ACS-Scope) + `appId` + `userId`. Aufruf `POST {acs}/teamsUser/:exchangeAccessToken?api-version=<aktuelle GA-Version, in M1 prüfen>` mit HMAC-SHA256-Signatur (selbst in Go).
  Nichts speichern. Rate-Limit pro IP; optional `ACAT_ALLOWED_TENANTS` (Prüfung des `tid`-Claims), damit die eigene ACS-Ressource nicht fremd genutzt wird.
- Client: ACS-Token holen, vor Ablauf erneuern, an den Video-Helfer weitergeben.

**Akzeptanz:** [auto] HMAC-Signatur per Unit-Test gegen das dokumentierte Beispiel geprüft; Handler gegen Fake-ACS getestet. [manuell] Gültiges ACS-Token für Nutzer aus 2 Mandanten.

### M9 – Electron-Video-Helfer (abgespeckt)
- Minimale Electron-App mit einem versteckten `BrowserWindow`, lädt nur das gebündelte `call.html` (kein Remote-Content außer den ACS-/Teams-Media-Endpunkten).
- Härtung: `contextIsolation`, `sandbox`, kein `nodeIntegration`, strikte CSP, Navigation/`window.open` blockiert, Permission-Handler nur für Mikro/Kamera/Bildschirm, keine DevTools im Release.
- Chromium abspecken: `disable-features` (Autofill, Translate, SpellCheck, MediaRouter, …), `disable-extensions`, `disable-background-networking`, `disable-sync`, `no-pings`, `partition` ohne `persist:`.
- IPC (versioniert, Schema in `/pkg/protocol`): `init{proto,token}`, `refreshToken`, `call`, `join`, `accept`, `reject`, `hangup`, `mute`, `camera`, `screenshare`, `devices`, `show/hide` ↔ `ready`, `incomingCall`, `callState`, `tokenExpired`, `error`.
- Go: Prozess starten/überwachen, bei Absturz mit Backoff neu starten. Einstellung „Eingehende Anrufe empfangen“: an = Helfer läuft dauerhaft, aus = Start nur bei Bedarf (spart RAM).

**Akzeptanz:** [auto] IPC-Protokoll: Go-Seite gegen Fake-Helfer getestet, JS-Seite gegen Fake-Stdin. [manuell] Leerlauf-RAM des Helfers < 200 MB (alle Prozesse zusammen, Win + macOS).

### M10 – Anrufe
- Ausgehend 1:1/Gruppe aus dem Chat (Teilnehmer = Chatmitglieder), Meeting-Beitritt über Join-Link (wird in Nachrichten erkannt).
- Eingehend: `incomingCall` → nativer Dialog in Go → annehmen → Video-Fenster anzeigen.
- Geräteauswahl, Stummschalten, Kamera an/aus, Bildschirm teilen (Win/macOS); Fenster nach dem Auflegen wieder verstecken.

**Akzeptanz:** [manuell] Anruf acat ↔ Teams in beide Richtungen mit Bild + Ton, Meeting-Beitritt, auf Windows + macOS (Linux best effort).

### M11 – Packaging & Release
- GoReleaser + electron-builder: Windows (MSIX/MSI), macOS (`.app` mit eingebettetem Helfer, signiert + notarisiert), Linux (AppImage/.deb). Nur benötigte Locales, ASAR.
- Autostart (optional), Single-Instance, Relay-Image auf GHCR, SBOM.
- Auto-Update optional (später).

**Akzeptanz:** [auto] Release-Pipeline erzeugt alle Artefakte. [manuell] Installation auf frischem System; Gesamt-RAM im Leerlauf < 300 MB; Installer < 150 MB. **Voraussetzung:** Apple-Developer-Account + Windows-Code-Signing-Zertifikat als CI-Secrets.

> **Checkpoint B:** M7–M11 abnehmen → Release `v0.2.0` (Chat + Video).

### M12 – Härtung
- Fehlerfälle: Token abgelaufen, Mandant entzieht die Freigabe, Netzwerkwechsel, Ruhezustand/Aufwachen (WS + Abos neu aufbauen).
- Lasttest Relay (10k WS), Fuzzing (`/hook`, Renderer, IPC).
- Sicherheits-Review: kein Logging sensibler Daten, `govulncheck`, `npm audit`, Dokumentation des Bedrohungsmodells in `docs/privacy.md`.

**Akzeptanz:** [auto] Lasttest + Fuzz in CI (nächtlich). [manuell] Checkliste abgehakt, keine offenen High-Findings → `v1.0.0`.

---

## 5. Nicht-Ziele
Teams-Kanäle, Dateien/SharePoint, Kalender, Apps/Bots, Whiteboard, Aufzeichnung, Mobile-Clients.

## 6. Arbeitsweise mit Claude Code
- Pro Session ein Milestone; zuerst diese Datei + `CLAUDE.md` lesen.
- Erst Tests/Mocks, dann Implementierung; echte Graph-Aufrufe nur mit Build-Tag `integration`.
- Keine Secrets im Repo (`.env` in `.gitignore`), Testmandanten über Env-Variablen (`ACAT_TEST_TENANT_A/B`).
- Manuelle Akzeptanzpunkte als Issue mit Label `needs-human` anlegen und weiterbauen, statt zu warten.
- Name: „Teams“ ist eine Marke von Microsoft. Deshalb **acat**, kein „Teams“ im Namen/Logo, Hinweis „inoffiziell“ im README.

## 7. Änderungsprotokoll v1 → v2 (Validierung)
| # | Befund | Änderung |
|---|---|---|
| 1 | chatMessage-Abos dürfen bis 3 Tage laufen; ab > 1 h ist `lifecycleNotificationUrl` Pflicht | `/lifecycle`-Route im Relay; 60 min bewusst gewählt, Lifecycle-URL immer gesetzt |
| 2 | `/users/{id}/chats/getAllMessages` unterstützt delegiert `Chat.Read`/`Chat.ReadWrite`, das Beispiel ist aber `beta` | Spike M1.1 statt Annahme |
| 3 | `/users?$search` geht nicht mit `User.Read` | `User.ReadBasic.All` + `ConsistencyLevel`-Header |
| 4 | Eigenen Status setzen geht nicht mit `Presence.Read.All` | `Presence.ReadWrite` ergänzt |
| 5 | ACS-Scopes brauchen laut Doku standardmäßig keine Admin-Zustimmung (Mandant kann sie erzwingen) | Consent-Link in der Doku, Spike M1.2 |
| 6 | Windows Credential Manager ca. 2,5 KB Limit | Verschlüsselte Cache-Datei, Schlüssel in der Keychain |
| 7 | Fyne hat Notifications + Tray eingebaut | beeep entfällt |
| 8 | Fyne braucht CGO | CI auf nativen Runnern statt Cross-Compile |
| 9 | Relay sieht beim ACS-Tausch das Entra-Token | Als Einschränkung dokumentiert; Tenant-Allowlist gegen Missbrauch der ACS-Ressource |
| 10 | ACS-Calling-SDK-Lizenz vermutlich nicht OSS | Spike M1.4; nur npm-Abhängigkeit |
| 11 | Eigene Infra mit Reverse-Proxy | TLS-Modus `proxy` + trusted proxies |
| 12 | Viele Akzeptanzkriterien nur manuell prüfbar | [auto]/[manuell]-Kennzeichnung + Checkpoints A/B |
| 13 | Relay/Client-Kompatibilität | Monorepo, Protokollversion N/N-1, CI-Compat-Test |
| 14 | Spikes standen nur als „offene Punkte“ im Plan | Eigener Milestone M1 vor allem anderen |
