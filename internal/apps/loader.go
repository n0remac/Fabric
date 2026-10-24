package apps

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/n0remac/Fabric/schemas"
	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
)

var (
	compileOnce      sync.Once
	compiledManifest *jsonschema.Schema
	compileError     error
)

// LoadDirectory discovers immediate child app directories. Invalid bundles are
// reported and excluded, while valid bundles remain available in the catalog.
// This function never executes script source and never publishes app pages.
func LoadDirectory(directory string, reservedPageIDs []string) (*Catalog, error) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, fmt.Errorf("read Fabric app directory %q: %w", directory, err)
	}
	if len(entries) > MaxBundles {
		// There can be unrelated regular files, but keep scanning bounded.
		return nil, fmt.Errorf("app directory %q has too many entries (maximum %d)", directory, MaxBundles)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })

	reserved := make(map[string]string, len(reservedPageIDs))
	for _, id := range reservedPageIDs {
		reserved[id] = "legacy page"
	}
	catalog := &Catalog{Bundles: make([]Bundle, 0)}
	usedApps := make(map[string]struct{})
	var problems []error
	for _, entry := range entries {
		if entry.Type()&os.ModeSymlink != 0 {
			problems = append(problems, fmt.Errorf("%s: app directory symlinks are forbidden", filepath.Join(directory, entry.Name())))
			continue
		}
		if !entry.IsDir() {
			continue
		}
		path := filepath.Join(directory, entry.Name())
		bundle, err := loadBundle(path)
		if err != nil {
			problems = append(problems, err)
			continue
		}
		if _, seen := usedApps[bundle.Manifest.ID]; seen {
			problems = append(problems, fmt.Errorf("%s: duplicate app id %q", path, bundle.Manifest.ID))
			continue
		}
		var collisions []string
		for _, id := range bundle.PageIDs {
			if owner, exists := reserved[id]; exists {
				collisions = append(collisions, fmt.Sprintf("%s (already owned by %s)", id, owner))
			}
		}
		if len(collisions) > 0 {
			problems = append(problems, fmt.Errorf("%s: page ID collision: %s", path, strings.Join(collisions, ", ")))
			continue
		}
		usedApps[bundle.Manifest.ID] = struct{}{}
		for _, id := range bundle.PageIDs {
			reserved[id] = bundle.Manifest.ID
		}
		catalog.Bundles = append(catalog.Bundles, bundle)
	}
	return catalog, errors.Join(problems...)
}

func loadBundle(directory string) (Bundle, error) {
	manifestBytes, err := readRegularFile(filepath.Join(directory, ManifestFile), MaxManifestBytes)
	if err != nil {
		return Bundle{}, fmt.Errorf("%s: %w", directory, err)
	}
	manifest, err := decodeManifest(manifestBytes)
	if err != nil {
		return Bundle{}, fmt.Errorf("%s/%s: %w", directory, ManifestFile, err)
	}
	if filepath.Base(directory) != manifest.ID {
		return Bundle{}, fmt.Errorf("%s: directory name must match app ID %q", directory, manifest.ID)
	}
	// The schema restricts entrypoints to flat basenames. Check explicitly
	// as well, so future schema versions cannot silently enable traversal.
	if manifest.Entrypoint != filepath.Base(manifest.Entrypoint) ||
		manifest.Entrypoint == "." || manifest.Entrypoint == ".." {
		return Bundle{}, fmt.Errorf("%s: entrypoint must be a single filename", directory)
	}
	pageIDs := make([]string, 0, len(manifest.Pages))
	for _, localID := range manifest.Pages {
		id := manifest.ID + "-" + localID
		if len(id) > 64 {
			return Bundle{}, fmt.Errorf("%s: composed page ID %q exceeds Fabric Page's 64-character limit", directory, id)
		}
		pageIDs = append(pageIDs, id)
	}
	script, err := readRegularFile(filepath.Join(directory, manifest.Entrypoint), MaxScriptBytes)
	if err != nil {
		return Bundle{}, fmt.Errorf("%s: entrypoint %q: %w", directory, manifest.Entrypoint, err)
	}
	if len(bytes.TrimSpace(script)) == 0 {
		return Bundle{}, fmt.Errorf("%s: entrypoint %q is empty", directory, manifest.Entrypoint)
	}
	return Bundle{Manifest: manifest, Directory: directory, Source: script, PageIDs: pageIDs}, nil
}

func readRegularFile(path string, limit int64) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s: must be a regular file (symlinks forbidden)", path)
	}
	if info.Size() > limit {
		return nil, fmt.Errorf("%s: exceeds %d bytes", path, limit)
	}
	// Restrict opening to the parent directory so an entrypoint cannot escape
	// its bundle even if a symlink is swapped in after Lstat.
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return nil, fmt.Errorf("open bundle root: %w", err)
	}
	defer root.Close()
	file, err := root.Open(filepath.Base(path))
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%s: exceeds %d bytes", path, limit)
	}
	return data, nil
}

func decodeManifest(data []byte) (Manifest, error) {
	if err := rejectDuplicateFields(data); err != nil {
		return Manifest{}, err
	}
	schema, err := manifestSchema()
	if err != nil {
		return Manifest{}, err
	}
	instance, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		return Manifest{}, fmt.Errorf("decode JSON: %w", err)
	}
	if err := schema.Validate(instance); err != nil {
		return Manifest{}, fmt.Errorf("manifest schema: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var manifest Manifest
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, fmt.Errorf("decode manifest: %w", err)
	}
	return manifest, nil
}

func manifestSchema() (*jsonschema.Schema, error) {
	compileOnce.Do(func() {
		document, err := jsonschema.UnmarshalJSON(bytes.NewReader(schemas.FabricAppV01))
		if err != nil {
			compileError = err
			return
		}
		compiler := jsonschema.NewCompiler()
		const url = "https://fabric.local/schemas/fabric-app-v0.1.json"
		if compileError = compiler.AddResource(url, document); compileError != nil {
			return
		}
		compiledManifest, compileError = compiler.Compile(url)
	})
	return compiledManifest, compileError
}

// Manifests only contain scalar fields and arrays (no nested JSON objects);
// checking duplicate top-level keys prevents unnoticed authorization overrides.
func rejectDuplicateFields(data []byte) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	first, err := dec.Token()
	if err != nil {
		return fmt.Errorf("decode JSON object: %w", err)
	}
	if first != json.Delim('{') {
		return errors.New("manifest must be a JSON object")
	}
	seen := make(map[string]bool)
	for dec.More() {
		token, err := dec.Token()
		if err != nil {
			return err
		}
		key, ok := token.(string)
		if !ok {
			return errors.New("manifest key must be a string")
		}
		if seen[key] {
			return fmt.Errorf("duplicate manifest key %q", key)
		}
		seen[key] = true
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return fmt.Errorf("field %q: %w", key, err)
		}
	}
	if _, err := dec.Token(); err != nil {
		return err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		if err != nil {
			return err
		}
		return errors.New("manifest must contain exactly one JSON object")
	}
	return nil
}
