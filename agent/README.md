# BackRoute Agent

The agent runs on a private Ubuntu machine and opens an outbound WebSocket connection to the BackRoute server.

## Run Locally

```bash
go run . -server ws://localhost:8080/agent -token dev-token -name office-ubuntu-01
```
