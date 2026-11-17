//go:build !windows

package uimemory

import (
	"fmt"
	"os"
)

func preparePrivateDirectory(path string) error {
	if err := os.MkdirAll(path, 0700); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return fmt.Errorf("UI memory requires a private directory")
	}
	return nil
}

func openPrivateFile(path string) (*os.File, error) {
	before, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !before.Mode().IsRegular() || before.Mode().Perm()&0077 != 0 {
		return nil, fmt.Errorf("UI memory file must be private and regular")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	after, err := f.Stat()
	if err != nil || !after.Mode().IsRegular() || after.Mode().Perm()&0077 != 0 || !os.SameFile(before, after) {
		_ = f.Close()
		return nil, fmt.Errorf("UI memory file changed or is not private")
	}
	return f, nil
}

func createPrivateTemp(directory string) (*os.File, error) {
	if err := preparePrivateDirectory(directory); err != nil {
		return nil, err
	}
	return os.CreateTemp(directory, ".ui-memory-*")
}

func replacePrivateFile(from, to string) error { return os.Rename(from, to) }
