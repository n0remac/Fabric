package firmware

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
)

func decodeJSON(r io.Reader, value any) error {
	d := json.NewDecoder(r)
	d.DisallowUnknownFields()
	if err := d.Decode(value); err != nil {
		return err
	}
	if err := d.Decode(new(any)); !errors.Is(err, io.EOF) {
		return errors.New("expected exactly one JSON value")
	}
	return nil
}

// AtomicJSON returns committed=true once rename succeeds, even if directory sync fails.
func AtomicJSON(path string, value any, mode os.FileMode) (committed bool, err error) {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return false, err
	}
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, ".json-*")
	if err != nil {
		return false, err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if stat, statErr := os.Stat(path); statErr == nil {
		if owner, ok := stat.Sys().(*syscall.Stat_t); ok {
			if err := f.Chown(int(owner.Uid), int(owner.Gid)); err != nil {
				return false, err
			}
		}
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return false, statErr
	}
	if err := f.Chmod(mode); err != nil {
		return false, err
	}
	if _, err := f.Write(append(data, '\n')); err != nil {
		return false, err
	}
	if err := f.Sync(); err != nil {
		return false, err
	}
	if err := f.Close(); err != nil {
		return false, err
	}
	if err := os.Rename(f.Name(), path); err != nil {
		return false, err
	}
	return true, syncDirectory(dir)
}

func syncDirectory(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

func regularFile(path string) (*os.File, error) {
	stat, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !stat.Mode().IsRegular() {
		return nil, fmt.Errorf("not a regular file: %s", path)
	}
	return os.Open(path)
}
