package main

import (
	"embed"
	"encoding/json"
	"io"
	"log"
	"net"
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
	Target string `json:"target,omitempty"`
}

type agentSession struct {
	name     string
	conn     *websocket.Conn
	writeMu  sync.Mutex
	tcpMu    sync.Mutex
	tcpConn  net.Conn
}

func (s *agentSession) writeJSON(v any) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.conn.WriteJSON(v)
}

func (s *agentSession) writeBinary(payload []byte) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.conn.WriteMessage(websocket.BinaryMessage, payload)
}

func (s *agentSession) setTCP(conn net.Conn) {
	s.tcpMu.Lock()
	defer s.tcpMu.Unlock()
	if s.tcpConn != nil {
		_ = s.tcpConn.Close()
	}
	s.tcpConn = conn
}

func (s *agentSession) closeTCP() {
	s.tcpMu.Lock()
	defer s.tcpMu.Unlock()
	if s.tcpConn != nil {
		_ = s.tcpConn.Close()
		s.tcpConn = nil
	}
}

func (s *agentSession) writeTCP(payload []byte) {
	s.tcpMu.Lock()
	conn := s.tcpConn
	s.tcpMu.Unlock()
	if conn == nil {
		return
	}
	if _, err := conn.Write(payload); err != nil {
		log.Printf("ssh client write failed: %v", err)
		s.closeTCP()
	}
}

type registry struct {
	mu     sync.RWMutex
	agents map[string]agent
	sessions map[string]*agentSession
}

func main() {
	addr := getenv("BACKROUTE_ADDR", ":8080")
	token := getenv("BACKROUTE_TOKEN", "dev-token")
	sshAddr := getenv("BACKROUTE_SSH_ADDR", ":2222")
	sshAgent := getenv("BACKROUTE_SSH_AGENT", "office-ubuntu-01")
	sshTarget := getenv("BACKROUTE_SSH_TARGET", "127.0.0.1:22")
	reg := &registry{agents: map[string]agent{}, sessions: map[string]*agentSession{}}

	mux := http.NewServeMux()
	mux.HandleFunc("/agent", handleAgent(reg, token))
	mux.HandleFunc("/api/agents", handleAgents(reg))
	mux.Handle("/", http.FileServer(http.FS(dashboardFS)))

	go listenSSH(reg, sshAddr, sshAgent, sshTarget)

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
		session := &agentSession{name: id, conn: conn}
		reg.set(agent{ID: id, Name: auth.Name, Online: true, LastSeen: time.Now().UTC()}, session)
		defer reg.offline(id)

		_ = session.writeJSON(message{Type: "auth_ok"})
		log.Printf("agent online: %s", id)

		for {
			messageType, payload, err := conn.ReadMessage()
			if err != nil {
				log.Printf("agent offline: %s: %v", id, err)
				return
			}
			if messageType == websocket.BinaryMessage {
				session.writeTCP(payload)
				continue
			}
			var msg message
			if err := json.Unmarshal(payload, &msg); err != nil {
				log.Printf("agent message parse failed: %v", err)
				continue
			}
			if msg.Type == "heartbeat" {
				reg.touch(id)
			} else if msg.Type == "tcp_close" {
				session.closeTCP()
			}
		}
	}
}

func listenSSH(reg *registry, addr, agentName, target string) {
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		log.Printf("ssh listener failed on %s: %v", addr, err)
		return
	}
	log.Printf("BackRoute SSH tunnel listening on %s for agent %s -> %s", addr, agentName, target)
	for {
		client, err := listener.Accept()
		if err != nil {
			log.Printf("ssh accept failed: %v", err)
			continue
		}
		go handleSSHClient(reg, client, agentName, target)
	}
}

func handleSSHClient(reg *registry, client net.Conn, agentName, target string) {
	session := reg.session(agentName)
	if session == nil {
		log.Printf("ssh client rejected: agent %s is offline", agentName)
		_ = client.Close()
		return
	}

	session.setTCP(client)
	if err := session.writeJSON(message{Type: "tcp_open", Target: target}); err != nil {
		log.Printf("failed to open ssh tunnel: %v", err)
		session.closeTCP()
		return
	}

	buf := make([]byte, 32*1024)
	for {
		n, err := client.Read(buf)
		if n > 0 {
			if writeErr := session.writeBinary(buf[:n]); writeErr != nil {
				log.Printf("agent write failed: %v", writeErr)
				session.closeTCP()
				return
			}
		}
		if err != nil {
			if err != io.EOF {
				log.Printf("ssh client read failed: %v", err)
			}
			_ = session.writeJSON(message{Type: "tcp_close"})
			session.closeTCP()
			return
		}
	}
}

func handleAgents(reg *registry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(reg.list())
	}
}

func (r *registry) set(a agent, session *agentSession) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.agents[a.ID] = a
	r.sessions[a.ID] = session
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
	if session := r.sessions[id]; session != nil {
		session.closeTCP()
	}
	delete(r.sessions, id)
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

func (r *registry) session(id string) *agentSession {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.sessions[id]
}

func getenv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
