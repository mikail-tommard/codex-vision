// Package uimemory stores bounded, session-scoped receipts for verified locators.
// No UI text, field values, images, coordinates or executable paths are persisted.
package uimemory

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const maxEntries = 128

var ErrUnsupported = errors.New("private UI memory storage unsupported by selected filesystem")

type receipt struct {
	VerifiedAt time.Time `json:"verified_at"`
	Successes  uint64    `json:"successes"`
}

type document struct {
	Version   int                `json:"version"`
	Namespace string             `json:"namespace"`
	Salt      string             `json:"salt"`
	Entries   map[string]receipt `json:"entries"`
}

type Store struct {
	mu     sync.Mutex
	path   string
	ttl    time.Duration
	doc    document
	clock  func() time.Time
	hits   uint64
	misses uint64
}

func Open(directory, namespace string, ttl time.Duration) (*Store, error) {
	if !filepath.IsAbs(directory) || namespace == "" || len(namespace) > 64 || ttl < time.Minute || ttl > 30*24*time.Hour {
		return nil, fmt.Errorf("invalid UI memory scope or retention")
	}
	if err := preparePrivateDirectory(directory); err != nil {
		return nil, err
	}
	scope := sha256.Sum256([]byte(namespace))
	s := &Store{path: filepath.Join(directory, "ui-memory-"+hex.EncodeToString(scope[:])+".json"), ttl: ttl, clock: time.Now}
	s.doc = document{Version: 1, Namespace: namespace, Entries: make(map[string]receipt)}
	f, err := openPrivateFile(s.path)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	if err == nil {
		info, statErr := f.Stat()
		if statErr != nil || !info.Mode().IsRegular() || info.Size() > 1<<20 {
			_ = f.Close()
			return nil, fmt.Errorf("UI memory file must be private, regular and bounded")
		}
		var saved document
		dec := json.NewDecoder(io.LimitReader(f, (1<<20)+1))
		dec.DisallowUnknownFields()
		err = dec.Decode(&saved)
		var extra any
		if err == nil && dec.Decode(&extra) != io.EOF {
			err = fmt.Errorf("invalid trailing memory data")
		}
		closeErr := f.Close()
		if err != nil || closeErr != nil || saved.Version != 1 || len(saved.Entries) > maxEntries {
			return nil, fmt.Errorf("invalid UI memory document")
		}
		if saved.Namespace == namespace {
			s.doc = saved
		}
	}
	if s.doc.Salt == "" {
		var salt [32]byte
		if _, err := rand.Read(salt[:]); err != nil {
			return nil, err
		}
		s.doc.Salt = hex.EncodeToString(salt[:])
	}
	if salt, err := hex.DecodeString(s.doc.Salt); err != nil || len(salt) != 32 {
		return nil, fmt.Errorf("invalid memory salt")
	}
	if s.doc.Entries == nil {
		s.doc.Entries = make(map[string]receipt)
	}
	for key := range s.doc.Entries {
		if value, err := hex.DecodeString(key); err != nil || len(value) != sha256.Size {
			return nil, fmt.Errorf("invalid memory receipt key")
		}
	}
	if s.prune() {
		if err := s.flush(); err != nil {
			return nil, err
		}
	}
	return s, nil
}

// Key uses length-delimited components; only the digest is stored on disk.
func (s *Store) Key(parts ...string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	h := hmac.New(sha256.New, []byte(s.doc.Salt))
	for _, part := range parts {
		fmt.Fprintf(h, "%d:", len(part))
		_, _ = io.WriteString(h, part)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func (s *Store) prune() bool {
	now, changed := s.clock(), false
	for key, entry := range s.doc.Entries {
		if !now.Before(entry.VerifiedAt.Add(s.ttl)) || entry.VerifiedAt.After(now) || entry.Successes == 0 {
			delete(s.doc.Entries, key)
			changed = true
		}
	}
	return changed
}

func (s *Store) Known(key string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.prune() {
		if err := s.flush(); err != nil {
			return false, err
		}
	}
	_, ok := s.doc.Entries[key]
	if ok {
		s.hits++
	} else {
		s.misses++
	}
	return ok, nil
}

func (s *Store) Record(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if value, err := hex.DecodeString(key); err != nil || len(value) != sha256.Size {
		return fmt.Errorf("invalid memory key")
	}
	s.prune()
	if _, exists := s.doc.Entries[key]; !exists && len(s.doc.Entries) == maxEntries {
		oldest := ""
		for k, v := range s.doc.Entries {
			if oldest == "" || v.VerifiedAt.Before(s.doc.Entries[oldest].VerifiedAt) {
				oldest = k
			}
		}
		delete(s.doc.Entries, oldest)
	}
	e := s.doc.Entries[key]
	e.VerifiedAt, e.Successes = s.clock().UTC(), e.Successes+1
	s.doc.Entries[key] = e
	return s.flush()
}

func (s *Store) Clear() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.doc.Entries = make(map[string]receipt)
	s.hits, s.misses = 0, 0
	return s.flush()
}

func (s *Store) Stats() map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	count := 0
	now := s.clock()
	for _, e := range s.doc.Entries {
		if now.Before(e.VerifiedAt.Add(s.ttl)) && !e.VerifiedAt.After(now) {
			count++
		}
	}
	return map[string]any{"entries": count, "hits": s.hits, "misses": s.misses, "retention_hours": s.ttl.Hours(), "scope": "session-scoped hashed verified-locator receipts; never input authorization"}
}

func (s *Store) flush() error {
	f, err := createPrivateTemp(filepath.Dir(s.path))
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err := json.NewEncoder(f).Encode(s.doc); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return replacePrivateFile(f.Name(), s.path)
}
