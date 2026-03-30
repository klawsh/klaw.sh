# Changelog

## 2026.03.03.1

### Added

- **Delegate Tool** (`internal/tool/delegate_tool.go`): `delegate` tool that spawns ephemeral sub-agents inline during a conversation. Supports task prompt, optional tool allowlist, custom system prompt, max tokens. Depth-limited nesting (max 3), 5-minute timeout, 30k char output truncation.
- **Parallel Tool Execution** (`internal/agent/agent.go`): When the LLM returns multiple tool calls, approved tools now execute concurrently via goroutines. Three-phase approach: sequential approval, parallel execution, ordered result collection.

### Changed

- **`RunOnce` (package-level)** (`internal/agent/agent.go`): Fixed assistant messages to include `ToolCalls` field (was missing, breaking provider context). Fixed tool results to use individual `ToolResult` messages instead of concatenated text. Added parallel tool execution and configurable `MaxIterations`.
- **Chat command** (`cmd/klaw/commands/chat.go`): Registers `delegate` tool wired to `agent.RunOnce` after provider and tools are created.

### Tests

- `internal/tool`: 13 new tests for delegate tool (param validation, depth limiting, successful delegation with mock, output truncation, tool allowlists, child delegate depth, error handling, custom system prompt/max tokens)

---

## 2026.03.03

### Added

- **Structured Errors** (`internal/agent/errors.go`): Machine-readable error codes (`max_iterations`, `provider_error`, `tool_execution`, `context_limit`, `budget_exceeded`) with Go error chain support
- **Max Iteration Limit**: Agent loop capped at configurable `MaxIterations` (default: 50) to prevent runaway loops
- **Tool Permission Filtering** (`internal/tool/tool.go`): `Registry.Filter()` returns a new registry with only allowed tools; `Registry.Names()` returns sorted tool list
- **Provider Resilience** (`internal/provider/resilient.go`): `ResilientProvider` wrapping primary provider with exponential backoff + jitter retry and fallback provider chain; configurable `MaxRetries`, `InitialBackoff`, `MaxBackoff`, `BackoffFactor`
- **Context Window Management** (`internal/agent/context.go`): `ContextManager` with token estimation (~4 chars/token), compaction threshold detection, and LLM-based conversation summarization to keep history within context limits
- **Cost Tracking** (`internal/agent/cost.go`): `CostTracker` with per-model pricing table (Claude Sonnet/Opus/Haiku, GPT-4o, Gemini, DeepSeek), session budget enforcement, and cost summary formatting
- **Structured Logging & Metrics** (`internal/observe/observe.go`): JSON structured logger via `log/slog`, `Metrics` with atomic counters for requests/tokens/tool calls/errors, per-session metric tracking
- **Reflection Loop** (`internal/agent/reflection.go`): Injects self-evaluation prompt after N tool calls (default: 3) to improve agent reasoning quality
- **Planning Phase** (`internal/agent/planner.go`): Prepends planning instruction to first user message, enabling the model to think before acting
- **Human-in-the-Loop Approval** (`internal/agent/approval.go`): Configurable tool allowlist requiring user confirmation before execution (e.g., `bash`, `write`)
- **Per-Agent Tool Config** (`internal/config/config.go`): `AgentInstanceConfig` with `tools`, `max_iterations`, `require_approval` fields; wired into orchestrator for per-agent tool filtering
- **Usage in Stream Events** (`internal/provider/provider.go`): `StreamEvent` now carries `Usage` data on stop events; Anthropic provider emits token counts from streaming responses

### Changed

- **Agent loop** (`internal/agent/agent.go`): Integrated context management, cost tracking, reflection, planning, approval, logging, and metrics into the main `handleMessage()` loop
- **Orchestrator** (`internal/orchestrator/orchestrator.go`): Per-agent tool filtering now active (was TODO)
- **Chat command** (`cmd/klaw/commands/chat.go`): Wired resilient provider wrapping, per-agent tool filtering, cost/approval config from CLI flags
- **Session** (`internal/session/session.go`): Added `TotalInputTokens`, `TotalOutputTokens`, `TotalCost` fields for cost persistence
- **Config** (`internal/config/config.go`): Added `MaxRetries`, `Fallback` to `ProviderConfig`; `MaxSessionCost` to `DefaultsConfig`; `Agents` map for per-agent configuration

### Removed

- Debug `fmt.Printf` statements from `internal/provider/openrouter.go`

### Tests

- **119 unit tests** across 7 packages, all passing
- `internal/agent`: 36 tests (errors, cost tracker, reflection, planner, approval, context manager, agent integration with mock providers)
- `internal/config`: 13 tests (defaults, TOML loading, env overrides, save/reload, path expansion)
- `internal/observe`: 10 tests (logger levels/JSON/filtering, metrics recording, concurrency safety)
- `internal/orchestrator`: 18 tests (routing modes, register/unregister, proxy channel, @all/@agent syntax)
- `internal/provider`: 14 tests (resilient retry/fallback/stream/context cancellation/backoff calculation)
- `internal/session`: 17 tests (CRUD, debouncing, cost field persistence, JSON serialization)
- `internal/tool`: 11 tests (registry filter/names/register/override/independence)
