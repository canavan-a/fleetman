# Fleetman

A control system for sending commands to a fleet of Linux devices. One master controls many agents; all traffic flows through a central server.

```
Master (CLI/TUI) ──HTTP──> Server <──WebSocket── Agent
                                   <──WebSocket── Agent
                                   <──WebSocket── Agent (...thousands)
```

## Components

- **Server (hub)** — HTTP control plane + WebSocket data plane. Holds the device registry, routes commands, correlates results. SQLite for persistence.
- **Agent** — small daemon on each device. Dials out to the server over WebSocket. Executes commands, reports back. *(not yet implemented)*
- **Master** — CLI/TUI. Talks to the server's HTTP API. *(not yet implemented)*

## Quick Start

```sh
# Build
go build -o fleetman-server ./server

# Run (master key is required)
FLEET_MASTER_KEYS="your-secret-key" ./fleetman-server --addr :8080 --db fleetman.db
```

The SQLite database is created automatically if it doesn't exist.

## Server API

All endpoints (except `/healthz` and `/ws`) require `Authorization: Bearer <master-key>`.

| Method | Endpoint | Description |
|--------|----------|-------------|
| `GET` | `/healthz` | Health check |
| `GET` | `/ws` | WebSocket endpoint (agent auth via Bearer token) |
| `POST` | `/tokens` | Mint a device token |
| `GET` | `/devices` | List all devices |
| `DELETE` | `/devices/{id}` | Delete a device and revoke its token |
| `POST` | `/devices/{id}/tags` | Add tags to a device |
| `DELETE` | `/devices/{id}/tags/{tag}` | Remove a tag from a device |
| `POST` | `/commands` | Send a command to targeted devices |
| `GET` | `/commands/{id}` | Get command results |
| `GET` | `/releases/{arch}` | Get current release URL for OTA |
| `GET` | `/tags` | List all tags |
| `POST` | `/tags` | Create a tag |
| `DELETE` | `/tags/{name}` | Delete a tag |
| `POST` | `/tags/{name}/devices` | Bulk-add tag to devices |
| `DELETE` | `/tags/{name}/devices` | Bulk-remove tag from devices |

## Command Targeting

Commands can target devices by:
- **All**: `{"all": true}`
- **IDs**: `{"ids": ["dev-abc", "dev-def"]}`
- **Tags**: `{"tags": ["production", "web"]}` (device must have all listed tags)
- **Labels**: `{"labels": {"role": "webserver"}}`
- **Init type**: `{"init_type": "systemd"}`

## License

See [LICENSE](LICENSE).
