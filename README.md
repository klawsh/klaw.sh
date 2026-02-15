<p align="center">
  <img src="docs/assets/klaw-logo.png" alt="klaw" width="120" />
</p>

<h1 align="center">klaw</h1>

<p align="center">
  <strong>The Kubernetes for AI Agents</strong>
</p>

<p align="center">
  Deploy, orchestrate, and scale AI agents across your infrastructure.<br/>
  From a single CLI to enterprise-grade distributed clusters.
</p>

<p align="center">
  <a href="https://klaw.sh">Website</a> •
  <a href="#quick-start">Quick Start</a> •
  <a href="#why-klaw">Why klaw</a> •
  <a href="#features">Features</a> •
  <a href="#architecture">Architecture</a>
</p>

<p align="center">
  <img src="https://img.shields.io/badge/license-each::labs-green.svg" alt="License" />
  <img src="https://img.shields.io/badge/go-%3E%3D1.22-00ADD8.svg" alt="Go Version" />
  <img src="https://img.shields.io/github/stars/eachlabs/klaw?style=social" alt="Stars" />
</p>

---

## What is klaw?

**klaw** is an open-source platform for deploying and managing AI agents at scale. Think of it as Kubernetes, but instead of containers, you're orchestrating intelligent agents that can code, research, communicate, and automate tasks.

```bash
# Create an agent
klaw create agent researcher \
  --model claude-sonnet-4 \
  --skills web-search,browser \
  --trigger "research,analyze"

# Chat with it
klaw chat

# Or deploy to Slack
klaw slack
```

## Quick Start

### 1. Install

```bash
curl -fsSL https://klaw.sh/install.sh | sh
```

Or build from source:

```bash
git clone https://github.com/eachlabs/klaw.git
cd klaw && make build
sudo mv bin/klaw /usr/local/bin/
```

### 2. Configure

```bash
# each::labs LLM Router (300+ models, single API key)
export EACHLABS_API_KEY=your-key

# Or direct provider
export ANTHROPIC_API_KEY=sk-ant-...
```

### 3. Run

```bash
klaw chat
```

That's it. You're running AI agents.

---

## Why klaw?

### The Problem

AI agents are powerful, but deploying them is a mess:
- **Claude Code** is great but single-user and closed-source
- **LangChain/CrewAI** require complex Python setups and don't scale
- **Custom solutions** mean reinventing auth, tools, and deployment

### The Solution

klaw brings Kubernetes-style orchestration to AI agents:

| Kubernetes | klaw | Purpose |
|------------|------|---------|
| Pod | Agent | Unit of deployment |
| Deployment | AgentBinding | Desired state specification |
| Service | Channel | How agents receive work |
| Node | Node | Where agents run |
| Namespace | Namespace | Isolation boundary |
| kubectl | klaw | CLI interface |

**One binary. No dependencies. Runs anywhere.**

---

## Features

### 300+ Models via each::labs Router

Use any LLM through a single API:
- Claude (Anthropic)
- GPT-4 (OpenAI)
- Gemini (Google)
- Llama (Meta)
- DeepSeek, Mistral, and more

### Multi-Channel Support

```bash
klaw slack    # Slack bot
klaw chat     # CLI chat
klaw api      # REST API
```

### Built-in Tools

| Tool | Description |
|------|-------------|
| `bash` | Execute shell commands |
| `read` / `write` / `edit` | File operations |
| `glob` / `grep` | File search |
| `web_fetch` / `web_search` | Web operations |

### Skills System

```bash
klaw skill install browser
klaw create agent researcher --skills web-search,browser
```

### Enterprise Ready

- **RBAC**: Control who can do what
- **Audit Logs**: Track every agent action
- **Namespaces**: Isolate teams and projects
- **Secrets Management**: Secure credential storage
- **Observability**: Prometheus metrics, structured logging

---

## Architecture

```
                                    ┌─────────────────────────────────────┐
                                    │           CONTROLLER                │
                                    │  ┌─────────────────────────────┐   │
      ┌──────────────┐              │  │    Agent Registry           │   │
      │    Slack     │◄────────────►│  │    Task Dispatcher          │   │
      └──────────────┘              │  │    State Manager            │   │
                                    │  └─────────────────────────────┘   │
      ┌──────────────┐              │              │                     │
      │     CLI      │◄────────────►│              │ gRPC                │
      └──────────────┘              │              ▼                     │
                                    └──────────────┬──────────────────────┘
      ┌──────────────┐                             │
      │     API      │◄────────────────────────────┤
      └──────────────┘                             │
                          ┌────────────────────────┼────────────────────────┐
                          │                        │                        │
                          ▼                        ▼                        ▼
                   ┌─────────────┐          ┌─────────────┐          ┌─────────────┐
                   │   NODE 1    │          │   NODE 2    │          │   NODE 3    │
                   │ ┌─────────┐ │          │ ┌─────────┐ │          │ ┌─────────┐ │
                   │ │ Agent A │ │          │ │ Agent C │ │          │ │ Agent E │ │
                   │ └─────────┘ │          │ └─────────┘ │          │ └─────────┘ │
                   └─────────────┘          └─────────────┘          └─────────────┘
```

### Local Mode

```bash
klaw chat                    # Interactive chat
klaw run "fix the bug"       # One-shot task
```

### Distributed Mode

```bash
klaw controller start --port 9090
klaw node join controller:9090 --token <token>
klaw create agent coder --replicas 3
```

---

## Commands

```bash
# Agent management
klaw create agent <name> --model <model> --skills <skills>
klaw get agents
klaw describe agent <name>
klaw delete agent <name>

# Task dispatch
klaw dispatch "your task here"
klaw dispatch "task" --agent coder

# Cluster operations
klaw controller start
klaw node join <addr> --token <token>
klaw cluster status

# Context management
klaw context list
klaw context use production
```

---

## Configuration

`~/.klaw/config.toml`:

```toml
[defaults]
model = "claude-sonnet-4-20250514"
namespace = "default"

[provider.eachlabs]
api_key = "${EACHLABS_API_KEY}"

[provider.anthropic]
api_key = "${ANTHROPIC_API_KEY}"

[slack]
bot_token = "${SLACK_BOT_TOKEN}"
app_token = "${SLACK_APP_TOKEN}"
```

---

## Comparison

| Feature | klaw | Claude Code | LangChain | AutoGPT |
|---------|------|-------------|-----------|---------|
| Open Source | Yes | No | Yes | Yes |
| Single Binary | Yes | No | No | No |
| Distributed | Yes | No | No | No |
| Multi-Agent | Yes | No | Yes | Yes |
| Slack/Teams | Yes | No | Manual | No |
| 300+ Models | Yes | No | Yes | No |
| Production Ready | Yes | Yes | Partial | No |

---

## Contributing

We welcome contributions! See [CONTRIBUTING.md](CONTRIBUTING.md) for guidelines.

```bash
git clone https://github.com/eachlabs/klaw.git
cd klaw
make build
make test
./bin/klaw chat
```

---

## Community

- [GitHub Issues](https://github.com/eachlabs/klaw/issues) - Bug reports and feature requests
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
