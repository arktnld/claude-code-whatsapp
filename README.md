# Claude Code WhatsApp

Bot que conecta WhatsApp ao [Claude Code](https://claude.ai/code). Manda mensagem pelo WhatsApp e o Claude le, edita e roda codigo nos seus projetos.

## Como funciona

Dois processos rodando juntos:

```
Seu WhatsApp  <-->  Bridge (Go)  <-->  Core (Python)  <-->  Claude Code
                    whatsmeow           claude-agent-sdk
                    porta 8080          sessions/storage
```

- **Bridge** — conecta no WhatsApp usando a lib [whatsmeow](https://github.com/tulir/whatsmeow). Recebe e envia mensagens via HTTP/WebSocket.
- **Core** — recebe as mensagens da bridge, manda pro Claude Code SDK, e devolve a resposta.

## Requisitos

- **Go 1.22+** — pra compilar a bridge
- **Python 3.11+** com Poetry
- **Claude Code CLI** — `npm install -g @anthropic-ai/claude-code` (precisa estar logado com `claude login`)
- **Um numero WhatsApp dedicado** — nao use seu numero pessoal (risco de ban)

## Setup

### 1. Clone e configure

```bash
git clone https://github.com/arktnld/claude-code-whatsapp.git
cd claude-code-whatsapp

cp env.example .env
```

Edite `.env`:

```bash
# Numero(s) que podem usar o bot (separado por virgula)
ALLOWED_PHONES=5511999999999

# Pasta raiz dos seus projetos — o Claude so acessa dentro dela
APPROVED_DIRECTORY=/home/user/projects
```

### 2. Instale as dependencias

```bash
# Python
make dev

# Go (compila a bridge)
make build-bridge
```

### 3. Rode a bridge (primeiro terminal)

```bash
make run-bridge
```

Na primeira vez, aparece um QR code no terminal. Abra o WhatsApp no celular do numero dedicado:
- **Configuracoes > Dispositivos conectados > Conectar dispositivo**
- Escaneie o QR code

Depois de conectar, a bridge salva a sessao em `bridge/whatsapp.db`. Nas proximas vezes conecta automatico.

### 4. Rode o core (segundo terminal)

```bash
make run-core
```

Pronto. Manda mensagem pro numero conectado a partir de um numero que esta em `ALLOWED_PHONES`.

## Uso

Manda qualquer mensagem de texto e o Claude responde direto:

```
Voce: olha o arquivo main.go e me diz o que ele faz
Bot:  Working...
Bot:  O main.go implementa uma bridge WhatsApp com 3 partes...

Voce: adiciona um endpoint /metrics que retorna uptime
Bot:  Working...
Bot:  Adicionei o endpoint. Veja as mudancas: ...

Voce: roda go build e ve se compila
Bot:  Working...
Bot:  Compilou sem erros.
```

Tambem aceita **fotos** (Claude Vision), **documentos** (le o conteudo) e responde sobre eles.

### Comandos

Manda como mensagem normal (WhatsApp nao tem sistema de comandos nativo):

| Comando | O que faz |
|---------|-----------|
| `/start` | Mostra mensagem de boas-vindas e diretorio atual |
| `/new` | Reseta sessao — Claude esquece contexto anterior |
| `/status` | Mostra diretorio e se tem sessao ativa |
| `/repo` | Lista projetos na pasta aprovada |
| `/repo nome` | Troca pra outro projeto |
| `/stop` | Interrompe requisicao em andamento |

### Exemplo real de troca de projeto

```
Voce: /repo
Bot:  *Repos*
      [git] api-backend/
      [git] frontend/
      [dir] scripts/
      Use: /repo <name>

Voce: /repo api-backend
Bot:  Switched to api-backend/ (git)

Voce: tem algum teste falhando?
Bot:  Working...
Bot:  Rodei pytest. 2 testes falhando em test_auth.py...
```

## Configuracao completa

Veja `env.example` pra todas as variaveis. As mais importantes:

| Variavel | Descricao | Default |
|----------|-----------|---------|
| `ALLOWED_PHONES` | Numeros autorizados (virgula) | obrigatorio |
| `APPROVED_DIRECTORY` | Pasta dos projetos | obrigatorio |
| `WHATSAPP_BRIDGE_URL` | URL da bridge | `http://localhost:8080` |
| `CLAUDE_MAX_TURNS` | Max turnos por conversa | `25` |
| `CLAUDE_TIMEOUT_SECONDS` | Timeout por request | `300` |
| `DEBUG` | Log detalhado | `false` |

## Avisos importantes

- **Risco de ban**: WhatsApp nao tem API oficial de bot. A lib whatsmeow faz engenharia reversa do protocolo. Use numero dedicado, nao mande spam, coloque delays entre mensagens.
- **Sessao pode expirar**: se o WhatsApp desconectar, a bridge reconecta automatico. Se nao funcionar, delete `bridge/whatsapp.db` e escaneie QR de novo.
- **Seguranca**: so numeros em `ALLOWED_PHONES` podem usar. O Claude so acessa arquivos dentro de `APPROVED_DIRECTORY`.

## Credits

- [claude-code-telegram](https://github.com/RichardAtCT/claude-code-telegram) — projeto base (MIT License)
- [whatsmeow](https://github.com/tulir/whatsmeow) — lib WhatsApp Web em Go

## License

MIT
