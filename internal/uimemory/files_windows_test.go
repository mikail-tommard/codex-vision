//go:build windows

package uimemory

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestWindowsNativeDescriptorPolicy(t *testing.T) {
	user, err := currentStorageSID()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, dacl string
		valid      bool
	}{
		{"private", "D:P(A;;FA;;;" + user.String() + ")(A;;FA;;;SY)", true},
		{"inheritable_directory", "D:P(A;OICI;FA;;;" + user.String() + ")", true},
		{"world_read", "D:P(A;;FA;;;" + user.String() + ")(A;;FR;;;WD)", false},
		{"unprotected", "D:(A;;FA;;;" + user.String() + ")", false},
		{"null_dacl", "D:NO_ACCESS_CONTROL", false},
		{"empty_dacl", "D:P", false},
		{"denied", "D:P(D;;FW;;;" + user.String() + ")(A;;FA;;;" + user.String() + ")", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sd, err := windows.SecurityDescriptorFromString("O:" + user.String() + tc.dacl)
			if err != nil {
				t.Fatal(err)
			}
			acl, err := normalizePrivateDescriptor(sd)
			if err == nil {
				err = validatePrivateACL(acl, user.String())
			}
			if (err == nil) != tc.valid {
				t.Fatal("native descriptor policy disagrees", err)
			}
		})
	}
}

func TestWindowsPrivateFileACLAfterReplacement(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "private")
	s, err := Open(directory, "session", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	key := s.Key("fixture")
	for range 3 {
		if err := s.Record(key); err != nil {
			t.Fatal("repeat atomic replacement failed", err)
		}
		h, err := openPrivateHandle(s.path, false)
		if err != nil {
			t.Fatal("replacement lost private ACL", err)
		}
		if err := windows.CloseHandle(h); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := Open(directory, "session", time.Hour); err != nil {
		t.Fatal("private replacement cannot be reopened", err)
	}
}

func TestWindowsUnsafeExistingACLIsRejectedWithoutRepair(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "broad")
	user, err := currentStorageSID()
	if err != nil {
		t.Fatal(err)
	}
	sd, err := windows.SecurityDescriptorFromString("O:" + user.String() + "D:P(A;OICI;FA;;;" + user.String() + ")(A;OICI;FR;;;WD)")
	if err != nil {
		t.Fatal(err)
	}
	sa := privateAttributes(sd)
	name, err := windows.UTF16PtrFromString(directory)
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.CreateDirectory(name, &sa); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(directory, "session", time.Hour); err == nil {
		t.Fatal("world-readable directory accepted")
	}
	actual, err := windows.GetNamedSecurityInfo(directory, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	acl, err := normalizePrivateDescriptor(actual)
	if err != nil || len(acl.Entries) != 2 || acl.Entries[1].SID != "S-1-1-0" {
		t.Fatal("existing directory permissions were modified", err)
	}
	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) != 0 {
		t.Fatal("unsafe directory received memory data", entries, err)
	}
}

func TestWindowsMemoryRejectsNonlocalPaths(t *testing.T) {
	for _, path := range []string{`\\server\share\cache`, `\\?\C:\cache`, `C:\cache:stream`} {
		if err := checkLocalMemoryPath(path); !errors.Is(err, ErrUnsupported) {
			t.Fatal("unsupported path not explicit", path, err)
		}
	}
}

func TestWindowsMemoryRejectsHardlinkedData(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "private")
	s, err := Open(directory, "session", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Record(s.Key("fixture")); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(s.path, filepath.Join(directory, "alias")); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(directory, "session", time.Hour); err == nil {
		t.Fatal("hardlinked data accepted")
	}
}
