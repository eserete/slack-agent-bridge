# Example Schedule Plists

These are example launchd plist files for scheduling proactive agent actions.

## How to use

1. Copy the example plists to a `schedules/` directory in your bridge installation
2. Edit paths to match your setup (BRIDGE_DIR, AGENT_DIR, log paths)
3. Run `bash scripts/install-schedules.sh schedules/` to install them

## Customizing

Each plist calls `scripts/run.sh <action> <send-rule>`:

- **action**: The action name passed to the agent (e.g., `daily-planning`, `state-check`)
- **send-rule**: `always`, `conditional`, or `silent`

The agent's SYSTEM.md should define what each action does. The bridge handles scheduling and Slack delivery.

## Naming convention

Use a consistent label prefix: `com.<username>.<agent-short-name>.<action>`

Example: `com.example.myagent.morning` for the morning daily planning action.
