# BackRoute

BackRoute is a secure remote access platform for machines behind office or home firewalls. A small agent runs on the private machine and creates an outbound tunnel to a public relay server.

## MVP Goal

The first version proves the core flow:

1. Agent starts on an Ubuntu machine.
2. Agent connects outbound to the BackRoute server.
3. Server authenticates the agent with a token.
4. Server tracks agent online/offline state.
5. Dashboard shows connected agents.
6. HTTP tunnel support is added next.

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
