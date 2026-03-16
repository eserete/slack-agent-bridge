#!/bin/bash
# Generic entry point for scheduled agent actions via opencode
# Usage: ./run.sh <action> [--send-rule always|conditional|silent]
#
# Reads configuration from .env file in the bridge directory.
# Required env vars: AGENT_NAME, AGENT_DIR
# Optional env vars: SLACK_BOT_TOKEN, SLACK_USER_ID (for Slack delivery)
#
# The --send-rule flag controls Slack delivery behavior:
#   always      — agent MUST send via slack-send.sh (default)
#   conditional — agent sends only if something needs attention
#   silent      — no Slack delivery (just run the action)

set -euo pipefail

# launchd doesn't inherit user's shell PATH
export PATH="$HOME/.local/bin:$HOME/.cargo/bin:/opt/homebrew/bin:/usr/local/bin:$PATH"

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
BRIDGE_DIR="$(dirname "$SCRIPT_DIR")"

# Load .env
ENV_FILE="${ENV_FILE:-$BRIDGE_DIR/.env}"
if [ -f "$ENV_FILE" ]; then
  set -a
  # shellcheck source=/dev/null
  source "$ENV_FILE"
  set +a
fi

ACTION="${1:-state-check}"
SEND_RULE="${2:---send-rule}"
# Parse --send-rule flag
if [ "$SEND_RULE" = "--send-rule" ]; then
  SEND_RULE="${3:-always}"
fi
# Also support: run.sh <action> always|conditional|silent (positional)
case "$ACTION" in
  --send-rule) echo "ERROR: action is required as first argument"; exit 1 ;;
esac
case "${2:-}" in
  always|conditional|silent) SEND_RULE="$2" ;;
esac

AGENT_DIR="${AGENT_DIR:?AGENT_DIR is required}"
AGENT_NAME="${AGENT_NAME:?AGENT_NAME is required}"
LOG_DIR="${AGENT_DIR}/logs"
mkdir -p "$LOG_DIR"

TIMESTAMP=$(date +%Y-%m-%d_%H-%M)
LOG_FILE="$LOG_DIR/$TIMESTAMP-$ACTION.log"

# Clean logs older than 30 days
find "$LOG_DIR" -name "*.log" -mtime +30 -delete 2>/dev/null || true

# Skip weekends (1=Monday ... 7=Sunday)
DAY_OF_WEEK=$(date +%u)
if [ "$DAY_OF_WEEK" -ge 6 ]; then
  echo "$(date -Iseconds) Skipping $ACTION — weekend" | tee "$LOG_FILE"
  exit 0
fi

# Verify opencode is available
if ! command -v opencode &>/dev/null; then
  echo "$(date -Iseconds) ERROR: opencode not found in PATH ($PATH)" | tee "$LOG_FILE"
  exit 1
fi

echo "$(date -Iseconds) Starting action: $ACTION" | tee "$LOG_FILE"

# Build Slack delivery instruction based on send rule
SLACK_SEND_CMD="bash $SCRIPT_DIR/slack-send.sh"
case "$SEND_RULE" in
  always)
    SEND_INSTRUCTION="Ao final, você DEVE executar o comando: $SLACK_SEND_CMD \"<mensagem formatada>\". Isso é OBRIGATÓRIO — a mensagem só chega ao usuário via esse comando. Sem executar esse comando, o usuário não recebe nada."
    ;;
  conditional)
    SEND_INSTRUCTION="Se houver algo que precisa de atenção, EXECUTE: $SLACK_SEND_CMD \"<mensagem>\". Se nada precisa de atenção, não envie."
    ;;
  silent)
    SEND_INSTRUCTION=""
    ;;
esac

# Build the prompt
PROMPT="Execução proativa agendada: $ACTION. Siga as instruções para a ação '$ACTION' no SYSTEM.md."
if [ -n "$SEND_INSTRUCTION" ]; then
  PROMPT="$PROMPT $SEND_INSTRUCTION"
fi

opencode run \
  --agent "$AGENT_NAME" \
  --dir "$AGENT_DIR" \
  "$PROMPT" \
  2>&1 | tee -a "$LOG_FILE"

echo "$(date -Iseconds) Finished action: $ACTION" | tee -a "$LOG_FILE"
