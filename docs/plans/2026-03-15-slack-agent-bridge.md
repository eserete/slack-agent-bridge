# slack-agent-bridge Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Extract an existing Slack listener into a generic, configurable, open-source Go bridge connecting any opencode agent to any Slack bot.

**Architecture:** One Go binary per Slack bot. Starts `opencode serve` as child process, connects to Slack via Socket Mode, bridges messages with real-time SSE streaming. All agent-specific config via env vars.

**Tech Stack:** Go 1.25+, slack-go/slack, joho/godotenv, gorilla/websocket

**Spec:** `docs/specs/2026-03-15-slack-agent-bridge-design.md`

**Source (extract from):** `/path/to/source/listener/` (main.go, handler.go, runner.go)

---

## File Structure

```
slack-agent-bridge/
├── main.go              # Entrypoint: load config, start server + Slack
├── config.go            # Config struct, Load(), Validate()
├── handler.go           # Slack message handler: busy guard, streaming UI
├── runner.go            # OpenCodeServer: process lifecycle, SSE, sessions
├── markdown.go          # markdownToSlack, splitMessage, toolToProgressText
├── go.mod / go.sum
├── .env.example
├── .gitignore
├── README.md
├── LICENSE              # MIT
├── Makefile             # build, install, uninstall (launchd)
└── examples/
    ├── agent.example.md
    ├── launchd.plist.tmpl
    └── opencode.example.jsonc
```

---

## Chunk 1: Project Scaffold + config.go

### Task 1.1: Initialize Go module and repo files

**Files:**
- Create: `go.mod`
- Create: `.gitignore`
- Create: `LICENSE`

- [ ] **Step 1: Create go.mod**

```
module github.com/eserete/slack-agent-bridge

go 1.25.0

require (
	github.com/gorilla/websocket v1.5.3
	github.com/joho/godotenv v1.5.1
	github.com/slack-go/slack v0.19.0
)
```

- [ ] **Step 2: Create .gitignore**

```
slack-agent-bridge
.env
*.log
```

- [ ] **Step 3: Create LICENSE (MIT)**

Standard MIT license with `eserete` and year `2026`.

- [ ] **Step 4: Run `go mod tidy`**

Run: `go mod tidy`
Expected: go.sum generated, no errors.

- [ ] **Step 5: Commit**

```bash
git add go.mod go.sum .gitignore LICENSE
git commit -m "feat: initialize Go module and repo files"
```

---

### Task 1.2: Create config.go

**Files:**
- Create: `config.go`

- [ ] **Step 1: Write config.go**

```go
package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds all configurable values for the bridge.
type Config struct {
	// Required
	SlackAppToken string
	SlackBotToken string
	AgentName     string
	AgentDir      string

	// Optional
	AllowedUserID        string
	ServerPort           int
	MaxMessagesPerSession int
	MaxSessionAge        time.Duration
	RunTimeout           time.Duration
	HealthTimeout        time.Duration
	ExtraPath            string

	// State injection
	StateFiles       []string // relative paths to AGENT_DIR
	DailyCacheFiles  []string // relative paths, only injected if modified today
	DailyCacheHeader string
}

// LoadConfig reads configuration from environment variables.
func LoadConfig() (*Config, error) {
	c := &Config{
		SlackAppToken:        os.Getenv("SLACK_APP_TOKEN"),
		SlackBotToken:        os.Getenv("SLACK_BOT_TOKEN"),
		AgentName:            os.Getenv("AGENT_NAME"),
		AgentDir:             os.Getenv("AGENT_DIR"),
		AllowedUserID:        os.Getenv("ALLOWED_USER_ID"),
		ExtraPath:            os.Getenv("EXTRA_PATH"),
		DailyCacheHeader:     os.Getenv("DAILY_CACHE_HEADER"),
		ServerPort:           14899,
		MaxMessagesPerSession: 10,
		MaxSessionAge:        60 * time.Minute,
		RunTimeout:           120 * time.Second,
		HealthTimeout:        30 * time.Second,
	}

	if c.DailyCacheHeader == "" {
		c.DailyCacheHeader = "Daily cache — queried today, do NOT query again unless user asks"
	}

	if v := os.Getenv("SERVER_PORT"); v != "" {
		port, err := strconv.Atoi(v)
		if err != nil {
			return nil, fmt.Errorf("SERVER_PORT invalid: %w", err)
		}
		c.ServerPort = port
	}

	if v := os.Getenv("MAX_MESSAGES_PER_SESSION"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return nil, fmt.Errorf("MAX_MESSAGES_PER_SESSION invalid: %w", err)
		}
		c.MaxMessagesPerSession = n
	}

	if v := os.Getenv("MAX_SESSION_AGE_MINUTES"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return nil, fmt.Errorf("MAX_SESSION_AGE_MINUTES invalid: %w", err)
		}
		c.MaxSessionAge = time.Duration(n) * time.Minute
	}

	if v := os.Getenv("RUN_TIMEOUT_SECONDS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return nil, fmt.Errorf("RUN_TIMEOUT_SECONDS invalid: %w", err)
		}
		c.RunTimeout = time.Duration(n) * time.Second
	}

	if v := os.Getenv("HEALTH_TIMEOUT_SECONDS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return nil, fmt.Errorf("HEALTH_TIMEOUT_SECONDS invalid: %w", err)
		}
		c.HealthTimeout = time.Duration(n) * time.Second
	}

	if v := os.Getenv("STATE_FILES"); v != "" {
		c.StateFiles = splitCSV(v)
	}

	if v := os.Getenv("DAILY_CACHE_FILES"); v != "" {
		c.DailyCacheFiles = splitCSV(v)
	}

	return c, c.Validate()
}

// Validate checks that all required fields are present.
func (c *Config) Validate() error {
	if c.SlackAppToken == "" {
		return fmt.Errorf("SLACK_APP_TOKEN is required (must start with xapp-)")
	}
	if c.SlackBotToken == "" {
		return fmt.Errorf("SLACK_BOT_TOKEN is required (must start with xoxb-)")
	}
	if c.AgentName == "" {
		return fmt.Errorf("AGENT_NAME is required")
	}
	if c.AgentDir == "" {
		return fmt.Errorf("AGENT_DIR is required")
	}
	return nil
}

// BaseURL returns the opencode serve base URL.
func (c *Config) BaseURL() string {
	return fmt.Sprintf("http://127.0.0.1:%d", c.ServerPort)
}

// splitCSV splits a comma-separated string, trimming whitespace.
func splitCSV(s string) []string {
	parts := strings.Split(s, ",")
	var result []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}
```

- [ ] **Step 2: Create .env.example**

```
# Required
SLACK_APP_TOKEN=xapp-1-...
SLACK_BOT_TOKEN=xoxb-...
AGENT_NAME=my-agent
AGENT_DIR=/path/to/agent/workdir

# Optional
# ALLOWED_USER_ID=U12345678
# SERVER_PORT=14899
# MAX_MESSAGES_PER_SESSION=10
# MAX_SESSION_AGE_MINUTES=60
# RUN_TIMEOUT_SECONDS=120
# HEALTH_TIMEOUT_SECONDS=30
# EXTRA_PATH=/opt/homebrew/bin:/usr/local/bin

# State injection (comma-separated paths relative to AGENT_DIR)
# STATE_FILES=state/tasks.yaml,state/projects.yaml
# DAILY_CACHE_FILES=state/jira-snapshot.md
# DAILY_CACHE_HEADER=Daily cache — queried today, do NOT query again unless user asks
```

- [ ] **Step 3: Verify compilation**

Create a minimal `main.go` stub so `go build` works:

```go
package main

func main() {}
```

Run: `go build ./...`
Expected: No errors.

- [ ] **Step 4: Commit**

```bash
git add config.go .env.example main.go
git commit -m "feat: add Config struct with env var loading and validation"
```

---

## Chunk 2: markdown.go (Utility Functions)

Extract `markdownToSlack`, `splitMessage`, `toolToProgressText` from source. Translate Portuguese strings to English.

### Task 2.1: Create markdown.go

**Files:**
- Create: `markdown.go`

**Source:** `runner.go:858-904` (markdownToSlack, toolToProgressText) + `handler.go:245-268` (splitMessage)

- [ ] **Step 1: Write markdown.go**

```go
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
```

**Changes from source:**
- `toolToProgressText`: Removed Jira/Microsoft/calendar-specific cases (agent-specific). Generic bridge uses tool name in default. All strings English.
- `splitMessage`: Identical logic, moved from handler.go.
- `markdownToSlack`: Identical logic, moved from runner.go.

- [ ] **Step 2: Verify compilation**

Run: `go build ./...`
Expected: No errors.

- [ ] **Step 3: Commit**

```bash
git add markdown.go
git commit -m "feat: add markdown-to-Slack conversion and message utilities"
```

---

## Chunk 3: runner.go (OpenCodeServer — Process Lifecycle + SSE)

The largest file. Extract from source `runner.go` (905 lines), replacing all hardcoded values with `Config` fields. The `cfg` global is set in `main.go` (created as stub in chunk 1).

### Task 3.1: Create runner.go — types, constants, constructor

**Files:**
- Create: `runner.go`

- [ ] **Step 1: Write runner.go — types and hardcoded constants section**

```go
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Hardcoded constants (intentionally not user-configurable)
const (
	healthInterval     = 500 * time.Millisecond
	crashRestartDelay  = 2 * time.Second
	maxCrashRestarts   = 5
	crashRestartWindow = 5 * time.Minute
	sseReconnectDelay  = 500 * time.Millisecond
	maxSSEReconnects   = 3
)

// StreamEventKind distinguishes between progress updates and text chunks.
type StreamEventKind int

const (
	EventProgress StreamEventKind = iota
	EventText
)

// StreamEvent carries either a progress update or a text chunk.
type StreamEvent struct {
	Kind            StreamEventKind
	ProgressText    string
	AccumulatedText string
}

// StreamCallback is called for each meaningful event during agent execution.
type StreamCallback func(event StreamEvent)

// RunResult holds the final output of an opencode run invocation.
type RunResult struct {
	Output         string
	Duration       time.Duration
	TimedOut       bool
	SessionRotated bool
	Err            error
}

// SSE event types
type sseEvent struct {
	Type       string          `json:"type"`
	Properties json.RawMessage `json:"properties"`
}

type partDeltaProps struct {
	SessionID string `json:"sessionID"`
	MessageID string `json:"messageID"`
	PartID    string `json:"partID"`
	Field     string `json:"field"`
	Delta     string `json:"delta"`
}

type partUpdatedProps struct {
	SessionID string `json:"sessionID"`
	MessageID string `json:"messageID"`
	Part      struct {
		Type   string `json:"type"`
		Tool   string `json:"tool"`
		Text   string `json:"text"`
		Reason string `json:"reason"`
		Tokens *struct {
			Total int `json:"total"`
		} `json:"tokens"`
	} `json:"part"`
}

type sessionStatusProps struct {
	SessionID string `json:"sessionID"`
	Status    struct {
		Type string `json:"type"`
	} `json:"status"`
}

// sseAction indicates what the caller should do after processing SSE events.
type sseAction int

const (
	sseActionDone      sseAction = iota
	sseActionTimeout
	sseActionReconnect
	sseActionError
)

// OpenCodeServer manages a persistent opencode serve process.
type OpenCodeServer struct {
	cfg          *Config
	cmd          *exec.Cmd
	sessionID    string
	msgCount     int
	lastRotation time.Time
	mu           sync.RWMutex

	alive         bool
	crashTimes    []time.Time
	stopWatchChan chan struct{}
}

// NewOpenCodeServer creates and starts an opencode serve process.
func NewOpenCodeServer(cfg *Config) (*OpenCodeServer, error) {
	s := &OpenCodeServer{
		cfg:           cfg,
		stopWatchChan: make(chan struct{}),
	}
	if err := s.start(); err != nil {
		return nil, err
	}
	go s.watchProcess()
	return s, nil
}
```

**Changes from source:**
- All constants from source that are now configurable (`runTimeout`, `serverPort`, `agentName`, etc.) removed — they come from `cfg`.
- `OpenCodeServer` stores `cfg *Config` instead of using globals.
- `NewOpenCodeServer` takes `cfg` parameter.

- [ ] **Step 2: Append runner.go — start, waitHealthy, createSession, warmSession**

Append to `runner.go`:

```go
func (s *OpenCodeServer) start() error {
	opencodePath, err := exec.LookPath("opencode")
	if err != nil {
		return fmt.Errorf("opencode not found in PATH: %w", err)
	}

	s.cmd = exec.Command(opencodePath, "serve",
		"--port", fmt.Sprintf("%d", s.cfg.ServerPort),
		"--hostname", "127.0.0.1",
	)
	s.cmd.Dir = s.cfg.AgentDir

	// Build PATH with optional EXTRA_PATH prepended
	currentPath := os.Getenv("PATH")
	if s.cfg.ExtraPath != "" {
		currentPath = s.cfg.ExtraPath + ":" + currentPath
	}
	s.cmd.Env = append(os.Environ(), "PATH="+currentPath)

	s.cmd.Stdout = nil
	s.cmd.Stderr = nil

	if err := s.cmd.Start(); err != nil {
		return fmt.Errorf("failed to start opencode serve: %w", err)
	}

	log.Printf("opencode serve started (pid=%d, port=%d)", s.cmd.Process.Pid, s.cfg.ServerPort)

	if err := s.waitHealthy(); err != nil {
		s.Stop()
		return fmt.Errorf("opencode serve failed health check: %w", err)
	}

	log.Println("opencode serve is healthy")

	if err := s.createSession(); err != nil {
		s.Stop()
		return fmt.Errorf("failed to create session: %w", err)
	}

	s.mu.Lock()
	s.lastRotation = time.Now()
	s.msgCount = 0
	s.alive = true
	s.mu.Unlock()

	log.Printf("opencode session created: %s", s.sessionID)
	s.warmSession()
	return nil
}

func (s *OpenCodeServer) waitHealthy() error {
	deadline := time.Now().Add(s.cfg.HealthTimeout)
	for time.Now().Before(deadline) {
		resp, err := http.Get(s.cfg.BaseURL() + "/global/health")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				return nil
			}
		}
		time.Sleep(healthInterval)
	}
	return fmt.Errorf("timeout after %s", s.cfg.HealthTimeout)
}

func (s *OpenCodeServer) createSession() error {
	resp, err := http.Post(s.cfg.BaseURL()+"/session", "application/json", bytes.NewBufferString("{}"))
	if err != nil {
		return fmt.Errorf("POST /session: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("decode session response: %w", err)
	}
	if result.ID == "" {
		return fmt.Errorf("empty session ID in response")
	}

	s.mu.Lock()
	s.sessionID = result.ID
	s.mu.Unlock()
	return nil
}

func (s *OpenCodeServer) warmSession() {
	go func() {
		sessionID := s.GetSessionID()
		warmMsg, _ := json.Marshal(map[string]interface{}{
			"parts": []map[string]string{{"type": "text", "text": "."}},
			"agent": s.cfg.AgentName,
		})

		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()

		req, err := http.NewRequestWithContext(ctx, "POST",
			fmt.Sprintf("%s/session/%s/message", s.cfg.BaseURL(), sessionID),
			bytes.NewReader(warmMsg),
		)
		if err != nil {
			log.Printf("WARNING: warm-up request creation failed: %v", err)
			return
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			log.Printf("WARNING: warm-up request failed: %v", err)
			return
		}
		defer resp.Body.Close()
		io.ReadAll(resp.Body)

		sseReq, err := http.NewRequestWithContext(ctx, "GET", s.cfg.BaseURL()+"/event", nil)
		if err != nil {
			return
		}
		sseReq.Header.Set("Accept", "text/event-stream")
		sseResp, err := http.DefaultClient.Do(sseReq)
		if err != nil {
			return
		}
		defer sseResp.Body.Close()

		go func() {
			<-ctx.Done()
			sseResp.Body.Close()
		}()

		scanner := bufio.NewScanner(sseResp.Body)
		scanner.Buffer(make([]byte, 0, 64*1024), 256*1024)
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "data: ") {
				var ev sseEvent
				if json.Unmarshal([]byte(line[6:]), &ev) == nil && ev.Type == "session.idle" {
					break
				}
			}
		}

		s.mu.Lock()
		s.msgCount++
		s.mu.Unlock()

		log.Printf("session warm-up complete (session=%s)", sessionID)
	}()
}
```

**Changes from source:**
- `start()`: Uses `s.cfg.AgentDir` instead of hardcoded path. Uses `s.cfg.ExtraPath` instead of hardcoded PATH entries. Uses `s.cfg.ServerPort`.
- `waitHealthy()`: Uses `s.cfg.HealthTimeout` and `s.cfg.BaseURL()`.
- `createSession()`: Uses `s.cfg.BaseURL()`.
- `warmSession()`: Uses `s.cfg.AgentName` and `s.cfg.BaseURL()`.

- [ ] **Step 3: Verify compilation**

Run: `go build ./...`
Expected: No errors (main.go stub exists).

- [ ] **Step 4: Commit**

```bash
git add runner.go
git commit -m "feat: add OpenCodeServer types, start, health, session, warm-up"
```

---

## Chunk 4: runner.go — Stop, Watch, Rotate, BuildMessage, RunAgentStreaming, SSE

### Task 4.1: Append runner.go — Stop, watchProcess, session helpers

- [ ] **Step 1: Append Stop, GetSessionID, shouldRotate, RotateSession, IsAlive, watchProcess**

Append to `runner.go`:

```go
func (s *OpenCodeServer) Stop() {
	select {
	case <-s.stopWatchChan:
	default:
		close(s.stopWatchChan)
	}

	s.mu.Lock()
	s.alive = false
	s.mu.Unlock()

	if s.cmd != nil && s.cmd.Process != nil {
		log.Println("Stopping opencode serve...")
		s.cmd.Process.Kill()
		s.cmd.Wait()
	}
}

func (s *OpenCodeServer) GetSessionID() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.sessionID
}

func (s *OpenCodeServer) shouldRotate() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.msgCount >= s.cfg.MaxMessagesPerSession || time.Since(s.lastRotation) >= s.cfg.MaxSessionAge
}

func (s *OpenCodeServer) RotateSession() (bool, error) {
	if !s.shouldRotate() {
		return false, nil
	}

	oldID := s.GetSessionID()
	if err := s.createSession(); err != nil {
		return false, fmt.Errorf("session rotation failed: %w", err)
	}

	s.mu.Lock()
	s.msgCount = 0
	s.lastRotation = time.Now()
	newID := s.sessionID
	s.mu.Unlock()

	log.Printf("session rotated: %s → %s", oldID, newID)
	return true, nil
}

func (s *OpenCodeServer) IsAlive() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.alive
}

func (s *OpenCodeServer) watchProcess() {
	for {
		if s.cmd != nil && s.cmd.Process != nil {
			_ = s.cmd.Wait()
		}

		select {
		case <-s.stopWatchChan:
			return
		default:
		}

		s.mu.Lock()
		s.alive = false
		now := time.Now()
		cutoff := now.Add(-crashRestartWindow)
		var recent []time.Time
		for _, t := range s.crashTimes {
			if t.After(cutoff) {
				recent = append(recent, t)
			}
		}
		recent = append(recent, now)
		s.crashTimes = recent
		s.mu.Unlock()

		if len(recent) > maxCrashRestarts {
			log.Printf("FATAL: opencode serve crashed %d times in %s — giving up",
				len(recent), crashRestartWindow)
			return
		}

		log.Printf("WARNING: opencode serve exited unexpectedly (crash #%d). Restarting in %s...",
			len(recent), crashRestartDelay)

		select {
		case <-time.After(crashRestartDelay):
		case <-s.stopWatchChan:
			return
		}

		if err := s.start(); err != nil {
			log.Printf("ERROR: failed to restart opencode serve: %v", err)
			select {
			case <-time.After(5 * time.Second):
			case <-s.stopWatchChan:
				return
			}
			continue
		}

		log.Printf("opencode serve restarted successfully (session=%s)", s.GetSessionID())
	}
}
```

Identical logic to source. Only change: `shouldRotate` uses `s.cfg.*` instead of package-level constants.

- [ ] **Step 2: Commit**

```bash
git add runner.go
git commit -m "feat: add Stop, watchProcess, RotateSession to OpenCodeServer"
```

### Task 4.2: Append runner.go — buildMessageWithContext

- [ ] **Step 1: Append buildMessageWithContext**

Append to `runner.go`:

```go
// buildMessageWithContext reads state/daily-cache files from disk and prepends
// them to the user message. Uses Config.StateFiles, DailyCacheFiles, DailyCacheHeader.
func (s *OpenCodeServer) buildMessageWithContext(userMessage string) string {
	var sb strings.Builder
	hasState := false

	// Always-injected state files
	for _, relPath := range s.cfg.StateFiles {
		absPath := filepath.Join(s.cfg.AgentDir, relPath)
		data, err := os.ReadFile(absPath)
		if err != nil || len(data) == 0 {
			continue
		}
		if !hasState {
			sb.WriteString("[Injected state — do NOT read these files from disk]\n\n")
			hasState = true
		}
		sb.WriteString(filepath.Base(relPath) + ":\n")
		sb.Write(data)
		sb.WriteString("\n\n")
	}

	// Daily cache files — only injected if modified today
	for _, relPath := range s.cfg.DailyCacheFiles {
		absPath := filepath.Join(s.cfg.AgentDir, relPath)
		info, err := os.Stat(absPath)
		if err != nil {
			continue
		}
		now := time.Now()
		mod := info.ModTime()
		if mod.Year() != now.Year() || mod.YearDay() != now.YearDay() {
			continue
		}
		data, err := os.ReadFile(absPath)
		if err != nil || len(data) == 0 {
			continue
		}
		sb.WriteString("[" + s.cfg.DailyCacheHeader + "]\n")
		sb.WriteString(filepath.Base(relPath) + ":\n")
		sb.Write(data)
		sb.WriteString("\n\n")
	}

	sb.WriteString("[User message]\n")
	sb.WriteString(userMessage)

	return sb.String()
}
```

**Changes from source:**
- Iterates `s.cfg.StateFiles` and `s.cfg.DailyCacheFiles` instead of hardcoded file names.
- Uses `s.cfg.DailyCacheHeader` instead of hardcoded Portuguese text.
- Uses `s.cfg.AgentDir` instead of hardcoded path.
- English labels instead of Portuguese.
- Removed Jira-specific `[jira_available: false]` instruction — that goes in `DAILY_CACHE_HEADER`.

- [ ] **Step 2: Commit**

```bash
git add runner.go
git commit -m "feat: add generic buildMessageWithContext with configurable state injection"
```

### Task 4.3: Append runner.go — RunAgentStreaming + SSE methods

- [ ] **Step 1: Append RunAgentStreaming, streamSSE, processSSEEvents**

Append to `runner.go`:

```go
func (s *OpenCodeServer) RunAgentStreaming(userMessage string, onStream StreamCallback) RunResult {
	start := time.Now()

	if !s.IsAlive() {
		log.Println("WARNING: opencode serve not alive, waiting for recovery...")
		for i := 0; i < 10; i++ {
			time.Sleep(1 * time.Second)
			if s.IsAlive() {
				break
			}
		}
		if !s.IsAlive() {
			return RunResult{Duration: time.Since(start), Err: fmt.Errorf("opencode serve is not running")}
		}
		log.Println("opencode serve recovered, proceeding")
	}

	rotated, err := s.RotateSession()
	if err != nil {
		return RunResult{Duration: time.Since(start), Err: fmt.Errorf("session rotation: %w", err)}
	}

	sessionID := s.GetSessionID()
	ctx, cancel := context.WithTimeout(context.Background(), s.cfg.RunTimeout)
	defer cancel()

	enrichedMessage := s.buildMessageWithContext(userMessage)

	// Pre-connect SSE before sending the message
	sseReq, err := http.NewRequestWithContext(ctx, "GET", s.cfg.BaseURL()+"/event", nil)
	if err != nil {
		return RunResult{Duration: time.Since(start), Err: fmt.Errorf("create SSE request: %w", err)}
	}
	sseReq.Header.Set("Accept", "text/event-stream")
	sseReq.Header.Set("Cache-Control", "no-cache")

	sseResp, err := http.DefaultClient.Do(sseReq)
	if err != nil {
		if ctx.Err() != nil {
			return RunResult{Duration: time.Since(start), TimedOut: true, Err: fmt.Errorf("timeout connecting SSE")}
		}
		return RunResult{Duration: time.Since(start), Err: fmt.Errorf("connect to SSE: %w", err)}
	}

	go func() {
		<-ctx.Done()
		sseResp.Body.Close()
	}()

	sseScanner := bufio.NewScanner(sseResp.Body)
	sseScanner.Buffer(make([]byte, 0, 256*1024), 1024*1024)

	// Wait for server.connected
	for sseScanner.Scan() {
		line := sseScanner.Text()
		if strings.HasPrefix(line, "data: ") {
			var ev sseEvent
			if json.Unmarshal([]byte(line[6:]), &ev) == nil && ev.Type == "server.connected" {
				break
			}
		}
	}

	log.Printf("  SSE pre-connected at +%s", time.Since(start).Round(time.Millisecond))

	// Send the message
	msgBody, _ := json.Marshal(map[string]interface{}{
		"parts": []map[string]string{{"type": "text", "text": enrichedMessage}},
		"agent": s.cfg.AgentName,
	})

	go func() {
		msgReq, err := http.NewRequestWithContext(ctx, "POST",
			fmt.Sprintf("%s/session/%s/message", s.cfg.BaseURL(), sessionID),
			bytes.NewReader(msgBody),
		)
		if err != nil {
			log.Printf("ERROR creating message request: %v", err)
			return
		}
		msgReq.Header.Set("Content-Type", "application/json")

		resp, err := http.DefaultClient.Do(msgReq)
		if err != nil {
			log.Printf("ERROR sending message: %v", err)
			return
		}
		defer resp.Body.Close()
		io.ReadAll(resp.Body)

		s.mu.Lock()
		s.msgCount++
		s.mu.Unlock()
	}()

	// Stream SSE events with reconnection
	var accumulated string
	var assistantMsgID string
	var firstTextAt time.Time
	var toolCallCount int
	reconnects := 0

	// First pass with pre-connected scanner
	{
		result, action := s.processSSEEvents(ctx, sseScanner, sseResp, sessionID, start, onStream,
			&accumulated, &assistantMsgID, &firstTextAt, &toolCallCount)

		switch action {
		case sseActionDone:
			result.SessionRotated = rotated
			return result
		case sseActionTimeout:
			return RunResult{
				Output: markdownToSlack(accumulated), Duration: time.Since(start),
				TimedOut: true, SessionRotated: rotated,
				Err: fmt.Errorf("timeout after %s", s.cfg.RunTimeout),
			}
		case sseActionReconnect:
			reconnects++
			log.Printf("WARNING: SSE dropped, reconnecting (1/%d)...", maxSSEReconnects)
			time.Sleep(sseReconnectDelay)
		case sseActionError:
			result.SessionRotated = rotated
			return result
		}
	}

	// Reconnect loop
	for {
		result, action := s.streamSSE(ctx, sessionID, start, onStream,
			&accumulated, &assistantMsgID, &firstTextAt, &toolCallCount)

		switch action {
		case sseActionDone:
			result.SessionRotated = rotated
			return result
		case sseActionTimeout:
			return RunResult{
				Output: markdownToSlack(accumulated), Duration: time.Since(start),
				TimedOut: true, SessionRotated: rotated,
				Err: fmt.Errorf("timeout after %s", s.cfg.RunTimeout),
			}
		case sseActionReconnect:
			reconnects++
			if reconnects > maxSSEReconnects {
				log.Printf("ERROR: SSE reconnect limit reached (%d)", maxSSEReconnects)
				return RunResult{
					Output: markdownToSlack(accumulated), Duration: time.Since(start),
					SessionRotated: rotated,
					Err: fmt.Errorf("SSE connection lost after %d reconnects", maxSSEReconnects),
				}
			}
			log.Printf("WARNING: SSE dropped, reconnecting (%d/%d)...", reconnects, maxSSEReconnects)
			time.Sleep(sseReconnectDelay)
			continue
		case sseActionError:
			result.SessionRotated = rotated
			return result
		}
	}
}

func (s *OpenCodeServer) streamSSE(
	ctx context.Context, sessionID string, start time.Time, onStream StreamCallback,
	accumulated *string, assistantMsgID *string, firstTextAt *time.Time, toolCallCount *int,
) (RunResult, sseAction) {
	sseReq, err := http.NewRequestWithContext(ctx, "GET", s.cfg.BaseURL()+"/event", nil)
	if err != nil {
		return RunResult{Duration: time.Since(start), Err: fmt.Errorf("create SSE request: %w", err)}, sseActionError
	}
	sseReq.Header.Set("Accept", "text/event-stream")
	sseReq.Header.Set("Cache-Control", "no-cache")

	sseResp, err := http.DefaultClient.Do(sseReq)
	if err != nil {
		if ctx.Err() != nil {
			return RunResult{}, sseActionTimeout
		}
		return RunResult{Duration: time.Since(start), Err: fmt.Errorf("connect to SSE: %w", err)}, sseActionError
	}

	go func() {
		<-ctx.Done()
		sseResp.Body.Close()
	}()

	scanner := bufio.NewScanner(sseResp.Body)
	scanner.Buffer(make([]byte, 0, 256*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data: ") {
			var ev sseEvent
			if json.Unmarshal([]byte(line[6:]), &ev) == nil && ev.Type == "server.connected" {
				break
			}
		}
	}

	return s.processSSEEvents(ctx, scanner, sseResp, sessionID, start, onStream,
		accumulated, assistantMsgID, firstTextAt, toolCallCount)
}

func (s *OpenCodeServer) processSSEEvents(
	ctx context.Context, scanner *bufio.Scanner, sseResp *http.Response,
	sessionID string, start time.Time, onStream StreamCallback,
	accumulated *string, assistantMsgID *string, firstTextAt *time.Time, toolCallCount *int,
) (RunResult, sseAction) {
	defer sseResp.Body.Close()

	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := line[6:]

		var ev sseEvent
		if err := json.Unmarshal([]byte(data), &ev); err != nil {
			continue
		}

		switch ev.Type {
		case "message.updated":
			var props struct {
				SessionID string `json:"sessionID"`
				Info      struct {
					ID   string `json:"id"`
					Role string `json:"role"`
				} `json:"info"`
			}
			if json.Unmarshal(ev.Properties, &props) != nil {
				continue
			}
			if props.SessionID != "" && props.SessionID != sessionID {
				continue
			}
			if props.Info.Role == "assistant" && *assistantMsgID == "" {
				*assistantMsgID = props.Info.ID
				log.Printf("  SSE assistant msg=%s at +%s", *assistantMsgID, time.Since(start).Round(time.Millisecond))
			}

		case "message.part.updated":
			var props partUpdatedProps
			if json.Unmarshal(ev.Properties, &props) != nil {
				continue
			}
			if props.SessionID != "" && props.SessionID != sessionID {
				continue
			}
			if props.Part.Type == "tool-call" && props.Part.Tool != "" {
				*toolCallCount++
				log.Printf("  SSE tool-call #%d: %s at +%s", *toolCallCount, props.Part.Tool, time.Since(start).Round(time.Millisecond))
			}
			if onStream != nil && props.Part.Type == "tool-call" && props.Part.Tool != "" {
				onStream(StreamEvent{Kind: EventProgress, ProgressText: toolToProgressText(props.Part.Tool)})
			}

		case "message.part.delta":
			var props partDeltaProps
			if json.Unmarshal(ev.Properties, &props) != nil {
				continue
			}
			if props.SessionID != "" && props.SessionID != sessionID {
				continue
			}
			if props.Field == "text" && props.Delta != "" {
				if firstTextAt.IsZero() {
					*firstTextAt = time.Now()
					log.Printf("  SSE first-text at +%s (TTFT)", time.Since(start).Round(time.Millisecond))
				}
				*accumulated += props.Delta
				if onStream != nil {
					onStream(StreamEvent{Kind: EventText, AccumulatedText: markdownToSlack(*accumulated)})
				}
			}

		case "session.idle":
			var statusProps sessionStatusProps
			if json.Unmarshal(ev.Properties, &statusProps) == nil {
				if statusProps.SessionID != "" && statusProps.SessionID != sessionID {
					continue
				}
			}
			if *assistantMsgID == "" {
				continue
			}
			log.Printf("  SSE session.idle at +%s | tools=%d", time.Since(start).Round(time.Millisecond), *toolCallCount)
			return RunResult{Output: markdownToSlack(*accumulated), Duration: time.Since(start)}, sseActionDone
		}

		if ctx.Err() != nil {
			return RunResult{}, sseActionTimeout
		}
	}

	if ctx.Err() != nil {
		return RunResult{}, sseActionTimeout
	}

	if *assistantMsgID != "" {
		scanErr := scanner.Err()
		log.Printf("WARNING: SSE stream ended unexpectedly (err=%v), assistantMsg=%s", scanErr, *assistantMsgID)
		return RunResult{}, sseActionReconnect
	}

	if scanErr := scanner.Err(); scanErr != nil {
		return RunResult{Duration: time.Since(start), Err: fmt.Errorf("SSE stream error: %w", scanErr)}, sseActionError
	}

	return RunResult{Output: markdownToSlack(*accumulated), Duration: time.Since(start)}, sseActionDone
}
```

**Changes from source:**
- All `serverBaseURL` → `s.cfg.BaseURL()`
- All `agentName` → `s.cfg.AgentName`
- All `runTimeout` → `s.cfg.RunTimeout`
- `buildMessageWithContext` is now a method on `*OpenCodeServer`

- [ ] **Step 2: Verify compilation**

Run: `go build ./...`
Expected: No errors.

- [ ] **Step 3: Commit**

```bash
git add runner.go
git commit -m "feat: add RunAgentStreaming with SSE pre-connect and reconnection"
```

---

## Chunk 5: handler.go + main.go

### Task 5.1: Create handler.go

**Files:**
- Create: `handler.go`

**Source:** `handler.go` from original listener (268 lines)

- [ ] **Step 1: Write handler.go**

```go
package main

import (
	"fmt"
	"log"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
)

const (
	slackMessageLimit        = 3900
	updateInterval           = 800 * time.Millisecond
	processingTimerInterval  = 1500 * time.Millisecond
)

var busy atomic.Bool

// HandleMessageEvent processes a single Slack message event.
func HandleMessageEvent(api *slack.Client, ev *slackevents.MessageEvent, cfg *Config, server *OpenCodeServer) {
	// Filter: allowed user (if configured)
	if cfg.AllowedUserID != "" && ev.User != cfg.AllowedUserID {
		return
	}

	// Filter: ignore bot messages
	if ev.BotID != "" || ev.SubType != "" {
		return
	}

	// Filter: ignore empty messages
	text := strings.TrimSpace(ev.Text)
	if text == "" {
		return
	}

	channel := ev.Channel
	log.Printf("MSG from=%s text=%q", ev.User, text)

	// Concurrency guard
	if !busy.CompareAndSwap(false, true) {
		log.Printf("BUSY — dropping message %q", text)
		api.PostMessage(channel,
			slack.MsgOptionText("⏳ _Please wait, I'm still processing the previous message..._", false),
		)
		return
	}
	defer busy.Store(false)

	// Processing indicator
	_, ts, err := api.PostMessage(channel,
		slack.MsgOptionText("⏳ Processing...", false),
	)
	if err != nil {
		log.Printf("ERROR sending processing indicator: %v", err)
		return
	}
	log.Printf("ACK sent processing indicator ts=%s", ts)

	// Processing timer
	processingDone := make(chan struct{})
	go func() {
		start := time.Now()
		tick := time.NewTicker(processingTimerInterval)
		defer tick.Stop()
		for {
			select {
			case <-tick.C:
				elapsed := int(time.Since(start).Seconds())
				api.UpdateMessage(channel, ts,
					slack.MsgOptionText(fmt.Sprintf("⏳ Processing... (%ds)", elapsed), false),
				)
			case <-processingDone:
				return
			}
		}
	}()

	// Streaming state
	var mu sync.Mutex
	var lastUpdate time.Time
	var latestDisplay string
	var pendingUpdate bool
	var hasTextStarted bool
	var lastProgressText string
	var processingOnce sync.Once

	ticker := time.NewTicker(updateInterval)
	done := make(chan struct{})

	updateSlack := func() {
		if latestDisplay == "" {
			return
		}
		displayText := latestDisplay
		if len(displayText) > slackMessageLimit {
			displayText = displayText[:slackMessageLimit] + "\n\n_(...generating response)_"
		}
		_, _, _, updateErr := api.UpdateMessage(channel, ts,
			slack.MsgOptionText(displayText, false),
		)
		if updateErr != nil {
			log.Printf("ERROR streaming update: %v", updateErr)
		}
		lastUpdate = time.Now()
		pendingUpdate = false
	}

	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				mu.Lock()
				if pendingUpdate {
					updateSlack()
				}
				mu.Unlock()
			case <-done:
				return
			}
		}
	}()

	onStream := func(event StreamEvent) {
		mu.Lock()
		defer mu.Unlock()

		processingOnce.Do(func() { close(processingDone) })

		switch event.Kind {
		case EventProgress:
			if event.ProgressText != lastProgressText {
				lastProgressText = event.ProgressText
				latestDisplay = event.ProgressText
				pendingUpdate = true
				if time.Since(lastUpdate) >= updateInterval {
					updateSlack()
				}
			}
		case EventText:
			if !hasTextStarted {
				hasTextStarted = true
				latestDisplay = event.AccumulatedText + "\n\n⏳ _typing..._"
				updateSlack()
			} else {
				latestDisplay = event.AccumulatedText + "\n\n⏳ _typing..._"
				pendingUpdate = true
			}
		}
	}

	result := server.RunAgentStreaming(text, onStream)
	close(done)

	if result.SessionRotated {
		api.PostMessage(channel,
			slack.MsgOptionText("🔄 _Session restarted for performance._", false),
		)
	}

	// Build final response
	timeoutSecs := int(cfg.RunTimeout.Seconds())
	var responseText string
	switch {
	case result.TimedOut:
		responseText = fmt.Sprintf("⏱️ Timeout — agent took more than %d seconds. Try simplifying your question.", timeoutSecs)
	case result.Err != nil && result.Output == "":
		responseText = fmt.Sprintf("❌ Error: %v", result.Err)
	case result.Output == "":
		responseText = "🤷 The agent returned no response."
	default:
		responseText = result.Output
	}

	// Final update
	if len(responseText) <= slackMessageLimit {
		_, _, _, err = api.UpdateMessage(channel, ts,
			slack.MsgOptionText(responseText, false),
		)
		if err != nil {
			log.Printf("ERROR updating message: %v", err)
		}
	} else {
		chunks := splitMessage(responseText, slackMessageLimit)
		_, _, _, err = api.UpdateMessage(channel, ts,
			slack.MsgOptionText(chunks[0], false),
		)
		if err != nil {
			log.Printf("ERROR updating first chunk: %v", err)
		}
		for i := 1; i < len(chunks); i++ {
			_, _, err = api.PostMessage(channel,
				slack.MsgOptionText(chunks[i], false),
			)
			if err != nil {
				log.Printf("ERROR sending chunk %d: %v", i+1, err)
			}
		}
	}

	log.Printf("DONE duration=%s output_len=%d", result.Duration.Round(time.Second), len(result.Output))
}
```

**Changes from source:**
- `HandleMessageEvent` takes `cfg *Config` and `server *OpenCodeServer` params instead of using globals.
- `AllowedUserID`: uses `cfg.AllowedUserID` with empty=allow-all (no hardcoded fallback).
- All Portuguese strings → English (see spec User-Facing Strings table).
- `maxMessageLen` → `slackMessageLimit` (matches spec constant name).
- `splitMessage` moved to `markdown.go` (already created in chunk 2).
- Timeout message uses `cfg.RunTimeout.Seconds()` instead of hardcoded "2 minutes".

- [ ] **Step 2: Verify compilation**

Run: `go build ./...`
Expected: No errors.

- [ ] **Step 3: Commit**

```bash
git add handler.go
git commit -m "feat: add Slack message handler with streaming UX"
```

### Task 5.2: Write final main.go

**Files:**
- Modify: `main.go` (replace stub)

- [ ] **Step 1: Write main.go**

```go
package main

import (
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/joho/godotenv"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"github.com/slack-go/slack/socketmode"
)

func main() {
	_ = godotenv.Load()

	cfg, err := LoadConfig()
	if err != nil {
		log.Fatalf("Configuration error: %v", err)
	}

	server, err := NewOpenCodeServer(cfg)
	if err != nil {
		log.Fatalf("Failed to start opencode server: %v", err)
	}

	api := slack.New(
		cfg.SlackBotToken,
		slack.OptionAppLevelToken(cfg.SlackAppToken),
	)

	client := socketmode.New(
		api,
		socketmode.OptionLog(log.New(os.Stdout, "socketmode: ", log.Lshortfile|log.LstdFlags)),
	)

	// Graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		sig := <-sigChan
		log.Printf("Received signal %v, shutting down...", sig)
		server.Stop()
		os.Exit(0)
	}()

	// Event handler loop
	go func() {
		for evt := range client.Events {
			switch evt.Type {
			case socketmode.EventTypeEventsAPI:
				eventsAPIEvent, ok := evt.Data.(slackevents.EventsAPIEvent)
				if !ok {
					continue
				}
				client.Ack(*evt.Request)

				switch innerEvent := eventsAPIEvent.InnerEvent.Data.(type) {
				case *slackevents.MessageEvent:
					go HandleMessageEvent(api, innerEvent, cfg, server)
				}

			case socketmode.EventTypeConnecting:
				log.Println("Connecting to Slack Socket Mode...")
			case socketmode.EventTypeConnected:
				log.Println("Connected to Slack Socket Mode")
			case socketmode.EventTypeConnectionError:
				log.Println("Connection error, will retry...")
			default:
			}
		}
	}()

	log.Printf("Starting slack-agent-bridge (agent=%s, port=%d)...", cfg.AgentName, cfg.ServerPort)
	if err := client.Run(); err != nil {
		log.Fatalf("Socket Mode client error: %v", err)
	}
}
```

**Changes from source:**
- No global `ocServer` — `server` is local, passed to handler.
- `LoadConfig()` replaces manual env reading.
- `HandleMessageEvent` receives `cfg` and `server` params.
- Log message says `slack-agent-bridge` instead of `cos-listener`.

- [ ] **Step 2: Verify full build**

Run: `go build -o slack-agent-bridge .`
Expected: Binary created, no errors.

- [ ] **Step 3: Commit**

```bash
git add main.go handler.go
git commit -m "feat: add main.go entrypoint with config loading and Slack Socket Mode"
```

---

## Chunk 6: Makefile, Examples, README

### Task 6.1: Create Makefile

**Files:**
- Create: `Makefile`

- [ ] **Step 1: Write Makefile**

```makefile
BINARY := slack-agent-bridge
AGENT ?= $(error AGENT is required for install/uninstall, e.g. make install AGENT=myagent)
PLIST_LABEL := com.slack-agent-bridge.$(AGENT)
PLIST_DIR := $(HOME)/Library/LaunchAgents
PLIST_PATH := $(PLIST_DIR)/$(PLIST_LABEL).plist

.PHONY: build install uninstall logs

build:
	go build -o $(BINARY) .

install: build
	@echo "Installing $(PLIST_LABEL)..."
	@mkdir -p $(PLIST_DIR)
	@sed -e 's|{{AGENT_NAME}}|$(AGENT)|g' \
		-e 's|{{BINARY_PATH}}|$(CURDIR)/$(BINARY)|g' \
		-e 's|{{WORKING_DIR}}|$(CURDIR)|g' \
		-e 's|{{LOG_DIR}}|$(CURDIR)/logs|g' \
		-e 's|{{PLIST_LABEL}}|$(PLIST_LABEL)|g' \
		examples/launchd.plist.tmpl > $(PLIST_PATH)
	@mkdir -p $(CURDIR)/logs
	launchctl load $(PLIST_PATH)
	@echo "Installed and started $(PLIST_LABEL)"

uninstall:
	@echo "Uninstalling $(PLIST_LABEL)..."
	-launchctl unload $(PLIST_PATH) 2>/dev/null
	-rm -f $(PLIST_PATH)
	@echo "Uninstalled $(PLIST_LABEL)"

logs:
	@tail -f logs/*.log
```

- [ ] **Step 2: Commit**

```bash
git add Makefile
git commit -m "feat: add Makefile with build, install, uninstall, logs targets"
```

### Task 6.2: Create examples/

**Files:**
- Create: `examples/launchd.plist.tmpl`
- Create: `examples/agent.example.md`
- Create: `examples/opencode.example.jsonc`

- [ ] **Step 1: Write examples/launchd.plist.tmpl**

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>{{PLIST_LABEL}}</string>
    <key>ProgramArguments</key>
    <array>
        <string>{{BINARY_PATH}}</string>
    </array>
    <key>WorkingDirectory</key>
    <string>{{WORKING_DIR}}</string>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <true/>
    <key>StandardOutPath</key>
    <string>{{LOG_DIR}}/stdout.log</string>
    <key>StandardErrorPath</key>
    <string>{{LOG_DIR}}/stderr.log</string>
</dict>
</plist>
```

- [ ] **Step 2: Write examples/agent.example.md**

```markdown
---
description: "Example agent for slack-agent-bridge"
model: gpt-4.1
mode: primary
tools:
  read: true
  write: true
  edit: true
  bash: true
---

You are a helpful assistant.

## Bootstrap Levels

Classify each message and load only the context needed:

### Level 1 — Immediate response (no file reads)
Greetings, general questions. Zero tool calls.

### Level 2 — Partial bootstrap (only needed files)
Questions about tasks, projects, status. State files are already
injected in the message — do NOT re-read them from disk.

### Level 3 — Full bootstrap (rituals)
Daily planning, reviews. Read additional files as needed.

## State Injection

The bridge injects state files at the start of each message.
Look for `[Injected state]` and `[Daily cache]` headers.
Do NOT read these files from disk — they're already in the message.
```

- [ ] **Step 3: Write examples/opencode.example.jsonc**

```jsonc
{
  // Minimal opencode config for use with slack-agent-bridge
  "provider": {
    "github-copilot": {}
  },
  // Agent is defined in ~/.config/opencode/agents/<agent-name>.md
  // AGENT_NAME env var must match the agent file name (without .md)
}
```

- [ ] **Step 4: Commit**

```bash
git add examples/
git commit -m "feat: add example files (launchd plist, agent prompt, opencode config)"
```

### Task 6.3: Create README.md

**Files:**
- Create: `README.md`

- [ ] **Step 1: Write README.md**

A concise README covering:
- What it does (1 paragraph)
- Quick start (5 steps: create Slack app, configure .env, build, run)
- Configuration table (Required + Optional env vars — copy from spec)
- State injection explanation
- Running as a service (launchd via Makefile)
- Multiple agents section
- Link to spec for architecture details

Keep under 150 lines. Reference `.env.example` and `examples/` for details.

- [ ] **Step 2: Commit**

```bash
git add README.md
git commit -m "docs: add README with setup guide and configuration reference"
```

### Task 6.4: Final verification

- [ ] **Step 1: Full build**

Run: `go build -o slack-agent-bridge .`
Expected: Binary created successfully.

- [ ] **Step 2: Verify all files present**

Run: `ls -la *.go Makefile README.md LICENSE .env.example .gitignore examples/`
Expected: All files from spec file structure present.

- [ ] **Step 3: Run go vet**

Run: `go vet ./...`
Expected: No issues.

- [ ] **Step 4: Final commit (if any changes)**

```bash
git add -A
git commit -m "chore: final cleanup and verification"
```
