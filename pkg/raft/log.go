package raft

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
)

// Log is the Raft log storage.
// Uses append-only file with length-prefixed JSON entries.
type Log struct {
	mu sync.RWMutex

	dir      string
	file     *os.File
	entries  []LogEntry // In-memory cache
	firstIdx Index      // First index in log (after compaction)

	// Snapshot metadata (for entries before firstIdx)
	snapshotIndex Index
	snapshotTerm  Term
}

// SnapshotMeta holds metadata about the last snapshot.
type SnapshotMeta struct {
	Index Index `json:"index"`
	Term  Term  `json:"term"`
}

// NewLog creates or opens a log at the given directory.
func NewLog(dir string) (*Log, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("create log dir: %w", err)
	}

	logPath := filepath.Join(dir, "raft.log")
	file, err := os.OpenFile(logPath, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0644)
	if err != nil {
		return nil, fmt.Errorf("open log file: %w", err)
	}

	l := &Log{
		dir:      dir,
		file:     file,
		entries:  make([]LogEntry, 0),
		firstIdx: 1, // Raft logs are 1-indexed
	}

	// Load snapshot metadata if exists
	if err := l.loadSnapshotMeta(); err != nil {
		file.Close()
		return nil, fmt.Errorf("load snapshot meta: %w", err)
	}

	// Load existing entries
	if err := l.load(); err != nil {
		file.Close()
		return nil, fmt.Errorf("load log: %w", err)
	}

	return l, nil
}

// loadSnapshotMeta loads snapshot metadata from disk.
func (l *Log) loadSnapshotMeta() error {
	metaPath := filepath.Join(l.dir, "snapshot.meta")
	data, err := os.ReadFile(metaPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // No snapshot yet
		}
		return err
	}

	var meta SnapshotMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		return err
	}

	l.snapshotIndex = meta.Index
	l.snapshotTerm = meta.Term
	l.firstIdx = meta.Index + 1 // First entry after snapshot

	return nil
}

// saveSnapshotMeta saves snapshot metadata to disk.
func (l *Log) saveSnapshotMeta() error {
	meta := SnapshotMeta{
		Index: l.snapshotIndex,
		Term:  l.snapshotTerm,
	}

	data, err := json.Marshal(meta)
	if err != nil {
		return err
	}

	metaPath := filepath.Join(l.dir, "snapshot.meta")
	tmpPath := metaPath + ".tmp"

	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return err
	}

	return os.Rename(tmpPath, metaPath)
}

// load reads all entries from the log file.
func (l *Log) load() error {
	if _, err := l.file.Seek(0, 0); err != nil {
		return err
	}

	reader := bufio.NewReader(l.file)
	for {
		// Read length prefix (4 bytes)
		var length uint32
		if err := binary.Read(reader, binary.LittleEndian, &length); err != nil {
			if err == io.EOF {
				break
			}
			return fmt.Errorf("read entry length: %w", err)
		}

		// Read entry data
		data := make([]byte, length)
		if _, err := io.ReadFull(reader, data); err != nil {
			return fmt.Errorf("read entry data: %w", err)
		}

		var entry LogEntry
		if err := json.Unmarshal(data, &entry); err != nil {
			return fmt.Errorf("unmarshal entry: %w", err)
		}

		l.entries = append(l.entries, entry)
	}

	return nil
}

// Append adds entries to the log and persists them.
func (l *Log) Append(entries ...LogEntry) error {
	if len(entries) == 0 {
		return nil
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	for _, entry := range entries {
		data, err := json.Marshal(entry)
		if err != nil {
			return fmt.Errorf("marshal entry: %w", err)
		}

		// Write length prefix
		if err := binary.Write(l.file, binary.LittleEndian, uint32(len(data))); err != nil {
			return fmt.Errorf("write length: %w", err)
		}

		// Write entry data
		if _, err := l.file.Write(data); err != nil {
			return fmt.Errorf("write entry: %w", err)
		}

		l.entries = append(l.entries, entry)
	}

	// Sync to disk
	return l.file.Sync()
}

// Get returns the entry at the given index.
func (l *Log) Get(idx Index) (LogEntry, bool) {
	l.mu.RLock()
	defer l.mu.RUnlock()

	arrIdx := int(idx - l.firstIdx)
	if arrIdx < 0 || arrIdx >= len(l.entries) {
		return LogEntry{}, false
	}
	return l.entries[arrIdx], true
}

// GetRange returns entries from start to end (inclusive).
func (l *Log) GetRange(start, end Index) []LogEntry {
	l.mu.RLock()
	defer l.mu.RUnlock()

	startArr := int(start - l.firstIdx)
	endArr := int(end - l.firstIdx)

	if startArr < 0 {
		startArr = 0
	}
	if endArr >= len(l.entries) {
		endArr = len(l.entries) - 1
	}
	if startArr > endArr {
		return nil
	}

	result := make([]LogEntry, endArr-startArr+1)
	copy(result, l.entries[startArr:endArr+1])
	return result
}

// FirstIndex returns the index of the first entry in the log.
// Entries before this have been compacted.
func (l *Log) FirstIndex() Index {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.firstIdx
}

// LastIndex returns the index of the last entry (0 if empty).
func (l *Log) LastIndex() Index {
	l.mu.RLock()
	defer l.mu.RUnlock()

	if len(l.entries) == 0 {
		return 0
	}
	return l.firstIdx + Index(len(l.entries)) - 1
}

// LastTerm returns the term of the last entry (0 if empty).
func (l *Log) LastTerm() Term {
	l.mu.RLock()
	defer l.mu.RUnlock()

	if len(l.entries) == 0 {
		return 0
	}
	return l.entries[len(l.entries)-1].Term
}

// TermAt returns the term of the entry at the given index.
func (l *Log) TermAt(idx Index) Term {
	entry, ok := l.Get(idx)
	if !ok {
		return 0
	}
	return entry.Term
}

// TruncateAfter removes all entries after the given index.
// Used when a follower's log conflicts with the leader's.
func (l *Log) TruncateAfter(idx Index) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	arrIdx := int(idx - l.firstIdx)
	if arrIdx < 0 {
		arrIdx = -1
	}

	// Truncate in-memory
	if arrIdx+1 < len(l.entries) {
		l.entries = l.entries[:arrIdx+1]
	}

	// Rewrite the log file
	return l.rewrite()
}

// TruncateAll removes all entries from the log.
// Used after installing a snapshot from the leader.
func (l *Log) TruncateAll() error {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.entries = nil
	l.firstIdx = 1

	return l.rewrite()
}

// rewrite recreates the log file from in-memory entries.
// Must be called with lock held.
func (l *Log) rewrite() error {
	l.file.Close()

	logPath := filepath.Join(l.dir, "raft.log")
	tmpPath := logPath + ".tmp"

	tmpFile, err := os.Create(tmpPath)
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}

	for _, entry := range l.entries {
		data, err := json.Marshal(entry)
		if err != nil {
			tmpFile.Close()
			os.Remove(tmpPath)
			return fmt.Errorf("marshal entry: %w", err)
		}

		if err := binary.Write(tmpFile, binary.LittleEndian, uint32(len(data))); err != nil {
			tmpFile.Close()
			os.Remove(tmpPath)
			return fmt.Errorf("write length: %w", err)
		}

		if _, err := tmpFile.Write(data); err != nil {
			tmpFile.Close()
			os.Remove(tmpPath)
			return fmt.Errorf("write entry: %w", err)
		}
	}

	if err := tmpFile.Sync(); err != nil {
		tmpFile.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("sync temp file: %w", err)
	}
	tmpFile.Close()

	// Atomic rename
	if err := os.Rename(tmpPath, logPath); err != nil {
		return fmt.Errorf("rename temp file: %w", err)
	}

	// Reopen for append
	l.file, err = os.OpenFile(logPath, os.O_RDWR|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("reopen log file: %w", err)
	}

	return nil
}

// Len returns the number of entries.
func (l *Log) Len() int {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return len(l.entries)
}

// Close closes the log file.
func (l *Log) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	// Sync to ensure all data is flushed to disk before closing
	if err := l.file.Sync(); err != nil {
		l.file.Close()
		return err
	}
	return l.file.Close()
}

// Compact removes log entries up to and including the given index.
// This should be called after taking a snapshot at the given index.
// The snapshot data itself is stored separately by the FSM.
func (l *Log) Compact(snapshotIndex Index, snapshotTerm Term) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	// Don't compact if the index is before our first entry or beyond our log
	if snapshotIndex < l.firstIdx || snapshotIndex > l.firstIdx+Index(len(l.entries))-1 {
		return nil
	}

	// Calculate how many entries to remove
	entriesToRemove := int(snapshotIndex - l.firstIdx + 1)

	// Update snapshot metadata
	l.snapshotIndex = snapshotIndex
	l.snapshotTerm = snapshotTerm

	// Remove compacted entries from memory
	l.entries = l.entries[entriesToRemove:]
	l.firstIdx = snapshotIndex + 1

	// Persist snapshot metadata
	if err := l.saveSnapshotMeta(); err != nil {
		return fmt.Errorf("save snapshot meta: %w", err)
	}

	// Rewrite the log file without compacted entries
	return l.rewrite()
}

// SnapshotIndex returns the index of the last snapshot.
func (l *Log) SnapshotIndex() Index {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.snapshotIndex
}

// SnapshotTerm returns the term of the last snapshot.
func (l *Log) SnapshotTerm() Term {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.snapshotTerm
}

// ShouldCompact returns true if the log has grown large enough to warrant compaction.
// threshold is the number of entries after which compaction should trigger.
func (l *Log) ShouldCompact(threshold int) bool {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return len(l.entries) > threshold
}
