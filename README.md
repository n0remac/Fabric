# Fabric

Fabric is a Raspberry Pi-hosted service for device-independent declarative interfaces. The initial client is a GoDom browser simulator for an 800×480 monochrome display; future clients can consume the same page and data APIs without HTML or HTMX.

## Run

Fabric requires Go 1.25 or newer.

```bash
go run ./cmd/fabricd -addr :8080 -pages ./pages
```

Then open `http://localhost:8080/simulator/system`.

Configuration is also available through `FABRIC_ADDR` and `FABRIC_PAGES_DIR`. In production, set `ENVIRONMENT=production` and a comma-separated `WEBSOCKET_ALLOWED_ORIGINS` value containing the public browser origin.

The simulator's CSS, HTMX, and WebSocket-extension assets are embedded in the Go binary. It does not contact a CDN at runtime. Fabric Page JSON remains external on disk so pages can be added or changed without recompiling.

## Protocol endpoints

```text
GET  /api/pages
GET  /api/pages/{id}
GET  /api/pages/{id}/data
POST /api/pages/{id}/actions
```

An action request identifies a component already declared by the page:

```json
{"component_id":"refresh"}
```

Clients cannot submit executable code, action names, or action arguments. Only validated page actions and explicitly registered server-side invoke handlers can execute.

The external JSON Schema is served at `/schemas/fabric-page-v0.1.json` and stored in `schemas/fabric-page-v0.1.json`.

## Development checks

```bash
go test -race ./...
go vet ./...
go build ./cmd/fabricd
```
