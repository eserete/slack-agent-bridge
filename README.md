# slack-agent-bridge

A generic bridge connecting any opencode agent to any Slack bot via Socket Mode. The bridge starts an opencode server, connects to Slack via Socket Mode, and bridges messages between them using SSE streaming for real-time response delivery.

## Quick Start

1. **Create Slack app with Socket Mode**: Go to api.slack.com/apps, create new app, enable Socket Mode, install to workspace
2. **Configure environment**: Copy `.env.example` to `.env` and set `SLACK_APP_TOKEN`, `SLACK_BOT_TOKEN`, `AGENT_NAME`, `AGENT_DIR`
3. **Setup opencode**: Make sure `opencode` is in PATH and agent is configured in `~/.config/opencode/agents/<agent-name>.md`
4. **Build**: `make build`
5. **Run**: `./slack-agent-bridge`

## Bootstrap with OpenCode

You can bootstrap your entire agent project using opencode itself with a free model — no API keys required.

### 1. Create the agent working directory

```bash
mkdir -p ~/agents/my-agent/state
cd ~/agents/my-agent
```

### 2. Run opencode with a free model to set up the project

```bash
opencode run --agent plan --model opencode/nemotron-3-super-free \
  "Set up this directory as an opencode agent project for a Slack bot called 'my-agent'. Do the following:
   1. Create a SYSTEM.md with a basic helpful assistant system prompt
   2. Create a .env file from this template: SLACK_APP_TOKEN=xapp-CHANGEME, SLACK_BOT_TOKEN=xoxb-CHANGEME, AGENT_NAME=my-agent, AGENT_DIR=$(pwd), ALLOWED_USER_ID=
   3. Create a state/config.yaml with empty placeholder config
   Tell me what was created and what I need to configure next."
```

This uses `opencode/nemotron-3-super-free` — a free model built into opencode, no API key or subscription needed.

### 3. Configure Slack tokens

Edit the `.env` file with your actual Slack tokens (from step 1 of Quick Start).

### 4. Register the agent with opencode

Create the agent definition file:

```bash
cp examples/agent.example.md ~/.config/opencode/agents/my-agent.md
```

Edit `~/.config/opencode/agents/my-agent.md` to customize the agent's behavior, or use opencode to do it:

```bash
opencode run --model opencode/nemotron-3-super-free \
  "Read SYSTEM.md and create a matching opencode agent definition at ~/.config/opencode/agents/my-agent.md with the right tools enabled (read, write, edit, bash). Use the content from SYSTEM.md as the agent's system prompt."
```

### 5. Build and run

```bash
make build
./slack-agent-bridge
```

### Available free models

These models work without any API key or subscription:

| Model | Best for |
|-------|----------|
| `opencode/nemotron-3-super-free` | General purpose, good quality |
| `opencode/minimax-m2.5-free` | Fast responses |
| `opencode/mimo-v2-flash-free` | Code-focused tasks |

To use a different model in your `opencode.jsonc`:

```jsonc
{
  "provider": { "opencode": {} },
  "model": { "default": "opencode/nemotron-3-super-free" }
}
```

## Configuration

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `SLACK_APP_TOKEN` | ✅ | - | Slack app-level token (starts with `xapp-`) |
| `SLACK_BOT_TOKEN` | ✅ | - | Slack bot token (starts with `xoxb-`) |
| `AGENT_NAME` | ✅ | - | Name of opencode agent (matches file in agents/) |
| `AGENT_DIR` | ✅ | - | Working directory for opencode agent |
| `ALLOWED_USER_ID` | ❌ | "" (allow all) | Restrict to specific Slack user ID |
| `SERVER_PORT` | ❌ | 14899 | Port for internal opencode server |
| `MAX_MESSAGES_PER_SESSION` | ❌ | 10 | Max messages before session reset |
| `MAX_SESSION_AGE_MINUTES` | ❌ | 60 | Session timeout in minutes |
| `RUN_TIMEOUT_SECONDS` | ❌ | 120 | Request timeout for opencode |
| `HEALTH_TIMEOUT_SECONDS` | ❌ | 30 | Health check timeout |
| `EXTRA_PATH` | ❌ | - | Additional PATH directories |
| `STATE_FILES` | ❌ | - | Comma-separated files to inject in every message |
| `DAILY_CACHE_FILES` | ❌ | - | Comma-separated files to inject if modified today |
| `DAILY_CACHE_HEADER` | ❌ | `[Daily cache]` | Header for daily cache injection |

## State Injection

The bridge automatically injects context into messages:

- **STATE_FILES**: Always injected at start of every message with `[Injected state]` header
- **DAILY_CACHE_FILES**: Only injected if modified today, with configurable header
- Files are injected as markdown code blocks with filename headers

This allows agents to access current state without reading files from disk.

## Running as a Service

Use launchd to run the bridge listener as a macOS service:

```bash
make install AGENT=myagent
make uninstall AGENT=myagent
make logs  # View logs
```

The service will auto-restart if it crashes and start at system boot.

## Scheduling Proactive Actions

The bridge includes scripts for scheduling proactive agent actions (daily planning, check-ins, reviews, etc.) via macOS launchd.

### Scripts

| Script | Purpose |
|--------|---------|
| `scripts/run.sh` | Entry point for scheduled actions — runs `opencode run` with action-specific prompts |
| `scripts/slack-send.sh` | Sends a message to Slack via Bot Token (used by agents during proactive runs) |
| `scripts/install-schedules.sh` | Installs launchd plist files from a directory |

### How it works

1. launchd triggers `scripts/run.sh <action> <send-rule>` at scheduled times
2. `run.sh` loads `.env`, skips weekends, and runs `opencode run` with a prompt telling the agent which action to execute
3. The agent reads its SYSTEM.md, executes the action, and (if the send rule requires it) calls `scripts/slack-send.sh` to deliver the result to Slack

### Send rules

| Rule | Behavior |
|------|----------|
| `always` | Agent MUST call `slack-send.sh` with the result (default) |
| `conditional` | Agent only sends if something needs attention |
| `silent` | No Slack delivery — just run the action |

### Setting up schedules

1. Create plist files for your agent (see `examples/schedules/` for chief-of-staff examples)
2. Install them:

```bash
bash scripts/install-schedules.sh /path/to/your/schedules
```

Or install the listener + schedules together:

```bash
make install AGENT=cos
bash scripts/install-schedules.sh examples/schedules
```

## Multiple Agents

Run separate instances for different agents by using different `.env` files and working directories. Each instance connects to its own Slack bot and runs its own opencode agent.

## Architecture

The bridge starts an opencode server in serve mode, connects to Slack via Socket Mode WebSocket, and bridges messages between them. Responses stream back to Slack in real-time via Server-Sent Events, allowing users to see the agent's response as it's being generated.

## License

MIT