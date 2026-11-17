package uimemory

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLocalReceiptsAreScopedBoundedAndContentFree(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "private")
	s, err := Open(dir, "session-a", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	key := s.Key("private-app-path", "private-label-token", "private-structure")
	if err := s.Record(key); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(s.path)
	if err != nil || strings.Contains(string(data), "private-") {
		t.Fatal("raw content persisted", string(data), err)
	}
	again, err := Open(dir, "session-a", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if known, err := again.Known(again.Key("private-app-path", "private-label-token", "private-structure")); err != nil || !known {
		t.Fatal("verified receipt not reusable in same scope", err)
	}
	other, err := Open(dir, "session-b", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if known, err := other.Known(key); err != nil || known {
		t.Fatal("receipt crossed session boundary", err)
	}
	for i := 0; i < maxEntries+5; i++ {
		if err := s.Record(s.Key(fmt.Sprint(i))); err != nil {
			t.Fatal(err)
		}
	}
	if s.Stats()["entries"].(int) != maxEntries {
		t.Fatal("memory limit exceeded", s.Stats())
	}
	if err := s.Clear(); err != nil || s.Stats()["entries"].(int) != 0 {
		t.Fatal("clear failed", err)
	}
}

func TestRetentionPrunesPersistedReceipts(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "private"), "session", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	key := s.Key("selector")
	if err := s.Record(key); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(2 * time.Hour)
	s.clock = func() time.Time { return future }
	if known, err := s.Known(key); err != nil || known {
		t.Fatal("expired receipt reused", err)
	}
	data, err := os.ReadFile(s.path)
	if err != nil || strings.Contains(string(data), key) {
		t.Fatal("expired receipt retained on disk", err)
	}
}

func TestInvalidMemoryFailsExplicitly(t *testing.T) {
	if _, err := Open("relative", "session", time.Hour); err == nil {
		t.Fatal("relative directory accepted")
	}
	s, err := Open(filepath.Join(t.TempDir(), "private"), "session", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Clear(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.path, []byte(`{"version":1,"arbitrary_secret":"value"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(filepath.Dir(s.path), "session", time.Hour); err == nil {
		t.Fatal("invalid schema accepted")
	}
}
