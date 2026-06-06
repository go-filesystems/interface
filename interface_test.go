package filesystem

import (
	"os"
	"testing"
	"time"
)

type fakeFS struct{}

var _ Filesystem = (*fakeFS)(nil)

func (fakeFS) Close() error                         { return nil }
func (fakeFS) ReadFile(path string) ([]byte, error) { return []byte(path), nil }
func (fakeFS) ListDir(path string) ([]DirEntry, error) {
	return []DirEntry{NewDirEntry(1, path, 2)}, nil
}
func (fakeFS) Stat(path string) (Stat, error)                             { return NewStat(0o644, uint64(len(path)), 3), nil }
func (fakeFS) WriteFile(path string, data []byte, perm os.FileMode) error { return nil }
func (fakeFS) ReadLink(path string) (string, error)                       { return path, nil }
func (fakeFS) MkDir(path string, perm os.FileMode) error                  { return nil }
func (fakeFS) DeleteFile(path string) error                               { return nil }
func (fakeFS) DeleteDir(path string) error                                { return nil }
func (fakeFS) Rename(oldPath, newPath string) error                       { return nil }

// capableFS satisfies Filesystem plus every capability interface this
// package exposes. It exists to keep the capability surface compile-
// checked in this package — drivers in sibling repos do the same with
// their own `var _ filesystem.X = (*driverFS)(nil)` assertions, but
// since this package has no driver it builds its own minimal mock.
type capableFS struct{ fakeFS }

func (capableFS) Symlink(target, linkPath string) error          { return nil }
func (capableFS) Link(oldPath, newPath string) error             { return nil }
func (capableFS) Truncate(path string, newSize int64) error      { return nil }
func (capableFS) Chmod(path string, perm os.FileMode) error      { return nil }
func (capableFS) Chown(path string, uid, gid uint32) error       { return nil }
func (capableFS) Chtimes(path string, atime, mtime time.Time) error {
	return nil
}
func (capableFS) Label() string                  { return "" }
func (capableFS) SetLabel(label string) error    { return nil }
func (capableFS) GrowTo(newSizeBytes int64) error { return nil }

var (
	_ Filesystem     = (*capableFS)(nil)
	_ Symlinker      = (*capableFS)(nil)
	_ HardLinker     = (*capableFS)(nil)
	_ Truncater      = (*capableFS)(nil)
	_ MetadataSetter = (*capableFS)(nil)
	_ LabelReader    = (*capableFS)(nil) // satisfied transitively via Labeller
	_ Labeller       = (*capableFS)(nil)
	_ Grower         = (*capableFS)(nil)
)

// readonlyLabelFS only implements LabelReader (no SetLabel) — models
// the apfs case where rename requires a full COW transaction. Used to
// verify the Labeller / LabelReader split.
type readonlyLabelFS struct{ fakeFS }

func (readonlyLabelFS) Label() string { return "static" }

var _ LabelReader = (*readonlyLabelFS)(nil)

func TestCapabilityProbes(t *testing.T) {
	// Returning the bare Filesystem interface, the capability probes
	// must succeed when the concrete value implements them.
	var fs Filesystem = capableFS{}

	if _, ok := fs.(Symlinker); !ok {
		t.Error("Symlinker probe failed on capableFS")
	}
	if _, ok := fs.(HardLinker); !ok {
		t.Error("HardLinker probe failed on capableFS")
	}
	if _, ok := fs.(Truncater); !ok {
		t.Error("Truncater probe failed on capableFS")
	}
	if _, ok := fs.(MetadataSetter); !ok {
		t.Error("MetadataSetter probe failed on capableFS")
	}
	if _, ok := fs.(Labeller); !ok {
		t.Error("Labeller probe failed on capableFS")
	}
	if _, ok := fs.(LabelReader); !ok {
		t.Error("LabelReader probe failed on capableFS (Labeller embeds LabelReader)")
	}
	if _, ok := fs.(Grower); !ok {
		t.Error("Grower probe failed on capableFS")
	}

	// Conversely, the bare fakeFS implements only Filesystem — every
	// capability probe must report false. This guards the contract:
	// drivers can't accidentally satisfy a capability interface via
	// some unrelated method that happens to share a name.
	var bare Filesystem = fakeFS{}
	if _, ok := bare.(Symlinker); ok {
		t.Error("Symlinker probe unexpectedly succeeded on fakeFS")
	}
	if _, ok := bare.(Truncater); ok {
		t.Error("Truncater probe unexpectedly succeeded on fakeFS")
	}

	// readonlyLabelFS implements Label() but not SetLabel() — proves
	// the Labeller / LabelReader split lets generic code probe each
	// capability independently.
	var ro Filesystem = readonlyLabelFS{}
	if _, ok := ro.(LabelReader); !ok {
		t.Error("LabelReader probe failed on readonlyLabelFS")
	}
	if _, ok := ro.(Labeller); ok {
		t.Error("Labeller probe unexpectedly succeeded on readonlyLabelFS (it only reads)")
	}
}

func TestNewDirEntry(t *testing.T) {
	entry := NewDirEntry(42, "hello", 7)
	if entry.Inode() != 42 {
		t.Fatalf("Inode() = %d, want 42", entry.Inode())
	}
	if entry.Name() != "hello" {
		t.Fatalf("Name() = %q, want %q", entry.Name(), "hello")
	}
	if entry.FileType() != 7 {
		t.Fatalf("FileType() = %d, want 7", entry.FileType())
	}
}

func TestNewStat(t *testing.T) {
	stat := NewStat(0o755, 99, 12)
	if stat.Mode() != 0o755 {
		t.Fatalf("Mode() = %#o, want %#o", stat.Mode(), uint16(0o755))
	}
	if stat.Size() != 99 {
		t.Fatalf("Size() = %d, want 99", stat.Size())
	}
	if stat.Inode() != 12 {
		t.Fatalf("Inode() = %d, want 12", stat.Inode())
	}
}
