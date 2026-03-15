# slack-agent-bridge

A generic bridge connecting any opencode agent to any Slack bot via Socket Mode. The bridge starts an opencode server, connects to Slack via Socket Mode, and bridges messages between them using SSE streaming for real-time response delivery.

## Quick Start

1. **Create Slack app with Socket Mode**: Go to api.slack.com/apps, create new app, enable Socket Mode, install to workspace
2. **Configure environment**: Copy `.env.example` to `.env` and set `SLACK_APP_TOKEN`, `SLACK_BOT_TOKEN`, `AGENT_NAME`, `AGENT_DIR`
3. **Setup opencode**: Make sure `opencode` is in PATH and agent is configured in `~/.config/opencode/agents/<agent-name>.md`
4. **Build**: `make build`
5. **Run**: `./slack-agent-bridge`

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

Use launchd to run the bridge as a macOS service:

```bash
make install AGENT=myagent
make uninstall AGENT=myagent
make logs  # View logs
```

The service will auto-restart if it crashes and start at system boot.

## Multiple Agents

Run separate instances for different agents by using different `.env` files and working directories. Each instance connects to its own Slack bot and runs its own opencode agent.

## Architecture

The bridge starts an opencode server in serve mode, connects to Slack via Socket Mode WebSocket, and bridges messages between them. Responses stream back to Slack in real-time via Server-Sent Events, allowing users to see the agent's response as it's being generated.

## License

MIT