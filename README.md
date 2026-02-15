<p align="center">
  <img src="docs/assets/klaw-logo.png" alt="klaw" width="120" />
</p>

<h1 align="center">klaw</h1>

<p align="center">
  <strong>The Kubernetes for AI Agents</strong>
</p>

<p align="center">
  Deploy, orchestrate, and scale AI agents across your infrastructure.<br/>
  One binary. No dependencies. From laptop to enterprise cluster.
</p>

<p align="center">
  <a href="https://klaw.sh">Website</a> •
  <a href="#quick-start">Quick Start</a> •
  <a href="#deployment-modes">Deployment Modes</a> •
  <a href="#features">Features</a> •
  <a href="#architecture">Architecture</a>
</p>

<p align="center">
  <img src="https://img.shields.io/badge/license-each::labs-green.svg" alt="License" />
  <img src="https://img.shields.io/badge/go-%3E%3D1.24-00ADD8.svg" alt="Go Version" />
  <img src="https://img.shields.io/github/stars/klawsh/klaw.sh?style=social" alt="Stars" />
</p>

---

## What is klaw?

**klaw** is an open-source platform for deploying and managing AI agents at scale. Think of it as Kubernetes, but instead of containers, you're orchestrating intelligent agents that can code, research, communicate, and automate tasks.

```bash
# Interactive chat
klaw chat

# Start full platform (Slack bot + scheduler + agents)
klaw start
```

---

## Quick Start

### 1. Install

```bash
# One-line install
curl -fsSL https://klaw.sh/install.sh | sh

# Or build from source
git clone https://github.com/klawsh/klaw.sh.git
cd klaw && make build
sudo mv bin/klaw /usr/local/bin/
```

### 2. Configure

```bash
# Option 1: each::labs Router (300+ models, single API key)
export EACHLABS_API_KEY=your-key

# Option 2: OpenRouter (multi-provider gateway)
export OPENROUTER_API_KEY=your-key

# Option 3: Direct Anthropic
export ANTHROPIC_API_KEY=sk-ant-...
```

### 3. Run

```bash
# Interactive CLI chat
klaw chat

# Or start full platform with Slack integration
export SLACK_BOT_TOKEN=xoxb-...
export SLACK_APP_TOKEN=xapp-...
klaw start
```

---

## Deployment Modes

klaw supports multiple deployment modes to fit your needs:

### Single-Node Mode

Run everything on a single machine—perfect for development and small teams:

```bash
# Interactive chat
klaw chat

# Full platform (Slack + scheduler)
klaw start

# Run agent in container
klaw run coder --task "Fix the bug"
```

### Distributed Mode

Scale across multiple machines with controller-node architecture:

```bash
# Start the controller (central brain)
klaw controller start --port 9090

# Join worker nodes
klaw node join controller:9090 --token <token>

# Dispatch tasks across the cluster
klaw dispatch "analyze this codebase" --agent researcher
```

### Container Mode (Podman)

Run agents in isolated containers for security and reproducibility:

```bash
# Build the container image
klaw build

# Run agent in container
klaw run coder --task "Review this PR"
klaw run coder --detach  # Background

# Manage containers
klaw ps                  # List running
klaw logs coder          # View logs
klaw stop coder          # Stop container
```

---

## Features

### 300+ Models via LLM Router

Use any LLM through a single API with automatic provider selection:

```bash
klaw chat --model claude-sonnet-4-20250514
klaw chat --model gpt-4o
klaw chat --model gemini-pro
```

**Supported Providers:**
- **each::labs Router** - 300+ models through single API
- **OpenRouter** - Multi-provider gateway
- **Anthropic** - Direct Claude access

### Namespaces & Clusters

Organize agents with Kubernetes-style multi-tenancy:

```bash
# Create cluster
klaw create cluster production

# Create namespaces
klaw create namespace engineering --cluster production
klaw create namespace marketing --cluster production

# Switch context
klaw context use production/engineering

# Agents are scoped to namespaces
klaw create agent coder --namespace engineering
```

### Built-in Tools

Agents come with powerful capabilities out of the box:

| Tool | Description |
|------|-------------|
| `bash` | Execute shell commands with 2-minute timeout |
| `read` | Read file contents |
| `write` | Create and write files |
| `edit` | Edit files with precise string replacement |
| `glob` | Find files by pattern |
| `grep` | Search file contents with regex |
| `web_fetch` | Fetch and process web content |
| `web_search` | Search the internet |
| `agent_spawn` | Create specialized sub-agents |
| `skill` | Install and manage agent skills |
| `cron` | Schedule recurring tasks |

### Skills System

Composable capability bundles for agents:

```bash
klaw skill list
klaw skill install browser
klaw create agent researcher --skills web-search,browser,code-exec
```

### Scheduled Tasks (Cron)

Automate recurring tasks:

```bash
# Create a cron job
klaw cron create daily-report \
  --schedule "0 9 * * *" \
  --agent reporter \
  --task "Generate daily status report"

# List jobs
klaw cron list

# Manage jobs
klaw cron enable daily-report
klaw cron disable daily-report
```

### Multi-Channel Support

Deploy agents to multiple surfaces:

```bash
klaw chat              # Interactive CLI
klaw chat --tui        # Rich TUI mode
klaw start             # Slack bot + scheduler
```

---

## Architecture

### Overview

```
┌─────────────────────────────────────────────────┐
│              CHANNELS                            │
│     (Slack, CLI, TUI, API, Telegram)            │
└──────────────────┬──────────────────────────────┘
                   │
         ┌─────────▼──────────┐
         │   ORCHESTRATOR     │
         │  (Message Routing) │
         └─────────┬──────────┘
                   │
         ┌─────────▼──────────┐
         │      AGENT         │
         │ (LLM + Tool Loop)  │
         └─────────┬──────────┘
                   │
    ┌──────┬───────┼───────┬──────┐
    │      │       │       │      │
    ▼      ▼       ▼       ▼      ▼
  BASH   READ   WRITE   GREP   WEB
```

### Distributed Architecture

```
┌─────────────────────────────────────┐
│           CONTROLLER                │
│  ┌─────────────────────────────┐   │
│  │    Agent Registry           │   │
│  │    Task Dispatcher          │   │
│  │    State Manager            │   │
│  └─────────────────────────────┘   │
│              │                     │
│              │ TCP/JSON            │
│              ▼                     │
└──────────────┬──────────────────────┘
               │
┌──────────────┼────────────────────────┐
│              │                        │
▼              ▼                        ▼
┌─────────────┐  ┌─────────────┐  ┌─────────────┐
│   NODE 1    │  │   NODE 2    │  │   NODE 3    │
│ ┌─────────┐ │  │ ┌─────────┐ │  │ ┌─────────┐ │
│ │ Agent A │ │  │ │ Agent C │ │  │ │ Agent E │ │
│ └─────────┘ │  │ └─────────┘ │  │ └─────────┘ │
└─────────────┘  └─────────────┘  └─────────────┘
```

### Why "Kubernetes for AI Agents"?

Just as Kubernetes revolutionized container orchestration, klaw brings the same paradigm to AI agents:

```
┌─────────────────────────────────────────────────────────────────────────┐
│                    KUBERNETES vs KLAW                                    │
├─────────────────────────────────────────────────────────────────────────┤
│                                                                          │
│   KUBERNETES (Containers)          KLAW (AI Agents)                     │
│   ════════════════════════         ═══════════════════                  │
│                                                                          │
│   Container Image    ────────►     Agent Definition                     │
│   Pod                ────────►     Agent Instance                       │
│   Deployment         ────────►     AgentBinding                         │
│   Service            ────────►     Channel (Slack, CLI, API)            │
│   Node               ────────►     Node (Worker Machine)                │
│   Namespace          ────────►     Namespace (Isolation)                │
│   ConfigMap          ────────►     SOUL.md / Config                     │
│   kubectl            ────────►     klaw CLI                             │
│   Scheduler          ────────►     Task Dispatcher                      │
│   CronJob            ────────►     klaw cron                            │
│                                                                          │
└─────────────────────────────────────────────────────────────────────────┘
```

**The Problem klaw Solves:**

| Challenge | Kubernetes Solution | klaw Solution |
|-----------|---------------------|---------------|
| "Where does my workload run?" | Schedules pods to nodes | Schedules agents to nodes |
| "How do I scale?" | HPA/VPA for containers | Spawn agents on demand |
| "How do I isolate?" | Namespaces | Namespaces + workspaces |
| "How do I update?" | Rolling deployments | Hot-reload agent configs |
| "How do I communicate?" | Services + Ingress | Channels (Slack, API) |
| "How do I schedule?" | CronJobs | klaw cron |

**Familiar Patterns:**

```bash
# Kubernetes                      # klaw
kubectl get pods                  klaw get agents
kubectl create deployment         klaw create agent
kubectl logs pod-name             klaw logs agent-name
kubectl exec -it pod -- bash      klaw attach agent-name
kubectl apply -f manifest.yaml    klaw apply -f agent.toml
kubectl config use-context        klaw context use
```

---

## Commands

### Core Commands

```bash
klaw chat                     # Interactive terminal chat
klaw chat --tui               # TUI mode with Bubble Tea
klaw start                    # Start platform (Slack + scheduler)
```

### Agent Management

```bash
klaw create agent <name> --model <model> --skills <skills>
klaw get agents
klaw describe agent <name>
klaw delete agent <name>
```

### Container Operations

```bash
klaw build                    # Build container image
klaw run <agent> --task "..." # Run in container
klaw run <agent> --detach     # Run in background
klaw ps                       # List containers
klaw logs <container>         # View logs
klaw stop <container>         # Stop container
klaw attach <container>       # Attach to container
```

### Cluster Operations

```bash
klaw controller start         # Start controller
klaw node join <addr>         # Join a controller
klaw dispatch "task"          # Dispatch task
klaw get nodes                # List nodes
klaw get tasks                # List tasks
```

### Namespace Management

```bash
klaw create cluster <name>
klaw create namespace <name> --cluster <cluster>
klaw get clusters
klaw get namespaces
klaw context use <cluster>/<namespace>
```

### Skill Management

```bash
klaw skill list
klaw skill install <name>
klaw skill uninstall <name>
klaw skill show <name>
```

### Cron Jobs

```bash
klaw cron create <name> --schedule "..." --agent <agent> --task "..."
klaw cron list
klaw cron enable <name>
klaw cron disable <name>
klaw cron delete <name>
```

---

## Configuration

### Config File

`~/.klaw/config.toml`:

```toml
[defaults]
model = "claude-sonnet-4-20250514"
agent = "default"

[workspace]
path = "~/.klaw/workspace"

[provider.eachlabs]
api_key = "${EACHLABS_API_KEY}"

[provider.anthropic]
api_key = "${ANTHROPIC_API_KEY}"
model = "claude-sonnet-4-20250514"

[provider.openrouter]
api_key = "${OPENROUTER_API_KEY}"

[channel.slack]
enabled = true
bot_token = "${SLACK_BOT_TOKEN}"
app_token = "${SLACK_APP_TOKEN}"

[server]
port = 8080
host = "127.0.0.1"

[logging]
level = "info"
```

### Directory Structure

```
~/.klaw/
├── config.toml          # Main configuration
├── workspace/           # Agent workspace files
│   ├── SOUL.md
│   ├── AGENTS.md
│   └── TOOLS.md
├── agents/              # Agent definitions (TOML)
├── skills/              # Installed skills
├── sessions/            # Session history
└── logs/                # Log files
```

### Environment Variables

```bash
# Provider API Keys
ANTHROPIC_API_KEY       # Anthropic Claude
OPENROUTER_API_KEY      # OpenRouter gateway
EACHLABS_API_KEY        # each::labs router

# Channel Tokens
SLACK_BOT_TOKEN         # Slack bot
SLACK_APP_TOKEN         # Slack app

# Overrides
KLAW_MODEL              # Default model
KLAW_STATE_DIR          # State directory
```

---

## Comparison

| Feature | klaw | Claude Code | LangChain | AutoGPT |
|---------|------|-------------|-----------|---------|
| Open Source | Yes | No | Yes | Yes |
| Single Binary | Yes | No | No | No |
| No Dependencies | Yes | No | No | No |
| Distributed | Yes | No | No | No |
| Container Support | Yes (Podman) | No | No | No |
| Multi-Agent | Yes | No | Yes | Yes |
| Namespaces | Yes | No | No | No |
| Slack/Teams | Yes | No | Manual | No |
| 300+ Models | Yes | No | Yes | No |
| Cron Jobs | Yes | No | No | No |
| Production Ready | Yes | Yes | Partial | No |

---

## Development

### Building

```bash
git clone https://github.com/klawsh/klaw.sh.git
cd klaw

make build          # Build binary
make test           # Run tests
make fmt            # Format code
make lint           # Lint code
make cross          # Cross-compile (Darwin, Linux, Windows)
```

### Tech Stack

- **Language:** Go 1.24+ (no runtime dependencies)
- **CLI Framework:** Cobra
- **TUI:** Bubble Tea, Lipgloss, Bubbles
- **LLM SDKs:** anthropic-sdk-go, openai-go
- **Config:** TOML
- **Containers:** Podman
- **IPC:** gRPC, JSON over TCP

### Project Structure

```
klaw/
├── cmd/klaw/
│   ├── main.go              # Entry point
│   └── commands/            # CLI commands
├── internal/
│   ├── agent/               # Agent logic and definitions
│   ├── channel/             # Channel implementations
│   ├── config/              # Configuration management
│   ├── cluster/             # Namespaces and clusters
│   ├── controller/          # Distributed controller
│   ├── memory/              # Workspace memory system
│   ├── node/                # Worker node
│   ├── orchestrator/        # Message routing
│   ├── provider/            # LLM providers
│   ├── runtime/             # Podman runtime
│   ├── scheduler/           # Cron scheduling
│   ├── skill/               # Skills system
│   ├── tool/                # Tool implementations
│   └── tui/                 # Bubble Tea TUI
└── proto/                   # gRPC definitions
```

---

## Contributing

We welcome contributions! See [CONTRIBUTING.md](CONTRIBUTING.md) for guidelines.

```bash
git clone https://github.com/klawsh/klaw.sh.git
cd klaw
make build
make test
./bin/klaw chat
```

---

## Community

- [GitHub Issues](https://github.com/klawsh/klaw.sh/issues) - Bug reports and feature requests
- [Discord](https://discord.gg/eachlabs) - Chat with the community
- [Twitter](https://twitter.com/eaborai) - Updates and announcements

---

## License

klaw is source-available under the [each::labs License](LICENSE).

**Free to use** for:
- Internal business purposes
- Personal projects
- Building your own AI applications
- Consulting and professional services

**Requires license** for:
- Multi-tenant SaaS offerings
- White-label/OEM distribution

See [LICENSE](LICENSE) for details. For enterprise licensing: enterprise@eachlabs.ai

---

<p align="center">
  <strong>klaw</strong> - The Kubernetes for AI Agents<br/>
  Built by <a href="https://eachlabs.ai">each::labs</a>
</p>
