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
	debug := getenv("BACKROUTE_DEBUG", "false") == "true"
	routes := mustParseSSHRoutes()
	reg := &registry{
		agents:   map[string]agent{},
		sessions: map[string]*agentSession{},
		routes:   routesByAgent(routes),
	}

	log.Printf("startup: BackRoute server booting")
	log.Printf("startup: dashboard/API/agent HTTP address=%s", addr)
	log.Printf("startup: debug logging=%v", debug)
	log.Printf("startup: configured SSH routes=%d", len(routes))
	for _, route := range routes {
		log.Printf("startup: route port=%d agent=%s target=%s", route.Port, route.AgentName, route.Target)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/agent", handleAgent(reg, token, debug))
	mux.HandleFunc("/api/agents", handleAgents(reg))
	mux.HandleFunc("/api/agents/clear-offline", handleClearOfflineAgents(reg))
	mux.Handle("/", http.FileServer(http.FS(dashboardFS)))

	for _, route := range routes {
		go listenSSH(reg, route)
	}

	log.Printf("BackRoute server listening on %s", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatal(err)
	}
}

func handleAgent(reg *registry, expectedToken string, debug bool) http.HandlerFunc {
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}

	return func(w http.ResponseWriter, r *http.Request) {
		remote := r.RemoteAddr
		log.Printf("agent: websocket connection attempt remote=%s", remote)

		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			log.Printf("agent: websocket upgrade failed remote=%s error=%v", remote, err)
			return
		}
		defer conn.Close()

		var auth message
		if err := conn.ReadJSON(&auth); err != nil {
			log.Printf("agent: auth read failed remote=%s error=%v", remote, err)
			return
		}
		if auth.Type != "auth" || auth.Token != expectedToken || auth.Name == "" {
			log.Printf("agent: auth failed remote=%s name=%q type=%q token_match=%v", remote, auth.Name, auth.Type, auth.Token == expectedToken)
			_ = conn.WriteJSON(message{Type: "auth_failed"})
			return
		}

		id := auth.Name
		session := &agentSession{name: id, conn: conn}
		reg.set(agent{ID: id, Name: auth.Name, Online: true, LastSeen: time.Now().UTC()}, session)
		defer reg.offline(id)

		_ = session.writeJSON(message{Type: "auth_ok"})
		log.Printf("agent: auth ok name=%s remote=%s", id, remote)
		log.Printf("agent: online name=%s active_agents=%d", id, reg.countOnline())

		for {
			messageType, payload, err := conn.ReadMessage()
			if err != nil {
				log.Printf("agent: disconnected name=%s remote=%s error=%v", id, remote, err)
				return
			}
			if messageType == websocket.BinaryMessage {
				if debug {
					log.Printf("agent: binary payload name=%s bytes=%d direction=agent_to_ssh_client", id, len(payload))
				}
				session.writeTCP(payload)
				continue
			}
			var msg message
			if err := json.Unmarshal(payload, &msg); err != nil {
				log.Printf("agent: message parse failed name=%s error=%v", id, err)
				continue
			}
			if msg.Type == "heartbeat" {
				reg.touch(id)
				if debug {
					log.Printf("agent: heartbeat name=%s time=%s", id, msg.Time)
				}
			} else if msg.Type == "tcp_close" {
				log.Printf("tunnel: close requested by agent name=%s", id)
				session.closeTCP()
			} else {
				log.Printf("agent: unhandled message name=%s type=%s", id, msg.Type)
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
	remote := client.RemoteAddr()
	log.Printf("ssh: client connected remote=%s public_port=%d route_agent=%s target=%s", remote, route.Port, route.AgentName, route.Target)

	session := reg.session(route.AgentName)
	if session == nil {
		log.Printf("ssh: rejected remote=%s reason=agent_offline agent=%s", remote, route.AgentName)
		_ = client.Close()
		return
	}

	session.setTCP(client)
	if err := session.writeJSON(message{Type: "tcp_open", Target: route.Target}); err != nil {
		log.Printf("ssh: failed to send tcp_open remote=%s agent=%s target=%s error=%v", remote, route.AgentName, route.Target, err)
		session.closeTCP()
		return
	}
	log.Printf("tunnel: opened remote=%s public_port=%d agent=%s target=%s", remote, route.Port, route.AgentName, route.Target)

	buf := make([]byte, 32*1024)
	for {
		n, err := client.Read(buf)
		if n > 0 {
			if writeErr := session.writeBinary(buf[:n]); writeErr != nil {
				log.Printf("tunnel: write to agent failed remote=%s agent=%s bytes=%d error=%v", remote, route.AgentName, n, writeErr)
				session.closeTCP()
				return
			}
		}
		if err != nil {
			if err != io.EOF {
				log.Printf("ssh: client read failed remote=%s error=%v", remote, err)
			}
			_ = session.writeJSON(message{Type: "tcp_close"})
			session.closeTCP()
			log.Printf("tunnel: closed remote=%s public_port=%d agent=%s", remote, route.Port, route.AgentName)
			return
		}
	}
}

func handleAgents(reg *registry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(reg.list())
	}
}

func handleClearOfflineAgents(reg *registry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		cleared := reg.clearOffline()
		log.Printf("dashboard: cleared offline agents count=%d", cleared)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]int{"cleared": cleared})
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

func (r *registry) countOnline() int {
	r.mu.RLock()
	defer r.mu.RUnlock()

	count := 0
	for _, a := range r.agents {
		if a.Online {
			count++
		}
	}
	return count
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

func (r *registry) clearOffline() int {
	r.mu.Lock()
	defer r.mu.Unlock()

	cleared := 0
	for id, a := range r.agents {
		if a.Online {
			continue
		}
		delete(r.agents, id)
		cleared++
	}
	return cleared
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
