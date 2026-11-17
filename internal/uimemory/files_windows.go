//go:build windows

package uimemory

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

func currentStorageSID() (*windows.SID, error) {
	u, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, err
	}
	return u.User.Sid.Copy()
}

func privateDescriptor(directory bool) (*windows.SECURITY_DESCRIPTOR, error) {
	sid, err := currentStorageSID()
	if err != nil {
		return nil, err
	}
	flags := ""
	if directory {
		flags = "OICI"
	}
	return windows.SecurityDescriptorFromString("O:" + sid.String() + "D:P(A;" + flags + ";FA;;;" + sid.String() + ")(A;" + flags + ";FA;;;SY)")
}

func privateAttributes(sd *windows.SECURITY_DESCRIPTOR) windows.SecurityAttributes {
	return windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd}
}

func checkLocalMemoryPath(path string) error {
	volume := filepath.VolumeName(path)
	if !filepath.IsAbs(path) || len(volume) != 2 || volume[1] != ':' || strings.Contains(path[len(volume):], ":") {
		return fmt.Errorf("%w: memory requires a local drive path without streams", ErrUnsupported)
	}
	root, err := windows.UTF16PtrFromString(volume + `\`)
	if err != nil {
		return err
	}
	typeID := windows.GetDriveType(root)
	if typeID != windows.DRIVE_FIXED && typeID != windows.DRIVE_REMOVABLE && typeID != windows.DRIVE_RAMDISK {
		return fmt.Errorf("%w: memory requires a local writable volume", ErrUnsupported)
	}
	var flags uint32
	if err := windows.GetVolumeInformation(root, nil, 0, nil, nil, &flags, nil, 0); err != nil {
		return err
	}
	if flags&windows.FILE_PERSISTENT_ACLS == 0 || flags&windows.FILE_READ_ONLY_VOLUME != 0 {
		return fmt.Errorf("%w: memory requires persistent file ACLs", ErrUnsupported)
	}
	// Reject pre-existing junctions/symlinks in the configured path, without
	// modifying their ACLs or following them to create the private leaf.
	current := volume + `\`
	for _, part := range strings.Split(strings.TrimPrefix(filepath.Clean(path), current), `\`) {
		if part == "" {
			continue
		}
		current = filepath.Join(current, part)
		name, err := windows.UTF16PtrFromString(current)
		if err != nil {
			return err
		}
		attrs, err := windows.GetFileAttributes(name)
		if errors.Is(err, windows.ERROR_FILE_NOT_FOUND) || errors.Is(err, windows.ERROR_PATH_NOT_FOUND) {
			break
		}
		if err != nil {
			return err
		}
		if attrs&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
			return fmt.Errorf("UI memory path contains a reparse point")
		}
	}
	return nil
}

func normalizePrivateDescriptor(sd *windows.SECURITY_DESCRIPTOR) (privateACL, error) {
	var out privateACL
	if sd == nil || !sd.IsValid() {
		return out, fmt.Errorf("invalid memory security descriptor")
	}
	owner, _, err := sd.Owner()
	if err != nil || owner == nil || !owner.IsValid() {
		return out, fmt.Errorf("memory owner unavailable")
	}
	out.Owner = owner.String()
	control, _, err := sd.Control()
	if err != nil {
		return out, err
	}
	out.Protected = control&windows.SE_DACL_PROTECTED != 0
	dacl, _, err := sd.DACL()
	if err != nil || dacl == nil || dacl.AceCount > 8 {
		return out, fmt.Errorf("memory DACL unavailable or unsupported")
	}
	out.Present = true
	for i := uint32(0); i < uint32(dacl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, i, &ace); err != nil {
			return out, err
		}
		offset := unsafe.Offsetof(windows.ACCESS_ALLOWED_ACE{}.SidStart)
		if ace == nil || ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE || uintptr(ace.Header.AceSize) < offset+8 {
			return out, fmt.Errorf("unsupported memory ACE")
		}
		sidPtr := unsafe.Pointer(&ace.SidStart)
		count := *(*byte)(unsafe.Add(sidPtr, 1))
		if offset+8+4*uintptr(count) > uintptr(ace.Header.AceSize) {
			return out, fmt.Errorf("invalid memory ACE SID size")
		}
		sid := (*windows.SID)(sidPtr)
		if !sid.IsValid() {
			return out, fmt.Errorf("invalid memory ACE SID")
		}
		out.Entries = append(out.Entries, privateACE{Type: ace.Header.AceType, Flags: ace.Header.AceFlags, Mask: uint32(ace.Mask), SID: sid.String()})
	}
	runtime.KeepAlive(sd)
	return out, nil
}

func validatePrivateHandle(handle windows.Handle, directory bool) error {
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
		return err
	}
	if (info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0) != directory || info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 || (!directory && info.NumberOfLinks != 1) {
		return fmt.Errorf("memory requires a regular non-reparse file/directory without file hardlinks")
	}
	sd, err := windows.GetSecurityInfo(handle, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return err
	}
	acl, err := normalizePrivateDescriptor(sd)
	if err != nil {
		return err
	}
	user, err := currentStorageSID()
	if err != nil {
		return err
	}
	return validatePrivateACL(acl, user.String())
}

func openPrivateHandle(path string, directory bool) (windows.Handle, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return windows.InvalidHandle, err
	}
	access := uint32(windows.READ_CONTROL | windows.FILE_READ_ATTRIBUTES)
	if !directory {
		access |= windows.GENERIC_READ
	}
	h, err := windows.CreateFile(name, access, windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT|windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return windows.InvalidHandle, err
	}
	if err := validatePrivateHandle(h, directory); err != nil {
		_ = windows.CloseHandle(h)
		return windows.InvalidHandle, err
	}
	return h, nil
}

func preparePrivateDirectory(path string) error {
	if err := checkLocalMemoryPath(path); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	sd, err := privateDescriptor(true)
	if err != nil {
		return err
	}
	sa := privateAttributes(sd)
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	err = windows.CreateDirectory(name, &sa)
	runtime.KeepAlive(sd)
	if err != nil && !errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		return err
	}
	if err := checkLocalMemoryPath(path); err != nil {
		return err
	}
	h, err := openPrivateHandle(path, true)
	if err != nil {
		return err
	}
	return windows.CloseHandle(h)
}

func openPrivateFile(path string) (*os.File, error) {
	if err := checkLocalMemoryPath(filepath.Dir(path)); err != nil {
		return nil, err
	}
	h, err := openPrivateHandle(path, false)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(h), path), nil
}

func createPrivateTemp(directory string) (*os.File, error) {
	if err := preparePrivateDirectory(directory); err != nil {
		return nil, err
	}
	sd, err := privateDescriptor(false)
	if err != nil {
		return nil, err
	}
	sa := privateAttributes(sd)
	for attempts := 0; attempts < 8; attempts++ {
		var nonce [16]byte
		if _, err := rand.Read(nonce[:]); err != nil {
			return nil, err
		}
		path := filepath.Join(directory, ".ui-memory-"+hex.EncodeToString(nonce[:]))
		name, err := windows.UTF16PtrFromString(path)
		if err != nil {
			return nil, err
		}
		h, err := windows.CreateFile(name, windows.GENERIC_READ|windows.GENERIC_WRITE|windows.READ_CONTROL, 0, &sa, windows.CREATE_NEW, windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
		runtime.KeepAlive(sd)
		if errors.Is(err, windows.ERROR_FILE_EXISTS) || errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if err := validatePrivateHandle(h, false); err != nil {
			_ = windows.CloseHandle(h)
			_ = os.Remove(path)
			return nil, err
		}
		return os.NewFile(uintptr(h), path), nil
	}
	return nil, fmt.Errorf("cannot allocate unique private memory file")
}

func replacePrivateFile(from, to string) error {
	if filepath.Dir(from) != filepath.Dir(to) {
		return fmt.Errorf("memory replacement must stay in the same directory")
	}
	if err := preparePrivateDirectory(filepath.Dir(to)); err != nil {
		return err
	}
	for _, path := range []string{from, to} {
		h, err := openPrivateHandle(path, false)
		if os.IsNotExist(err) && path == to {
			continue
		}
		if err != nil {
			return err
		}
		if err := windows.CloseHandle(h); err != nil {
			return err
		}
	}
	source, err := windows.UTF16PtrFromString(from)
	if err != nil {
		return err
	}
	target, err := windows.UTF16PtrFromString(to)
	if err != nil {
		return err
	}
	// No delete-then-rename gap or cross-volume copy fallback. The source has a
	// protected DACL at creation; never inherit broader destination-file access.
	if err := windows.MoveFileEx(source, target, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH); err != nil {
		return err
	}
	h, err := openPrivateHandle(to, false)
	if err != nil {
		return err
	}
	return windows.CloseHandle(h)
}
