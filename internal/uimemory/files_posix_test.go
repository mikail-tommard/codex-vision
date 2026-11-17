//go:build !windows

package uimemory

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPrivateStorageRejectsLinksAndBroadPermissions(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "private")
	s, err := Open(directory, "session", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Record(s.Key("fixture")); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(s.path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := openPrivateFile(s.path); err == nil {
		t.Fatal("broad file permissions accepted")
	}
	if err := os.Chmod(s.path, 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(directory, "link")
	if err := os.Symlink(s.path, link); err != nil {
		t.Fatal(err)
	}
	if _, err := openPrivateFile(link); err == nil {
		t.Fatal("symlink accepted as memory file")
	}
	if err := os.Chmod(directory, 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := createPrivateTemp(directory); err == nil {
		t.Fatal("temporary file created in non-private directory")
	}
}
