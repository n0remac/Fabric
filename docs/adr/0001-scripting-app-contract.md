# ADR-0001: Server-side Fabric applications and scripting contract

- Status: Accepted for milestones #2 and #3; runtime implementation is deferred
- Date: 2026-10-23
- Parent: https://github.com/n0remac/Fabric/issues/1
- Implements design issue: https://github.com/n0remac/Fabric/issues/2

## Context and objective

Fabric currently serves validated declarative Fabric Page v0.1/v0.2 documents and data from hard-coded Go providers. Adding a new integration requires modifying and rebuilding the server. We want an author to install a small application that reads configured resources, transforms JSON data, defines device-independent pages, and eventually exposes authorized actions, with no per-application changes to the X3/X4 firmware or Go server.

The Raspberry Pi runs all scripts. Device clients only receive authenticated, validated Fabric Page JSON, page data, and action results through the existing endpoints. A script is not shipped to or executed by a client.

## Decision

Adopt **Starlark for Go** as the embedded language for `.fabric` files (the interpreter is **not** introduced by this ADR or issue #3). Starlark has a familiar Python-like syntax, deterministic core semantics, limited builtins, and a maintained Go implementation. It has no ambient system/network/file primitives; Fabric must only inject explicitly approved host functions. A script is *not* a general-purpose Python program.

Alternatives considered:

| Approach | Benefit | Reason not selected |
| --- | --- | --- |
| JSON-only DSL | Small, declarative, already available | Unwieldy for filtering, joining, grouping, formatting and conditional resource transforms |
| Lua | Mature compact embedded language | More language/stdlib surface than required and a different developer experience |
| Embedded JavaScript | Familiar and broad ecosystem | More VM surface and mutable state than this MVP needs |
| Custom `.fabric` parser | Tailor-made syntax | High cost to maintain parser, diagnostics, semantics, toolchain |
| Starlark for Go | Go-hosted, Python-like, deterministic, controllable runtime | Interpreter budgets and safe host APIs still require careful implementation |

`.fabric` is a filename convention, **not a new parser**. The runtime binds one selected Starlark implementation behind an internal interface so the language can change under a new manifest/host API version without changing the on-device Page protocol.

## Versioned application bundle contract

A bundle is one immediate subdirectory of `FABRIC_APPS_DIR`, which is disabled by default. It contains a `fabric-app.json` manifest and one script entrypoint (initially a flat basename such as `main.fabric`). Example: `examples/apps/homelab/`. Bundles can be installed outside the Fabric Git repository. Manifest schema version `fabric_app: "0.1"` is independent of Fabric Page version `fabric: "0.2"`.

Required manifest fields:

- `fabric_app`: exactly `0.1`.
- `id`: stable, lowercase app ID (1–32 characters, `[a-z][a-z0-9_-]*`).
- `name`: human-facing app name.
- `entrypoint`: a relative, flat `.fabric` filename; no `/`, `..` or symlink.
- `pages`: 1–16 unique, lowercase local page IDs (1–32 characters).
- `resources`: optional array of up to 32 distinct *requested* host resource names. Declaring a name does not grant access; a server-side administrator must explicitly authorize/configure it.

A local page `overview` of app `homelab` publishes page ID `homelab-overview`. Each composed ID must match the existing Page v0.2 ID format and length; app/page combinations that collide with legacy JSON pages or other app pages are rejected. The page list is an upfront declaration so collisions can be checked before evaluating code. Runtime generation may not invent undeclared pages.

The app subdirectory name must equal the manifest `id`. The manifest is limited to 64 KiB, and the script entrypoint is limited to 256 KiB. App discovery scans at most 128 root entries and does not recurse. Files/directories and manifest contents have bounded sizes. A loader must not follow symlinks, escape the app root, accept duplicate manifest keys/IDs, or publish any pages from a malformed bundle. At #3 it *discovers/validates* bundles but does **not** execute or register them in the page API; that happens in #8.

## Proposed host API v0.1 (specified now, implemented in #4–#10)

The script exports two pure entrypoints:

- `pages()`: called at installation/reload, returns a map from **declared local page IDs** to `{"title": string, "layout": FabricComponentObject, "refresh": RefreshSpec}`. Fabric stamps full page IDs and `fabric: "0.2"`; builds the page's data provider automatically. Page layout is validated with existing Page v0.2 schemas and semantic checks. `pages()` must be independent of live network data.
- `data(page_id, resources)`: called on data refresh. `resources` is a read-only map of host-fetched, JSON-compatible resource values keyed by requested resource name. Return a JSON-compatible mapping whose keys are used by the page's existing `bind` expressions. `page_id` is the local page ID. Fabric provides no secrets, raw HTTP client, or arbitrary module imports.

The proposed generated provider ID is `{appID}.{localPageID}` (e.g. `homelab.overview`), and the page ID is `{appID}-{localPageID}`. This keeps existing `/api/pages`, `/api/pages/{id}` and `/api/pages/{id}/data` intact. Refresh behavior is specified on each generated page using the existing `manual`, `on-open` or `interval` strategies. Host execution must be bounded with a deadline, step limit, recursion limit and output-size limit (numeric budgets selected/tested in #4). Data-fetch concurrency, caching, stale/error fields and rate limiting belong to #5/#9.

The script's component value is an ordinary dictionary with the *existing* `row`, `column`, `card`, `text`, `metric`, `list`, `progress`, `chart` and `button` shapes. For v0.1, returning plain dictionaries is canonical; higher-level `ui` helpers may be added in later API versions but are not required to author a page. The host verifies that bindings refer to allowed structured data, that page refs remain within declared IDs, and that components/actions pass the existing Fabric validators.

### Illustrative author experience (not executable until #4–#8)

```python
def pages():
    return {
        "overview": {
            "title": "Homelab",
            "refresh": {"strategy": "interval", "seconds": 60},
            "layout": {
                "type": "column",
                "children": [
                    {"type": "text", "text": "Homelab", "style": "heading"},
                    {"type": "metric", "label": "Online", "bind": "homelab.online"},
                    {"type": "metric", "label": "Total", "bind": "homelab.total"}
                ],
            },
        },
    }

def data(page_id, resources):
    services = resources["homelab.services"]["services"]
    online = len([s for s in services if s["status"] == "running"])
    return {"homelab": {"online": online, "total": len(services)}}
```

`resources["homelab.services"]` comes from a host-configured, permitted JSON endpoint. The example source in `examples/apps/homelab` is intentionally not executed by #3.

## Security and execution boundaries

- **Trust model:** Initial install is a trusted server-administrator operation, not an end-user script upload or public marketplace. Even trusted scripts run with bounded computation and narrow capabilities. Do not claim the scripting VM is a strong security sandbox against actively malicious arbitrary tenant code.
- **No ambient authority:** No file reads/writes, subprocesses, arbitrary DNS/socket/HTTP requests, environment variables, tokens, random, wall-clock or unsafe module imports in scripts. Fabric resolves declared resource aliases and provides JSON values only.
- **Resource access:** Manifests declare requested resource names; separate server-only configuration authorizes endpoint, allowed method/host/redirect policy, TLS, size/time budget and secret references. Do not allow a script to control URL or HTTP method.
- **Credentials:** Keep node bearer tokens and upstream resource credentials outside Git and out of every script/page/data/error response; use opaque resource values. Explicit host-configured homelab private addresses are allowed only by administrator choice.
- **Node isolation:** Per-node data/state is keyed to authenticated node identity; sharing resource caches is allowed only for explicitly public/shared resources. App ID never acts as authentication; the existing `pages.read` and `actions.invoke` checks remain in force.
- **Actions:** The read-only MVP does not invoke services. When #10 adds script-defined actions, scripts may only reference registered capability IDs and server-validated arguments. A caller cannot submit source code, arbitrary action names, shell arguments or URLs.
- **Limits/handling:** Enforce CPU/step/cancellation, allocation/output, source, page, data and network budgets; errors identify bundle and line but redact sensitive host values. A failed app reload preserves the last-known-good snapshot and does not affect other apps; #3 only rejects malformed bundles before any publication.
- **Updates:** Loading/refreshing pages and script code occurs server-side. Changing a script does not require a firmware build. Firmware must first support the generic Fabric Page component subset and the existing authenticated protocol.

## Execution lifecycle (future work)

1. Discover and validate manifest/file layout (#3).
2. Compile Starlark source into per-app immutable entrypoints; reject unsupported builtins (#4).
3. Resolve authorized named resource handles using the host registry (#5).
4. Register transformed `data` outputs as Fabric providers (#6).
5. Compile `pages()` outputs to validated Page v0.2 with stable names (#7).
6. Atomically register/unregister app pages with the existing page store and API; hot reload retains last known good app on error (#8).
7. Cache/refresh and expose stale/error metadata with observability (#9); add capability-gated actions only later (#10).
8. Validate/preview locally, demo homelab data in browser and on physical X3 without per-app firmware changes (#11–#14).

## Compatibility and migration

Do **not** change Fabric Page v0.1/v0.2 shapes, existing stock/system Go providers or `/api/pages` endpoints. The app manifest has its own independent v0.1 schema. Apps are opt-in, disabled when `FABRIC_APPS_DIR` is unset. A legacy `pages/*.json` deployment works unchanged. A future incompatible script host API requires a new `fabric_app` version, not a silent semantic change.

## Test matrix

- #3: valid and invalid manifest schemas, unexpected/duplicate keys, missing/oversized entrypoints, symlink/path traversal, duplicate app/page/resource IDs, collisions with legacy pages, valid external app directories, no-config backward compatibility.
- #4–#7: evaluate scripts, line-specific error diagnostics, resource transforms, page binding/schema validation, budget exhaustion/cancellation, compatibility with static stock/system pages.
- #8–#10: atomic reload and rollback, permissions/secret redaction, unauthorized actions, cache isolation and rate limits.
- #11–#14: `fabricctl` validate/preview, mock and live homelab tests, physical X3/browser parity, and Pi performance smoke tests.

## Non-goals

No custom language parser, remote untrusted-app installation, arbitrary script OS/network access, device-side execution, new page renderer, dynamic data-defined component trees, DigitalOcean write APIs, or cross-game agent orchestration in this milestone.
