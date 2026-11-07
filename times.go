package filesystem

import "time"

// Timestamp is one file time that a filesystem may or may not record, together
// with the resolution at which it records it.
//
// # A missing time is not a zero one
//
// The zero Timestamp means "this format does not keep that time". It is not an
// instant: there is no accessor that returns a bare time.Time without also
// returning whether one exists, so a caller cannot write a missing time out as
// 0001-01-01, 1601-01-01 (the FILETIME epoch) or 1970-01-01 (the Unix epoch)
// without having first been told it was absent. Get returns (time, ok); Or makes
// the caller name the substitute; Recorded is the plain test.
//
// This matters because the protocols disagree about what absence looks like.
// MS-FSCC FILE_BASIC_INFORMATION counts 100 ns from 1601 and treats 0 as "do not
// change" when setting; SFTP v3 has no "absent" for atime/mtime except omitting
// the ACMODTIME flag, and carries uint32 seconds; NFSv3 fattr3 always carries
// atime, mtime and ctime and has no birth time at all; WebDAV simply omits the
// property. A zero time.Time converted blindly would be a plausible, wrong date
// in each of them.
//
// # Resolution is not hidden
//
// Resolution reports the unit the format stores the time in: FAT keeps the write
// time to 2 s, the access time to one day and the creation time to 10 ms; HFS+
// keeps every time to 1 s; NTFS to 100 ns; ext4 and the host to 1 ns. The Time
// is exactly what the format holds (a FAT mtime has an even number of seconds),
// so two values from formats of different resolution are not equal merely
// because they are the same instant to the coarser one: use Same, which compares
// at the coarser of the two resolutions.
//
// Timestamp is a comparable value (no pointers, no monotonic reading), so a
// caller may use it as a map key or compare it with ==.
type Timestamp struct {
	t   time.Time
	res time.Duration
}

// NewTimestamp returns the Timestamp recording t at resolution res. It is for
// drivers.
//
// A zero t (t.IsZero) returns the zero Timestamp, "not recorded": a driver that
// reads an all-zero directory field and passes the converted result gets the
// right answer without a special case. A res below one nanosecond is taken as
// one nanosecond. The monotonic clock reading, if any, is stripped, and t is
// converted to UTC; its Location is the driver's wall-clock decision, stated in
// the driver's documentation, and the instant is what callers compare.
func NewTimestamp(t time.Time, res time.Duration) Timestamp {
	if t.IsZero() {
		return Timestamp{}
	}
	if res < time.Nanosecond {
		res = time.Nanosecond
	}
	return Timestamp{t: t.Round(0).UTC(), res: res}
}

// Recorded reports whether the filesystem keeps this time for the file.
func (ts Timestamp) Recorded() bool { return !ts.t.IsZero() }

// Get returns the time and true, or the zero time.Time and false when the time
// is not recorded. Check the boolean: the zero time it returns in that case is
// the one value a caller must never write out.
func (ts Timestamp) Get() (time.Time, bool) { return ts.t, ts.Recorded() }

// Or returns the recorded time, or fallback when none is recorded. It is the
// explicit form of the old behaviour: a server that has no time to report passes
// time.Now() here, on purpose, rather than by omission.
func (ts Timestamp) Or(fallback time.Time) time.Time {
	if ts.Recorded() {
		return ts.t
	}
	return fallback
}

// Resolution returns the unit the format stores this time in, or 0 when the time
// is not recorded.
func (ts Timestamp) Resolution() time.Duration {
	if !ts.Recorded() {
		return 0
	}
	return ts.res
}

// Same reports whether both times are recorded and equal when truncated to the
// coarser of the two resolutions. A time that is not recorded is the same as
// nothing, itself included: Same is false if either side is absent.
//
// Truncation is to multiples of the resolution since the zero time.Time, which
// is aligned for every resolution up to one hour and for 24 h on the UTC day;
// FAT's access date is a local day, so two FAT access dates compared across a
// time zone are compared on the instants the driver produced, not on a calendar.
func (ts Timestamp) Same(o Timestamp) bool {
	if !ts.Recorded() || !o.Recorded() {
		return false
	}
	r := max(ts.res, o.res)
	return ts.t.Truncate(r).Equal(o.t.Truncate(r))
}

// String formats the time in RFC 3339 with nanoseconds, or "unrecorded".
func (ts Timestamp) String() string {
	if !ts.Recorded() {
		return "unrecorded"
	}
	return ts.t.Format(time.RFC3339Nano)
}

// FileTimes is the four times POSIX stat and statx name, as one value:
// st_atim, st_mtim, st_ctim and stx_btime (statx(2), STATX_BTIME). Each may be
// unrecorded independently.
type FileTimes struct {
	// Atime is the last access (FAT: a date only).
	Atime Timestamp
	// Mtime is the last modification of the contents; the time io/fs
	// FileInfo.ModTime reports.
	Mtime Timestamp
	// Ctime is the last change of the file's metadata. It is NOT the creation
	// time (Windows' "CreationTime" is Btime). FAT has none.
	Ctime Timestamp
	// Btime is the birth (creation) time. ext2/3 have none; NFSv3 cannot carry it.
	Btime Timestamp
}

// TimesReader is the optional read side of the timestamps MetadataSetter.Chtimes
// writes, on the Filesystem:
//
//	if tr, ok := fs.(filesystem.TimesReader); ok {
//	    ft, err := tr.Times(path)
//	    if err != nil {
//	        return err
//	    }
//	    mtime := ft.Mtime.Or(time.Now()) // an explicit fallback, never a zero
//	}
//	// otherwise: today's behaviour, time.Now().
//
// # The four decisions
//
//  1. Not recorded is a distinct state (Timestamp), not a zero time.Time.
//  2. Resolution is reported per time (Timestamp.Resolution), and Same compares
//     at the coarser one.
//  3. One call returns all four times (FileTimes): a driver resolves the path
//     once and reads one directory entry or inode, not four.
//  4. It lives on the Filesystem only, taking a path, not on File. The callers
//     that need it (NFS GETATTR and READDIRPLUS, SMB QUERY_DIRECTORY, SFTP
//     stat, WebDAV PROPFIND) have a path, not an open handle, and a File is
//     documented as a snapshot of the size at open: a time that WriteAt
//     changes cannot honestly be one. A File-scoped twin can be added the day a
//     caller has a handle and no path; none does.
//
// Times follows the Filesystem error contract: a missing path satisfies
// errors.Is(err, fs.ErrNotExist). It reads; it does not update atime.
//
// Chtimes and Times are asymmetric by design in one respect: Chtimes treats a
// zero time.Time as "leave unchanged" (os.Chtimes does too), while Times never
// returns one.
type TimesReader interface {
	// Times returns the times the filesystem records for path, with the
	// resolution of each. A symbolic link is not followed.
	Times(path string) (FileTimes, error)
}
