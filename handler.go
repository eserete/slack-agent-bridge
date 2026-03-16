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
	slackMessageLimit       = 3900
	updateInterval          = 800 * time.Millisecond
	processingTimerInterval = 1500 * time.Millisecond
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
