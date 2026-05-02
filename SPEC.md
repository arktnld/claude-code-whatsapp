# claude-code-whatsapp — Project Spec

Bot WhatsApp que da acesso remoto ao Claude Code, baseado na arquitetura do `claude-code-telegram`.

## Arquitetura

```
┌──────────────┐        HTTP API        ┌──────────────────────┐
│  WhatsApp    │  (WebSocket ou REST)   │  Python Core         │
│  Bridge      │ ◄────────────────────► │                      │
│              │   JSON messages        │  claude-agent-sdk    │
│  whatsmeow   │                        │  storage (SQLite)    │
│  (Go)        │                        │  auth / security     │
│              │                        │  session mgmt        │
│  - connect   │                        │  rate limiting       │
│  - send msg  │                        │  event bus           │
│  - recv msg  │                        │                      │
│  - QR login  │                        │  (fork adaptado do   │
│  - media     │                        │   claude-code-tg)    │
└──────────────┘                        └──────────────────────┘
```

**Por que dois processos?**
- Reutiliza ~70% do core Python (Claude SDK, sessions, storage, security)
- whatsmeow (Go) eh a lib WhatsApp mais estavel e bem mantida
- Desacoplamento: bridge pode ser trocada sem mexer no core

## Componentes

### 1. WhatsApp Bridge (Go — whatsmeow)

Processo Go que gerencia conexao WhatsApp e expoe API HTTP.

#### Endpoints da Bridge API

| Metodo | Path | Descricao |
|--------|------|-----------|
| GET | `/health` | Health check |
| GET | `/qr` | Retorna QR code para login |
| POST | `/send` | Envia mensagem (text, image, document) |
| GET | `/status` | Status da conexao |
| WS | `/ws` | WebSocket para receber mensagens em tempo real |

#### Mensagem recebida (WS → Python)

```json
{
  "type": "message",
  "from": "5511999999999@s.whatsapp.net",
  "chat": "5511999999999@s.whatsapp.net",
  "message_id": "3EB0...",
  "timestamp": 1714600000,
  "content": {
    "type": "text|image|document|audio",
    "text": "mensagem aqui",
    "caption": "caption se media",
    "media_url": "/tmp/media/xxx.jpg",
    "mimetype": "image/jpeg",
    "filename": "file.pdf"
  }
}
```

#### Enviar mensagem (Python → Bridge)

```json
{
  "to": "5511999999999@s.whatsapp.net",
  "type": "text|image|document",
  "text": "resposta aqui",
  "media_path": "/tmp/media/resp.png",
  "caption": "caption opcional"
}
```

### 2. Python Core (fork adaptado do claude-code-telegram)

#### Camadas reutilizadas do telegram (sem mudanca)

- [x] `src/claude/` — Claude SDK integration, facade, session manager
- [x] `src/storage/` — SQLite, repository pattern, models
- [x] `src/security/` — auth manager, validators, rate limiter, audit
- [x] `src/config/` — Pydantic Settings (adaptar vars)
- [x] `src/events/` — EventBus, AgentHandler
- [x] `src/scheduler/` — cron jobs
- [x] `src/utils/` — constants

#### Camadas que precisam ser reescritas/adaptadas

- [ ] `src/bot/core.py` → `src/whatsapp/core.py` — WhatsApp bot principal
- [ ] `src/bot/orchestrator.py` → `src/whatsapp/orchestrator.py` — roteamento de msgs
- [ ] `src/bot/handlers/` → `src/whatsapp/handlers/` — command/message handlers
- [ ] `src/bot/middleware/` → `src/whatsapp/middleware/` — auth, rate limit (adaptar)
- [ ] `src/bot/features/` → `src/whatsapp/features/` — file, image, voice handlers
- [ ] `src/bot/utils/` → `src/whatsapp/utils/` — formatting (HTML→WhatsApp markdown)
- [ ] `src/main.py` — adaptar bootstrap

---

## Checklist de Implementacao

### Fase 0: Setup do Projeto
- [ ] Criar repo `claude-code-whatsapp`
- [ ] Estrutura de pastas (Python core + Go bridge)
- [ ] pyproject.toml (fork do telegram, trocar deps)
- [ ] go.mod para bridge
- [ ] .env.example com vars WhatsApp
- [ ] Makefile (dev, run-bridge, run-core, run-all)
- [ ] Docker Compose (bridge + core)

### Fase 1: WhatsApp Bridge (Go)
- [ ] Scaffold projeto Go com whatsmeow
- [ ] Login via QR code (terminal + endpoint HTTP)
- [ ] Persistencia de sessao (SQLite store do whatsmeow)
- [ ] Receber mensagens texto
- [ ] Receber mensagens media (image, audio, document)
- [ ] Enviar mensagens texto
- [ ] Enviar mensagens media
- [ ] Health check endpoint
- [ ] Status da conexao endpoint
- [ ] WebSocket server para push de mensagens
- [ ] Reconnect automatico
- [ ] Logging estruturado (JSON)
- [ ] Testes basicos

### Fase 2: Python Core — Adapter WhatsApp
- [ ] `src/whatsapp/client.py` — HTTP + WebSocket client para bridge
- [ ] `src/whatsapp/models.py` — dataclasses WhatsApp (Message, Chat, User)
- [ ] `src/whatsapp/core.py` — WhatsAppBot (equivalente ao ClaudeCodeBot)
  - [ ] Inicializacao e conexao com bridge
  - [ ] Loop de mensagens via WebSocket
  - [ ] Graceful shutdown
- [ ] `src/whatsapp/orchestrator.py` — MessageOrchestrator adaptado
  - [ ] Roteamento texto → Claude
  - [ ] Roteamento media → Claude
  - [ ] Comandos via prefixo (ex: `/new`, `/status`, `/repo`)
  - [ ] Progress messages (editar msg ou enviar nova)
  - [ ] Typing indicator via bridge
- [ ] `src/whatsapp/handlers/` — handlers adaptados
  - [ ] `message.py` — texto, documento, foto, audio
  - [ ] `command.py` — comandos WhatsApp
- [ ] `src/whatsapp/middleware/`
  - [ ] `auth.py` — auth por numero de telefone (whitelist)
  - [ ] `rate_limit.py` — rate limiting adaptado
- [ ] `src/whatsapp/utils/`
  - [ ] `formatting.py` — converter markdown Claude → WhatsApp formatting
    - WhatsApp suporta: *bold*, _italic_, ~strikethrough~, ```monospace```
    - Sem HTML, sem inline keyboard
    - Mensagens longas: split em chunks de 4096 chars

### Fase 3: Integracao Core ↔ Bridge
- [ ] Wire up: main.py bootstrap completo
- [ ] Fluxo end-to-end: mensagem texto → Claude → resposta
- [ ] Fluxo media: foto → Claude (vision) → resposta
- [ ] Fluxo documento: arquivo → Claude → resposta
- [ ] Fluxo audio: audio → transcricao → Claude → resposta
- [ ] Session auto-resume por numero de telefone
- [ ] Persistencia em SQLite
- [ ] Testes de integracao

### Fase 4: Features Avancadas
- [ ] Stop/interrupt de requests em andamento
- [ ] Verbose levels (progress updates)
- [ ] Multi-repo support (/repo command)
- [ ] Webhook/event bus integration
- [ ] Scheduler (cron jobs)
- [ ] MCP server support
- [ ] Notificacoes proativas

### Fase 5: Producao
- [ ] Dockerfile otimizado (multi-stage)
- [ ] Docker Compose producao
- [ ] Systemd service files
- [ ] Documentacao (.env, setup, troubleshooting)
- [ ] Rate limiting por numero
- [ ] Monitoramento (health checks)
- [ ] Backup de sessao WhatsApp

---

## Diferencas Chave: Telegram vs WhatsApp

| Aspecto | Telegram | WhatsApp |
|---------|----------|----------|
| Auth | User ID numerico | Numero telefone (JID) |
| Formatacao | HTML + Markdown | WhatsApp markdown limitado |
| Inline keyboards | Sim | Nao (usar texto/lista) |
| Editar mensagem | Sim | Nao nativamente |
| Typing indicator | `sendChatAction` | Via bridge API |
| File size limit | 50MB (bot) | 64MB (media) |
| Bot API | Oficial, estavel | Engenharia reversa (risco ban) |
| Grupos/Topics | Supergroups + topics | Grupos + Communities |
| Media groups | Album (2-10 fotos) | Envio individual |
| Comandos | /command nativo | Prefixo manual |
| Progress updates | Editar msg existente | Enviar nova msg (ou "typing...") |

## Riscos

1. **Ban do WhatsApp** — usar numero dedicado, nao spam, delays entre msgs
2. **Quebra de protocolo** — whatsmeow depende de engenharia reversa, pode quebrar
3. **Rate limits WhatsApp** — nao documentados, ser conservador
4. **Sessao expira** — reconexao automatica obrigatoria

## Config (.env)

```bash
# WhatsApp Bridge
WHATSAPP_BRIDGE_URL=http://localhost:8080
WHATSAPP_BRIDGE_WS_URL=ws://localhost:8080/ws
WHATSAPP_PHONE_NUMBER=5511999999999

# Auth
ALLOWED_PHONES=5511999999999,5511888888888

# Claude (mesmo do telegram)
ANTHROPIC_API_KEY=sk-ant-...
APPROVED_DIRECTORY=/home/user/projects
CLAUDE_MAX_TURNS=25
CLAUDE_TIMEOUT_SECONDS=300

# Features
AGENTIC_MODE=true
VERBOSE_LEVEL=1
ENABLE_MCP=false

# Storage
DATABASE_URL=sqlite:///data/whatsapp_bot.db
```

## Estimativa de Reuso

| Modulo | Reuso | Observacao |
|--------|-------|------------|
| claude/ | 100% | Zero mudanca |
| storage/ | 95% | Adicionar campo phone_number |
| security/ | 80% | Auth por telefone em vez de user_id |
| config/ | 70% | Novas vars WhatsApp, remover vars Telegram |
| events/ | 100% | Zero mudanca |
| scheduler/ | 100% | Zero mudanca |
| bot/ → whatsapp/ | 30% | Logica similar, API diferente |
| main.py | 60% | Adaptar bootstrap |
