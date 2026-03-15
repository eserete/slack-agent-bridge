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
	AllowedUserID         string
	ServerPort            int
	MaxMessagesPerSession int
	MaxSessionAge         time.Duration
	RunTimeout            time.Duration
	HealthTimeout         time.Duration
	ExtraPath             string

	// State injection
	StateFiles       []string // relative paths to AGENT_DIR
	DailyCacheFiles  []string // relative paths, only injected if modified today
	DailyCacheHeader string
}

// LoadConfig reads configuration from environment variables.
func LoadConfig() (*Config, error) {
	c := &Config{
		SlackAppToken:         os.Getenv("SLACK_APP_TOKEN"),
		SlackBotToken:         os.Getenv("SLACK_BOT_TOKEN"),
		AgentName:             os.Getenv("AGENT_NAME"),
		AgentDir:              os.Getenv("AGENT_DIR"),
		AllowedUserID:         os.Getenv("ALLOWED_USER_ID"),
		ExtraPath:             os.Getenv("EXTRA_PATH"),
		DailyCacheHeader:      os.Getenv("DAILY_CACHE_HEADER"),
		ServerPort:            14899,
		MaxMessagesPerSession: 10,
		MaxSessionAge:         60 * time.Minute,
		RunTimeout:            120 * time.Second,
		HealthTimeout:         30 * time.Second,
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
