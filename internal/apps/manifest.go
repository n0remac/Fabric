// Package apps validates and discovers declarative Fabric app bundles.
// It does not evaluate scripts or register pages.
package apps

import "path/filepath"

const (
	ManifestFile           = "fabric-app.json"
	ManifestVersion        = "0.1"
	MaxManifestBytes int64 = 64 << 10
	MaxScriptBytes   int64 = 256 << 10
	MaxBundles             = 128
)

type Manifest struct {
	FabricApp  string   `json:"fabric_app"`
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Entrypoint string   `json:"entrypoint"`
	Pages      []string `json:"pages"`
	Resources  []string `json:"resources,omitempty"`
}

type Bundle struct {
	Manifest  Manifest
	Directory string
	// Source contains bytes for future interpretation; never executed by discovery.
	Source  []byte
	PageIDs []string
}

func (b Bundle) EntrypointPath() string {
	return filepath.Join(b.Directory, b.Manifest.Entrypoint)
}

type Catalog struct {
	Bundles []Bundle
}
