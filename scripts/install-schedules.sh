#!/bin/bash
# Install (or reinstall) all scheduled plist files into launchd
# Usage: ./install-schedules.sh <schedules-dir>
#
# If no schedules-dir is given, looks for schedules/ in the bridge directory.
# Plists can be generated from templates or provided as-is.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
BRIDGE_DIR="$(dirname "$SCRIPT_DIR")"

SCHEDULES_DIR="${1:-$BRIDGE_DIR/schedules}"
LAUNCH_AGENTS_DIR="$HOME/Library/LaunchAgents"

if [ ! -d "$SCHEDULES_DIR" ]; then
  echo "ERROR: Schedules directory not found: $SCHEDULES_DIR"
  echo "Usage: $0 [schedules-dir]"
  exit 1
fi

mkdir -p "$LAUNCH_AGENTS_DIR"

PLIST_COUNT=0
echo "Installing schedules from $SCHEDULES_DIR..."

for plist in "$SCHEDULES_DIR"/*.plist; do
  [ -f "$plist" ] || continue
  LABEL=$(basename "$plist" .plist)

  # Unload if already loaded
  launchctl bootout "gui/$(id -u)/$LABEL" 2>/dev/null || true

  # Copy to LaunchAgents
  cp "$plist" "$LAUNCH_AGENTS_DIR/"

  # Load
  launchctl bootstrap "gui/$(id -u)" "$LAUNCH_AGENTS_DIR/$(basename "$plist")"

  echo "  ✓ $LABEL installed"
  PLIST_COUNT=$((PLIST_COUNT + 1))
done

if [ "$PLIST_COUNT" -eq 0 ]; then
  echo "  No .plist files found in $SCHEDULES_DIR"
  exit 1
fi

echo ""
echo "$PLIST_COUNT schedule(s) installed. Verify with:"
echo "  launchctl list | grep -E '$(basename "$SCHEDULES_DIR" | head -c 10)'"
