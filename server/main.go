package main

import (
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
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
	SSH      *sshRoute `json:"ssh,omitempty"`
}

type sshRoute struct {
	Port      int    `json:"port"`
	AgentName string `json:"agentName"`
	Target    string `json:"target"`
}

type message struct {
	Type   string `json:"type"`
	Name   string `json:"name,omitempty"`
	Token  string `json:"token,omitempty"`
	Time   string `json:"time,omitempty"`
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
	mu       sync.RWMutex
	agents   map[string]agent
	sessions map[string]*agentSession
	routes   map[string]sshRoute
}

func main() {
	addr := getenv("BACKROUTE_ADDR", ":8080")
	token := getenv("BACKROUTE_TOKEN", "dev-token")
	routes := mustParseSSHRoutes()
	reg := &registry{
		agents:   map[string]agent{},
		sessions: map[string]*agentSession{},
		routes:   routesByAgent(routes),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/agent", handleAgent(reg, token))
	mux.HandleFunc("/api/agents", handleAgents(reg))
	mux.Handle("/", http.FileServer(http.FS(dashboardFS)))

	for _, route := range routes {
		go listenSSH(reg, route)
	}

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

func listenSSH(reg *registry, route sshRoute) {
	addr := fmt.Sprintf(":%d", route.Port)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		log.Printf("ssh listener failed on %s: %v", addr, err)
		return
	}
	log.Printf("BackRoute SSH tunnel listening on %s for agent %s -> %s", addr, route.AgentName, route.Target)
	for {
		client, err := listener.Accept()
		if err != nil {
			log.Printf("ssh accept failed: %v", err)
			continue
		}
		go handleSSHClient(reg, client, route)
	}
}

func handleSSHClient(reg *registry, client net.Conn, route sshRoute) {
	session := reg.session(route.AgentName)
	if session == nil {
		log.Printf("ssh client rejected: agent %s is offline", route.AgentName)
		_ = client.Close()
		return
	}

	session.setTCP(client)
	if err := session.writeJSON(message{Type: "tcp_open", Target: route.Target}); err != nil {
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
		if route, ok := r.routes[a.ID]; ok {
			routeCopy := route
			a.SSH = &routeCopy
		}
		items = append(items, a)
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].Name < items[j].Name
	})
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

func mustParseSSHRoutes() []sshRoute {
	routes, err := parseSSHRoutes(os.Getenv("BACKROUTE_SSH_ROUTES"))
	if err != nil {
		log.Fatalf("invalid BACKROUTE_SSH_ROUTES: %v", err)
	}
	if len(routes) > 0 {
		return routes
	}

	portValue := strings.TrimPrefix(getenv("BACKROUTE_SSH_ADDR", ":2222"), ":")
	port, err := strconv.Atoi(portValue)
	if err != nil {
		log.Fatalf("invalid BACKROUTE_SSH_ADDR: %s", portValue)
	}

	return []sshRoute{{
		Port:      port,
		AgentName: getenv("BACKROUTE_SSH_AGENT", "office-ubuntu-01"),
		Target:    getenv("BACKROUTE_SSH_TARGET", "127.0.0.1:22"),
	}}
}

func parseSSHRoutes(value string) ([]sshRoute, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}

	entries := strings.Split(value, ",")
	routes := make([]sshRoute, 0, len(entries))
	seenPorts := map[int]bool{}
	for _, entry := range entries {
		parts := strings.Split(strings.TrimSpace(entry), ":")
		if len(parts) != 4 {
			return nil, fmt.Errorf("route %q must use port:agent:host:targetPort", entry)
		}

		port, err := strconv.Atoi(parts[0])
		if err != nil || port <= 0 || port > 65535 {
			return nil, fmt.Errorf("route %q has invalid listen port", entry)
		}
		if seenPorts[port] {
			return nil, fmt.Errorf("duplicate listen port %d", port)
		}
		seenPorts[port] = true

		targetPort, err := strconv.Atoi(parts[3])
		if err != nil || targetPort <= 0 || targetPort > 65535 {
			return nil, fmt.Errorf("route %q has invalid target port", entry)
		}
		if parts[1] == "" || parts[2] == "" {
			return nil, fmt.Errorf("route %q has empty agent or target host", entry)
		}

		routes = append(routes, sshRoute{
			Port:      port,
			AgentName: parts[1],
			Target:    net.JoinHostPort(parts[2], parts[3]),
		})
	}
	return routes, nil
}

func routesByAgent(routes []sshRoute) map[string]sshRoute {
	byAgent := make(map[string]sshRoute, len(routes))
	for _, route := range routes {
		byAgent[route.AgentName] = route
	}
	return byAgent
}
