# Claude Code WhatsApp

[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)
[![Go 1.22+](https://img.shields.io/badge/Go-1.22+-00ADD8?logo=go&logoColor=white)](https://go.dev/)
[![Python 3.11+](https://img.shields.io/badge/Python-3.11+-3776AB?logo=python&logoColor=white)](https://www.python.org/)
[![Release](https://img.shields.io/badge/release-v1.0.0-blue)](https://github.com/arktnld/claude-code-whatsapp/releases)

WhatsApp bot that connects to [Claude Code](https://claude.ai/code). Send a message and Claude reads, edits, and runs code in your projects.

## How it works

Two processes running together:

```
Your WhatsApp  <-->  Bridge (Go)  <-->  Core (Python)  <-->  Claude Code
                     whatsmeow          claude-agent-sdk
                     port 8080          sessions/storage
```

- **Bridge** — connects to WhatsApp using [whatsmeow](https://github.com/tulir/whatsmeow). Receives and sends messages via HTTP/WebSocket.
- **Core** — receives messages from the bridge, sends them to Claude Code SDK, and returns the response.

Only private (1:1) chats from phone numbers listed in `ALLOWED_PHONES` are processed. Group messages, broadcasts, and unknown numbers are silently ignored.

## Requirements

- **Go 1.22+** — to build the bridge
- **Python 3.11+** with Poetry
- **Claude Code CLI** — `npm install -g @anthropic-ai/claude-code` (must be logged in via `claude login`)
- **A dedicated WhatsApp number** — don't use your personal number (ban risk)

## Setup

### 1. Clone and configure

```bash
git clone https://github.com/arktnld/claude-code-whatsapp.git
cd claude-code-whatsapp

cp env.example .env
```

Edit `.env`:

```bash
# Phone numbers allowed to use the bot (comma-separated)
ALLOWED_PHONES=5511999999999

# Root folder for your projects — Claude can only access files inside it
APPROVED_DIRECTORY=/home/user/projects
```

### 2. Install dependencies

```bash
# Python
make dev

# Go (builds the bridge binary)
make build-bridge
```

### 3. Run the bridge (first terminal)

```bash
make run-bridge
```

On first run, a QR code appears in the terminal. On the dedicated phone:
- **Settings > Linked Devices > Link a Device**
- Scan the QR code

After linking, the session is saved in `bridge/whatsapp.db`. Next time it connects automatically.

### 4. Run the core (second terminal)

```bash
make run-core
```

Done. Send a message to the linked number from a phone listed in `ALLOWED_PHONES`.

## Usage

Send any text message and Claude responds directly:

```
You:  look at main.go and tell me what it does
Bot:  Working...
Bot:  main.go implements a WhatsApp bridge with 3 parts...

You:  add a /metrics endpoint that returns uptime
Bot:  Working...
Bot:  Added the endpoint. Here are the changes: ...

You:  run go build and check if it compiles
Bot:  Working...
Bot:  Compiled without errors.
```

Also accepts **photos** (Claude Vision) and **documents** (reads the content).

### Commands

Send as a regular message (WhatsApp has no native command system):

| Command | What it does |
|---------|-------------|
| `/start` | Shows welcome message and current directory |
| `/new` | Resets session — Claude forgets previous context |
| `/status` | Shows directory and active session info |
| `/repo` | Lists projects in the approved directory |
| `/repo name` | Switches to a different project |
| `/stop` | Interrupts a running request |

### Switching projects

```
You:  /repo
Bot:  *Repos*
      [git] api-backend/
      [git] frontend/
      [dir] scripts/
      Use: /repo <name>

You:  /repo api-backend
Bot:  Switched to api-backend/ (git)

You:  any failing tests?
Bot:  Working...
Bot:  Ran pytest. 2 tests failing in test_auth.py...
```

## Configuration

See `env.example` for all variables. The most important ones:

| Variable | Description | Default |
|----------|-------------|---------|
| `ALLOWED_PHONES` | Authorized phone numbers (comma-separated) | required |
| `APPROVED_DIRECTORY` | Project root folder | required |
| `WHATSAPP_BRIDGE_URL` | Bridge HTTP URL | `http://localhost:8080` |
| `CLAUDE_MAX_TURNS` | Max turns per conversation | `25` |
| `CLAUDE_TIMEOUT_SECONDS` | Timeout per request | `300` |
| `DEBUG` | Verbose logging | `false` |

## Security

- **Phone whitelist** — only numbers in `ALLOWED_PHONES` can interact. Unknown numbers are silently dropped (no response sent).
- **Group/broadcast blocking** — the bridge ignores all group and broadcast messages at the protocol level.
- **Directory sandbox** — Claude can only access files inside `APPROVED_DIRECTORY`.
- **Dedicated number** — use a separate WhatsApp number, not your personal one.

## Important notes

- **Ban risk**: WhatsApp has no official bot API. The whatsmeow library reverse-engineers the protocol. Use a dedicated number, don't spam, keep delays between messages.
- **Session expiry**: if WhatsApp disconnects, the bridge reconnects automatically. If it fails, delete `bridge/whatsapp.db` and scan the QR code again.

## Credits

- [claude-code-telegram](https://github.com/RichardAtCT/claude-code-telegram) — original project this is based on (MIT License)
- [whatsmeow](https://github.com/tulir/whatsmeow) — Go WhatsApp Web API library

## License

MIT
