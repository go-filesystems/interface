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

// memWritableFile is a minimal, correct WritableFile over a byte slice. Like
// memFile above it exists to pin semantics rather than to be useful: it is the
// reference every driver in a sibling repo is expected to behave like, and the
// place the contract's edge cases are written down executably.
type memWritableFile struct {
	b      []byte
	synced int // counts Sync calls, so a test can prove one happened
}

func (f *memWritableFile) Size() int64  { return int64(len(f.b)) }
func (f *memWritableFile) Close() error { return nil }

func (f *memWritableFile) ReadAt(p []byte, off int64) (int, error) {
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

// WriteAt follows io.WriterAt: it writes all of p or reports why it could not.
// This implementation extends the file, zero-filling any gap left by an offset
// past the current end — a driver whose format cannot do that returns an error
// instead and says so in its own doc comment.
func (f *memWritableFile) WriteAt(p []byte, off int64) (int, error) {
	if off < 0 {
		return 0, errors.New("filesystem: negative offset")
	}
	if end := off + int64(len(p)); end > int64(len(f.b)) {
		grown := make([]byte, end)
		copy(grown, f.b)
		f.b = grown
	}
	return copy(f.b[off:], p), nil
}

func (f *memWritableFile) Truncate(size int64) error {
	if size < 0 {
		return errors.New("filesystem: negative size")
	}
	if size <= int64(len(f.b)) {
		f.b = f.b[:size]
		return nil
	}
	grown := make([]byte, size)
	copy(grown, f.b)
	f.b = grown
	return nil
}

func (f *memWritableFile) Sync() error { f.synced++; return nil }

// fixedSizeFile models the other documented shape: a driver that can overwrite
// bytes in place but cannot grow the file, which must refuse rather than write
// short. It proves the contract has room for that case without weakening
// io.WriterAt for everyone else.
type fixedSizeFile struct{ memWritableFile }

var errCannotExtend = errors.New("filesystem: file cannot be extended")

func (f *fixedSizeFile) WriteAt(p []byte, off int64) (int, error) {
	if off < 0 || off+int64(len(p)) > int64(len(f.b)) {
		return 0, errCannotExtend
	}
	return copy(f.b[off:], p), nil
}

func (f *fixedSizeFile) Truncate(size int64) error { return errCannotExtend }

var (
	_ File         = (*memWritableFile)(nil)
	_ WritableFile = (*memWritableFile)(nil)
	_ WritableFile = (*fixedSizeFile)(nil)
	_ io.ReaderAt  = (*memWritableFile)(nil)
	_ io.WriterAt  = (*memWritableFile)(nil)
)

// TestWritableFileProbe pins the capability's central promise: the assertion is
// on the FILE, a driver without it is not broken by the addition, and the
// caller learns which it has instead of guessing.
func TestWritableFileProbe(t *testing.T) {
	// A read-only File must NOT satisfy WritableFile — otherwise a caller
	// would write into a snapshot and lose the bytes silently.
	var ro File = &memFile{b: []byte("hello")}
	if _, ok := ro.(WritableFile); ok {
		t.Error("WritableFile probe unexpectedly succeeded on memFile")
	}

	// ...and the fallback path a caller takes in that case still works.
	if _, err := ro.ReadAt(make([]byte, 5), 0); err != nil {
		t.Errorf("read-only fallback failed: %v", err)
	}

	var rw File = &memWritableFile{b: []byte("hello")}
	w, ok := rw.(WritableFile)
	if !ok {
		t.Fatal("WritableFile probe failed on memWritableFile")
	}
	// Every WritableFile is a File: the embedded read surface still answers.
	if got := w.Size(); got != 5 {
		t.Fatalf("Size() = %d, want 5", got)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// TestWritableFileWriteAtSemantics pins the io.WriterAt contract the doc
// comment makes binding, plus the Size rule that distinguishes a WritableFile
// from the read-only snapshot File describes.
func TestWritableFileWriteAtSemantics(t *testing.T) {
	f := &memWritableFile{b: []byte("0123456789")}

	// Overwrite inside the file: n == len(p), nil error, no size change.
	n, err := f.WriteAt([]byte("ab"), 2)
	if n != 2 || err != nil {
		t.Fatalf("WriteAt(2) = %d, %v", n, err)
	}
	if string(f.b) != "01ab456789" {
		t.Fatalf("after in-place write, content = %q", f.b)
	}
	if f.Size() != 10 {
		t.Fatalf("Size() = %d after in-place write, want 10", f.Size())
	}

	// A write that extends: Size must report the new length immediately —
	// the rule that separates a WritableFile from a read-only snapshot.
	if n, err := f.WriteAt([]byte("XY"), 10); n != 2 || err != nil {
		t.Fatalf("WriteAt(extend) = %d, %v", n, err)
	}
	if f.Size() != 12 {
		t.Fatalf("Size() = %d after extending write, want 12", f.Size())
	}

	// A write past the end leaves a hole that reads back as zeros.
	if _, err := f.WriteAt([]byte("Z"), 15); err != nil {
		t.Fatalf("WriteAt(hole): %v", err)
	}
	got := make([]byte, 3)
	if _, err := f.ReadAt(got, 12); err != nil {
		t.Fatalf("ReadAt over hole: %v", err)
	}
	if got[0] != 0 || got[1] != 0 || got[2] != 0 {
		t.Fatalf("hole read back as %v, want zeros", got)
	}

	// Negative offset is an error, never a panic.
	if n, err := f.WriteAt([]byte("q"), -1); n != 0 || err == nil {
		t.Fatalf("WriteAt(-1) = %d, %v, want an error", n, err)
	}

	// Read and write agree: what WriteAt put there is what ReadAt returns.
	back := make([]byte, 2)
	if _, err := f.ReadAt(back, 2); err != nil {
		t.Fatalf("ReadAt: %v", err)
	}
	if string(back) != "ab" {
		t.Fatalf("ReadAt after WriteAt = %q, want %q", back, "ab")
	}

	// A WritableFile is usable as an io.WriterAt by generic code.
	var w io.WriterAt = f
	if _, err := w.WriteAt([]byte("!"), 0); err != nil {
		t.Fatalf("WriteAt via io.WriterAt: %v", err)
	}
	if f.b[0] != '!' {
		t.Fatalf("io.WriterAt write did not land: %q", f.b)
	}
}

func TestWritableFileTruncateAndSync(t *testing.T) {
	f := &memWritableFile{b: []byte("0123456789")}

	// Shrink: trailing bytes go, Size follows.
	if err := f.Truncate(4); err != nil {
		t.Fatalf("Truncate(4): %v", err)
	}
	if f.Size() != 4 || string(f.b) != "0123" {
		t.Fatalf("after Truncate(4): size=%d content=%q", f.Size(), f.b)
	}

	// Grow: zero-filled, Size follows, and the new bytes read back as zeros.
	if err := f.Truncate(8); err != nil {
		t.Fatalf("Truncate(8): %v", err)
	}
	if f.Size() != 8 {
		t.Fatalf("Size() = %d after Truncate(8), want 8", f.Size())
	}
	tail := make([]byte, 4)
	if _, err := f.ReadAt(tail, 4); err != nil {
		t.Fatalf("ReadAt of grown tail: %v", err)
	}
	for i, b := range tail {
		if b != 0 {
			t.Fatalf("grown byte %d = %d, want 0", i, b)
		}
	}

	// Truncate to zero, then negative: the second is an error.
	if err := f.Truncate(0); err != nil {
		t.Fatalf("Truncate(0): %v", err)
	}
	if err := f.Truncate(-1); err == nil {
		t.Fatal("Truncate(-1) returned nil, want an error")
	}

	// Sync is what lets a server answer NFS COMMIT truthfully, so a caller
	// must be able to observe that it ran and that it reported success.
	if err := f.Sync(); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if f.synced != 1 {
		t.Fatalf("Sync count = %d, want 1", f.synced)
	}
}

// TestFixedSizeWritableFile pins the documented alternative: a driver whose
// format cannot extend a file refuses the write rather than writing short,
// which is what keeps io.WriterAt's "all of p, or an error" rule intact for
// generic callers.
func TestFixedSizeWritableFile(t *testing.T) {
	var f WritableFile = &fixedSizeFile{memWritableFile{b: []byte("0123")}}

	// In-place overwrite still works.
	if n, err := f.WriteAt([]byte("ab"), 1); n != 2 || err != nil {
		t.Fatalf("WriteAt in place = %d, %v", n, err)
	}

	// A write that would extend is refused with n == 0 and a non-nil error —
	// never a short write with a nil error, which callers read as success.
	n, err := f.WriteAt([]byte("cd"), 3)
	if n != 0 || !errors.Is(err, errCannotExtend) {
		t.Fatalf("WriteAt(extend) = %d, %v, want 0, errCannotExtend", n, err)
	}
	if _, err := f.WriteAt([]byte("x"), -1); !errors.Is(err, errCannotExtend) {
		t.Fatalf("WriteAt(-1) = %v, want errCannotExtend", err)
	}
	if err := f.Truncate(2); !errors.Is(err, errCannotExtend) {
		t.Fatalf("Truncate = %v, want errCannotExtend", err)
	}
	if err := f.Sync(); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if got := f.Size(); got != 4 {
		t.Fatalf("Size() = %d, want 4", got)
	}
	if _, err := f.ReadAt(make([]byte, 4), 0); err != nil {
		t.Fatalf("ReadAt: %v", err)
	}
}

// TestOpenerReturningWritableFile is the shape the callers this capability
// exists for actually use: probe the Filesystem for Opener, open, then probe
// the File for WritableFile. Both probes must be independent — a driver can
// have the first without the second.
func TestOpenerReturningWritableFile(t *testing.T) {
	var fs Filesystem = writableOpenerFS{data: map[string][]byte{"/a": []byte("hello")}}
	o, ok := fs.(Opener)
	if !ok {
		t.Fatal("Opener probe failed")
	}
	f, err := o.OpenFile("/a")
	if err != nil {
		t.Fatalf("OpenFile: %v", err)
	}
	defer f.Close()
	w, ok := f.(WritableFile)
	if !ok {
		t.Fatal("WritableFile probe failed on a writable driver's File")
	}
	if _, err := w.WriteAt([]byte("HELLO"), 0); err != nil {
		t.Fatalf("WriteAt: %v", err)
	}
	if err := w.Sync(); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	// The same probe on a read-only driver's File must fail, so the caller
	// falls back instead of writing into a snapshot.
	var ro Filesystem = openerFS{data: map[string][]byte{"/a": []byte("hello")}}
	rof, err := ro.(Opener).OpenFile("/a")
	if err != nil {
		t.Fatalf("OpenFile on read-only driver: %v", err)
	}
	defer rof.Close()
	if _, ok := rof.(WritableFile); ok {
		t.Error("WritableFile probe unexpectedly succeeded on a read-only driver")
	}
	if _, err := ro.ReadFile("/a"); err != nil {
		t.Errorf("ReadFile fallback failed: %v", err)
	}
}

// writableOpenerFS models a driver that can write in place.
type writableOpenerFS struct {
	fakeFS
	data map[string][]byte
}

func (fs writableOpenerFS) OpenFile(path string) (File, error) {
	b, ok := fs.data[path]
	if !ok {
		return nil, os.ErrNotExist
	}
	return &memWritableFile{b: b}, nil
}

var _ Opener = (*writableOpenerFS)(nil)
