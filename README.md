# DevOS

**English** | [简体中文](README.zh-CN.md)

DevOS is a local-first developer workspace that runs as a native desktop app (Wails v3 + WebView2). A single Go binary ships a desktop-like shell (dock, start menu, windows, lock screen) plus a set of built-in developer apps — SSH terminal, SFTP client, database client, API tool, notes, toolbox and a secret vault — and can load additional offline web plugins without restarting.

Everything is served from `127.0.0.1`; no account, cloud service or external database is involved.

## Highlights

- **Single binary, zero runtime deps** — the desktop shell and all built-in apps are embedded into the Go executable via `go:embed` (the database drivers are pure-Go where possible, including SQLite).
- **Local by default** — binds to loopback only, detects an already running instance and reuses it, and keeps all data in a directory you choose.
- **Desktop-like shell** — dock, start menu with search, draggable/resizable windows, preset or uploaded wallpapers, and a lock screen.
- **9 interface languages** — Simplified Chinese, Traditional Chinese, English, Japanese, Korean, French, German, Russian and Hindi, switchable at runtime.
- **App password protection** — optional unlock password (scrypt), session-based API guard, login throttling, and AES-256-GCM encryption for stored credentials.
- **Pluggable apps** — built-in apps and third-party web plugins share one manifest format; plugins are installed offline from a ZIP file.

## Built-in apps

| App | ID | What it does |
| --- | --- | --- |
| SSH Terminal | `ssh` | Session library, multi-tab terminal over WebSocket, jump hosts, character-encoding support, encrypted import/export of sessions |
| SFTP Files | `sftp` | Dual-pane file manager, transfer queue, permissions, inline editing |
| Database | `db` | Multi-connection manager with schema browsing, SQL console, row editing and NoSQL consoles; connectors for MySQL, PostgreSQL, SQLite, SQL Server, Oracle, ClickHouse, Redis, MongoDB, Cassandra, NebulaGraph and more (domestic, cloud and analytical engines included — some connectors are marked as planned) |
| API Tool | `api` | HTTP request collections, environments/variables, debugging, and a local mock server |
| Notes | `notes` | Notion-like block editor with page tree, search, favorites, Markdown import/export |
| Toolbox | `toolbox` | Everyday utilities: JSON, encoding, timestamps, hashing |
| Vault | `vault` | Grouped secrets with tags, search, an unlock password and a fully encrypted store |
| Plugin Manager | `plugins` | Install web plugins offline, resolve version conflicts, uninstall |

## Requirements

- **Go 1.27+** to build from source.
- On Windows 10/11 the **WebView2 Runtime** is required (preinstalled on Windows 11; available from Microsoft for Windows 10).
- The desktop window is hosted by Wails v3; on non-Windows platforms DevOS falls back to opening the page in the default browser.

## Quick start

```bash
git clone <repository-url> devos
cd devos
go build -o devos.exe .
./devos.exe
```

On first launch DevOS shows a setup wizard in a native window where you choose the data directory. After confirming, the same window switches to the desktop (the service still listens on `http://127.0.0.1:7788`).

Release packages for Windows are built with `-H windowsgui`, so double-clicking the executable starts the service and shows the desktop window without a console window. To reproduce that locally:

```bash
go build -trimpath -ldflags "-s -w -H windowsgui" -o devos.exe .
```

For development you can skip the build step:

```bash
go run .
```

### Command-line flags

| Flag | Description |
| --- | --- |
| `-port <n>` | Listen port; defaults to `config.json` or `7788` (the next free port is used if busy, up to `maxPortTries`). |
| `-no-tray` | Do not create the system tray icon; closing the window quits the app. |
| `-no-browser` | Do not show the main window on startup (stay in the tray only). |
| `-setup` | Force the data-directory setup wizard, even if a location has already been chosen. |

## Configuration

Settings live in `<data dir>/config.json` and are created with defaults on first run:

| Key | Default | Description |
| --- | --- | --- |
| `port` | `7788` | Preferred listen port. |
| `openBrowser` | `true` | Show the desktop window at startup; when disabled, DevOS stays in the tray and must be opened from the tray menu. |
| `maxPortTries` | `20` | How many consecutive ports to probe when the preferred one is busy. |

Language, wallpaper and app-password settings are stored separately in `security.json` and changed from the in-app settings panel.

## Data directory

The default location is the OS configuration directory plus `DevOS`:

| OS | Default data directory |
| --- | --- |
| Windows | `%APPDATA%\DevOS` |
| macOS | `~/Library/Application Support/DevOS` |
| Linux | `~/.config/DevOS` |

The chosen directory is recorded in `location.json` inside the OS configuration directory, so the wizard only appears once. Changing to a new directory does **not** migrate existing data.

```text
<data dir>/
├── config.json          # port / browser behaviour
├── security.json        # app password, locale, wallpaper preference
├── wallpapers/          # uploaded wallpaper images
├── apps/<app-id>/       # per-app data, one SQLite file per domain
│   ├── profiles.db      # ssh / sftp session profiles
│   ├── connections.db   # database connections
│   ├── vault.db         # vault metadata, groups and entries
│   └── notes.db         # notes pages and blocks
└── plugins/<plugin-id>/ # installed web plugins
```

Each app domain uses its own SQLite file with WAL mode and a single writer connection, so concurrent writes from different apps never contend for the same database lock. Schema upgrades are applied through `PRAGMA user_version`. When upgrading from an older build, the previous `profiles.json`, `connections.json`, `notes.json`, `meta.json` and `vault.json` files are imported automatically on first start and renamed to `*.bak`.

## Security

- **App password** — optional; when enabled, DevOS asks for it on startup and blocks apps and APIs until the desktop is unlocked. Passwords are hashed with scrypt (`N=32768, r=8, p=1`) and verified serially to bound memory use.
- **Sessions and guard** — the kernel protects every API route with a session check and origin verification; only a small set of endpoints (health, system info, unlock, wallpaper files) stay public.
- **Login throttling** — repeated failed unlock attempts hit a cooldown and return the remaining wait time.
- **Credential encryption** — credentials saved by apps (SSH passwords/keys, database passwords, vault entries) are encrypted with AES-256-GCM and written with owner-only permissions.
- **Encrypted export/import** — apps that support it (SSH, database) re-encrypt exports with a fresh random key derived via scrypt. The key is shown once and is required to import; it cannot be recovered.

## Internationalization

The interface ships with these locales:

| Code | Language |
| --- | --- |
| `zh-CN` | 简体中文 |
| `zh-TW` | 繁體中文 |
| `en` | English |
| `ja` | 日本語 |
| `ko` | 한국어 |
| `fr` | Français |
| `de` | Deutsch |
| `ru` | Русский |
| `hi` | हिन्दी |

Add or edit dictionaries in [`web/assets/locales`](web/assets/locales); each file registers a flat key/value map through `window.DevOSI18n.register`. The desktop shell, lock screen, settings and setup wizard are fully translated. The built-in apps additionally keep their own dictionaries in `web/assets/locales/apps` (currently Chinese, English and Traditional Chinese) and fall back to English for other interface languages.

## Plugin development

Web plugins are pure front-end apps: the kernel only loads, validates and serves their static files — it never executes plugin code. A plugin is a directory with a `plugin.json` manifest and a static root (default `web/`), packaged as a ZIP and installed through **Plugin Manager** without restarting DevOS.

```text
my-plugin/
├── plugin.json      # id, name, version, entry, staticRoot, spa, ...
└── web/
    └── index.html
```

The full specification — manifest fields, icon rules, path safety rules and Next.js static-export recipes — is in [`docs/web-plugins.md`](docs/web-plugins.md). A minimal example lives in [`plugins/hello`](plugins/hello).

## Project structure

```text
.
├── main.go                 # entry point, flags, single-instance handling, tray
├── setup.go                # first-run data-directory wizard
├── web/                    # desktop shell (embedded)
│   ├── index.html          # desktop, dock, start menu, lock screen, settings
│   ├── setup.html          # setup wizard
│   └── assets/             # desktop.js, i18n.js, locales/, styles
├── internal/
│   ├── kernel/             # HTTP kernel, app windows, security guard, web plugin loader
│   ├── plugin/             # plugin interface and manifest types
│   ├── plugins/            # built-in apps (ssh, sftp, db, apitool, notes, toolbox, vault, pluginmgr)
│   ├── config/             # config.json handling
│   ├── security/           # app password, sessions, locale storage
│   ├── secure/             # AES-256-GCM credential store, encrypted export/import
│   ├── profile/            # shared profile storage for connection apps
│   └── sshtools/           # SSH/SFTP helpers
├── plugins/hello/          # sample external web plugin
└── docs/web-plugins.md     # web plugin specification (v1)
```

## Development and testing

```bash
gofmt -l .
go test ./...
go build ./...
```

Assets under `web/` (and each plugin's `web/`) are compiled into the binary with `go:embed`, so **editing HTML/CSS/JS requires a rebuild and restart**. When changing static assets, bump the `?v=` query in the script/style URLs so browsers do not reuse cached copies, then hard-refresh the page.

The kernel exposes a small internal HTTP API used by the shell: `/api/health`, `/api/system`, `/api/apps`, `/api/plugins`, and `/api/security/*` (status, unlock, lock, password, settings, wallpapers). Built-in apps mount their own routes under `/api/apps/<id>/*`; the API tool also exposes its mock service publicly under `/mock/*`.

## License

[Apache License 2.0](LICENSE) © 2026 HiCodeOnline
