package firmware

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

var (
	ErrInvalid     = errors.New("invalid firmware")
	ErrNotFound    = errors.New("firmware build not found")
	ErrTooLarge    = errors.New("firmware exceeds upload limit")
	buildIDPattern = regexp.MustCompile(`^[a-f0-9]{32}$`)
	shaPattern     = regexp.MustCompile(`^[a-f0-9]{64}$`)
	commitPattern  = regexp.MustCompile(`^[a-f0-9]{40}$|^[a-f0-9]{64}$`)
)

type Metadata struct {
	Version   string `json:"version"`
	Size      int64  `json:"size"`
	SHA256    string `json:"sha256"`
	GitCommit string `json:"git_commit"`
	Dirty     bool   `json:"dirty"`
}

func (m Metadata) Validate() error {
	if m.Version == "" || len(m.Version) > 128 || strings.TrimSpace(m.Version) != m.Version || strings.ContainsAny(m.Version, "\r\n\x00") {
		return fmt.Errorf("%w: invalid version", ErrInvalid)
	}
	if m.Size > MaxImageBytes {
		return ErrTooLarge
	}
	if m.Size < MinImageBytes || !shaPattern.MatchString(m.SHA256) || !commitPattern.MatchString(m.GitCommit) {
		return fmt.Errorf("%w: size, SHA256 or Git commit is invalid", ErrInvalid)
	}
	return nil
}

type Build struct {
	Metadata
	ID           string    `json:"id"`
	Device       string    `json:"device"`
	CreatedAt    time.Time `json:"created_at"`
	ChipID       uint16    `json:"chip_id"`
	BoardTag     string    `json:"board_tag"`
	DownloadPath string    `json:"download_path"`
}

type Channels struct {
	Dev    *string `json:"dev"`
	Stable *string `json:"stable"`
}
type Manifest struct {
	SchemaVersion int      `json:"schema_version"`
	Device        string   `json:"device"`
	Channels      Channels `json:"channels"`
	Builds        []Build  `json:"builds"`
}

type Store struct {
	mu        sync.Mutex
	directory string
	lock      *os.File
	manifest  Manifest
}

func NewStore(directory string) (_ *Store, err error) {
	if err := os.MkdirAll(directory, 0750); err != nil {
		return nil, err
	}
	lock, err := os.OpenFile(filepath.Join(directory, ".lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		lock.Close()
		return nil, fmt.Errorf("firmware registry already has a writer: %w", err)
	}
	s := &Store{directory: filepath.Join(directory, "x3"), lock: lock, manifest: Manifest{SchemaVersion: 1, Device: "x3", Builds: []Build{}}}
	defer func() {
		if err != nil {
			s.Close()
		}
	}()
	if err = os.MkdirAll(filepath.Join(s.directory, "builds"), 0750); err != nil {
		return nil, err
	}
	if err = os.MkdirAll(filepath.Join(s.directory, ".staging"), 0750); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(filepath.Join(s.directory, ".staging"))
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if err = os.Remove(filepath.Join(s.directory, ".staging", entry.Name())); err != nil {
			return nil, err
		}
	}
	f, err := regularFile(s.manifestPath())
	if errors.Is(err, os.ErrNotExist) {
		if _, err := AtomicJSON(s.manifestPath(), s.manifest, 0640); err != nil {
			return nil, err
		}
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if err = decodeJSON(f, &s.manifest); err != nil {
		return nil, fmt.Errorf("corrupt firmware manifest: %w", err)
	}
	if err = s.validateManifest(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error               { return s.lock.Close() }
func (s *Store) manifestPath() string       { return filepath.Join(s.directory, "manifest.json") }
func (s *Store) buildPath(id string) string { return filepath.Join(s.directory, "builds", id+".bin") }
func downloadPath(id string) string         { return "/api/firmware/v1/x3/builds/" + id + "/download" }

func (s *Store) validateManifest() error {
	if s.manifest.SchemaVersion != 1 || s.manifest.Device != "x3" || s.manifest.Builds == nil {
		return errors.New("unsupported or corrupt firmware manifest")
	}
	seen := map[string]bool{}
	for _, b := range s.manifest.Builds {
		if b.Metadata.Validate() != nil || !buildIDPattern.MatchString(b.ID) || seen[b.ID] || b.Device != "x3" || b.ChipID != ImageChipID || b.BoardTag != BoardTag || b.CreatedAt.IsZero() || b.DownloadPath != downloadPath(b.ID) {
			return errors.New("corrupt firmware build record")
		}
		seen[b.ID] = true
		f, err := regularFile(s.buildPath(b.ID))
		if err != nil {
			return fmt.Errorf("firmware build %s: %w", b.ID, err)
		}
		h := sha256.New()
		n, err := io.Copy(h, f)
		f.Close()
		if err != nil {
			return err
		}
		if n != b.Size || hex.EncodeToString(h.Sum(nil)) != b.SHA256 {
			return fmt.Errorf("stored firmware build %s failed integrity check", b.ID)
		}
	}
	for _, pointer := range []*string{s.manifest.Channels.Dev, s.manifest.Channels.Stable} {
		if pointer != nil && !seen[*pointer] {
			return errors.New("firmware pointer references missing build")
		}
	}
	return nil
}

func (s *Store) Manifest() Manifest {
	s.mu.Lock()
	defer s.mu.Unlock()
	m := s.manifest
	m.Builds = append([]Build{}, m.Builds...)
	if m.Channels.Dev != nil {
		id := *m.Channels.Dev
		m.Channels.Dev = &id
	}
	if m.Channels.Stable != nil {
		id := *m.Channels.Stable
		m.Channels.Stable = &id
	}
	return m
}

func (s *Store) Latest(channel string) (Build, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var id *string
	switch channel {
	case "stable":
		id = s.manifest.Channels.Stable
	case "dev":
		id = s.manifest.Channels.Dev
	default:
		return Build{}, ErrInvalid
	}
	if id == nil {
		return Build{}, ErrNotFound
	}
	return s.find(*id)
}

func (s *Store) find(id string) (Build, error) {
	for _, b := range s.manifest.Builds {
		if b.ID == id {
			return b, nil
		}
	}
	return Build{}, ErrNotFound
}

func (s *Store) Open(id string) (Build, *os.File, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := s.find(id)
	if err != nil {
		return Build{}, nil, err
	}
	f, err := regularFile(s.buildPath(id))
	return b, f, err
}

func (s *Store) Publish(metadata Metadata, source io.Reader) (Build, error) {
	if err := metadata.Validate(); err != nil {
		return Build{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := os.CreateTemp(filepath.Join(s.directory, ".staging"), "upload-*")
	if err != nil {
		return Build{}, err
	}
	defer f.Close()
	defer os.Remove(f.Name())
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(source, MaxImageBytes+1))
	if err != nil {
		return Build{}, err
	}
	if n > MaxImageBytes {
		return Build{}, ErrTooLarge
	}
	if n != metadata.Size || hex.EncodeToString(h.Sum(nil)) != metadata.SHA256 {
		return Build{}, fmt.Errorf("%w: uploaded size or SHA256 does not match metadata", ErrInvalid)
	}
	if err := ValidateImage(f); err != nil {
		return Build{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	var randomID [16]byte
	if _, err := rand.Read(randomID[:]); err != nil {
		return Build{}, err
	}
	b := Build{Metadata: metadata, ID: hex.EncodeToString(randomID[:]), Device: "x3", CreatedAt: time.Now().UTC(), ChipID: ImageChipID, BoardTag: BoardTag}
	b.DownloadPath = downloadPath(b.ID)
	if err := f.Chmod(0440); err != nil {
		return Build{}, err
	}
	if err := f.Sync(); err != nil {
		return Build{}, err
	}
	if err := f.Close(); err != nil {
		return Build{}, err
	}
	// A hard link publishes without replacing an existing immutable binary.
	if err := os.Link(f.Name(), s.buildPath(b.ID)); err != nil {
		return Build{}, err
	}
	if err := syncDirectory(filepath.Join(s.directory, "builds")); err != nil {
		return Build{}, err
	}
	m := s.manifest
	m.Builds = append(append([]Build{}, m.Builds...), b)
	m.Channels.Dev = &b.ID
	committed, err := AtomicJSON(s.manifestPath(), m, 0640)
	if committed {
		s.manifest = m
	}
	if err != nil {
		return Build{}, err
	}
	return b, nil
}

func (s *Store) Promote(id string) (Build, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := s.find(id)
	if err != nil {
		return Build{}, err
	}
	m := s.manifest
	m.Channels.Stable = &b.ID
	committed, err := AtomicJSON(s.manifestPath(), m, 0640)
	if committed {
		s.manifest = m
	}
	return b, err
}
