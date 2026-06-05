# BackRoute

BackRoute is a secure remote access platform for machines behind office or home firewalls. A small agent runs on the private machine and creates an outbound tunnel to a public relay server.

## MVP Goal

The first version proves the core flow:

1. Agent starts on an Ubuntu machine.
2. Agent connects outbound to the BackRoute server.
3. Server authenticates the agent with a token.
4. Server tracks agent online/offline state.
5. Dashboard lets an admin create/delete node SSH routes.
6. Dashboard shows connected agents and the exact test commands.

## Project Layout

```text
agent/       Go agent that runs on the remote Ubuntu machine
server/      Go relay/API server for the Hostinger VPS
dashboard/   Reserved for a future React dashboard
deploy/      Deployment notes and VPS configuration
docs/        Architecture and design notes
```

## Quick Start

Run the server:

```powershell
cd server
go run .
```

Run the agent in another terminal:

```powershell
cd agent
go run . -server ws://localhost:8080/agent -token dev-token -name office-ubuntu-01
```

Open the dashboard:

```text
http://localhost:8080
```

Test SSH through BackRoute:

```powershell
ssh -p 2222 your-linux-user@localhost
```

On a VPS, replace `localhost` with the VPS IP or domain:

```bash
ssh -p 2222 your-linux-user@76.13.211.64
```

## Portal Node Routes

The dashboard creates SSH routes dynamically. When an admin creates `node-1`, the server assigns the next free port from the configured range and immediately starts listening on that port.

Default dynamic port range:

```text
2222-2999
```

Change it on the VPS with:

```bash
export BACKROUTE_PORT_START=2222
export BACKROUTE_PORT_END=3999
```

For Docker deployment on an Ubuntu VPS, BackRoute uses host networking so newly allocated ports are reachable without editing `docker-compose.yml` each time a node is added.

Portal flow:

1. Log in to the dashboard.
2. Enter a node name such as `node-1`.
3. Keep SSH target as `127.0.0.1:22` for normal Linux SSH.
4. Click `Create node`.
5. Copy the agent command shown by the portal to the remote Linux machine.
6. Use the SSH command shown by the portal to connect through the VPS.

## Dashboard Login

Protect the dashboard and dashboard API with HTTP Basic Auth:

```bash
export BACKROUTE_DASHBOARD_USER="br_admin_ops_2026"
export BACKROUTE_DASHBOARD_PASSWORD="change-this-password"
docker compose up -d --build
```

The agent WebSocket endpoint `/agent` is still protected separately by `BACKROUTE_TOKEN`.

## GeoIP Location

BackRoute can look up approximate public IP location for connected agents using the external `ip-api.com` JSON API:

```bash
export BACKROUTE_GEOIP_ENABLED=true
docker compose up -d --build
```

Private and loopback addresses are not sent to the external API. They are shown as local labels such as `Private network` or `Loopback`.
