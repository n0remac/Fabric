# Fabric

Fabric is a Raspberry Pi-hosted service for device-independent declarative interfaces. The initial client is a GoDom browser simulator for an 800×480 monochrome display; future clients can consume the same page and data APIs without HTML or HTMX.

## Run

Fabric requires Go 1.25 or newer.

```bash
go run ./cmd/fabricd -addr :8080 -pages ./pages
```

Then open `http://localhost:8080/simulator/system`.

Startup prints the server's browser URLs, including its LAN and Tailscale IP addresses when listening on all interfaces. Use one of those URLs to open Fabric from another device.

Configuration is also available through `FABRIC_ADDR` and `FABRIC_PAGES_DIR`. Set `FABRIC_STOCK_TICKERS=AAPL,NVDA,GOOG,MSFT,VOO` to choose up to seven ticker symbols for the 800×480 stock watchlist; when unset, that list is the development default. In development, simulator WebSockets accept the same host used to open Fabric, including a Pi's Tailscale IP or hostname. In production, set `ENVIRONMENT=production` and a comma-separated `WEBSOCKET_ALLOWED_ORIGINS` value containing the exact browser origin (for example, `http://100.101.102.103:8080` or `https://pi.tailnet.ts.net`).

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

Page schemas are served at `/schemas/fabric-page-v0.1.json` and `/schemas/fabric-page-v0.2.json`. Version 0.2 adds `chart` and numeric formats. A chart binds to an array of objects and declares `x` and `y` field names; `y` must be numeric, while `x` must be numeric or an RFC3339 timestamp. The simulator plots these values on a monochrome line chart. The stock pages use 0.2, and existing 0.1 pages remain supported.

Open `/simulator/stocks` for the stock watchlist. Market data is retrieved by `fabricd` from Yahoo Finance's chart endpoint. Quotes are cached for 45 seconds; chart history is cached for 3 or 30 minutes depending on period. When retrieval fails, the provider serves its last successful values with `stale` and `status` fields. The stock detail selection and period are shared process-wide across connected clients in this first version.

## Firmware registry

Fabric can store and serve authenticated CrossPoint X3 development and stable
firmware from a directory registry. See [firmware setup and API](docs/firmware.md)
for systemd installation on this Pi, credential provisioning, publishing with
`fabricctl`, and the subsequent device OTA integration.

## Development checks

```bash
go test ./...
go vet ./...
go build ./cmd/fabricd
```

On this Pi's 16 KiB-page ARM64 kernel, Go's ThreadSanitizer cannot start;
run `go test -race ./...` on a 4 KiB-page CI runner.
