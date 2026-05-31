# BackRoute Commercial MVP Interfaces

## 1. Product Goal

BackRoute is a secure remote access product for machines behind office, home, or customer firewalls.

The commercial MVP should let a customer:

1. Create an account.
2. Register a Linux machine.
3. Install the BackRoute agent on that machine.
4. See the machine online in the dashboard.
5. Connect to it using SSH through the BackRoute cloud relay.
6. Revoke access or remove the machine.
7. View basic connection logs.

## 2. Main Components

```text
User Browser
   |
   v
BackRoute Dashboard
   |
   v
BackRoute API
   |
   +--> Database
   |
   +--> Relay Server
            |
            v
      BackRoute Agent
            |
            v
      Local SSH / HTTP / TCP service
```

## 3. User Interfaces

### 3.1 Marketing Website

Purpose:

- Explain what BackRoute does.
- Convert visitors into signups.
- Show documentation, pricing, and contact options.

Pages:

- Home
- Pricing
- Docs
- Security
- Contact
- Login

Primary actions:

- Start free trial
- Sign in
- View install guide

### 3.2 Customer Dashboard

Purpose:

- Main product UI for customers.
- Manage machines, tunnels, users, and access.

Core screens:

```text
Login
Dashboard Overview
Agents / Machines
Agent Details
Tunnels
Access Tokens
Connection Logs
Team Members
Settings
Billing later
```

### 3.3 Agents Screen

Shows all registered machines.

Fields:

```text
Agent name
Status: online/offline
Operating system
Last seen
Public SSH command
Tunnel count
Created date
```

Example:

```text
office-ubuntu-01   Online    Ubuntu 24.04   ssh -p 2222 user@relay.backroute.com
home-linux-01      Offline   Ubuntu 22.04   ssh -p 2223 user@relay.backroute.com
```

Actions:

- Register new agent
- Copy install command
- Copy SSH command
- Rotate token
- Disable agent
- Delete agent
- View logs

### 3.4 Agent Details Screen

Purpose:

- Show one machine and its tunnels.

Fields:

```text
Agent ID
Agent name
Status
Last heartbeat
Version
OS
Architecture
IP seen by relay
Install token status
```

Actions:

- Rename agent
- Rotate token
- Disable agent
- Delete agent
- Create tunnel

### 3.5 Tunnel Screen

Purpose:

- Manage SSH, HTTP, and TCP access routes.

Tunnel fields:

```text
Tunnel ID
Agent
Protocol: ssh/http/tcp
Public endpoint
Local target
Status
Created by
Created at
```

Examples:

```text
SSH  relay.backroute.com:2222       -> office-ubuntu-01:127.0.0.1:22
HTTP https://app-123.backroute.com  -> office-ubuntu-01:127.0.0.1:3000
TCP  relay.backroute.com:5433       -> db-server-01:127.0.0.1:5432
```

Actions:

- Create tunnel
- Disable tunnel
- Delete tunnel
- Copy connection command

## 4. Agent Interfaces

### 4.1 Agent Command Line

The agent should support:

```bash
backroute-agent run --token TOKEN
backroute-agent status
backroute-agent version
backroute-agent install-service --token TOKEN
backroute-agent uninstall-service
```

Commercial MVP install command:

```bash
curl -fsSL https://download.backroute.com/install.sh | sudo bash -s -- --token AGENT_TOKEN
```

### 4.2 Agent Configuration File

Location:

```text
/etc/backroute/agent.yaml
```

Example:

```yaml
server_url: wss://relay.backroute.com/agent
agent_name: office-ubuntu-01
agent_token: br_agent_xxx
ssh_target: 127.0.0.1:22
log_level: info
```

Rules:

- File should be readable only by root.
- Token should not be printed in logs.
- Agent should reconnect automatically.

### 4.3 Agent Service Interface

Linux systemd service:

```text
backroute-agent.service
```

Expected behavior:

- Start on boot.
- Restart on crash.
- Wait for network.
- Log to journald.

Commands:

```bash
sudo systemctl status backroute-agent
sudo systemctl restart backroute-agent
sudo journalctl -u backroute-agent -f
```

## 5. Relay Server Interfaces

### 5.1 Agent WebSocket Interface

Endpoint:

```text
wss://relay.backroute.com/agent
```

Purpose:

- Persistent connection from agent to relay.
- Agent authentication.
- Heartbeats.
- Tunnel control messages.
- Binary traffic forwarding.

Agent auth message:

```json
{
  "type": "auth",
  "agent_id": "agt_123",
  "token": "br_agent_xxx",
  "version": "0.1.0",
  "hostname": "office-ubuntu-01",
  "os": "linux",
  "arch": "amd64"
}
```

Server response:

```json
{
  "type": "auth_ok",
  "agent_id": "agt_123"
}
```

Heartbeat:

```json
{
  "type": "heartbeat",
  "time": "2026-05-31T10:00:00Z"
}
```

Open TCP tunnel:

```json
{
  "type": "tcp_open",
  "connection_id": "conn_123",
  "target": "127.0.0.1:22"
}
```

Close TCP tunnel:

```json
{
  "type": "tcp_close",
  "connection_id": "conn_123"
}
```

Binary payload:

```text
WebSocket binary frame containing raw TCP bytes
```

### 5.2 Public SSH Interface

Purpose:

- Users connect to relay port.
- Relay forwards traffic to selected agent.

MVP mode:

```text
relay.backroute.com:2222 -> office-ubuntu-01 -> 127.0.0.1:22
relay.backroute.com:2223 -> home-linux-01    -> 127.0.0.1:22
```

User command:

```bash
ssh -p 2222 ubuntu@relay.backroute.com
```

Production improvement:

- Use one SSH gateway port.
- Route based on username or certificate.

Example future command:

```bash
ssh office-ubuntu-01@ssh.backroute.com
```

## 6. Backend API Interfaces

Base URL:

```text
https://api.backroute.com
```

### 6.1 Authentication API

Endpoints:

```text
POST /auth/login
POST /auth/logout
GET  /auth/me
```

Login request:

```json
{
  "email": "admin@example.com",
  "password": "secret"
}
```

Login response:

```json
{
  "user": {
    "id": "usr_123",
    "email": "admin@example.com"
  },
  "organization": {
    "id": "org_123",
    "name": "Example Company"
  }
}
```

### 6.2 Agent API

Endpoints:

```text
GET    /agents
POST   /agents
GET    /agents/{agent_id}
PATCH  /agents/{agent_id}
DELETE /agents/{agent_id}
POST   /agents/{agent_id}/rotate-token
```

Create agent request:

```json
{
  "name": "office-ubuntu-01"
}
```

Create agent response:

```json
{
  "agent_id": "agt_123",
  "name": "office-ubuntu-01",
  "install_command": "curl -fsSL https://download.backroute.com/install.sh | sudo bash -s -- --token br_agent_xxx"
}
```

### 6.3 Tunnel API

Endpoints:

```text
GET    /tunnels
POST   /tunnels
GET    /tunnels/{tunnel_id}
PATCH  /tunnels/{tunnel_id}
DELETE /tunnels/{tunnel_id}
```

Create SSH tunnel request:

```json
{
  "agent_id": "agt_123",
  "protocol": "ssh",
  "local_target": "127.0.0.1:22"
}
```

Create SSH tunnel response:

```json
{
  "tunnel_id": "tun_123",
  "public_host": "relay.backroute.com",
  "public_port": 2222,
  "command": "ssh -p 2222 ubuntu@relay.backroute.com"
}
```

### 6.4 Logs API

Endpoints:

```text
GET /logs/connections
GET /logs/agents
```

Connection log fields:

```text
time
user_id
agent_id
tunnel_id
source_ip
protocol
status
duration
bytes_in
bytes_out
```

## 7. Database Interfaces

Recommended database:

```text
PostgreSQL
```

Main tables:

```text
organizations
users
agents
agent_tokens
tunnels
relay_sessions
connection_logs
audit_logs
```

### 7.1 agents

Fields:

```text
id
organization_id
name
status
last_seen_at
version
os
arch
created_at
disabled_at
```

### 7.2 tunnels

Fields:

```text
id
organization_id
agent_id
protocol
public_host
public_port
local_target
status
created_by
created_at
disabled_at
```

### 7.3 connection_logs

Fields:

```text
id
organization_id
agent_id
tunnel_id
user_id
source_ip
started_at
ended_at
status
bytes_in
bytes_out
```

## 8. Security Interfaces

### 8.1 Token Model

Commercial MVP should not use one shared token.

Use:

```text
one token per agent
hashed token storage in database
token revocation
token rotation
```

Token format:

```text
br_agent_xxxxxxxxxxxxxxxxx
```

### 8.2 Transport Security

Required:

```text
HTTPS for dashboard/API
WSS for agent relay
TLS certificates through Caddy/Nginx
No plaintext token logging
```

### 8.3 Access Control

Commercial MVP roles:

```text
Owner
Admin
Operator
Viewer
```

Minimum rules:

- Only authenticated users can view dashboard.
- Only admins can create/delete agents.
- Only admins can rotate tokens.
- SSH tunnel access must be logged.

## 9. Deployment Interfaces

### 9.1 Public DNS

Suggested domains:

```text
app.backroute.i2itelesource.com
api.backroute.i2itelesource.com
relay.backroute.i2itelesource.com
ssh.backroute.i2itelesource.com
download.backroute.i2itelesource.com
```

### 9.2 VPS Ports

MVP:

```text
80    HTTP redirect to HTTPS
443   dashboard/API/agent WSS
2222+ SSH tunnel ports
```

Current prototype:

```text
8080  dashboard and agent WebSocket
2222  SSH route 1
2223  SSH route 2
2224  SSH route 3
```

### 9.3 Docker Compose Services

Commercial MVP services:

```text
backroute-api
backroute-relay
backroute-dashboard
postgres
caddy
```

Prototype currently combines dashboard/API/relay into one server.

## 10. Commercial MVP Scope

### Must Have

```text
User login
Organization account
Register agent
Per-agent token
Linux installer script
Agent systemd service
Agent online/offline status
SSH tunnel
Multiple agents
Connection logs
HTTPS domain
Basic documentation
```

### Should Have

```text
HTTP tunnel
Token rotation
Agent version display
Dashboard copy buttons
Basic rate limiting
Email/password reset
```

### Later

```text
Billing
Teams and RBAC depth
SSO
RDP
Agent auto-update
Multi-region relay
SSH certificates
Mobile app
Advanced analytics
```

## 11. Current Prototype vs Commercial MVP

| Area | Current Prototype | Commercial MVP |
|---|---|---|
| Agent auth | Shared token | Per-agent token |
| Dashboard auth | None | User login |
| Database | None | PostgreSQL |
| Agent install | Manual `go run` / service | Installer script |
| SSH routing | Env-configured ports | Database-managed tunnels |
| Logs | Console logs | Persistent audit logs |
| HTTPS | Not yet | Required |
| Multi-agent | Basic | Full UI-managed |

## 12. Recommended Next Build Order

1. Add PostgreSQL.
2. Add agents table and tunnels table.
3. Replace shared token with per-agent token.
4. Add dashboard login.
5. Add create-agent screen.
6. Generate Linux install command.
7. Add systemd installer script.
8. Add connection logs.
9. Add HTTPS with Caddy.
10. Add HTTP tunnel support.
