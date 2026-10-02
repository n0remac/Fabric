# Firmware on the Raspberry Pi

Fabric stores raw CrossPoint OTA applications in
`/var/lib/fabric/firmware/x3/builds/<id>.bin` and records them in
`/var/lib/fabric/firmware/x3/manifest.json`. Binary IDs are randomly generated;
files are never replaced. The manifest has schema version 1, build metadata,
and nullable `channels.dev` and `channels.stable` build IDs. A successful upload
advances dev. Stable changes only through explicit promotion. An empty stable
channel returns 404, even when dev has a build.

The initial X3 profile accepts the fork's combined X3/X4 ESP32-C3 application:
chip ID 5, `CROSSPOINT-BOARD-V1:x4;`, and 64 KiB–6,553,600 bytes. The maximum is
the smaller OTA slot in `crosspoint-reader/partitions.csv`, not total flash size.
Applications must have an ESP application descriptor, valid segment bounds,
checksum and (when present) appended image SHA256. Bootloaders, merged images,
untagged firmware and other device images are rejected. Registry SHA256 hashes
cover the entire uploaded file, including its appended image digest.

## Install the service

Build on this ARM64 Pi:

```bash
mkdir -p deploy/bin
go build -o deploy/bin/fabricd ./cmd/fabricd
go build -o deploy/bin/fabricctl ./cmd/fabricctl
sudo bash deploy/install.sh
```

The installer creates the `fabric` system user, installs both binaries, replaces
the managed pages in `/etc/fabric/pages`, and enables `fabricd.service`. It uses
`deploy/fabric.env` only when `/etc/fabric/fabric.env` does not yet exist.
Configuration stays external to the binaries. Subsequent installation preserves
firmware and credentials, replaces the installed binaries and page files, and
restarts the service.

If a tmux-launched Fabric process already owns port 8080, the installer exits
before making changes. When intentionally migrating, stop that process after
preparing this build, then run the installer. Do not leave both processes
trying to serve the same Funnel target.

The service binds to `127.0.0.1:8080`. This Pi's existing Tailscale Funnel proxies
`https://raspberrypi.tail628049.ts.net` to that address. The installer does not
change Tailscale configuration. All page, action, simulator, and firmware
requests require a node credential through either transport. `/healthz` stays
public. See [node identity](nodes.md) for the registry and migration.

On first installation, two random tokens are written into private files:

- `/home/pi/.config/fabric/publisher.token`: upload, promotion and read access.
- `/home/pi/.config/fabric/x3.token`: read-only access for the first X3.

The installer never prints tokens. On first node-registry installation it
imports the digests of the existing X3 and publisher tokens into
`/etc/fabric/nodes.json`, and creates a separate simulator-browser token.
`firmware-access.json` remains only as an offline migration source. Registry
configuration is root-owned and group-readable by `fabric`; token files are
operator-owned and mode 0600. Additional X3 devices should each receive their
own node credential.

```bash
sudo systemctl status fabricd --no-pager
sudo journalctl -u fabricd -n 30 --no-pager
tailscale funnel status
curl --fail https://raspberrypi.tail628049.ts.net/healthz
```

For manual development, `FABRIC_NODES_FILE` is required for all API endpoints.
Firmware storage is enabled by `FABRIC_FIRMWARE_DIR` or `-firmware`; when
enabled, it uses the same node identity and capability checks as page APIs.
A second process cannot write the same firmware registry while the service is
running.

## Publish and promote

Build firmware in the CrossPoint checkout, then publish the raw application:

```bash
fabricctl upload \
  --source /home/pi/Projects/crosspoint-reader \
  --file /home/pi/Projects/crosspoint-reader/.pio/build/default/firmware.bin \
  --version 1.6.5-dev \
  --token-file /home/pi/.config/fabric/publisher.token
```

The helper validates the image locally, computes size and SHA256, and records
the source checkout's current full Git commit and dirty flag. Build using the
same checkout state before uploading: these fields are publisher declarations,
not signed proof of how a binary was produced. The server independently
validates the image and hashes the received bytes. The helper streams multipart
data, validates HTTPS certificates, and rejects redirects. HTTP is accepted
only for loopback development. Use `--server http://127.0.0.1:8080` for a local
upload without routing through Funnel.

The successful JSON response includes `id`, `version`, `size`, `sha256`,
`git_commit`, `dirty`, `created_at`, `device`, `chip_id`, `board_tag` and
`download_path`. Copy the ID when intentionally selecting stable:

```bash
fabricctl promote --build-id BUILD_ID \
  --token-file /home/pi/.config/fabric/publisher.token
```

Promotion updates only the stable pointer. An upload does not promote itself.
Repeated uploads create distinct immutable builds, including retries following
an ambiguous network failure; inspect the manifest before retrying.

## API contract

All paths start with `/api/firmware/v1` and require a bearer token in the
Authorization header. Credentials in query strings do not authorize requests.
Firmware responses use `Cache-Control: private, no-store`.

| Request | Result |
| --- | --- |
| `GET /x3/manifest` | Versioned manifest with builds and both channel pointers |
| `GET /x3/latest` | Stable build record, or 404 if unset |
| `GET /x3/latest?channel=dev` | Dev build record, or 404 if unset |
| `GET /x3/builds/{id}/download` | Raw image, Content-Length and SHA256 ETag |
| `POST /x3/builds` | Publisher-only upload; returns 201 and the build record |
| `PUT /x3/stable` | Publisher-only JSON `{"build_id":"…"}`; returns the build record |

Uploads contain exactly two multipart parts in order: `metadata` (a JSON
object) and `firmware` (the binary). Metadata requires `version`, `size`,
`sha256` (64 lowercase hex characters), and `git_commit` (40 or 64 lowercase
hex characters); `dirty` defaults to false. Creation time and compatibility
fields are assigned by the server. The filename supplied by the client is
ignored. Metadata is capped at 16 KiB, request overhead at 64 KiB, and uploads
have a five-minute read deadline. Invalid channels or metadata return 422,
oversize requests return 413, invalid authentication returns 401, insufficient
role/device scope returns 403, and unknown builds return 404. Errors use
`{"error":"code","message":"description"}`. Download paths are relative to the
configured Fabric origin and contain no credentials or local filesystem paths.

For manual reads without putting the token in command-line arguments:

```bash
python3 - <<'PY'
from pathlib import Path
from urllib.request import Request, urlopen
token = Path('/home/pi/.config/fabric/x3.token').read_text().strip()
request = Request('https://raspberrypi.tail628049.ts.net/api/firmware/v1/x3/manifest',
                  headers={'Authorization': 'Bearer ' + token})
with urlopen(request, timeout=30) as response:
    print(response.read().decode())
PY
```

## Provision and revoke devices

Use `fabricctl node create`, `node disable`, and `node revoke` for active
credentials. Each node receives a unique token and its own `firmware.read` or
`firmware.publish` permission. Existing firmware token digests are imported
without touching the X3's NVS token. See [node identity](nodes.md) for commands,
the browser simulator credential, and restart requirements. The legacy
`fabricctl token-issue`/`token-revoke` commands no longer change `fabricd` access.

## Recovery and verification

Publication syncs the immutable binary before atomically replacing the synced
manifest. A failed publication can leave an unreferenced binary, which is never
served. Staging uploads are discarded at startup. Existing build files are
checked for size and SHA256 at startup; corrupt JSON, missing referenced files,
symlinked files or corrupted images fail startup instead of erasing the registry.
If a manifest rename succeeds but directory sync fails, publication may already
be visible despite an error response: inspect the manifest before retrying.

Back up `/var/lib/fabric/firmware` while the service is stopped, together with
`/etc/fabric` and private operator token files. Restore ownership to `fabric`
for firmware state and `root:fabric` for server configuration. Do not manually
rewrite the manifest or replace binaries while the service is running. Retention
is manual for this milestone; no builds are pruned automatically.

```bash
go test ./...
go vet ./...
go build ./cmd/fabricd
go build ./cmd/fabricctl
```

On this Pi's 16 KiB-page ARM64 kernel, `go test -race ./...` cannot execute:
ThreadSanitizer reports an unsupported 47-bit virtual-address range. Run the
race check on a 4 KiB-page CI runner; keep ordinary tests on the Pi.

The registry tests cover image integrity and device checks, authenticated
publishing/download, promotion, revocation, failed manifest replacement,
concurrent publication, writer exclusion and restart recovery. After installing,
verify anonymous manifest/download requests return 401, readers cannot upload,
and authenticated downloads match the selected build's exact size and SHA256.
Test public access from ordinary internet Wi-Fi outside the tailnet and check
the simulator and WebSockets through the same origin.

## CrossPoint integration on this Pi

The source is `/home/pi/Projects/crosspoint-reader`, branch `develop`. The
`freeink-sdk` submodule is initialized. PlatformIO's `default` environment
builds the combined X3/X4 application. This Pi's gitignored
`platformio.local.ini` supplies its HTTPS Fabric origin. A private reader token
was copied into the first X3's NVS by a USB bootstrap image; the provisioning
header was removed before the registry image was built.

The firmware updater checks Fabric's dev channel on the combined X3/X4 ESP32-C3
build when a compiled HTTPS origin is present. It uses a separate
certificate-verified client, bearer token, size and SHA256 validation, and the
existing inactive-partition OTA writer. The normal Fabric page client also sends
the NVS reader token and verifies HTTPS certificates and hostnames. See the
fork's `docs/fabric-client.md` for device behavior and the first USB bootstrap
flow.

Publish a token-free development build with `pio run -e default -t fabric-deploy`
from the CrossPoint checkout. The target uses the local publisher credential,
builds the application, rejects an embedded reader credential and uploads to
the immutable registry. The first test build has ID
`e5443637397bf1a8623ca7d365c6b7ff` and SHA256
`256bc957bf182bb17400e44c12bcd414bf5ac59bc96b50b6c61dcd4f48b83451`.
The public HTTPS endpoint returned 401 without a credential and served bytes
matching the local build to an authenticated reader.

The X3 acceptance run should open Fabric pages and invoke an action, then select
**Settings → Update Firmware**, install an offered dev build, and check **About →
Fabric Build ID** for the installed build. Verify the page and OTA requests work
with the same NVS credential before promotion.
Stable-channel selection, signed manifests, rollback automation, and automatic
retention are future work.
