<p align="center">
  <img src="docs/assets/klaw-logo.png" alt="klaw" width="140" />
</p>

<h1 align="center">klaw</h1>

<p align="center">
  <strong>kubectl for AI Agents</strong>
</p>

<p align="center">
  Enterprise AI agent orchestration. Manage, monitor, and scale your AI workforce.<br/>
  One binary. Deploys in seconds. Scales to hundreds of agents.
</p>

<p align="center">
  <a href="https://klaw.sh/docs">Documentation</a> •
  <a href="#what-is-klaw">What is klaw?</a> •
  <a href="#quick-start">Quick Start</a> •
  <a href="#slack-control">Slack Control</a> •
  <a href="#architecture">Architecture</a>
</p>

<p align="center">
  <img src="https://img.shields.io/badge/license-each::labs-green.svg" alt="License" />
  <img src="https://img.shields.io/badge/go-1.24+-00ADD8.svg" alt="Go Version" />
  <img src="https://img.shields.io/github/stars/klawsh/klaw.sh?style=social" alt="Stars" />
  <a href="https://deepwiki.com/klawsh/klaw.sh"><img src="https://deepwiki.com/badge.svg" alt="Ask DeepWiki"></a>
</p>

---

## What is klaw?

**klaw** is enterprise AI agent orchestration — like kubectl, but for AI agents.

```bash
# See all your agents
$ klaw get agents

NAME              NAMESPACE    STATUS    MODEL              LAST RUN
lead-scorer       sales        running   claude-sonnet-4    2m ago
competitor-watch  research     idle      gpt-4o             1h ago
ticket-handler    support      running   claude-sonnet-4    30s ago
report-gen        analytics    idle      claude-sonnet-4    6h ago

# Check what an agent is doing
$ klaw describe agent lead-scorer

Name:         lead-scorer
Namespace:    sales
Status:       Running
Model:        claude-sonnet-4-20250514
Skills:       crm, web-search
Tools:        hubspot, clearbit, web_fetch
Last Run:     2 minutes ago
Next Run:     in 58 minutes (cron: 0 * * * *)

# View real-time logs
$ klaw logs lead-scorer --follow

[14:32:01] Fetching new leads from HubSpot...
[14:32:03] Found 12 new leads
[14:32:05] Analyzing lead: john@acme.com
[14:32:08] Score: 85/100 (Enterprise, good fit)
[14:32:09] Updated HubSpot lead score
...
```

### Or control everything from Slack

```
You: @klaw status

klaw: 📊 Agent Status
      ├── lead-scorer (sales) — running, 2m ago
      ├── competitor-watch (research) — idle
      ├── ticket-handler (support) — running, 30s ago
      └── report-gen (analytics) — idle

You: @klaw run competitor-watch

klaw: 🚀 Starting competitor-watch...
      Checking competitor.com/pricing...
      Found 2 pricing changes since yesterday.
      Posted summary to #competitive-intel
```

---

## The Problem

You're running AI agents in production:
- **Lead Scorer** — analyzes CRM leads every hour
- **Competitor Watch** — monitors competitor websites daily
- **Ticket Handler** — auto-responds to support tickets
- **Report Generator** — creates weekly analytics reports

But managing them is chaos:

| Challenge | Current State | With klaw |
|-----------|---------------|-----------|
| **Visibility** | "Is the agent running? What's it doing?" | `klaw get agents`, `klaw logs` |
| **Isolation** | Sales agent accessing support secrets | Namespaces with scoped permissions |
| **Scheduling** | Messy cron jobs, Lambda functions | `klaw cron create` — built-in |
| **Scaling** | Manual server provisioning | `klaw node join` — auto-dispatch |
| **Debugging** | grep through CloudWatch | `klaw logs agent --follow` |
| **Deployment** | Complex setup, many dependencies | Single binary, one command |

**OpenClaw works, but deployment is painful and scaling is worse.**

klaw brings Kubernetes-style operations to AI agents. One binary. Deploys in seconds.

---

## Quick Start

### 1. Install

```bash
curl -fsSL https://klaw.sh/install.sh | sh
```

### 2. Configure

```bash
# Pick one provider
export ANTHROPIC_API_KEY=sk-ant-...      # Direct Anthropic
export OPENROUTER_API_KEY=sk-or-...      # OpenRouter (100+ models)
export EACHLABS_API_KEY=...              # each::labs (300+ models)
```

### 3. Run

```bash
# Interactive chat
klaw chat

# Or start the full platform (Slack + scheduler + agents)
export SLACK_BOT_TOKEN=xoxb-...
export SLACK_APP_TOKEN=xapp-...
klaw start
```

---

## Real-World Examples

### Sales: Lead Scoring

```bash
# Create the agent
klaw create agent lead-scorer \
  --namespace sales \
  --model claude-sonnet-4-20250514 \
  --skills crm,web-search

# Schedule hourly runs
klaw cron create score-leads \
  --schedule "0 * * * *" \
  --agent lead-scorer \
  --task "Analyze new leads in HubSpot, score 1-100 based on fit, update Lead Score field"

# Check status anytime
klaw get agents -n sales
klaw logs lead-scorer
```

### Research: Competitor Intelligence

```bash
klaw create agent competitor-watch \
  --namespace research \
  --model gpt-4o \
  --skills web-search,web-fetch,slack

klaw cron create competitor-daily \
  --schedule "0 9 * * *" \
  --agent competitor-watch \
  --task "Check competitor.com/pricing for changes. Post diff to #competitive-intel"
```

### Support: Ticket Automation

```bash
klaw create agent ticket-handler \
  --namespace support \
  --model claude-sonnet-4-20250514 \
  --skills zendesk,slack

# This agent responds to Slack mentions automatically
# @klaw check ticket #12345
# @klaw draft response for angry customer
```

### Analytics: Automated Reports

```bash
klaw create agent report-gen \
  --namespace analytics \
  --model claude-sonnet-4-20250514 \
  --skills sql,slack,charts

klaw cron create weekly-report \
  --schedule "0 8 * * MON" \
  --agent report-gen \
  --task "Query last week's metrics, generate summary with charts, post to #team-updates"
```

---

## Slack Control

klaw turns Slack into your AI command center:

```
# Agent management
@klaw status                          # List all agents
@klaw describe lead-scorer            # Agent details
@klaw logs ticket-handler             # Recent logs

# Run tasks
@klaw run lead-scorer                 # Trigger immediately
@klaw ask lead-scorer "score this: john@bigco.com"

# Scheduling
@klaw cron list                       # View scheduled jobs
@klaw cron disable daily-report       # Pause a job

# Quick queries
@klaw "summarize today's support tickets"
@klaw "what did competitor-watch find yesterday?"
```

---

## Architecture

```
┌────────────────────────────────────────────────────────────────────────┐
│                           KLAW CONTROL PLANE                            │
│                                                                          │
│  ┌────────────────────────────────────────────────────────────────┐    │
│  │                      NAMESPACES (Isolation)                     │    │
│  │                                                                  │    │
│  │  ┌───────────────┐  ┌───────────────┐  ┌───────────────┐       │    │
│  │  │    SALES      │  │   RESEARCH    │  │    SUPPORT    │       │    │
│  │  │               │  │               │  │               │       │    │
│  │  │ lead-scorer   │  │ competitor-   │  │ ticket-       │       │    │
│  │  │               │  │ watch         │  │ handler       │       │    │
│  │  │ ┌───────────┐ │  │ ┌───────────┐ │  │ ┌───────────┐ │       │    │
│  │  │ │ tools:    │ │  │ │ tools:    │ │  │ │ tools:    │ │       │    │
│  │  │ │ • hubspot │ │  │ │ • web     │ │  │ │ • zendesk │ │       │    │
│  │  │ │ • clearbit│ │  │ │ • slack   │ │  │ │ • slack   │ │       │    │
│  │  │ └───────────┘ │  │ └───────────┘ │  │ └───────────┘ │       │    │
│  │  │               │  │               │  │               │       │    │
│  │  │ secrets:      │  │ secrets:      │  │ secrets:      │       │    │
│  │  │ HUBSPOT_KEY   │  │ SLACK_TOKEN   │  │ ZENDESK_KEY   │       │    │
│  │  └───────────────┘  └───────────────┘  └───────────────┘       │    │
│  └────────────────────────────────────────────────────────────────┘    │
│                                                                          │
│  ┌─────────────┐  ┌─────────────┐  ┌─────────────┐  ┌─────────────┐    │
│  │  SCHEDULER  │  │  CHANNELS   │  │   ROUTER    │  │    NODES    │    │
│  │  (cron)     │  │(Slack, CLI) │  │ (300+ LLMs) │  │  (workers)  │    │
│  └─────────────┘  └─────────────┘  └─────────────┘  └─────────────┘    │
└────────────────────────────────────────────────────────────────────────┘
```

### The Kubernetes Parallel

| You Want To... | kubectl | klaw |
|----------------|---------|------|
| List workloads | `kubectl get pods` | `klaw get agents` |
| Inspect | `kubectl describe pod` | `klaw describe agent` |
| View logs | `kubectl logs` | `klaw logs` |
| Deploy | `kubectl apply -f` | `klaw apply -f` |
| Isolate | Namespaces | Namespaces |
| Schedule | CronJob | `klaw cron` |
| Scale out | Add nodes | `klaw node join` |

### Deployment Modes

**Single Node** — Development & small teams
```bash
klaw chat              # Interactive
klaw start             # Full platform
```

**Distributed** — Production & enterprise
```bash
# Controller (central brain)
klaw controller start --port 9090

# Worker nodes join the cluster
klaw node join controller.internal:9090 --token $TOKEN

# Tasks auto-dispatch to available nodes
klaw dispatch "analyze all Q4 leads" --agent lead-scorer
```

---

## CLI Reference

```bash
# Core
klaw chat                        # Interactive terminal chat
klaw start                       # Start platform (Slack + scheduler)
klaw dispatch "task"             # One-off task execution

# Agent Management (kubectl-style)
klaw get agents                  # List all agents
klaw get agents -n sales         # List in namespace
klaw describe agent <name>       # Detailed info
klaw create agent <name>         # Create new agent
klaw delete agent <name>         # Remove agent
klaw logs <agent>                # View logs
klaw logs <agent> -f             # Follow logs

# Namespaces
klaw get namespaces              # List namespaces
klaw create namespace <name>     # Create namespace
klaw config use-context <ns>     # Switch namespace

# Scheduling
klaw cron create <name>          # Create scheduled job
klaw cron list                   # List all jobs
klaw cron describe <name>        # Job details
klaw cron enable <name>          # Enable job
klaw cron disable <name>         # Disable job
klaw cron delete <name>          # Remove job

# Cluster (Distributed Mode)
klaw controller start            # Start controller
klaw node join <addr>            # Join cluster
klaw get nodes                   # List nodes
klaw drain node <name>           # Drain node

# Configuration
klaw config view                 # Show config
klaw config set <key> <value>    # Update config
```

---

## Configuration

### Minimal (`~/.klaw/config.toml`)

```toml
[provider.anthropic]
api_key = "${ANTHROPIC_API_KEY}"
```

### Full Example

```toml
[defaults]
model = "claude-sonnet-4-20250514"
namespace = "default"

[provider.anthropic]
api_key = "${ANTHROPIC_API_KEY}"

[provider.eachlabs]
api_key = "${EACHLABS_API_KEY}"

[channel.slack]
enabled = true
bot_token = "${SLACK_BOT_TOKEN}"
app_token = "${SLACK_APP_TOKEN}"

[channel.cli]
enabled = true

[namespace.sales]
secrets = ["HUBSPOT_KEY", "CLEARBIT_KEY"]
allowed_tools = ["hubspot", "clearbit", "web_search"]

[namespace.support]
secrets = ["ZENDESK_KEY"]
allowed_tools = ["zendesk", "slack"]

[scheduler]
enabled = true
timezone = "America/New_York"
```

---

## FAQ

### "How is this different from OpenClaw?"

OpenClaw is powerful but complex — Node.js, multiple services, difficult deployment, hard to scale. klaw is a single Go binary with the same agent capabilities but kubectl-style operations. Deploy in seconds, scale by adding nodes.

### "How is this different from LangChain/CrewAI?"

Those are frameworks for **building** agents. klaw is infrastructure for **operating** them. You could build agents with LangChain and deploy them on klaw.

### "Is this about sandboxing agents?"

**Partial.** Namespaces provide *logical* isolation:
- **Scoped secrets** — sales can't access support's API keys
- **Tool permissions** — agents only get the tools you allow
- **Resource isolation** — each namespace is independent

**Important:** Non-containerized agents have **no filesystem sandboxing** — they run under your user account and can access any file you can. For true process/filesystem isolation, run agents in Podman containers with `klaw run`.

### "Can I run this on-prem?"

Yes. Single binary, no external dependencies. Run on your servers, your VPC, air-gapped environments. You control everything.

### "What models are supported?"

300+ models via each::labs router, or direct connections to Anthropic, OpenAI, Google, Azure, Ollama, and any OpenAI-compatible endpoint.

---

## Comparison

| Feature | klaw | OpenClaw | LangChain | AutoGPT |
|---------|------|----------|-----------|---------|
| Single Binary | ✅ | ❌ | ❌ | ❌ |
| kubectl-style CLI | ✅ | ❌ | ❌ | ❌ |
| Slack Control | ✅ | ✅ | Manual | ❌ |
| Namespaces | ✅ | ❌ | ❌ | ❌ |
| Built-in Cron | ✅ | ❌ | ❌ | ❌ |
| Distributed Mode | ✅ | ❌ | ❌ | ❌ |
| 300+ Models | ✅ | ✅ | ✅ | ❌ |
| Easy Deployment | ✅ | ❌ | ❌ | ❌ |
| Enterprise Ready | ✅ | Partial | Partial | ❌ |

---

## Installation

### Quick Install

```bash
curl -fsSL https://klaw.sh/install.sh | sh
```

### From Source

```bash
git clone https://github.com/klawsh/klaw.sh.git
cd klaw && make build
sudo mv bin/klaw /usr/local/bin/
```

### Verify

```bash
klaw version
# klaw v1.0.0 (darwin/arm64)
```

---

## Contributing

```bash
git clone https://github.com/klawsh/klaw.sh.git
cd klaw
make build && make test
./bin/klaw chat
```

See [CONTRIBUTING.md](CONTRIBUTING.md) for guidelines.

---

## Community

- [Documentation](https://klaw.sh/docs) — Full docs and guides
- [GitHub Issues](https://github.com/klawsh/klaw.sh/issues) — Bug reports & feature requests
- [Discord](https://discord.gg/eachlabs) — Chat with the community
- [Twitter](https://twitter.com/eaborai) — Updates & announcements

---

## License

klaw is source-available under the [each::labs License](LICENSE).

**Free** for internal business use, personal projects, and consulting.
**License required** for multi-tenant SaaS or white-label distribution.

See [LICENSE](LICENSE) for details. Enterprise licensing: enterprise@eachlabs.ai

---

<p align="center">
  <strong>klaw</strong> — kubectl for AI Agents<br/>
  Built by <a href="https://eachlabs.ai">each::labs</a>
</p>
