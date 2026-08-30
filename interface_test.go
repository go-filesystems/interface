package filesystem

import (
	"errors"
	"io"
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

func (capableFS) Symlink(target, linkPath string) error     { return nil }
func (capableFS) Link(oldPath, newPath string) error        { return nil }
func (capableFS) Truncate(path string, newSize int64) error { return nil }
func (capableFS) Chmod(path string, perm os.FileMode) error { return nil }
func (capableFS) Chown(path string, uid, gid uint32) error  { return nil }
func (capableFS) Chtimes(path string, atime, mtime time.Time) error {
	return nil
}
func (capableFS) Label() string                   { return "" }
func (capableFS) SetLabel(label string) error     { return nil }
func (capableFS) GrowTo(newSizeBytes int64) error { return nil }
func (capableFS) Resize(newSize int64) error      { return nil }

var (
	_ Filesystem     = (*capableFS)(nil)
	_ Symlinker      = (*capableFS)(nil)
	_ HardLinker     = (*capableFS)(nil)
	_ Truncater      = (*capableFS)(nil)
	_ MetadataSetter = (*capableFS)(nil)
	_ LabelReader    = (*capableFS)(nil) // satisfied transitively via Labeller
	_ Labeller       = (*capableFS)(nil)
	_ Grower         = (*capableFS)(nil)
	_ Resizer        = (*capableFS)(nil)
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
	if _, ok := fs.(Resizer); !ok {
		t.Error("Resizer probe failed on capableFS")
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
	if _, ok := bare.(Resizer); ok {
		t.Error("Resizer probe unexpectedly succeeded on fakeFS")
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

// shrinkUnsupportedFS models an XFS/ZFS-style driver that can grow
// but rejects shrink with the package sentinel.
type shrinkUnsupportedFS struct{ fakeFS }

func (shrinkUnsupportedFS) Resize(newSize int64) error {
	if newSize < 1024 { // arbitrary "current size" for the test
		return ErrShrinkUnsupported
	}
	return nil
}

var _ Resizer = (*shrinkUnsupportedFS)(nil)

func TestErrShrinkUnsupported(t *testing.T) {
	if ErrShrinkUnsupported == nil {
		t.Fatal("ErrShrinkUnsupported sentinel is nil")
	}
	if ErrShrinkUnsupported.Error() != "filesystem: shrink not supported" {
		t.Fatalf("ErrShrinkUnsupported message = %q", ErrShrinkUnsupported.Error())
	}
	// Probe via the Resizer interface to make sure the sentinel is
	// usable with errors.Is — the documented contract for callers.
	var fs Filesystem = shrinkUnsupportedFS{}
	r, ok := fs.(Resizer)
	if !ok {
		t.Fatal("shrinkUnsupportedFS does not satisfy Resizer")
	}
	if err := r.Resize(2048); err != nil {
		t.Fatalf("Resize(grow) returned %v, want nil", err)
	}
	err := r.Resize(512)
	if !errors.Is(err, ErrShrinkUnsupported) {
		t.Fatalf("Resize(shrink) = %v, want ErrShrinkUnsupported", err)
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

// memFile is a minimal, correct File implementation over a byte slice. It
// exists to pin the io.ReaderAt semantics this package documents: short read
// only with a non-nil error, io.EOF at the end, offsets independent between
// calls. Drivers in sibling repos are expected to behave exactly like this.
type memFile struct{ b []byte }

func (f *memFile) Size() int64  { return int64(len(f.b)) }
func (f *memFile) Close() error { return nil }

func (f *memFile) ReadAt(p []byte, off int64) (int, error) {
	if off < 0 {
		return 0, errors.New("filesystem: negative offset")
	}
	if off >= int64(len(f.b)) {
		return 0, io.EOF
	}
	n := copy(p, f.b[off:])
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

// openerFS models a driver that can serve byte ranges.
type openerFS struct {
	fakeFS
	data map[string][]byte
}

func (fs openerFS) OpenFile(path string) (File, error) {
	b, ok := fs.data[path]
	if !ok {
		return nil, os.ErrNotExist
	}
	return &memFile{b: b}, nil
}

var (
	_ Opener = (*openerFS)(nil)
	_ File   = (*memFile)(nil)
)

func TestOpenerProbe(t *testing.T) {
	// A driver with the capability answers the probe...
	var fs Filesystem = openerFS{data: map[string][]byte{"/a": []byte("hello")}}
	o, ok := fs.(Opener)
	if !ok {
		t.Fatal("Opener probe failed on openerFS")
	}

	// ...and a driver without it does not, which is the whole point: the
	// caller falls back to ReadFile instead of failing.
	var bare Filesystem = fakeFS{}
	if _, ok := bare.(Opener); ok {
		t.Error("Opener probe unexpectedly succeeded on fakeFS")
	}
	if _, ok := bare.(Opener); !ok {
		if _, err := bare.ReadFile("/a"); err != nil {
			t.Errorf("ReadFile fallback failed: %v", err)
		}
	}

	if _, err := o.OpenFile("/missing"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("OpenFile(missing) = %v, want os.ErrNotExist", err)
	}

	f, err := o.OpenFile("/a")
	if err != nil {
		t.Fatalf("OpenFile: %v", err)
	}
	defer f.Close()
	if got := f.Size(); got != 5 {
		t.Fatalf("Size() = %d, want 5", got)
	}
}

// TestFileReadAtSemantics pins the three rules the doc comment on File makes
// binding, using the reference implementation. A driver that violates any of
// them breaks io.SectionReader silently, so they are asserted here as the
// canonical expectations.
func TestFileReadAtSemantics(t *testing.T) {
	f := &memFile{b: []byte("0123456789")}

	// Full read in the middle: n == len(p), nil error.
	p := make([]byte, 4)
	n, err := f.ReadAt(p, 2)
	if n != 4 || err != nil || string(p) != "2345" {
		t.Fatalf("ReadAt(4, 2) = %d, %v, %q", n, err, p)
	}

	// Read straddling the end: short read WITH io.EOF, bytes still delivered.
	p = make([]byte, 4)
	n, err = f.ReadAt(p, 8)
	if n != 2 || !errors.Is(err, io.EOF) || string(p[:n]) != "89" {
		t.Fatalf("ReadAt(4, 8) = %d, %v, %q", n, err, p[:n])
	}

	// Exactly at EOF and past it: 0, io.EOF.
	if n, err := f.ReadAt(p, 10); n != 0 || !errors.Is(err, io.EOF) {
		t.Fatalf("ReadAt at Size() = %d, %v, want 0, io.EOF", n, err)
	}
	if n, err := f.ReadAt(p, 99); n != 0 || !errors.Is(err, io.EOF) {
		t.Fatalf("ReadAt past Size() = %d, %v, want 0, io.EOF", n, err)
	}

	// Negative offset is an error, never a panic.
	if n, err := f.ReadAt(p, -1); n != 0 || err == nil {
		t.Fatalf("ReadAt(-1) = %d, %v, want an error", n, err)
	}

	// A File is usable as an io.SectionReader source — the consumer whose
	// assumptions the contract exists to protect.
	sr := io.NewSectionReader(f, 3, 4)
	got, err := io.ReadAll(sr)
	if err != nil {
		t.Fatalf("ReadAll(SectionReader): %v", err)
	}
	if string(got) != "3456" {
		t.Fatalf("SectionReader gave %q, want %q", got, "3456")
	}

	if err := f.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}
