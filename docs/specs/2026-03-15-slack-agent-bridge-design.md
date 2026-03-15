# slack-agent-bridge — Design Spec

**Date:** 2026-03-15
**Status:** Draft
**Repo:** github.com/eserete/slack-agent-bridge

## Problem

We have a working Slack-to-opencode bridge built specifically for the "Carolina" (chief-of-staff) agent. The code is tightly coupled to that agent — hardcoded agent name, directory paths, state file names, port, and user ID. We want to extract this into a generic, configurable bridge that anyone can use to connect any opencode agent to any Slack bot.

## Solution

A standalone Go binary that bridges a single Slack bot (via Socket Mode) to a single opencode agent (via `opencode serve` HTTP API). One instance per bot. All agent-specific configuration via environment variables.

## Architecture

```
Slack Bot (Socket Mode)
    ↕
slack-agent-bridge (Go binary)
    ↕
opencode serve (child process, HTTP + SSE)
    ↕
opencode agent (defined in opencode config)
```

The bridge:
1. Starts `opencode serve` as a child process
2. Connects to Slack via Socket Mode
3. For each DM: POSTs to `/session/{id}/message`, streams response via SSE `/event`
4. Updates Slack message in real-time with streaming text

## Configuration

All via environment variables (loaded from `.env` if present).

### Required

| Variable | Description | Example |
|---|---|---|
| `SLACK_APP_TOKEN` | Slack app-level token (xapp-) | `xapp-1-...` |
| `SLACK_BOT_TOKEN` | Slack bot token (xoxb-) | `xoxb-...` |
| `AGENT_NAME` | opencode agent name (matches agent file name) | `chief-of-staff` |
| `AGENT_DIR` | Working directory for opencode serve | `/Users/you/agents/my-agent` |

### Optional

| Variable | Default | Description |
|---|---|---|
| `ALLOWED_USER_ID` | _(empty = allow all)_ | Slack user ID to restrict access |
| `SERVER_PORT` | `14899` | Port for opencode serve |
| `MAX_MESSAGES_PER_SESSION` | `10` | Messages before session rotation |
| `MAX_SESSION_AGE_MINUTES` | `60` | Minutes before session rotation |
| `RUN_TIMEOUT_SECONDS` | `120` | Max seconds per message |
| `STATE_FILES` | _(empty)_ | Comma-separated paths relative to AGENT_DIR to inject as context |
| `DAILY_CACHE_FILES` | _(empty)_ | Comma-separated paths relative to AGENT_DIR; only injected if modified today |
| `DAILY_CACHE_HEADER` | `Daily cache — queried today, do NOT query again unless user asks` | Header text prepended to each daily cache file injection |
| `HEALTH_TIMEOUT_SECONDS` | `30` | Max seconds to wait for opencode serve health check |
| `EXTRA_PATH` | _(empty)_ | Additional PATH entries (colon-separated) prepended to child process PATH |

## File Structure

```
slack-agent-bridge/
├── main.go              # Entrypoint: load config, start server + Slack
├── config.go            # Config struct, load from env, validate
├── handler.go           # Slack message handler: busy guard, streaming UI
├── runner.go            # OpenCodeServer: process lifecycle, SSE, sessions
├── markdown.go          # markdownToSlack, splitMessage, toolToProgressText
├── go.mod / go.sum
├── .env.example         # Configuration template
├── .gitignore
├── README.md            # Setup guide, usage, examples
├── LICENSE              # MIT
├── Makefile             # build, install, uninstall (launchd)
└── examples/
    ├── agent.example.md       # Example opencode agent prompt
    ├── launchd.plist.tmpl     # macOS launchd template
    └── opencode.example.jsonc # Minimal opencode config
```

## Key Design Decisions

### 1. State injection is opt-in via env vars

The current code hardcodes `tasks.yaml`, `projects.yaml`, `jira-snapshot.md`. The generic bridge uses two env vars:

- `STATE_FILES=state/tasks.yaml,state/projects.yaml` — always injected (if files exist)
- `DAILY_CACHE_FILES=state/jira-snapshot.md` — only injected if file was modified today

Both are optional. If unset, no state injection happens.

Injected format:
```
[Injected state — do NOT read these files from disk]

<filename>:
<contents>

[<DAILY_CACHE_HEADER>]
<filename>:
<contents>

[User message]
<actual message>
```

The daily cache header is configurable via `DAILY_CACHE_HEADER` env var. This allows agent-specific instructions like "do NOT use jira tools, use only this data" without hardcoding tool-specific behavior into the bridge. If unset, uses a generic default.

### 2. One instance per bot

No multi-agent routing within a single bridge. Each Slack bot = one bridge instance = one opencode agent. Simple, predictable, no shared state. To run multiple agents, run multiple instances on different ports.

### 3. Session warm-up on startup

After creating a session, the bridge sends a minimal warm-up message (`.`) to prime the LLM's prompt cache. This happens in a background goroutine and doesn't block startup. First real user message benefits from warm cache.

### 4. Crash recovery and SSE reconnection

- `watchProcess()` monitors `opencode serve`, restarts on crash (max 5 in 5-min window)
- SSE reconnection: up to 3 reconnects with 500ms delay if stream drops
- Pre-connect SSE before POSTing message to never miss events

### 5. Concurrency

One message at a time per instance. New messages while busy get a "please wait" reply. The `atomic.Bool` busy guard is simple and correct for single-bot use.

### 6. Streaming UX

- "Processing..." indicator sent immediately (<100ms)
- Elapsed time counter every 1.5s while waiting
- Tool progress indicators (reading files, querying Jira, etc.)
- Text streaming with "typing..." suffix, updated every 800ms
- Final message has no suffix
- Long messages split at 3900 chars (Slack limit 4000)

## opencode serve API Contract

The bridge depends on these `opencode serve` endpoints:

| Endpoint | Method | Description |
|---|---|---|
| `/global/health` | GET | Returns 200 when server is ready |
| `/session` | POST | Creates a new session. Response: `{ "id": "..." }` |
| `/session/{id}/message` | POST | Sends a message. Body: `{ "message": "...", "agent": "..." }`. Returns 200 on accept. |
| `/event` | GET | SSE stream — **global**, returns events for ALL sessions. Client must filter by `sessionID`. |

### SSE Event Types

| Event | Key Fields | Meaning |
|---|---|---|
| `server.connected` | — | SSE connection established |
| `message.part.delta` | `sessionID`, `partID`, `field`, `delta` | Incremental text/reasoning delta |
| `message.part.updated` | `sessionID`, `part.type` (text/tool), `part.text`, `part.tool` | Part state update (tool start/complete, text snapshot) |
| `message.updated` | `sessionID` | Full message update (used for completion detection via `message.part.updated` parts) |
| `session.idle` | `sessionID` | Agent finished processing — session is idle |

SSE events are JSON with structure: `{ "type": "<event-type>", "properties": { ... } }`. Event lines follow the `data:` SSE format.

### SSE Architecture (Preserved Design)

The SSE processing uses a pre-connect + reconnect pattern with an action-based state machine:

1. **Pre-connect:** Before POSTing the message, the bridge opens an SSE connection to `/event`. This ensures no events are missed between POST and stream start.
2. **processSSEEvents():** Shared event loop that reads from any SSE scanner. Returns an action enum: `done` (session idle), `timeout` (context expired), `reconnect` (stream error, retryable), `error` (fatal).
3. **streamSSE():** Creates a new SSE connection and delegates to `processSSEEvents()`. Used for reconnections.
4. **Reconnect loop:** On `reconnect` action, retries up to `maxSSEReconnects` (3) times with 500ms delay. On `done`, `timeout`, or `error`, exits.

The first pass uses the pre-connected scanner directly with `processSSEEvents()`. If that returns `reconnect`, subsequent attempts go through `streamSSE()`.

## User-Facing Strings

All user-facing strings are in English. The current implementation has Portuguese strings that must be translated.

| Context | String |
|---|---|
| Busy guard | `⏳ _Please wait, I'm still processing the previous message..._` |
| Processing start | `⏳ Processing...` |
| Processing timer | `⏳ Processing... (%ds)` |
| Session rotation | `🔄 _Session restarted for performance._` |
| Timeout | `⏱️ Timeout — agent took more than %d seconds. Try simplifying your question.` |
| Error | `❌ Error: %v` |
| Empty response | `🤷 The agent returned no response.` |
| Generating | `_(...generating response)_` |
| Typing indicator | `⏳ _typing..._` |
| Tool: read | `📖 Reading files...` |
| Tool: edit/write | `✏️ Editing files...` |
| Tool: glob | `🔍 Searching files...` |
| Tool: grep | `🔍 Searching content...` |
| Tool: bash | `⚙️ Running command...` |
| Tool: default | `🔧 Using %s...` |

## Hardcoded Constants

These values are intentionally not user-configurable:

| Constant | Value | Purpose |
|---|---|---|
| `healthInterval` | 500ms | Polling interval for `/global/health` during startup |
| `crashRestartDelay` | 2s | Delay before restarting crashed `opencode serve` |
| `maxCrashRestarts` | 5 | Max restarts within crash window |
| `crashRestartWindow` | 5min | Window for counting crash restarts |
| `sseReconnectDelay` | 500ms | Delay between SSE reconnection attempts |
| `maxSSEReconnects` | 3 | Max SSE reconnection attempts per message |
| `processingTimerInterval` | 1.5s | Interval for elapsed time counter in Slack |
| `textUpdateInterval` | 800ms | Min interval between Slack text streaming updates |
| `slackMessageLimit` | 3900 | Max chars per Slack message (Slack API limit is 4000) |

## Process Environment

The bridge starts `opencode serve` as a child process. Environment setup:

- **Working directory:** Set to `AGENT_DIR`
- **PATH:** If `EXTRA_PATH` is set, it's prepended to the inherited PATH. This is important for launchd/systemd environments where PATH is minimal and tools like `opencode`, `rg`, `fd` may be in non-standard locations (e.g., `/opt/homebrew/bin`, `~/.local/bin`).
- **Other env vars:** Inherited from the bridge process

## What Changes From Current Code

| File | Change |
|---|---|
| `main.go` | Replace hardcoded values with `Config` struct. Remove cos-specific references. `AGENT_NAME` is passed in the POST body `agent` field and used in logging. |
| `handler.go` | Replace `AllowedUserID()` with config lookup. Remove hardcoded user ID fallback. Translate all Portuguese strings to English (see User-Facing Strings table). |
| `runner.go` | Replace all constants with config fields. Extract `buildMessageWithContext` to use `STATE_FILES` / `DAILY_CACHE_FILES` / `DAILY_CACHE_HEADER`. Change module name. Translate Portuguese strings. |
| `markdown.go` | Consolidate utility functions from `runner.go` and `handler.go`: `markdownToSlack`, `splitMessage`, `toolToProgressText`. No logic changes. |
| `config.go` | New file — `Config` struct with `Load()` and `Validate()`. |

## Makefile Targets

```makefile
build:          # go build -o slack-agent-bridge .
install:        # Generate plist from template, copy to ~/Library/LaunchAgents/, launchctl load
uninstall:      # launchctl unload, remove plist
logs:           # tail -f logs
```

`make install` requires `AGENT` parameter: `make install AGENT=carolina`
Generates plist label: `com.slack-agent-bridge.<AGENT>`

## Examples

### `examples/agent.example.md`

Minimal opencode agent prompt showing:
- Frontmatter (model, mode, tools)
- Bootstrap levels (L1/L2/L3)
- How state injection works
- How daily cache files work

### `examples/launchd.plist.tmpl`

Template with `{{AGENT_NAME}}`, `{{BINARY_PATH}}`, `{{WORKING_DIR}}`, `{{LOG_DIR}}` placeholders.

### `examples/opencode.example.jsonc`

Minimal opencode config showing provider + agent definition.

## Out of Scope

- Multi-bot routing (one bridge = one bot)
- Web UI or admin panel
- Slack slash commands (DMs only)
- Channel messages (DMs only)
- File/image attachments
- Thread replies (all replies are top-level)
