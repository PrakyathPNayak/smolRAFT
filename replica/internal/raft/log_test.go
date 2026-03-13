package raft

import (
	"testing"

	"github.com/smolRAFT/types"
)

func TestNewLog(t *testing.T) {
	l := NewLog()
	if l.Length() != 0 {
		t.Errorf("new log length = %d, want 0", l.Length())
	}
	if l.LastIndex() != 0 {
		t.Errorf("new log lastIndex = %d, want 0", l.LastIndex())
	}
	if l.LastTerm() != 0 {
		t.Errorf("new log lastTerm = %d, want 0", l.LastTerm())
	}
}

func TestLogAppend(t *testing.T) {
	l := NewLog()
	entry := types.LogEntry{Term: 1, Stroke: types.StrokeEvent{ID: "s1"}}
	idx := l.Append(entry)
	if idx != 1 {
		t.Errorf("first append index = %d, want 1", idx)
	}
	if l.Length() != 1 {
		t.Errorf("log length after 1 append = %d, want 1", l.Length())
	}
	if l.LastIndex() != 1 {
		t.Errorf("lastIndex after 1 append = %d, want 1", l.LastIndex())
	}
	if l.LastTerm() != 1 {
		t.Errorf("lastTerm after 1 append = %d, want 1", l.LastTerm())
	}

	// Append second entry
	entry2 := types.LogEntry{Term: 1, Stroke: types.StrokeEvent{ID: "s2"}}
	idx2 := l.Append(entry2)
	if idx2 != 2 {
		t.Errorf("second append index = %d, want 2", idx2)
	}
	if l.Length() != 2 {
		t.Errorf("log length after 2 appends = %d, want 2", l.Length())
	}
}

func TestLogGetEntry(t *testing.T) {
	l := NewLog()
	l.Append(types.LogEntry{Term: 1, Stroke: types.StrokeEvent{ID: "s1"}})
	l.Append(types.LogEntry{Term: 2, Stroke: types.StrokeEvent{ID: "s2"}})

	tests := []struct {
		name   string
		index  int
		wantOK bool
		wantID string
	}{
		{"sentinel", 0, true, ""},
		{"first entry", 1, true, "s1"},
		{"second entry", 2, true, "s2"},
		{"out of range", 3, false, ""},
		{"negative", -1, false, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			entry, ok := l.GetEntry(tt.index)
			if ok != tt.wantOK {
				t.Errorf("GetEntry(%d) ok = %v, want %v", tt.index, ok, tt.wantOK)
			}
			if ok && entry.Stroke.ID != tt.wantID {
				t.Errorf("GetEntry(%d).Stroke.ID = %q, want %q", tt.index, entry.Stroke.ID, tt.wantID)
			}
		})
	}
}

func TestLogGetFrom(t *testing.T) {
	l := NewLog()
	l.Append(types.LogEntry{Term: 1, Stroke: types.StrokeEvent{ID: "s1"}})
	l.Append(types.LogEntry{Term: 1, Stroke: types.StrokeEvent{ID: "s2"}})
	l.Append(types.LogEntry{Term: 2, Stroke: types.StrokeEvent{ID: "s3"}})

	tests := []struct {
		name      string
		fromIndex int
		wantLen   int
	}{
		{"from beginning (sentinel)", 0, 4},
		{"from first entry", 1, 3},
		{"from second entry", 2, 2},
		{"from last entry", 3, 1},
		{"beyond log", 4, 0},
		{"negative", -1, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			entries := l.GetFrom(tt.fromIndex)
			if len(entries) != tt.wantLen {
				t.Errorf("GetFrom(%d) len = %d, want %d", tt.fromIndex, len(entries), tt.wantLen)
			}
		})
	}
}

func TestLogTerm(t *testing.T) {
	l := NewLog()
	l.Append(types.LogEntry{Term: 1})
	l.Append(types.LogEntry{Term: 3})

	tests := []struct {
		index    int
		wantTerm int
	}{
		{0, 0},  // sentinel
		{1, 1},  // first entry
		{2, 3},  // second entry
		{3, 0},  // out of range
		{-1, 0}, // negative
	}
	for _, tt := range tests {
		got := l.Term(tt.index)
		if got != tt.wantTerm {
			t.Errorf("Term(%d) = %d, want %d", tt.index, got, tt.wantTerm)
		}
	}
}

func TestLogAppendEntries_NoConflict(t *testing.T) {
	l := NewLog()
	l.Append(types.LogEntry{Term: 1, Stroke: types.StrokeEvent{ID: "s1"}})

	// Append entries that come after existing ones
	newEntries := []types.LogEntry{
		{Index: 2, Term: 1, Stroke: types.StrokeEvent{ID: "s2"}},
		{Index: 3, Term: 2, Stroke: types.StrokeEvent{ID: "s3"}},
	}
	l.AppendEntries(newEntries, 0)
	if l.Length() != 3 {
		t.Errorf("log length = %d, want 3", l.Length())
	}
	if l.LastTerm() != 2 {
		t.Errorf("last term = %d, want 2", l.LastTerm())
	}
}

func TestLogAppendEntries_DuplicateSkip(t *testing.T) {
	l := NewLog()
	l.Append(types.LogEntry{Term: 1, Stroke: types.StrokeEvent{ID: "s1"}})
	l.Append(types.LogEntry{Term: 1, Stroke: types.StrokeEvent{ID: "s2"}})

	// Re-send entries that already exist (same index, same term)
	dupEntries := []types.LogEntry{
		{Index: 1, Term: 1, Stroke: types.StrokeEvent{ID: "s1"}},
		{Index: 2, Term: 1, Stroke: types.StrokeEvent{ID: "s2"}},
	}
	l.AppendEntries(dupEntries, 0)
	if l.Length() != 2 {
		t.Errorf("log length after duplicates = %d, want 2", l.Length())
	}
}

func TestLogAppendEntries_ConflictTruncation(t *testing.T) {
	l := NewLog()
	l.Append(types.LogEntry{Term: 1, Stroke: types.StrokeEvent{ID: "s1"}})
	l.Append(types.LogEntry{Term: 1, Stroke: types.StrokeEvent{ID: "s2-old"}}) // will conflict

	// Entry at index 2 has different term - should truncate and replace
	conflictEntries := []types.LogEntry{
		{Index: 2, Term: 2, Stroke: types.StrokeEvent{ID: "s2-new"}},
		{Index: 3, Term: 2, Stroke: types.StrokeEvent{ID: "s3-new"}},
	}
	l.AppendEntries(conflictEntries, 0)
	if l.Length() != 3 {
		t.Errorf("log length after conflict = %d, want 3", l.Length())
	}
	entry, ok := l.GetEntry(2)
	if !ok {
		t.Fatal("entry at index 2 not found after conflict resolution")
	}
	if entry.Stroke.ID != "s2-new" {
		t.Errorf("entry 2 stroke ID = %q, want %q", entry.Stroke.ID, "s2-new")
	}
	if entry.Term != 2 {
		t.Errorf("entry 2 term = %d, want 2", entry.Term)
	}
}

func TestLogAppendEntries_PanicOnCommittedTruncation(t *testing.T) {
	l := NewLog()
	l.Append(types.LogEntry{Term: 1})

	// Try to overwrite entry at index 1 which is "committed" (commitIndex=1)
	conflictEntries := []types.LogEntry{
		{Index: 1, Term: 2},
	}
	defer func() {
		r := recover()
		if r == nil {
			t.Error("expected panic when truncating committed entry, got none")
		}
	}()
	l.AppendEntries(conflictEntries, 1) // commitIndex=1, so index 1 is committed
}
