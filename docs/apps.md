# Fabric app bundles (discovery-only milestone)

**Current status:** Issue #3 implements app manifest/schema validation and discovery. It does *not* evaluate `.fabric` scripts, enable script providers, or expose their pages in the API. Those features are tracked in #4–#8. See [ADR-0001](adr/0001-scripting-app-contract.md) for the scripting interface proposal.

## Bundle layout

```text
apps/
  homelab/
    fabric-app.json
    main.fabric
```

The folder basename must equal the manifest's `id`. A v0.1 example is in [examples/apps/homelab](../examples/apps/homelab):

```json
{
  "fabric_app": "0.1",
  "id": "homelab",
  "name": "Homelab Monitor",
  "entrypoint": "main.fabric",
  "pages": ["overview"],
  "resources": ["homelab.services"]
}
```

This declares the eventual public page ID `homelab-overview` and requested resource alias `homelab.services`. Declaring a resource alone never grants access or defines a network endpoint. Future host-side configuration authorizes resource access and stores credentials separately. Do not put tokens in manifests or scripts.

## Opt-in discovery

Set `FABRIC_APPS_DIR` to an external directory or use `fabricd -apps /path/to/apps`. With the variable unset (the default), behavior is unchanged. Discovery validates manifests and source file types/lengths, reserves stable page IDs against current static page IDs and other app bundles, and reports errors in the server logs; valid bundles are logged as *discovered*, not as executable/installed apps. A malformed bundle is not published and does not disable static JSON pages.

Example: from the repository root, point discovery at `examples/apps`, supply your existing node registry, and run fabricd normally. No app page will appear in `/api/pages` yet.

Supported manifest fields are described by [schemas/fabric-app-v0.1.json](../schemas/fabric-app-v0.1.json). IDs use lowercase letters, numbers, underscores and hyphens; page IDs use `{appID}-{localID}` with the existing 64-character limit. Resource names may also contain periods. The entrypoint is a single regular `.fabric` file within the bundle, not a symlink or a path. Directories and scripts are limited in size; no recursive app discovery or imports yet.

## Deployment model

The app directory remains separate from `/etc/fabric/pages` and may be kept in an entirely separate repository. Bundles can be added without rebuilding Go. **Hot reload and script execution are not included yet.** Existing stock/system pages, OTA firmware registry, auth endpoints, and device protocols remain unchanged.
