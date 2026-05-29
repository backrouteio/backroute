package main

import (
	"encoding/json"
	"flag"
	"log"
	"time"

	"github.com/gorilla/websocket"
)

type message struct {
	Type  string `json:"type"`
	Name  string `json:"name,omitempty"`
	Token string `json:"token,omitempty"`
	Time  string `json:"time,omitempty"`
}

func main() {
	serverURL := flag.String("server", "ws://localhost:8080/agent", "BackRoute server WebSocket URL")
	token := flag.String("token", "dev-token", "agent authentication token")
	name := flag.String("name", "office-ubuntu-01", "agent display name")
	flag.Parse()

	for {
		if err := run(*serverURL, *token, *name); err != nil {
			log.Printf("agent disconnected: %v", err)
			time.Sleep(5 * time.Second)
		}
	}
}

func run(serverURL, token, name string) error {
	conn, _, err := websocket.DefaultDialer.Dial(serverURL, nil)
	if err != nil {
		return err
	}
	defer conn.Close()

	if err := conn.WriteJSON(message{Type: "auth", Name: name, Token: token}); err != nil {
		return err
	}

	log.Printf("connected to %s as %s", serverURL, name)

	done := make(chan error, 1)
	go func() {
		for {
			_, payload, err := conn.ReadMessage()
			if err != nil {
				done <- err
				return
			}

			var msg message
			if err := json.Unmarshal(payload, &msg); err != nil {
				log.Printf("received non-json message: %s", string(payload))
				continue
			}
			log.Printf("server message: %s", msg.Type)
		}
	}()

	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case err := <-done:
			return err
		case <-ticker.C:
			if err := conn.WriteJSON(message{Type: "heartbeat", Time: time.Now().UTC().Format(time.RFC3339)}); err != nil {
				return err
			}
		}
	}
}
