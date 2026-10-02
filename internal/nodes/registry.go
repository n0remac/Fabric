package nodes

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"sync"
	"syscall"

	"golang.org/x/sys/unix"
)

var safeID = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)
var capability = regexp.MustCompile(`^[a-z][a-z0-9_.-]{0,127}$`)
var digestPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

var ErrExists = errors.New("node already exists")
var ErrNotFound = errors.New("node not found")

type Node struct {
	ID          string            `json:"id"`
	Type        string            `json:"type"`
	Name        string            `json:"name"`
	Enabled     bool              `json:"enabled"`
	Permissions []string          `json:"permissions"`
	Provides    []string          `json:"provides,omitempty"`
	Attributes  map[string]string `json:"attributes,omitempty"`
}

// Credential is separate from the node record so another credential kind can
// later resolve to the same node without changing application code.
type Credential struct {
	NodeID string `json:"node_id"`
	Kind   string `json:"kind"`
	SHA256 string `json:"sha256"`
}

type document struct {
	SchemaVersion int          `json:"schema_version"`
	Nodes         []Node       `json:"nodes"`
	Credentials   []Credential `json:"credentials"`
}

type Registry struct {
	mu   sync.RWMutex
	path string
	doc  document
}

func Open(path string, create bool) (*Registry, error) {
	r := &Registry{path: path, doc: document{SchemaVersion: 1, Nodes: []Node{}, Credentials: []Credential{}}}
	if err := r.Reload(); err != nil {
		if !create || !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		if err := os.MkdirAll(filepath.Dir(path), 0750); err != nil {
			return nil, err
		}
		if err := r.save(r.doc); err != nil {
			return nil, err
		}
	}
	return r, nil
}

func validate(d document) error {
	if d.SchemaVersion != 1 || d.Nodes == nil || d.Credentials == nil {
		return errors.New("invalid node registry schema")
	}
	ids, digests := map[string]bool{}, map[string]bool{}
	for _, n := range d.Nodes {
		if !safeID.MatchString(n.ID) || ids[n.ID] || !safeID.MatchString(n.Type) || n.Name == "" || len(n.Name) > 128 {
			return fmt.Errorf("invalid node %q", n.ID)
		}
		ids[n.ID] = true
		for _, p := range append(append([]string{}, n.Permissions...), n.Provides...) {
			if !capability.MatchString(p) {
				return fmt.Errorf("invalid capability %q", p)
			}
		}
		for k, v := range n.Attributes {
			if !safeID.MatchString(k) || len(v) > 256 {
				return fmt.Errorf("invalid attribute %q", k)
			}
		}
	}
	for _, c := range d.Credentials {
		if !ids[c.NodeID] || c.Kind != "bearer-sha256" || !digestPattern.MatchString(c.SHA256) || digests[c.SHA256] {
			return errors.New("invalid node credential")
		}
		digests[c.SHA256] = true
	}
	return nil
}

func (r *Registry) Reload() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	info, err := os.Lstat(r.path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0007 != 0 || info.Size() > 1<<20 {
		return errors.New("node registry must be a private regular file under 1 MiB")
	}
	f, err := os.Open(r.path)
	if err != nil {
		return err
	}
	defer f.Close()
	dec := json.NewDecoder(f)
	dec.DisallowUnknownFields()
	var d document
	if err := dec.Decode(&d); err != nil {
		return err
	}
	if err := dec.Decode(new(any)); !errors.Is(err, io.EOF) {
		return errors.New("node registry contains extra JSON values")
	}
	if err := validate(d); err != nil {
		return err
	}
	r.doc = d
	return nil
}

func (r *Registry) save(d document) error {
	if err := validate(d); err != nil {
		return err
	}
	dir := filepath.Dir(r.path)
	f, err := os.CreateTemp(dir, ".nodes-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if err := f.Chmod(0640); err != nil {
		return err
	}
	if info, err := os.Stat(r.path); err == nil {
		// Keep the installed root:fabric ownership across CLI edits.
		if stat, ok := info.Sys().(*syscall.Stat_t); ok {
			if err := f.Chown(int(stat.Uid), int(stat.Gid)); err != nil {
				return err
			}
		}
	}
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := enc.Encode(d); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(f.Name(), r.path); err != nil {
		return err
	}
	dirFile, errFile := os.Open(dir)
	if errFile != nil {
		return errFile
	}
	defer dirFile.Close()
	return dirFile.Sync()
}

func clone(n Node) Node {
	n.Permissions = append([]string(nil), n.Permissions...)
	n.Provides = append([]string(nil), n.Provides...)
	if n.Attributes != nil {
		attrs := make(map[string]string, len(n.Attributes))
		for k, v := range n.Attributes {
			attrs[k] = v
		}
		n.Attributes = attrs
	}
	return n
}

func (r *Registry) List() []Node {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Node, 0, len(r.doc.Nodes))
	for _, n := range r.doc.Nodes {
		out = append(out, clone(n))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
func (r *Registry) Get(id string) (Node, bool) {
	if err := r.Reload(); err != nil {
		return Node{}, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, n := range r.doc.Nodes {
		if n.ID == id {
			return clone(n), true
		}
	}
	return Node{}, false
}
func (r *Registry) Authenticate(token string) (Node, bool) {
	if len(token) != 64 {
		return Node{}, false
	}
	if err := r.Reload(); err != nil {
		return Node{}, false
	}
	sum := sha256.Sum256([]byte(token))
	r.mu.RLock()
	defer r.mu.RUnlock()
	var id string
	for _, c := range r.doc.Credentials {
		decoded, _ := hex.DecodeString(c.SHA256)
		if subtle.ConstantTimeCompare(sum[:], decoded) == 1 {
			id = c.NodeID
		}
	}
	for _, n := range r.doc.Nodes {
		if n.ID == id && n.Enabled {
			return clone(n), true
		}
	}
	return Node{}, false
}
func (r *Registry) edit(fn func(*document) error) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	lock, err := os.OpenFile(r.path+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := unix.Flock(int(lock.Fd()), unix.LOCK_EX); err != nil {
		return err
	}
	// Read the latest contents while holding the interprocess edit lock.
	f, err := os.Open(r.path)
	if err != nil {
		return err
	}
	var d document
	dec := json.NewDecoder(f)
	dec.DisallowUnknownFields()
	err = dec.Decode(&d)
	if err == nil {
		if tail := dec.Decode(new(any)); !errors.Is(tail, io.EOF) {
			err = errors.New("node registry contains extra JSON values")
		}
	}
	f.Close()
	if err != nil {
		return err
	}
	if err := validate(d); err != nil {
		return err
	}
	if err := fn(&d); err != nil {
		return err
	}
	if err := r.save(d); err != nil {
		return err
	}
	r.doc = d
	return nil
}
func (r *Registry) Add(n Node, digest string) error {
	if !digestPattern.MatchString(digest) {
		return errors.New("invalid credential digest")
	}
	return r.edit(func(d *document) error {
		for _, existing := range d.Nodes {
			if existing.ID == n.ID {
				return ErrExists
			}
		}
		for _, c := range d.Credentials {
			if c.SHA256 == digest {
				return errors.New("credential already registered")
			}
		}
		d.Nodes = append(d.Nodes, clone(n))
		d.Credentials = append(d.Credentials, Credential{NodeID: n.ID, Kind: "bearer-sha256", SHA256: digest})
		return nil
	})
}
func (r *Registry) SetEnabled(id string, enabled bool) error {
	return r.edit(func(d *document) error {
		for i := range d.Nodes {
			if d.Nodes[i].ID == id {
				d.Nodes[i].Enabled = enabled
				return nil
			}
		}
		return ErrNotFound
	})
}
func (r *Registry) Revoke(id string) error {
	return r.edit(func(d *document) error {
		found := false
		nodes := d.Nodes[:0]
		for _, n := range d.Nodes {
			if n.ID == id {
				found = true
			} else {
				nodes = append(nodes, n)
			}
		}
		if !found {
			return ErrNotFound
		}
		d.Nodes = nodes
		credentials := d.Credentials[:0]
		for _, c := range d.Credentials {
			if c.NodeID != id {
				credentials = append(credentials, c)
			}
		}
		d.Credentials = credentials
		return nil
	})
}
func RandomToken() (string, string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", "", err
	}
	token := hex.EncodeToString(raw[:])
	sum := sha256.Sum256([]byte(token))
	return token, hex.EncodeToString(sum[:]), nil
}
func Has(n Node, permission string) bool {
	for _, p := range n.Permissions {
		if p == permission {
			return true
		}
	}
	return false
}
