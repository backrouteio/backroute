package main

import (
	"context"
	"crypto/subtle"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
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
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Online      bool      `json:"online"`
	SourceIP    string    `json:"sourceIp"`
	Location    string    `json:"location"`
	ConnectedAt time.Time `json:"connectedAt"`
	LastSeen    time.Time `json:"lastSeen"`
	ActiveFor   string    `json:"activeFor"`
	SSH         *sshRoute `json:"ssh,omitempty"`
}

type sshRoute struct {
	Port      int    `json:"port"`
	AgentName string `json:"agentName"`
	Target    string `json:"target"`
}

type createRouteRequest struct {
	Name   string `json:"name"`
	Target string `json:"target"`
	Port   int    `json:"port"`
}

type routeResponse struct {
	Route sshRoute `json:"route"`
}

type dashboardConfig struct {
	AgentToken string `json:"agentToken"`
	PortStart  int    `json:"portStart"`
	PortEnd    int    `json:"portEnd"`
}

type message struct {
	Type   string `json:"type"`
	Name   string `json:"name,omitempty"`
	Token  string `json:"token,omitempty"`
	Time   string `json:"time,omitempty"`
	Target string `json:"target,omitempty"`
}

type agentSession struct {
	name    string
	conn    *websocket.Conn
	writeMu sync.Mutex
	tcpMu   sync.Mutex
	tcpConn net.Conn
}

type geoIPResolver struct {
	enabled bool
	client  *http.Client
	mu      sync.RWMutex
	cache   map[string]string
}

type geoIPResponse struct {
	Status     string `json:"status"`
	Message    string `json:"message"`
	Country    string `json:"country"`
	RegionName string `json:"regionName"`
	City       string `json:"city"`
	ISP        string `json:"isp"`
	Query      string `json:"query"`
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
	mu             sync.RWMutex
	agents         map[string]agent
	sessions       map[string]*agentSession
	routes         map[string]sshRoute
	routeListeners map[string]net.Listener
	portStart      int
	portEnd        int
}

const (
	defaultDynamicPortStart = 2222
	defaultDynamicPortEnd   = 2999
)

func main() {
	addr := getenv("BACKROUTE_ADDR", ":8080")
	token := getenv("BACKROUTE_TOKEN", "dev-token")
	debug := getenv("BACKROUTE_DEBUG", "false") == "true"
	dashboardUser := os.Getenv("BACKROUTE_DASHBOARD_USER")
	dashboardPassword := os.Getenv("BACKROUTE_DASHBOARD_PASSWORD")
	geoIP := newGeoIPResolver(getenv("BACKROUTE_GEOIP_ENABLED", "true") == "true")
	portStart := getenvInt("BACKROUTE_PORT_START", defaultDynamicPortStart)
	portEnd := getenvInt("BACKROUTE_PORT_END", defaultDynamicPortEnd)
	if portEnd < portStart {
		log.Fatalf("invalid dynamic port range: BACKROUTE_PORT_END must be greater than or equal to BACKROUTE_PORT_START")
	}
	routes := mustParseSSHRoutes()
	reg := &registry{
		agents:         map[string]agent{},
		sessions:       map[string]*agentSession{},
		routes:         map[string]sshRoute{},
		routeListeners: map[string]net.Listener{},
		portStart:      portStart,
		portEnd:        portEnd,
	}

	log.Printf("startup: BackRoute server booting")
	log.Printf("startup: dashboard/API/agent HTTP address=%s", addr)
	log.Printf("startup: debug logging=%v", debug)
	log.Printf("startup: geoip lookup enabled=%v", geoIP.enabled)
	log.Printf("startup: dynamic SSH port range=%d-%d", reg.portStart, reg.portEnd)
	if dashboardUser != "" && dashboardPassword != "" {
		log.Printf("startup: dashboard basic auth enabled user=%s", dashboardUser)
	} else {
		log.Printf("startup: dashboard basic auth disabled; set BACKROUTE_DASHBOARD_USER and BACKROUTE_DASHBOARD_PASSWORD to protect the portal")
	}
	log.Printf("startup: configured SSH routes=%d", len(routes))
	for _, route := range routes {
		log.Printf("startup: route port=%d agent=%s target=%s", route.Port, route.AgentName, route.Target)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/agent", handleAgent(reg, token, debug, geoIP))
	mux.HandleFunc("/api/config", requireBasicAuth(handleConfig(reg, token), dashboardUser, dashboardPassword))
	mux.HandleFunc("/api/agents", requireBasicAuth(handleAgents(reg), dashboardUser, dashboardPassword))
	mux.HandleFunc("/api/agents/clear-offline", requireBasicAuth(handleClearOfflineAgents(reg), dashboardUser, dashboardPassword))
	mux.HandleFunc("/api/routes", requireBasicAuth(handleRoutes(reg), dashboardUser, dashboardPassword))
	mux.HandleFunc("/api/routes/", requireBasicAuth(handleRouteByName(reg), dashboardUser, dashboardPassword))
	mux.Handle("/", requireBasicAuth(http.FileServer(http.FS(dashboardFS)).ServeHTTP, dashboardUser, dashboardPassword))

	for _, route := range routes {
		if err := reg.addRoute(route); err != nil {
			log.Fatalf("failed to start configured SSH route agent=%s port=%d: %v", route.AgentName, route.Port, err)
		}
	}

	log.Printf("BackRoute server listening on %s", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatal(err)
	}
}

func requireBasicAuth(next http.HandlerFunc, expectedUser, expectedPassword string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if expectedUser == "" || expectedPassword == "" {
			next(w, r)
			return
		}

		user, password, ok := r.BasicAuth()
		if !ok || !secureEqual(user, expectedUser) || !secureEqual(password, expectedPassword) {
			w.Header().Set("WWW-Authenticate", `Basic realm="BackRoute Dashboard"`)
			http.Error(w, "authentication required", http.StatusUnauthorized)
			return
		}

		next(w, r)
	}
}

func secureEqual(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

func handleAgent(reg *registry, expectedToken string, debug bool, geoIP *geoIPResolver) http.HandlerFunc {
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
		now := time.Now().UTC()
		sourceIP := sourceIPFromRemote(remote)
		location := geoIP.lookup(r.Context(), sourceIP)
		session := &agentSession{name: id, conn: conn}
		reg.set(agent{
			ID:          id,
			Name:        auth.Name,
			Online:      true,
			SourceIP:    sourceIP,
			Location:    location,
			ConnectedAt: now,
			LastSeen:    now,
		}, session)
		defer reg.offline(id)

		_ = session.writeJSON(message{Type: "auth_ok"})
		log.Printf("agent: auth ok name=%s remote=%s source_ip=%s location=%q", id, remote, sourceIP, location)
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

func serveSSHListener(reg *registry, listener net.Listener, route sshRoute) {
	log.Printf("BackRoute SSH tunnel listening on :%d for agent %s -> %s", route.Port, route.AgentName, route.Target)
	for {
		client, err := listener.Accept()
		if err != nil {
			log.Printf("ssh listener stopped port=%d agent=%s error=%v", route.Port, route.AgentName, err)
			return
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

func handleConfig(reg *registry, token string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(dashboardConfig{
			AgentToken: token,
			PortStart:  reg.portStart,
			PortEnd:    reg.portEnd,
		})
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

func handleRoutes(reg *registry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var req createRouteRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}

		name := strings.TrimSpace(req.Name)
		if name == "" {
			http.Error(w, "node name is required", http.StatusBadRequest)
			return
		}

		target := strings.TrimSpace(req.Target)
		if target == "" {
			target = "127.0.0.1:22"
		}

		port := req.Port
		if port == 0 {
			nextPort, err := reg.nextAvailablePort()
			if err != nil {
				http.Error(w, err.Error(), http.StatusConflict)
				return
			}
			port = nextPort
		}

		route := sshRoute{Port: port, AgentName: name, Target: target}
		if err := reg.addRoute(route); err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}

		log.Printf("dashboard: created route agent=%s port=%d target=%s", route.AgentName, route.Port, route.Target)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(routeResponse{Route: route})
	}
}

func handleRouteByName(reg *registry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		name := strings.TrimPrefix(r.URL.Path, "/api/routes/")
		name, err := url.PathUnescape(name)
		if err != nil || strings.TrimSpace(name) == "" {
			http.Error(w, "invalid route name", http.StatusBadRequest)
			return
		}

		if !reg.deleteRoute(name) {
			http.Error(w, "route not found", http.StatusNotFound)
			return
		}

		log.Printf("dashboard: deleted route agent=%s", name)
		w.WriteHeader(http.StatusNoContent)
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
	a.ActiveFor = formatDuration(a.LastSeen.Sub(a.ConnectedAt))
	r.agents[id] = a
	if session := r.sessions[id]; session != nil {
		session.closeTCP()
	}
	delete(r.sessions, id)
}

func (r *registry) addRoute(route sshRoute) error {
	if route.Port < 1 || route.Port > 65535 {
		return fmt.Errorf("invalid SSH port %d", route.Port)
	}
	if strings.TrimSpace(route.AgentName) == "" {
		return fmt.Errorf("node name is required")
	}
	if strings.TrimSpace(route.Target) == "" {
		return fmt.Errorf("target is required")
	}

	listener, err := net.Listen("tcp", fmt.Sprintf(":%d", route.Port))
	if err != nil {
		return fmt.Errorf("could not listen on port %d: %w", route.Port, err)
	}

	r.mu.Lock()
	if _, exists := r.routes[route.AgentName]; exists {
		r.mu.Unlock()
		_ = listener.Close()
		return fmt.Errorf("node %s already exists", route.AgentName)
	}
	if r.portInUseLocked(route.Port) {
		r.mu.Unlock()
		_ = listener.Close()
		return fmt.Errorf("port %d is already assigned", route.Port)
	}
	r.routes[route.AgentName] = route
	r.routeListeners[route.AgentName] = listener
	r.mu.Unlock()

	go serveSSHListener(r, listener, route)
	return nil
}

func (r *registry) deleteRoute(name string) bool {
	r.mu.Lock()
	listener, exists := r.routeListeners[name]
	delete(r.routeListeners, name)
	delete(r.routes, name)
	r.mu.Unlock()

	if listener != nil {
		_ = listener.Close()
	}
	return exists
}

func (r *registry) nextAvailablePort() (int, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	for port := r.portStart; port <= r.portEnd; port++ {
		if !r.portInUseLocked(port) {
			return port, nil
		}
	}
	return 0, fmt.Errorf("no free SSH ports available between %d and %d", r.portStart, r.portEnd)
}

func (r *registry) portInUseLocked(port int) bool {
	for _, route := range r.routes {
		if route.Port == port {
			return true
		}
	}
	return false
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
	items := make([]agent, 0, len(r.agents)+len(r.routes))
	seen := map[string]bool{}

	for name, route := range r.routes {
		a, ok := r.agents[name]
		if !ok {
			a = agent{ID: name, Name: name, Online: false}
		}
		if a.Online {
			a.ActiveFor = formatDuration(time.Since(a.ConnectedAt))
		} else if a.ActiveFor == "" && !a.ConnectedAt.IsZero() {
			a.ActiveFor = formatDuration(a.LastSeen.Sub(a.ConnectedAt))
		}
		routeCopy := route
		a.SSH = &routeCopy
		items = append(items, a)
		seen[name] = true
	}

	for _, a := range r.agents {
		if seen[a.ID] {
			continue
		}
		if a.Online {
			a.ActiveFor = formatDuration(time.Since(a.ConnectedAt))
		} else if a.ActiveFor == "" && !a.ConnectedAt.IsZero() {
			a.ActiveFor = formatDuration(a.LastSeen.Sub(a.ConnectedAt))
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

func getenvInt(key string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		log.Fatalf("invalid %s: %s", key, value)
	}
	return parsed
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

	agentName := getenv("BACKROUTE_SSH_AGENT", "")
	if agentName != "" {
		return []sshRoute{{
			Port:      port,
			AgentName: agentName,
			Target:    getenv("BACKROUTE_SSH_TARGET", "127.0.0.1:22"),
		}}
	}

	return nil
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

func sourceIPFromRemote(remote string) string {
	host, _, err := net.SplitHostPort(remote)
	if err != nil {
		return remote
	}
	return host
}

func locationLabel(ipText string) string {
	ip := net.ParseIP(ipText)
	if ip == nil {
		return "Unknown"
	}
	if ip.IsLoopback() {
		return "Loopback"
	}
	if ip.IsPrivate() {
		return "Private network"
	}
	return "Public IP - GeoIP not configured"
}

func newGeoIPResolver(enabled bool) *geoIPResolver {
	return &geoIPResolver{
		enabled: enabled,
		client:  &http.Client{Timeout: 3 * time.Second},
		cache:   map[string]string{},
	}
}

func (r *geoIPResolver) lookup(parent context.Context, ipText string) string {
	ip := net.ParseIP(ipText)
	if ip == nil {
		return "Unknown"
	}
	if ip.IsLoopback() || ip.IsPrivate() {
		return locationLabel(ipText)
	}
	if !r.enabled {
		return "Public IP - GeoIP disabled"
	}

	r.mu.RLock()
	if cached, ok := r.cache[ipText]; ok {
		r.mu.RUnlock()
		return cached
	}
	r.mu.RUnlock()

	ctx, cancel := context.WithTimeout(parent, 3*time.Second)
	defer cancel()

	endpoint := "http://ip-api.com/json/" + url.PathEscape(ipText) + "?fields=status,message,country,regionName,city,isp,query"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "Public IP - GeoIP request failed"
	}

	resp, err := r.client.Do(req)
	if err != nil {
		log.Printf("geoip: lookup failed ip=%s error=%v", ipText, err)
		return "Public IP - GeoIP lookup failed"
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Printf("geoip: lookup failed ip=%s status=%d", ipText, resp.StatusCode)
		return "Public IP - GeoIP unavailable"
	}

	var data geoIPResponse
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		log.Printf("geoip: decode failed ip=%s error=%v", ipText, err)
		return "Public IP - GeoIP decode failed"
	}
	if data.Status != "success" {
		log.Printf("geoip: lookup returned failure ip=%s message=%s", ipText, data.Message)
		return "Public IP - GeoIP unknown"
	}

	location := formatGeoIPLocation(data)
	r.mu.Lock()
	r.cache[ipText] = location
	r.mu.Unlock()
	log.Printf("geoip: lookup ok ip=%s location=%q", ipText, location)
	return location
}

func formatGeoIPLocation(data geoIPResponse) string {
	parts := make([]string, 0, 4)
	for _, value := range []string{data.City, data.RegionName, data.Country} {
		if strings.TrimSpace(value) != "" {
			parts = append(parts, value)
		}
	}
	location := strings.Join(parts, ", ")
	if location == "" {
		location = "Public IP"
	}
	if strings.TrimSpace(data.ISP) != "" {
		location += " - " + data.ISP
	}
	return location
}

func formatDuration(duration time.Duration) string {
	if duration < 0 {
		duration = 0
	}
	seconds := int64(duration.Seconds())
	days := seconds / 86400
	seconds %= 86400
	hours := seconds / 3600
	seconds %= 3600
	minutes := seconds / 60
	seconds %= 60

	if days > 0 {
		return fmt.Sprintf("%dd %dh %dm", days, hours, minutes)
	}
	if hours > 0 {
		return fmt.Sprintf("%dh %dm %ds", hours, minutes, seconds)
	}
	if minutes > 0 {
		return fmt.Sprintf("%dm %ds", minutes, seconds)
	}
	return fmt.Sprintf("%ds", seconds)
}
