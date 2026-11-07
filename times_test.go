package filesystem

import (
	"testing"
	"time"
)

var _ TimesReader = timesFS{}

type timesFS struct{ fakeFS }

func (timesFS) Times(path string) (FileTimes, error) {
	return FileTimes{
		Mtime: NewTimestamp(time.Date(2026, 11, 7, 21, 0, 2, 0, time.UTC), 2*time.Second),
	}, nil
}

func TestZeroTimestampIsNotRecorded(t *testing.T) {
	var ts Timestamp
	if ts.Recorded() {
		t.Fatal("zero Timestamp must be unrecorded")
	}
	if got, ok := ts.Get(); ok || !got.IsZero() {
		t.Fatalf("Get = %v, %v", got, ok)
	}
	if ts.Resolution() != 0 {
		t.Fatal("unrecorded resolution must be 0")
	}
	now := time.Unix(1700000000, 0)
	if !ts.Or(now).Equal(now) {
		t.Fatal("Or must return the fallback")
	}
	if ts.String() != "unrecorded" {
		t.Fatal(ts.String())
	}
}

func TestNewTimestampZeroTimeIsUnrecorded(t *testing.T) {
	if NewTimestamp(time.Time{}, time.Second) != (Timestamp{}) {
		t.Fatal("a zero time.Time must produce the zero Timestamp")
	}
}

func TestNewTimestampNormalises(t *testing.T) {
	loc := time.FixedZone("x", 3600)
	in := time.Now().In(loc) // carries a monotonic reading
	ts := NewTimestamp(in, 0)
	got, ok := ts.Get()
	if !ok || !got.Equal(in) || got.Location() != time.UTC {
		t.Fatalf("got %v %v", got, ok)
	}
	if ts.Resolution() != time.Nanosecond {
		t.Fatalf("res = %v", ts.Resolution())
	}
	if NewTimestamp(in, time.Second) != NewTimestamp(in.Round(0), time.Second) {
		t.Fatal("monotonic reading must not affect ==")
	}
}

func TestSame(t *testing.T) {
	a := time.Date(2026, 11, 7, 21, 0, 2, 0, time.UTC)
	fat := NewTimestamp(a, 2*time.Second)
	exact := NewTimestamp(a.Add(900*time.Millisecond), time.Nanosecond)
	if !fat.Same(exact) || !exact.Same(fat) {
		t.Fatal("same at the coarser resolution")
	}
	if NewTimestamp(a.Add(2*time.Second), time.Nanosecond).Same(fat) {
		t.Fatal("two seconds apart must differ")
	}
	var none Timestamp
	if none.Same(none) || none.Same(fat) || fat.Same(none) {
		t.Fatal("an absent time is the same as nothing")
	}
}

func TestTimesReaderProbe(t *testing.T) {
	var fs Filesystem = timesFS{}
	tr, ok := fs.(TimesReader)
	if !ok {
		t.Fatal("not a TimesReader")
	}
	ft, err := tr.Times("/x")
	if err != nil {
		t.Fatal(err)
	}
	if !ft.Mtime.Recorded() || ft.Atime.Recorded() || ft.Ctime.Recorded() || ft.Btime.Recorded() {
		t.Fatalf("%+v", ft)
	}
	if _, ok := Filesystem(fakeFS{}).(TimesReader); ok {
		t.Fatal("fakeFS must not be a TimesReader")
	}
}

// FuzzNewTimestamp: for any instant and resolution, a recorded Timestamp
// returns a non-zero time with a positive resolution, and an unrecorded one
// returns neither. No input produces a "recorded zero".
func FuzzNewTimestamp(f *testing.F) {
	f.Add(int64(0), int64(0), int64(0))
	f.Add(int64(-62135596800), int64(0), int64(1))
	f.Add(int64(1<<40), int64(999999999), int64(2e9))
	f.Fuzz(func(t *testing.T, sec, nsec, res int64) {
		in := time.Unix(sec, nsec%1e9)
		ts := NewTimestamp(in, time.Duration(res))
		got, ok := ts.Get()
		if ok != ts.Recorded() {
			t.Fatal("Get and Recorded disagree")
		}
		if ok {
			if got.IsZero() || ts.Resolution() < time.Nanosecond {
				t.Fatalf("recorded but invalid: %v %v", got, ts.Resolution())
			}
			if !ts.Same(ts) {
				t.Fatal("a recorded time is the same as itself")
			}
		} else if !got.IsZero() || ts.Resolution() != 0 {
			t.Fatal("unrecorded must carry nothing")
		}
	})
}
