// Package raft implements a simplified RAFT consensus protocol for the
// smolRAFT distributed drawing board. This package contains the core
// state machine, election logic, log replication, and RPC client helpers.
package raft

import (
	"sync"

	"github.com/smolRAFT/types"
)

// Log is a thread-safe, append-only replicated log. Entries are 1-indexed;
// index 0 is a sentinel entry with term 0 that simplifies boundary checks
// in the AppendEntries consistency protocol.
//
// Thread safety: All exported methods acquire the internal mutex and are
// safe for concurrent use. Callers must NOT hold the mutex when calling
// these methods to avoid deadlocks.
type Log struct {
	mu      sync.RWMutex
	entries []types.LogEntry // entries[0] is the sentinel
}

// NewLog creates a new Log with a sentinel entry at index 0.
func NewLog() *Log {
	return &Log{
		entries: []types.LogEntry{
			{Index: 0, Term: 0}, // sentinel
		},
	}
}

// Append adds a new entry to the end of the log. It automatically assigns
// the correct index (previous last index + 1). Returns the index of the
// newly appended entry.
func (l *Log) Append(entry types.LogEntry) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	entry.Index = len(l.entries) // 1-based: entries[1] has Index=1
	l.entries = append(l.entries, entry)
	return entry.Index
}

// AppendEntries appends multiple entries starting at the correct position.
// Used during log replication from the leader. The caller must ensure that
// log consistency has been verified (prevLogIndex/prevLogTerm match) before
// calling this method.
//
// If any of the new entries conflict with existing entries (same index but
// different term), the log is truncated at the conflict point before
// appending. Committed entries (at or before commitIndex) are never
// truncated — if a conflict is detected at a committed index, this method
// panics, as this indicates a serious protocol violation.
func (l *Log) AppendEntries(entries []types.LogEntry, commitIndex int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, entry := range entries {
		if entry.Index < len(l.entries) {
			// Entry at this index already exists
			existing := l.entries[entry.Index]
			if existing.Term == entry.Term {
				// Same term — already have this entry, skip
				continue
			}
			// Conflict: different term at same index
			if entry.Index <= commitIndex {
				panic("raft: attempted to truncate committed log entry")
			}
			// Truncate from conflict point onward
			l.entries = l.entries[:entry.Index]
		}
		// Append the new entry
		entry.Index = len(l.entries)
		l.entries = append(l.entries, entry)
	}
}

// GetEntry returns the log entry at the given 1-based index.
// Returns the entry and true if the index is valid, or a zero-value entry
// and false if the index is out of range.
func (l *Log) GetEntry(index int) (types.LogEntry, bool) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	if index < 0 || index >= len(l.entries) {
		return types.LogEntry{}, false
	}
	return l.entries[index], true
}

// GetFrom returns all entries from the given 1-based index (inclusive) to
// the end of the log. Returns nil if fromIndex is beyond the log length.
func (l *Log) GetFrom(fromIndex int) []types.LogEntry {
	l.mu.RLock()
	defer l.mu.RUnlock()
	if fromIndex < 0 || fromIndex >= len(l.entries) {
		return nil
	}
	// Return a copy to prevent data races on the underlying slice
	result := make([]types.LogEntry, len(l.entries)-fromIndex)
	copy(result, l.entries[fromIndex:])
	return result
}

// LastIndex returns the index of the last entry in the log.
// Returns 0 if the log contains only the sentinel.
func (l *Log) LastIndex() int {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return len(l.entries) - 1
}

// LastTerm returns the term of the last entry in the log.
// Returns 0 if the log contains only the sentinel.
func (l *Log) LastTerm() int {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.entries[len(l.entries)-1].Term
}

// Length returns the number of entries in the log, excluding the sentinel.
func (l *Log) Length() int {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return len(l.entries) - 1
}

// Term returns the term of the entry at the given index.
// Returns 0 if the index is out of range (treats missing entries as term 0).
func (l *Log) Term(index int) int {
	l.mu.RLock()
	defer l.mu.RUnlock()
	if index < 0 || index >= len(l.entries) {
		return 0
	}
	return l.entries[index].Term
}
