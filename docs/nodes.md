# Fabric node identity

`fabricd` requires `FABRIC_NODES_FILE` (or `-nodes`) at startup. The registry is
a private JSON file with separate `nodes` and `credentials` arrays. Node records
contain a stable ID, type, display name, enabled flag, `permissions`, optional
`provides`, and attributes such as `firmware_channel`. Credential records contain
only SHA-256 digests of random bearer tokens. Neither IP address nor session ID
defines a node. The registry is reloaded for authentication requests, so a revoked or disabled
node is rejected on its next request. There is no unauthenticated API mode.

HTTP adapters authenticate `Authorization: Bearer <token>` and place the node in
request context. `pages.read`, `actions.invoke`, `firmware.read`, and
`firmware.publish` are checked before handlers run. Missing or invalid tokens
return 401; a valid node without the capability gets 403. `/healthz` and static
assets remain public. An optional `X-Fabric-Session` value (1–64 letters,
digits, underscores or hyphens) becomes the request's session ID. Session
values are always keyed with the authenticated node ID; a client cannot claim
another identity through a JSON field or session header.

The stock watchlist's selected ticker and chart period are kept in memory per
node. They survive reconnects while `fabricd` runs and reset when it restarts.
Quote and history caches remain shared because those values are public market
data, not interaction state. A node's `provides` list is separate from the
`permissions` it may invoke; capability discovery is not implemented yet.

## Provisioning

Create the token directory first. A new token file must not already exist; the
CLI writes it mode 0600 and never prints its contents.

```bash
mkdir -p ~/.config/fabric/nodes
sudo fabricctl node create --id laptop --type desktop --name 'Laptop' \
  --permissions pages.read,actions.invoke,firmware.read,firmware.publish \
  --output /home/pi/.config/fabric/nodes/laptop.token
sudo chown pi:pi /home/pi/.config/fabric/nodes/laptop.token
```

`fabricctl node list`, `fabricctl node show laptop`, `fabricctl node disable
laptop`, `fabricctl node enable laptop`, and `fabricctl node revoke laptop`
manage registry records. Use `--config` to operate on a different registry.
Revocation removes only that node and its digest. A disabled or revoked node
cannot authenticate on the next request. Credential rotation currently requires
recreating the record with the same ID and provisioning the new token to its client.

For an existing firmware credential, import its stored digest without revealing
or replacing the device's raw token:

```bash
sudo fabricctl node import-firmware --id x3-pocket --type xteink-x3 \
  --name 'Pocket Reader' --credential-id x3-personal --firmware-channel dev
sudo fabricctl node import-firmware --id pi-home --type raspberry-pi \
  --name 'Pi Publisher' --credential-id pi-publisher
```

The installer performs those imports on the first node-registry installation
and creates `simulator-browser` with a separate token at
`~/.config/fabric/nodes/simulator.token`. The existing X3 token stays in NVS;
no device wipe or USB bootstrap is needed. The old
`/etc/fabric/firmware-access.json` is retained for offline import only and is
not read by `fabricd`. `fabricctl token-issue` and `token-revoke` edit that legacy
file, so use `fabricctl node` for active credentials.

The browser simulator prompts for HTTP Basic credentials on first visit. Enter
any username and the **simulator node token** as the password. The token selects
the node; the username does not. A random, HttpOnly, same-site browser session
cookie then carries that identity for simulator requests and WebSockets for up
to 12 hours. Browser sessions are server-side and lost on restart. The normal
JSON APIs accept bearer tokens only. Production simulator routes do not bypass
authentication.

## Deployment order

1. Build both Fabric binaries and the updated CrossPoint firmware.
2. Flash or OTA the updated CrossPoint build while the existing Fabric server
   still accepts unauthenticated page requests. The updated client already
   attaches its NVS reader token to those requests.
3. Install Fabric using `sudo bash deploy/install.sh`. It imports the existing
   X3 token digest and enables API authentication.
4. Verify `node show x3-pocket`, `node show pi-home`, and a 401 from an
   anonymous `/api/pages` request. Check the simulator with its own token,
   then check page listing, action navigation, and firmware metadata/download
   on the X3.

Use HTTPS for readers outside a trusted LAN. CrossPoint verifies certificates
and hostnames for HTTPS page, action, and firmware requests. Plain HTTP still
works for local development but exposes the bearer token to anyone able to
observe that network.
