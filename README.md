# ☁️ CloudSync

Status: Work In Progress (WIP)

Bidirectional file sync between **Google Drive, Azure Blob Storage, and AWS S3** and a local folder.  
OS-agnostic (Linux · macOS · Windows) · No runtime dependencies · Single binary.

---

## Features

| | |
|---|---|
| **Providers** | Google Drive, Azure Blob Storage, AWS S3 (extensible) |
| **Directions** | Cloud → Local, Local → Cloud, or Both |
| **Change detection** | Checksum (MD5/SHA-256) with modtime+size fallback |
| **Disk guard** | Warns and prompts before consuming >N% of free space |
| **Excludes** | Shell-glob patterns (`.DS_Store`, `*.tmp`, …) |
| **Notifications** | HTML email report via SMTP (STARTTLS / implicit TLS) |
| **Security** | OAuth 2.0 for Google, AES-256 transport, config permission check |
| **State** | Per-provider JSON state files track last-synced metadata |
| **Deployment** | Single binary; cross-compile for any target with `make cross` |

---

## Quick Start

### 1 — Prerequisites

- [Go 1.22+](https://go.dev/dl/) installed

### 2 — Build

```bash
git clone https://github.com/yourname/cloudsync
cd cloudsync
make          # tidy deps + build
```

The binary `cloudsync` (or `cloudsync.exe` on Windows) is created in the project root.

### 3 — Configure

```bash
cp config.example.yaml config.yaml
chmod 600 config.yaml      # protect secrets
$EDITOR config.yaml
```

Set your secrets either **inline** or as **environment variables** using `${VAR}` syntax in the YAML.

### 4 — Run

```bash
./cloudsync                  # uses config.yaml in current directory
./cloudsync /path/to/config.yaml   # explicit path
```

---

## Provider Setup

### Google Drive

1. Go to [Google Cloud Console](https://console.cloud.google.com/) → **APIs & Services → Library** → enable **Google Drive API**.
2. **APIs & Services → Credentials → + Create Credentials → OAuth client ID**.
3. Application type: **Desktop app**. Download the JSON and note the `client_id` and `client_secret`.
4. Set in config (or env):
   ```yaml
   credentials:
     client_id: "${GDRIVE_CLIENT_ID}"
     client_secret: "${GDRIVE_CLIENT_SECRET}"
     token_file: "~/.cloudsync/gdrive_token.json"
   ```
5. On first run, CloudSync prints an authorisation URL. Open it, approve, paste the code back. The token is saved for subsequent runs.

### Azure Blob Storage

Option A — **Connection String** (simplest):
```bash
export AZURE_STORAGE_CONNECTION_STRING="DefaultEndpointsProtocol=https;AccountName=...;AccountKey=...;EndpointSuffix=core.windows.net"
```

Option B — **Account name + key**:
```yaml
credentials:
  account_name: "${AZURE_STORAGE_ACCOUNT}"
  account_key: "${AZURE_STORAGE_KEY}"
```

`remote_folder` format: `container-name` or `container-name/optional/prefix`.

### AWS S3

```bash
export AWS_ACCESS_KEY_ID="AKIA..."
export AWS_SECRET_ACCESS_KEY="..."
```

Or leave the credentials blank to use the standard AWS credential chain  
(`~/.aws/credentials`, instance role, ECS task role, etc.).

`remote_folder` format: `bucket-name` or `bucket-name/prefix/sub`.

---

## Configuration Reference

```yaml
state_dir: "~/.cloudsync/state"   # where sync state is persisted

providers:
  - name: "My Drive"
    type: gdrive                   # gdrive | azure | s3
    enabled: true
    remote_folder: "University"          # cloud path
    local_destination: "~/backups/University"
    sync_direction: both           # both | cloud-to-local | local-to-cloud
    credentials: { ... }

sync:
  disk_threshold: 0.75             # prompt if file > 75% of free space
  max_file_size_mb: 0              # 0 = no limit
  exclude_patterns:
    - "*.tmp"
    - ".DS_Store"

notification:
  enabled: true
  email: "you@example.com"
  smtp:
    host: smtp.gmail.com
    port: 587                      # 587=STARTTLS  465=TLS
    username: sender@gmail.com
    password: "${SMTP_PASSWORD}"
    from: "CloudSync <sender@gmail.com>"
    use_tls: false
```

---

## Cross-Compilation

```bash
make cross
# Produces binaries in ./dist/ for:
#   Linux   amd64 / arm64
#   macOS   amd64 / arm64
#   Windows amd64
```

---

## Deployment Tips

### Run on a schedule (Linux — cron)

```cron
0 * * * * /usr/local/bin/cloudsync /etc/cloudsync/config.yaml >> /var/log/cloudsync.log 2>&1
```

### Run on a schedule (macOS — launchd)

Create `~/Library/LaunchAgents/com.cloudsync.plist`:
```xml
<plist version="1.0"><dict>
  <key>Label</key><string>com.cloudsync</string>
  <key>ProgramArguments</key><array>
    <string>/usr/local/bin/cloudsync</string>
    <string>/Users/you/.cloudsync/config.yaml</string>
  </array>
  <key>StartInterval</key><integer>3600</integer>
  <key>RunAtLoad</key><true/>
</dict></plist>
```
```bash
launchctl load ~/Library/LaunchAgents/com.cloudsync.plist
```

### Run on a schedule (Windows — Task Scheduler)

```powershell
schtasks /Create /SC HOURLY /TN "CloudSync" /TR "C:\tools\cloudsync.exe C:\Users\you\.cloudsync\config.yaml"
```

---

## Security Notes

- Config file permissions are checked on startup — a warning is printed if the file is group- or world-readable.
- OAuth 2.0 tokens are stored with `0600` permissions.
- State files are stored with `0600` permissions.
- All cloud communications use TLS 1.2+ (enforced in code).
- Secrets are never logged; use `${ENV_VAR}` references to keep them out of the config file entirely.

---

## Project Layout

```
cloudsync/
├── main.go                # entry point, CLI, graceful shutdown
├── config/
│   └── config.go          # YAML loader, env-var expansion, defaults
├── providers/
│   ├── provider.go        # Provider interface + FileInfo type
│   ├── gdrive.go          # Google Drive backend (OAuth2)
│   ├── azure.go           # Azure Blob Storage backend
│   └── s3.go              # AWS S3 backend
├── sync/
│   ├── syncer.go          # bidirectional sync engine + report
│   ├── state.go           # per-provider change-detection state
│   ├── disk_unix.go       # free disk space (Linux/macOS)
│   └── disk_windows.go    # free disk space (Windows)
├── notify/
│   └── email.go           # SMTP email (STARTTLS + implicit TLS)
├── config.example.yaml
├── Makefile
└── go.mod
```
