package migratortest

import (
	"errors"
	"testing"
)

// MockRow.Scan assigns each value into its destination by reflection, skipping
// any destination that is not a pointer. Both halves of that rule are covered
// here: a pointer destination must receive the value, and a non-pointer
// destination must be skipped rather than panicking.

func TestMockRow_Scan_AssignsIntoPointerDest(t *testing.T) {
	row := NewMockRow()
	row.Values = []interface{}{uint64(7), "ok"}

	var gotCount uint64
	var gotName string
	if err := row.Scan(&gotCount, &gotName); err != nil {
		t.Fatalf("Scan returned an error: %v", err)
	}

	if gotCount != 7 {
		t.Errorf("pointer destination not assigned: got %d, want 7", gotCount)
	}
	if gotName != "ok" {
		t.Errorf("pointer destination not assigned: got %q, want \"ok\"", gotName)
	}
}

func TestMockRow_Scan_SkipsNonPointerDest(t *testing.T) {
	row := NewMockRow()
	row.Values = []interface{}{uint64(7), uint64(9)}

	// The first destination is passed by value, so it cannot be assigned and
	// must be skipped; the second still gets its value.
	notAPointer := uint64(0)
	var gotSecond uint64
	if err := row.Scan(notAPointer, &gotSecond); err != nil {
		t.Fatalf("Scan returned an error: %v", err)
	}

	if notAPointer != 0 {
		t.Errorf("non-pointer destination must be left alone, got %d", notAPointer)
	}
	if gotSecond != 9 {
		t.Errorf("pointer destination after a skipped one: got %d, want 9", gotSecond)
	}
}

func TestMockRow_Scan_ReturnsConfiguredError(t *testing.T) {
	wantErr := errors.New("scan failed")
	row := NewMockRow()
	row.ScanError = wantErr

	var got uint64
	if err := row.Scan(&got); !errors.Is(err, wantErr) {
		t.Errorf("expected the configured ScanError, got %v", err)
	}
}

func TestMockRow_Scan_StopsAtValueCount(t *testing.T) {
	row := NewMockRow()
	row.Values = []interface{}{uint64(1)}

	// More destinations than values: the extra one is left untouched.
	var first, second uint64
	if err := row.Scan(&first, &second); err != nil {
		t.Fatalf("Scan returned an error: %v", err)
	}

	if first != 1 {
		t.Errorf("first destination: got %d, want 1", first)
	}
	if second != 0 {
		t.Errorf("destination beyond the value count must be untouched, got %d", second)
	}
}
