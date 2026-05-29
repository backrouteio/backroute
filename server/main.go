package main

import (
	"embed"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

//go:embed dashboard/*
var dashboardFS embed.FS

type agent struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	Online   bool      `json:"online"`
	LastSeen time.Time `json:"lastSeen"`
}

type message struct {
	Type  string `json:"type"`
	Name  string `json:"name,omitempty"`
	Token string `json:"token,omitempty"`
	Time  string `json:"time,omitempty"`
}

type registry struct {
	mu     sync.RWMutex
	agents map[string]agent
}

func main() {
	addr := getenv("BACKROUTE_ADDR", ":8080")
	token := getenv("BACKROUTE_TOKEN", "dev-token")
	reg := &registry{agents: map[string]agent{}}

	mux := http.NewServeMux()
	mux.HandleFunc("/agent", handleAgent(reg, token))
	mux.HandleFunc("/api/agents", handleAgents(reg))
	mux.Handle("/", http.FileServer(http.FS(dashboardFS)))

	log.Printf("BackRoute server listening on %s", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatal(err)
	}
}

func handleAgent(reg *registry, expectedToken string) http.HandlerFunc {
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}

	return func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			log.Printf("upgrade failed: %v", err)
			return
		}
		defer conn.Close()

		var auth message
		if err := conn.ReadJSON(&auth); err != nil {
			log.Printf("auth read failed: %v", err)
			return
		}
		if auth.Type != "auth" || auth.Token != expectedToken || auth.Name == "" {
			_ = conn.WriteJSON(message{Type: "auth_failed"})
			return
		}

		id := auth.Name
		reg.set(agent{ID: id, Name: auth.Name, Online: true, LastSeen: time.Now().UTC()})
		defer reg.offline(id)

		_ = conn.WriteJSON(message{Type: "auth_ok"})
		log.Printf("agent online: %s", id)

		for {
			var msg message
			if err := conn.ReadJSON(&msg); err != nil {
				log.Printf("agent offline: %s: %v", id, err)
				return
			}
			if msg.Type == "heartbeat" {
				reg.touch(id)
			}
		}
	}
}

func handleAgents(reg *registry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(reg.list())
	}
}

func (r *registry) set(a agent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.agents[a.ID] = a
}

func (r *registry) touch(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	a := r.agents[id]
	a.Online = true
	a.LastSeen = time.Now().UTC()
	r.agents[id] = a
}

func (r *registry) offline(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	a := r.agents[id]
	a.Online = false
	a.LastSeen = time.Now().UTC()
	r.agents[id] = a
}

func (r *registry) list() []agent {
	r.mu.RLock()
	defer r.mu.RUnlock()
	items := make([]agent, 0, len(r.agents))
	for _, a := range r.agents {
		items = append(items, a)
	}
	return items
}

func getenv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
