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
