package pages

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/n0remac/Fabric/internal/fabric"
)

const maxPageBytes = 256 << 10

type Summary struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Fabric string `json:"fabric"`
}

type Event struct {
	Changed []string
	Removed []string
}

type Store struct {
	directory string
	validator *fabric.Validator
	transform func(fabric.Page) (fabric.Page, error)

	mu      sync.RWMutex
	pages   map[string]fabric.Page
	digests map[string][sha256.Size]byte
	events  chan Event
}

func NewStore(directory string, validator *fabric.Validator, transforms ...func(fabric.Page) (fabric.Page, error)) (*Store, error) {
	if validator == nil {
		return nil, errors.New("page validator is required")
	}
	store := &Store{
		directory: directory,
		validator: validator,
		pages:     make(map[string]fabric.Page),
		digests:   make(map[string][sha256.Size]byte),
		events:    make(chan Event, 16),
	}
	if len(transforms) > 1 {
		return nil, errors.New("only one page transform is supported")
	}
	if len(transforms) == 1 {
		store.transform = transforms[0]
	}
	if _, err := store.Reload(); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *Store) Directory() string { return s.directory }

func (s *Store) Get(id string) (fabric.Page, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	page, ok := s.pages[id]
	return page, ok
}

func (s *Store) List() []Summary {
	s.mu.RLock()
	summaries := make([]Summary, 0, len(s.pages))
	for _, page := range s.pages {
		summaries = append(summaries, Summary{ID: page.ID, Title: page.Title, Fabric: page.Fabric})
	}
	s.mu.RUnlock()
	sort.Slice(summaries, func(i, j int) bool { return summaries[i].ID < summaries[j].ID })
	return summaries
}

func (s *Store) Events() <-chan Event { return s.events }

func (s *Store) Reload() (Event, error) {
	loaded, digests, err := loadDirectory(s.directory, s.validator, s.transform)
	if err != nil {
		return Event{}, err
	}

	s.mu.Lock()
	event := diffSnapshots(s.digests, digests)
	s.pages, s.digests = loaded, digests
	s.mu.Unlock()
	return event, nil
}

func (s *Store) Watch(ctx context.Context, interval time.Duration, onError func(error)) {
	if interval <= 0 {
		interval = time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			event, err := s.Reload()
			if err != nil {
				if onError != nil {
					onError(err)
				}
				continue
			}
			if len(event.Changed) == 0 && len(event.Removed) == 0 {
				continue
			}
			select {
			case s.events <- event:
			case <-ctx.Done():
				return
			}
		}
	}
}

func loadDirectory(directory string, validator *fabric.Validator, transforms ...func(fabric.Page) (fabric.Page, error)) (map[string]fabric.Page, map[string][sha256.Size]byte, error) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, nil, fmt.Errorf("read pages directory %s: %w", directory, err)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	loaded := make(map[string]fabric.Page)
	digests := make(map[string][sha256.Size]byte)
	var loadErrors []error
	for _, entry := range entries {
		if entry.IsDir() || strings.ToLower(filepath.Ext(entry.Name())) != ".json" {
			continue
		}
		path := filepath.Join(directory, entry.Name())
		info, err := entry.Info()
		if err != nil {
			loadErrors = append(loadErrors, fmt.Errorf("%s: stat: %w", path, err))
			continue
		}
		if info.Size() > maxPageBytes {
			loadErrors = append(loadErrors, fmt.Errorf("%s: page exceeds %d bytes", path, maxPageBytes))
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			loadErrors = append(loadErrors, fmt.Errorf("%s: read: %w", path, err))
			continue
		}
		page, err := validator.Decode(data)
		if err != nil {
			loadErrors = append(loadErrors, fmt.Errorf("%s: %w", path, err))
			continue
		}
		if len(transforms) > 0 && transforms[0] != nil {
			page, err = transforms[0](page)
			if err != nil {
				loadErrors = append(loadErrors, fmt.Errorf("%s: transform: %w", path, err))
				continue
			}
			encoded, err := json.Marshal(page)
			if err != nil {
				loadErrors = append(loadErrors, fmt.Errorf("%s: encode transformed page: %w", path, err))
				continue
			}
			page, err = validator.Decode(encoded)
			if err != nil {
				loadErrors = append(loadErrors, fmt.Errorf("%s: validate transformed page: %w", path, err))
				continue
			}
		}
		filenameID := strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name()))
		if filenameID != page.ID {
			loadErrors = append(loadErrors, fmt.Errorf("%s: /id: page ID %q must match filename %q", path, page.ID, filenameID))
			continue
		}
		if _, exists := loaded[page.ID]; exists {
			loadErrors = append(loadErrors, fmt.Errorf("%s: duplicate page ID %q", path, page.ID))
			continue
		}
		loaded[page.ID] = page
		digests[page.ID] = sha256.Sum256(data)
	}
	if len(loadErrors) > 0 {
		return nil, nil, errors.Join(loadErrors...)
	}
	if len(loaded) == 0 {
		return nil, nil, fmt.Errorf("pages directory %s contains no page documents", directory)
	}
	for id, page := range loaded {
		if err := validator.ValidateReferences(page, loaded); err != nil {
			loadErrors = append(loadErrors, fmt.Errorf("%s.json: %w", id, err))
		}
	}
	if len(loadErrors) > 0 {
		return nil, nil, errors.Join(loadErrors...)
	}
	return loaded, digests, nil
}

func diffSnapshots(previous, current map[string][sha256.Size]byte) Event {
	var event Event
	for id, digest := range current {
		if old, exists := previous[id]; !exists || old != digest {
			event.Changed = append(event.Changed, id)
		}
	}
	for id := range previous {
		if _, exists := current[id]; !exists {
			event.Removed = append(event.Removed, id)
		}
	}
	sort.Strings(event.Changed)
	sort.Strings(event.Removed)
	return event
}
