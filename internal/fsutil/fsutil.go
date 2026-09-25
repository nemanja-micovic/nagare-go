package fsutil

import (
	"os"
	"path/filepath"
)

// AtomicWrite writes data to a temp file and renames it into place.
// This prevents corruption from concurrent readers/writers.
//
// The temp file gets a unique name. A fixed "<path>.tmp" let two writers of
// the same file — two hooks of one session, a delivery and a reply to one
// message — write into the same temp file at once, and whichever renamed it
// first put a half-written mix of both into place. The name never ends in
// the target's extension, so a directory watcher filtering on it cannot pick
// up a file mid-write.
func AtomicWrite(path string, data []byte, perm os.FileMode) error {
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Chmod(tmp, perm); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}
