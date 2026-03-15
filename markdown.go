package main

import (
	"fmt"
	"regexp"
	"strings"
)

// Pre-compiled regexes for markdown-to-Slack conversion.
var (
	boldRegex       = regexp.MustCompile(`\*\*(.+?)\*\*`)
	boldAltRegex    = regexp.MustCompile(`__(.+?)__`)
	headerRegex     = regexp.MustCompile(`(?m)^#{1,6}\s+(.+)$`)
	hrRegex         = regexp.MustCompile(`(?m)^[-*_]{3,}$`)
	linkRegex       = regexp.MustCompile(`\[([^\]]+)\]\(([^)]+)\)`)
	multiBlankRegex = regexp.MustCompile(`\n{3,}`)
)

// markdownToSlack converts Markdown formatting to Slack mrkdwn syntax.
func markdownToSlack(text string) string {
	text = boldRegex.ReplaceAllString(text, "*$1*")
	text = boldAltRegex.ReplaceAllString(text, "*$1*")
	text = headerRegex.ReplaceAllString(text, "*$1*")
	text = hrRegex.ReplaceAllString(text, "───────────────────")
	text = linkRegex.ReplaceAllString(text, "<$2|$1>")
	text = multiBlankRegex.ReplaceAllString(text, "\n\n")
	return strings.TrimSpace(text)
}

// splitMessage splits text into chunks of at most maxLen characters,
// preferring to break at newline boundaries.
func splitMessage(text string, maxLen int) []string {
	if len(text) <= maxLen {
		return []string{text}
	}

	var chunks []string
	for len(text) > 0 {
		if len(text) <= maxLen {
			chunks = append(chunks, text)
			break
		}

		// Try to break at last newline within limit
		cutPoint := maxLen
		if idx := strings.LastIndex(text[:maxLen], "\n"); idx > 0 {
			cutPoint = idx + 1
		}

		chunks = append(chunks, text[:cutPoint])
		text = text[cutPoint:]
	}

	return chunks
}

// toolToProgressText converts a tool name to a user-friendly progress message.
// All strings are in English (translated from original Portuguese).
func toolToProgressText(toolName string) string {
	switch {
	case toolName == "read":
		return "📖 Reading files..."
	case toolName == "glob" || toolName == "list":
		return "🔍 Searching files..."
	case toolName == "grep":
		return "🔍 Searching content..."
	case toolName == "write" || toolName == "edit":
		return "✏️ Editing files..."
	case toolName == "bash":
		return "⚙️ Running command..."
	default:
		return fmt.Sprintf("🔧 Using %s...", toolName)
	}
}
