package main

import (
	"fmt"
	"regexp"
	"strings"
)

// markdownToSlack converts Markdown formatting to Slack mrkdwn syntax.
func markdownToSlack(text string) string {
	// Convert **bold** to *bold* (Slack uses single asterisk for bold)
	boldRegex := regexp.MustCompile(`\*\*(.+?)\*\*`)
	text = boldRegex.ReplaceAllString(text, "*$1*")

	// Convert __bold__ to *bold*
	boldAltRegex := regexp.MustCompile(`__(.+?)__`)
	text = boldAltRegex.ReplaceAllString(text, "*$1*")

	// Convert ### Header, ## Header, # Header to *Header*
	headerRegex := regexp.MustCompile(`(?m)^#{1,6}\s+(.+)$`)
	text = headerRegex.ReplaceAllString(text, "*$1*")

	// Convert horizontal rules to visual separator
	hrRegex := regexp.MustCompile(`(?m)^[-*_]{3,}$`)
	text = hrRegex.ReplaceAllString(text, "───────────────────")

	// Convert [text](url) to <url|text>
	linkRegex := regexp.MustCompile(`\[([^\]]+)\]\(([^)]+)\)`)
	text = linkRegex.ReplaceAllString(text, "<$2|$1>")

	// Collapse 3+ consecutive blank lines into 2
	multiBlankRegex := regexp.MustCompile(`\n{3,}`)
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
