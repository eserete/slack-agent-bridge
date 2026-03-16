#!/bin/bash
# Send a message to Slack via Bot Token
# Usage: ./slack-send.sh "message"
# Reads SLACK_BOT_TOKEN and SLACK_USER_ID from environment or .env file
# Requires: jq (brew install jq)

set -euo pipefail

# Ensure common tool paths are available (launchd has minimal PATH)
export PATH="$HOME/.local/bin:/opt/homebrew/bin:/usr/local/bin:$PATH"

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
BRIDGE_DIR="$(dirname "$SCRIPT_DIR")"
LOG_DIR="${LOG_DIR:-$BRIDGE_DIR/logs}"
mkdir -p "$LOG_DIR"

# Verify jq dependency
if ! command -v jq &>/dev/null; then
  echo "$(date -Iseconds) ERROR: jq not found. Install with: brew install jq" | tee -a "$LOG_DIR/slack-errors.log"
  exit 1
fi

# Load .env if tokens not already in environment
if [ -z "${SLACK_BOT_TOKEN:-}" ] || [ -z "${SLACK_USER_ID:-}" ]; then
  ENV_FILE="${ENV_FILE:-$BRIDGE_DIR/.env}"
  if [ -f "$ENV_FILE" ]; then
    set -a
    # shellcheck source=/dev/null
    source "$ENV_FILE"
    set +a
  fi
fi

# SLACK_USER_ID can also be ALLOWED_USER_ID (bridge convention)
SLACK_USER_ID="${SLACK_USER_ID:-${ALLOWED_USER_ID:-}}"

if [ -z "${SLACK_BOT_TOKEN:-}" ] || [ -z "${SLACK_USER_ID:-}" ]; then
  echo "$(date -Iseconds) ERROR: SLACK_BOT_TOKEN or SLACK_USER_ID not configured" | tee -a "$LOG_DIR/slack-errors.log"
  exit 1
fi

MESSAGE="$1"

# Build JSON payload with jq to prevent injection (messages with quotes, newlines, etc.)
PAYLOAD=$(jq -n \
  --arg channel "$SLACK_USER_ID" \
  --arg text "$MESSAGE" \
  '{channel: $channel, text: $text, mrkdwn: true}')

# Separate body and HTTP code robustly (body can be multiline JSON)
TMPFILE=$(mktemp)
trap 'rm -f "$TMPFILE"' EXIT

HTTP_CODE=$(curl -s -o "$TMPFILE" -w "%{http_code}" -X POST https://slack.com/api/chat.postMessage \
  -H "Authorization: Bearer $SLACK_BOT_TOKEN" \
  -H "Content-Type: application/json" \
  -d "$PAYLOAD")

BODY=$(cat "$TMPFILE")

if [ "$HTTP_CODE" != "200" ]; then
  echo "$(date -Iseconds) ERROR: Slack API returned HTTP $HTTP_CODE - $BODY" | tee -a "$LOG_DIR/slack-errors.log"
  exit 1
fi

# Check Slack API ok field
OK=$(echo "$BODY" | jq -r '.ok' 2>/dev/null || echo "false")
if [ "$OK" != "true" ]; then
  ERROR_MSG=$(echo "$BODY" | jq -r '.error // "unknown"' 2>/dev/null || echo "parse error")
  echo "$(date -Iseconds) ERROR: Slack API ok=false, error=$ERROR_MSG - $BODY" | tee -a "$LOG_DIR/slack-errors.log"
  exit 1
fi

echo "$(date -Iseconds) OK: Message sent" >> "$LOG_DIR/slack.log"
