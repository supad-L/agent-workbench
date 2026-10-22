package workspace

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"strings"
)

// Patch applies one exact file replacement to a frozen private snapshot.
// expected is the SHA-256 of the existing bytes, or "absent" for a new file.
// The execution supervisor must stop the snapshot's writers before calling it.
func Patch(directory string, scope Scope, name, expected string, content []byte) (returnErr error) {
	if err := scope.Validate(); err != nil {
		return err
	}
	if !scope.CanPatch(name) {
		return errors.New("file is not authorized for broker patching")
	}
	if len(content) > 8<<20 {
		return errors.New("patch exceeds size limit")
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return err
	}
	defer func() { returnErr = errors.Join(returnErr, root.Close()) }()
	parts := strings.Split(name, "/")
	for i := 1; i < len(parts); i++ {
		info, err := root.Lstat(strings.Join(parts[:i], "/"))
		if err != nil {
			return err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("patch parent must be a real directory")
		}
	}
	info, statErr := root.Lstat(name)
	mode := fs.FileMode(0644)
	if errors.Is(statErr, fs.ErrNotExist) {
		if expected != "absent" {
			return errors.New("patch baseline changed")
		}
	} else {
		if statErr != nil {
			return statErr
		}
		if !info.Mode().IsRegular() || info.Size() > 8<<20 {
			return errors.New("patch target is not a bounded regular file")
		}
		old, err := root.ReadFile(name)
		if err != nil {
			return err
		}
		digest := sha256.Sum256(old)
		if hex.EncodeToString(digest[:]) != expected {
			return errors.New("patch baseline changed")
		}
		mode = info.Mode().Perm()
	}
	parent := path.Dir(name)
	file, err := root.OpenFile(path.Join(parent, ".awb-patch-"+randomSuffix()), os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	tempName := file.Name()
	// Root.OpenFile returns a display name; only the generated base is used below.
	tempRel := path.Join(parent, path.Base(tempName))
	defer func() {
		if err := root.Remove(tempRel); err != nil && !errors.Is(err, fs.ErrNotExist) {
			returnErr = errors.Join(returnErr, err)
		}
	}()
	if _, err := file.Write(content); err != nil {
		return errors.Join(err, file.Close())
	}
	if err := file.Sync(); err != nil {
		return errors.Join(err, file.Close())
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := root.Rename(tempRel, name); err != nil {
		return fmt.Errorf("commit patch: %w", err)
	}
	return nil
}
