package filesystem

import (
	"errors"
	"io"
	"os"
	"time"
)

// ErrShrinkUnsupported is returned by Resizer.Resize when the driver
// cannot shrink its on-disk format and the caller passed a newSize
// smaller than the current size. Filesystems whose layout precludes
// shrink entirely (e.g. XFS, ZFS) return this sentinel rather than
// attempting a destructive resize.
var ErrShrinkUnsupported = errors.New("filesystem: shrink not supported")

// DirEntry describes a directory entry. Implementations must provide accessors
// for the inode number, name and file type.
type DirEntry interface {
	Inode() uint64
	Name() string
	FileType() uint8
}

type dirEntry struct {
	inode    uint64
	name     string
	fileType uint8
}

func (d *dirEntry) Inode() uint64   { return d.inode }
func (d *dirEntry) Name() string    { return d.name }
func (d *dirEntry) FileType() uint8 { return d.fileType }

// NewDirEntry constructs a DirEntry implementation backed by an unexported
// struct. Returning the interface enforces encapsulation.
func NewDirEntry(inode uint64, name string, fileType uint8) DirEntry {
	return &dirEntry{inode: inode, name: name, fileType: fileType}
}

// Stat describes basic metadata for a filesystem path.
type Stat interface {
	Mode() uint16
	Size() uint64
	Inode() uint64
}

type stat struct {
	mode  uint16
	size  uint64
	inode uint64
}

func (s *stat) Mode() uint16  { return s.mode }
func (s *stat) Size() uint64  { return s.size }
func (s *stat) Inode() uint64 { return s.inode }

// NewStat constructs a Stat implementation backed by an unexported struct.
func NewStat(mode uint16, size uint64, inode uint64) Stat {
	return &stat{mode: mode, size: size, inode: inode}
}

// Filesystem defines a minimal common API implemented by concrete
// filesystem packages (ext4, xfs, btrfs).
type Filesystem interface {
	Close() error
	ReadFile(path string) ([]byte, error)
	ListDir(path string) ([]DirEntry, error)
	Stat(path string) (Stat, error)
	WriteFile(path string, data []byte, perm os.FileMode) error
	ReadLink(path string) (string, error)
	MkDir(path string, perm os.FileMode) error
	DeleteFile(path string) error
	DeleteDir(path string) error
	Rename(oldPath, newPath string) error
}

// LabelReader is the optional read-only interface for filesystems that
// can decode an on-disk volume label. Implementations that can also
// mutate the label additionally satisfy Labeller (which embeds this).
//
// Some drivers (notably ones with transactional / COW write models)
// can read the label cheaply but cannot yet rewrite it through their
// regular commit machinery. Exposing the read capability separately
// lets generic tools (`diskimage exec <fs> label get`, status displays)
// work everywhere while keeping the write surface honest.
//
//	if r, ok := fs.(filesystem.LabelReader); ok {
//	    fmt.Println(r.Label())
//	}
type LabelReader interface {
	// Label returns the current volume label, decoded from the
	// implementation's on-disk metadata. An empty string means the
	// filesystem has no label set (not an error).
	Label() string
}

// Labeller is the optional interface implemented by filesystems that
// expose a read/write volume label. Embeds LabelReader so every
// Labeller is automatically a LabelReader too — generic code can
// downgrade the assertion when only read access is needed.
//
//	if l, ok := fs.(filesystem.Labeller); ok {
//	    l.SetLabel("rootfs")
//	}
//
// Kept separate from Filesystem so implementations that genuinely have
// no concept of a label (or where label mutation is non-trivial)
// aren't forced to stub it. The label's encoding and length limit are
// filesystem-specific (e.g. ext2/3/4 caps at 16 bytes; FAT caps at 11).
// SetLabel must reject labels exceeding its filesystem's limit.
type Labeller interface {
	LabelReader
	// SetLabel writes a new volume label. Concrete implementations
	// document whether the call is safe with a live, actively-mutating
	// filesystem; the conservative assumption is "offline only".
	SetLabel(label string) error
}

// Symlinker is the optional interface implemented by filesystems that
// support creating symbolic links. ReadLink is already part of
// Filesystem; this capability gates the write side.
//
//	if s, ok := fs.(filesystem.Symlinker); ok {
//	    s.Symlink("/target", "/link")
//	}
type Symlinker interface {
	// Symlink creates a symbolic link at linkPath whose target is the
	// literal string `target`. The parent of linkPath must exist; the
	// path itself must not. Symlink targets are stored as-is and are
	// not resolved at creation time.
	Symlink(target, linkPath string) error
}

// HardLinker is the optional interface for filesystems that support
// POSIX hardlinks (multiple directory entries pointing at the same
// inode). Directories cannot be hardlinked — implementations must
// reject that case.
type HardLinker interface {
	// Link adds a new directory entry at newPath that points at the
	// same inode as oldPath. The source must not be a directory and
	// newPath must not already exist. The source inode's nlink count
	// is bumped.
	Link(oldPath, newPath string) error
}

// Truncater is the optional interface for filesystems that support
// resizing a regular file in place. Growing extends the file with
// implicit zero-fill (sparse where the format allows); shrinking
// drops or trims the trailing data.
type Truncater interface {
	// Truncate resizes the regular file at path to newSize bytes.
	// mtime and ctime are refreshed per POSIX truncate(2).
	Truncate(path string, newSize int64) error
}

// MetadataSetter is the optional interface bundling the POSIX
// metadata mutators (chmod / chown / utimes). Filesystems that
// support any of these typically support all three, so they're
// bundled — drivers that only implement a subset can still expose
// the methods they have and return an error from the others. (The
// type assertion only proves they all compile, not that any one
// call must succeed.)
type MetadataSetter interface {
	// Chmod replaces the permission bits at path, preserving the
	// file-type bits. ctime is refreshed.
	Chmod(path string, perm os.FileMode) error
	// Chown updates uid/gid at path. ctime is refreshed; mode, body,
	// and the other timestamps are left alone.
	Chown(path string, uid, gid uint32) error
	// Chtimes sets atime and mtime at path. ctime is refreshed to
	// "now" per POSIX. Birth time (if the filesystem records one) is
	// left untouched.
	Chtimes(path string, atime, mtime time.Time) error
}

// Grower is the optional interface for filesystems that can expand
// in place to fill a larger backing image. The opposite direction
// (shrink) is intentionally not part of this surface — most
// filesystems' shrink paths are either unsupported or far more
// invasive than grow.
type Grower interface {
	// GrowTo resizes the underlying image and the filesystem
	// metadata so that it spans newSizeBytes. The implementation may
	// require that the new size is strictly larger than the current
	// size and/or aligned to a filesystem-specific boundary.
	GrowTo(newSizeBytes int64) error
}

// Resizer is the optional, uniform resize capability implemented by
// every driver that can change its on-disk size, in either direction.
// It supersedes the older grow-only Grower split by giving callers a
// single entry point and a clearly-typed sentinel for the one case
// where the semantics genuinely diverge (shrink-unsupported formats).
//
//	if r, ok := fs.(filesystem.Resizer); ok {
//	    if err := r.Resize(newSize); errors.Is(err, filesystem.ErrShrinkUnsupported) {
//	        // driver only grows — caller decides how to handle
//	    }
//	}
type Resizer interface {
	// Resize changes the filesystem's underlying size in bytes. The newSize
	// is the new total capacity of the on-disk image. Implementations may
	// reject sizes that would lose data; semantics per driver:
	//
	//   - grow (always allowed where the on-disk format supports it)
	//   - shrink (allowed only if the data fits in newSize; some FS forbid
	//     shrink entirely — e.g. XFS, ZFS — and return ErrShrinkUnsupported)
	//
	// Returns ErrShrinkUnsupported when the driver doesn't implement shrink
	// and newSize < current; returns wrapping I/O errors for failed grow.
	Resize(newSize int64) error
}

// Opener is the optional interface a filesystem implements when it can read
// part of a file without materialising the whole thing.
//
// The base Filesystem API is per-path: ReadFile(path) returns the ENTIRE file.
// That is fine for a config file and useless for anything that has to serve
// reads on demand — a mount, an NFS or 9P export, a loop-mounted image — where
// answering a 4 KiB request out of a 4 GiB file must not allocate 4 GiB. Opener
// is the capability that makes those callers possible; it is the single missing
// piece, not an OS-specific one, so it lives here rather than in any one driver.
//
// It is optional and non-breaking by construction: Filesystem is unchanged, and
// a caller probes for it and falls back when a driver does not have it.
//
//	if o, ok := fs.(filesystem.Opener); ok {
//	    f, err := o.OpenFile(path)
//	    if err != nil {
//	        return err
//	    }
//	    defer f.Close()
//	    n, err := f.ReadAt(buf, off)
//	    ...
//	}
//	// otherwise: data, err := fs.ReadFile(path) and slice it.
//
// Drivers whose on-disk model cannot answer a byte range without decoding
// everything before it (whole-file compression, for instance) should simply not
// implement Opener rather than emulate it with a hidden full read — the point of
// the probe is that the caller learns the truth and can budget accordingly.
type Opener interface {
	// OpenFile opens the regular file at path for random access. The
	// returned File holds whatever per-file state the driver needs (an
	// extent list, a cluster chain, a block map); it must be closed by the
	// caller. Opening must not read the file's contents.
	//
	// Implementations reject directories, and paths that do not resolve,
	// with the same errors their ReadFile would return. The returned File
	// remains valid only while the Filesystem is open: closing the
	// Filesystem invalidates every File obtained from it.
	OpenFile(path string) (File, error)
}

// File is an open file: random access, and the size the caller needs to
// answer a stat without reading anything.
//
// ReadAt follows io.ReaderAt TO THE LETTER, and that is the whole point of the
// type. Callers — io.SectionReader, io.NewSectionReader, bufio over one, every
// generic Go consumer — assume the documented contract:
//
//   - ReadAt returns n < len(p) ONLY together with a non-nil error explaining
//     why. A short read with a nil error is the one behaviour that breaks those
//     consumers silently, because they treat it as "keep going" and lose data.
//   - Reading that stops at end of file returns io.EOF, either with the last
//     bytes (n > 0) or on its own (n == 0). An offset at or past Size() returns
//     0, io.EOF.
//   - ReadAt does not affect, and is not affected by, any notion of a seek
//     offset; each call is independent.
//   - Multiple ReadAt calls on the same File may run concurrently. Read paths
//     must therefore not mutate shared state without synchronisation. This too
//     is io.ReaderAt's contract, and a mount serving parallel requests relies
//     on it.
//
// Size returns the file's length in bytes, taken from the metadata the driver
// already read when the File was opened, so a caller can answer a stat or size
// an io.SectionReader without touching the data.
type File interface {
	io.ReaderAt
	io.Closer

	// Size returns the length of the file in bytes, as recorded in the
	// filesystem's own metadata. It is fixed for the lifetime of the File:
	// a File is a snapshot of the file as it was when opened.
	Size() int64
}
