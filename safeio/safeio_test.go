package safeio

import (
	"bytes"
	"errors"
	"io"
	"math"
	"testing"
)

func TestMakeBytes(t *testing.T) {
	tests := []struct {
		name    string
		n, max  int64
		wantLen int
		wantErr error
	}{
		{"zero", 0, 10, 0, nil},
		{"ok", 8, 8, 8, nil},
		{"under", 4, 100, 4, nil},
		{"negative n", -1, 100, 0, ErrTooLarge},
		{"negative max", 4, -1, 0, ErrTooLarge},
		{"over max", 101, 100, 0, ErrTooLarge},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			b, err := MakeBytes(tc.n, tc.max)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("err = %v, want %v", err, tc.wantErr)
				}
				if !errors.Is(err, ErrSafeIO) {
					t.Fatalf("err %v does not wrap ErrSafeIO", err)
				}
				if b != nil {
					t.Fatalf("buf = %v, want nil on error", b)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if b == nil {
				t.Fatal("buf is nil on success")
			}
			if len(b) != tc.wantLen {
				t.Fatalf("len = %d, want %d", len(b), tc.wantLen)
			}
		})
	}
}

func TestCheckBounds(t *testing.T) {
	tests := []struct {
		name           string
		off, n, length int
		wantErr        bool
	}{
		{"full", 0, 10, 10, false},
		{"sub", 2, 4, 10, false},
		{"empty at end", 10, 0, 10, false},
		{"empty at zero", 0, 0, 0, false},
		{"neg off", -1, 1, 10, true},
		{"neg n", 0, -1, 10, true},
		{"neg length", 0, 0, -1, true},
		{"over", 8, 4, 10, true},
		{"off past end", 11, 0, 10, true},
		{"overflow", math.MaxInt, 1, 10, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := CheckBounds(tc.off, tc.n, tc.length)
			if tc.wantErr {
				if err == nil {
					t.Fatal("want error, got nil")
				}
				if !errors.Is(err, ErrOutOfBounds) || !errors.Is(err, ErrSafeIO) {
					t.Fatalf("err %v not ErrOutOfBounds/ErrSafeIO", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
		})
	}
}

func TestSlice(t *testing.T) {
	buf := []byte("0123456789")
	got, err := Slice(buf, 2, 3)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if string(got) != "234" {
		t.Fatalf("got %q, want %q", got, "234")
	}
	// aliasing check
	got[0] = 'X'
	if buf[2] != 'X' {
		t.Fatal("Slice did not alias the backing array")
	}

	if _, err := Slice(buf, 8, 5); !errors.Is(err, ErrOutOfBounds) {
		t.Fatalf("out-of-range slice err = %v, want ErrOutOfBounds", err)
	}
}

func TestReadAtFull(t *testing.T) {
	data := []byte("ABCDEFGHIJ")
	r := bytes.NewReader(data)

	got, err := ReadAtFull(r, 2, 4, 100)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if string(got) != "CDEF" {
		t.Fatalf("got %q, want %q", got, "CDEF")
	}

	// n > max → ErrTooLarge (class A)
	if _, err := ReadAtFull(r, 0, 1000, 100); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("err = %v, want ErrTooLarge", err)
	}
	// negative offset → ErrOutOfBounds
	if _, err := ReadAtFull(r, -1, 4, 100); !errors.Is(err, ErrOutOfBounds) {
		t.Fatalf("err = %v, want ErrOutOfBounds", err)
	}
	// short read → io error (truncated image)
	if _, err := ReadAtFull(r, 8, 8, 100); err == nil {
		t.Fatal("want short-read error, got nil")
	} else if !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		t.Fatalf("err = %v, want EOF/ErrUnexpectedEOF", err)
	}
	// zero-length read at end succeeds
	if got, err := ReadAtFull(r, 10, 0, 100); err != nil || len(got) != 0 {
		t.Fatalf("zero read: got %v err %v", got, err)
	}
}

func TestLoopGuard(t *testing.T) {
	g := NewLoopGuard(3)
	for i := 0; i < 3; i++ {
		if err := g.Next(); err != nil {
			t.Fatalf("iter %d: unexpected err %v", i, err)
		}
	}
	if g.Count() != 3 {
		t.Fatalf("Count = %d, want 3", g.Count())
	}
	if err := g.Next(); !errors.Is(err, ErrLoopLimit) {
		t.Fatalf("err = %v, want ErrLoopLimit", err)
	}
	if !errors.Is(g.Next(), ErrSafeIO) {
		t.Fatal("ErrLoopLimit does not wrap ErrSafeIO")
	}

	// non-positive max: first Next must fail immediately
	z := NewLoopGuard(0)
	if err := z.Next(); !errors.Is(err, ErrLoopLimit) {
		t.Fatalf("zero-max Next err = %v, want ErrLoopLimit", err)
	}
	neg := NewLoopGuard(-5)
	if err := neg.Next(); !errors.Is(err, ErrLoopLimit) {
		t.Fatalf("neg-max Next err = %v, want ErrLoopLimit", err)
	}
}

func TestVisitSet(t *testing.T) {
	var s VisitSet
	if !s.Add(1) {
		t.Fatal("first Add(1) should be firstTime")
	}
	if !s.Add(2) {
		t.Fatal("first Add(2) should be firstTime")
	}
	if s.Add(1) {
		t.Fatal("second Add(1) should not be firstTime")
	}
	if s.Len() != 2 {
		t.Fatalf("Len = %d, want 2", s.Len())
	}

	// Check: nil on first, ErrCycle on repeat
	var c VisitSet
	if err := c.Check(7); err != nil {
		t.Fatalf("first Check err = %v, want nil", err)
	}
	if err := c.Check(7); !errors.Is(err, ErrCycle) {
		t.Fatalf("repeat Check err = %v, want ErrCycle", err)
	}
	if !errors.Is(c.Check(7), ErrSafeIO) {
		t.Fatal("ErrCycle does not wrap ErrSafeIO")
	}
}

// FuzzSlice asserts Slice never panics on arbitrary inputs.
func FuzzSlice(f *testing.F) {
	f.Add([]byte("hello"), 0, 5)
	f.Add([]byte("hello"), -1, 1)
	f.Add([]byte("hello"), 2, math.MaxInt)
	f.Add([]byte(nil), 0, 0)
	f.Add([]byte("x"), math.MaxInt, 1)
	f.Fuzz(func(t *testing.T, buf []byte, off, n int) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("Slice panicked: %v (buf=%d off=%d n=%d)", r, len(buf), off, n)
			}
		}()
		got, err := Slice(buf, off, n)
		if err == nil && len(got) != n {
			t.Fatalf("len(got)=%d want %d", len(got), n)
		}
	})
}

// FuzzReadAtFull asserts ReadAtFull never panics and honors its ceiling.
func FuzzReadAtFull(f *testing.F) {
	f.Add([]byte("payload"), int64(0), int64(4), int64(8))
	f.Add([]byte("payload"), int64(0), int64(math.MaxInt64), int64(8))
	f.Add([]byte("payload"), int64(-1), int64(1), int64(8))
	f.Add([]byte(nil), int64(0), int64(0), int64(0))
	f.Fuzz(func(t *testing.T, data []byte, off, n, max int64) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("ReadAtFull panicked: %v", r)
			}
		}()
		r := bytes.NewReader(data)
		got, err := ReadAtFull(r, off, n, max)
		if err == nil && int64(len(got)) != n {
			t.Fatalf("len(got)=%d want %d", len(got), n)
		}
	})
}

// FuzzMakeBytes asserts MakeBytes never panics or over-allocates.
func FuzzMakeBytes(f *testing.F) {
	f.Add(int64(0), int64(0))
	f.Add(int64(-1), int64(10))
	f.Add(int64(math.MaxInt64), int64(1024))
	f.Fuzz(func(t *testing.T, n, max int64) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("MakeBytes panicked: %v", r)
			}
		}()
		// Keep the ceiling small so a passing call cannot actually OOM the
		// fuzz process; we only exercise the validation logic.
		if max > 1<<20 {
			max = 1 << 20
		}
		b, err := MakeBytes(n, max)
		if err == nil && int64(len(b)) != n {
			t.Fatalf("len(b)=%d want %d", len(b), n)
		}
	})
}
