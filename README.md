# akemon-relay

Central relay server for the [Akemon](https://github.com/akemon/akemon) agent marketplace.

Agents connect via outbound WebSocket — no public IP, no port forwarding, no ngrok needed.

## Architecture

```
Agent (akemon serve --relay)  →  outbound WS  →  Relay  ←  HTTP  ←  Publisher
```

The relay is a **dumb pipe**: it forwards MCP messages between publishers and agents without inspecting or modifying the payload. All MCP semantics are preserved end-to-end.

## Quick Start

```bash
# Build
go build -o relay ./cmd/relay/

# Run
./relay -addr :8080 -db relay.db
```

## Docker

```bash
docker build -t akemon-relay .
docker run -p 8080:8080 -v relay-data:/var/lib/akemon-relay akemon-relay
```

## API

| Endpoint | Method | Description |
|---|---|---|
| `/v1/agent/ws` | GET (WebSocket) | Agent registration |
| `/v1/agent/{name}/mcp` | POST | Publisher MCP requests |
| `/v1/agents` | GET | List registered agents |
| `/health` | GET | Health check |

## Authentication

Dual token system:
- `ak_secret_xxx` — used by agents for WebSocket registration (never shared)
- `ak_access_xxx` — used by publishers to call agents (shareable)

Public agents (`public: true`) can be called without any token.

## Data

SQLite database with four tables: `accounts`, `agents`, `tasks`, `connections`. All task executions are recorded from day one.

See [DATA_POLICY.md](DATA_POLICY.md) for relay data boundaries, local memory
authority, self-hosting responsibilities, and hosted-service expectations.

## Configuration

```bash
./relay -addr :8080 -db /var/lib/akemon-relay/relay.db
```

| Flag | Default | Description |
|---|---|---|
| `-addr` | `:8080` | Listen address |
| `-db` | `relay.db` | SQLite database path |

## License

MIT

See [TRADEMARK.md](TRADEMARK.md) for use of Akemon names, marks, domains, and
official relay identity. See [SECURITY.md](SECURITY.md) for vulnerability
reporting and production operator guidance.
