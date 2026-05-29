# BackRoute Architecture

BackRoute uses reverse tunnels. The private machine initiates the connection, so the office/home firewall does not need inbound port forwarding.

```text
User Browser / SSH Client
        |
        v
BackRoute Server on VPS
        |
  outbound tunnel
        |
        v
BackRoute Agent on Ubuntu
        |
        v
Local service, for example localhost:8080
```

## Components

- Agent: runs on the customer Ubuntu machine.
- Server: runs on the public Hostinger VPS.
- Dashboard: shows agents, tunnels, and status.

## MVP Protocol

The MVP starts with a WebSocket control channel:

- Agent connects to `/agent`.
- Agent sends an auth message containing name and token.
- Server validates the token.
- Agent sends heartbeat messages.
- Server exposes connected agents via `/api/agents`.

HTTP/TCP forwarding will build on top of this control channel.
