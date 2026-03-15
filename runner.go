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
	sseActionDone sseAction = iota
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
					Err:            fmt.Errorf("SSE connection lost after %d reconnects", maxSSEReconnects),
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
