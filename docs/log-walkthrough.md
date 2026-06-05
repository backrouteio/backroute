# BackRoute Log Walkthrough

Use this file when watching logs on the Hostinger VPS:

```bash
cd ~/backroute
docker compose logs -f
```

## 1. Server Startup

Expected logs:

```text
startup: BackRoute server booting
startup: dashboard/API/agent HTTP address=:8080
startup: debug logging=false
startup: configured SSH routes=3
startup: route port=2222 agent=office-ubuntu-01 target=127.0.0.1:22
BackRoute SSH tunnel listening on :2222 for agent office-ubuntu-01 -> 127.0.0.1:22
BackRoute server listening on :8080
```

Meaning:

- Dashboard and agent API are available on port `8080`.
- SSH tunnel ports are open.
- Each SSH port maps to one agent name.

## 2. Agent Connects

Expected logs:

```text
agent: websocket connection attempt remote=1.2.3.4:52344
agent: auth ok name=office-ubuntu-01 remote=1.2.3.4:52344
agent: online name=office-ubuntu-01 active_agents=1
```

Meaning:

- Agent reached the VPS.
- Token matched.
- Agent is now available for tunnel traffic.

If token is wrong:

```text
agent: auth failed remote=1.2.3.4:52344 name="office-ubuntu-01" type="auth" token_match=false
```

Fix:

- Make sure `BACKROUTE_TOKEN` on VPS matches the agent `-token`.

## 3. SSH User Connects

When a user runs:

```bash
ssh -p 2222 user@76.13.211.64
```

Expected logs:

```text
ssh: client connected remote=5.6.7.8:60220 public_port=2222 route_agent=office-ubuntu-01 target=127.0.0.1:22
tunnel: opened remote=5.6.7.8:60220 public_port=2222 agent=office-ubuntu-01 target=127.0.0.1:22
```

Meaning:

- SSH client connected to the VPS.
- VPS selected the route for port `2222`.
- VPS told the agent to open `127.0.0.1:22`.

If the agent is offline:

```text
ssh: rejected remote=5.6.7.8:60220 reason=agent_offline agent=office-ubuntu-01
```

Fix:

- Start the matching agent.
- Confirm the agent name matches the route exactly.

## 4. SSH Session Closes

Expected logs:

```text
tunnel: close requested by agent name=office-ubuntu-01
tunnel: closed remote=5.6.7.8:60220 public_port=2222 agent=office-ubuntu-01
```

Meaning:

- SSH session ended.
- BackRoute closed both sides of the tunnel.

## 5. Debug Logging

To log heartbeats and binary payload direction, enable:

```bash
export BACKROUTE_DEBUG=true
docker compose up -d --build
```

Debug mode adds logs like:

```text
agent: heartbeat name=office-ubuntu-01 time=2026-06-06T10:00:00Z
agent: binary payload name=office-ubuntu-01 bytes=1024 direction=agent_to_ssh_client
```

Do not keep debug mode on for production because it can create many logs.
