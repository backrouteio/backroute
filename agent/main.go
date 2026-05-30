package main

import (
	"encoding/json"
	"flag"
	"io"
	"log"
	"net"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

type message struct {
	Type  string `json:"type"`
	Name  string `json:"name,omitempty"`
	Token string `json:"token,omitempty"`
	Time  string `json:"time,omitempty"`
	Target string `json:"target,omitempty"`
}

type safeWriter struct {
	mu   sync.Mutex
	conn *websocket.Conn
}

func (w *safeWriter) writeJSON(v any) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.conn.WriteJSON(v)
}

func (w *safeWriter) writeBinary(payload []byte) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.conn.WriteMessage(websocket.BinaryMessage, payload)
}

func main() {
	serverURL := flag.String("server", "ws://localhost:8080/agent", "BackRoute server WebSocket URL")
	token := flag.String("token", "dev-token", "agent authentication token")
	name := flag.String("name", "office-ubuntu-01", "agent display name")
	sshTarget := flag.String("ssh-target", "127.0.0.1:22", "local SSH target forwarded by this agent")
	flag.Parse()

	for {
		if err := run(*serverURL, *token, *name, *sshTarget); err != nil {
			log.Printf("agent disconnected: %v", err)
			time.Sleep(5 * time.Second)
		}
	}
}

func run(serverURL, token, name, sshTarget string) error {
	conn, _, err := websocket.DefaultDialer.Dial(serverURL, nil)
	if err != nil {
		return err
	}
	defer conn.Close()
	writer := &safeWriter{conn: conn}

	if err := writer.writeJSON(message{Type: "auth", Name: name, Token: token}); err != nil {
		return err
	}

	log.Printf("connected to %s as %s", serverURL, name)

	done := make(chan error, 1)
	var localMu sync.Mutex
	var local net.Conn
	closeLocal := func() {
		localMu.Lock()
		defer localMu.Unlock()
		if local != nil {
			_ = local.Close()
			local = nil
		}
	}

	go func() {
		for {
			messageType, payload, err := conn.ReadMessage()
			if err != nil {
				done <- err
				return
			}

			if messageType == websocket.BinaryMessage {
				localMu.Lock()
				dst := local
				localMu.Unlock()
				if dst != nil {
					if _, err := dst.Write(payload); err != nil {
						log.Printf("ssh target write failed: %v", err)
						closeLocal()
					}
				}
				continue
			}

			var msg message
			if err := json.Unmarshal(payload, &msg); err != nil {
				log.Printf("received non-json message: %s", string(payload))
				continue
			}
			log.Printf("server message: %s", msg.Type)
			switch msg.Type {
			case "tcp_open":
				closeLocal()
				target := msg.Target
				if target == "" {
					target = sshTarget
				}
				dst, err := net.Dial("tcp", target)
				if err != nil {
					log.Printf("ssh target dial failed for %s: %v", target, err)
					_ = writer.writeJSON(message{Type: "tcp_close"})
					continue
				}
				localMu.Lock()
				local = dst
				localMu.Unlock()
				log.Printf("ssh tunnel connected to %s", target)
				go func(c net.Conn) {
					defer closeLocal()
					buf := make([]byte, 32*1024)
					for {
						n, err := c.Read(buf)
						if n > 0 {
							if writeErr := writer.writeBinary(buf[:n]); writeErr != nil {
								log.Printf("server write failed: %v", writeErr)
								return
							}
						}
						if err != nil {
							if err != io.EOF {
								log.Printf("ssh target read failed: %v", err)
							}
							_ = writer.writeJSON(message{Type: "tcp_close"})
							return
						}
					}
				}(dst)
			case "tcp_close":
				closeLocal()
			}
		}
	}()

	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case err := <-done:
			return err
		case <-ticker.C:
			if err := writer.writeJSON(message{Type: "heartbeat", Time: time.Now().UTC().Format(time.RFC3339)}); err != nil {
				return err
			}
		}
	}
}
