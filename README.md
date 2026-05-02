# Claude Code WhatsApp

WhatsApp bot that gives you remote access to [Claude Code](https://claude.ai/code). Chat with Claude about your projects from WhatsApp.

**Inspired by [claude-code-telegram](https://github.com/RichardAtCT/claude-code-telegram)** by RichardAtCT (MIT License). Core Claude SDK integration, session management, storage, and security modules are adapted from that project.

## Architecture

```
WhatsApp <-> Bridge (Go/whatsmeow) <-> Python Core (claude-agent-sdk)
```

Two processes:
- **Bridge** (Go) — connects to WhatsApp via whatsmeow, exposes HTTP/WS API
- **Core** (Python) — Claude SDK integration, sessions, storage, auth

## Quick Start

### Prerequisites

- Go 1.22+
- Python 3.11+
- Claude Code CLI installed (`npm install -g @anthropic-ai/claude-code`)

### Setup

```bash
# Configure
cp .env.example .env
# Edit .env with your ALLOWED_PHONES and APPROVED_DIRECTORY

# Install Python deps
make dev

# Build Go bridge
make build-bridge

# Run bridge (scan QR code on first run)
make run-bridge

# In another terminal, run Python core
make run-core
```

### Usage

Send a message to the WhatsApp number:

```
You: Can you help me fix the auth bug in src/api.py?
Bot: Working...
     I found the issue. The token expiry check uses...

You: /new
Bot: Session reset. What's next?

You: /repo myproject
Bot: Switched to myproject/ (git)
```

### Commands

| Command | Description |
|---------|-------------|
| `/start` | Welcome message |
| `/new` | Reset session |
| `/status` | Show current directory and session |
| `/repo` | List repos or switch workspace |
| `/stop` | Interrupt running request |

## Credits

- [claude-code-telegram](https://github.com/RichardAtCT/claude-code-telegram) — original project this is based on
- [whatsmeow](https://github.com/tulir/whatsmeow) — Go WhatsApp Web API library

## License

MIT
