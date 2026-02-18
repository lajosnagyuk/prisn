// Package storage implements local deployment storage with hot/cold priming.
//
// Storage States:
//   - HOT: Files extracted locally, ready for immediate execution (~0ms)
//   - WARM: Compressed archive exists locally, needs extraction (~10-50ms)
//   - COLD: Not on this node, needs fetch from peer (~100ms+)
//
// The hot path (script execution) reads from local storage and NEVER waits
// for Raft consensus or network operations.
package storage

import (
	"container/heap"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/pierrec/lz4/v4"
)

// State represents the thermal state of a deployment.
type State uint8

const (
	Cold State = iota // Not on this node
	Warm              // Compressed archive exists
	Hot               // Files extracted and ready
)

func (s State) String() string {
	switch s {
	case Cold:
		return "cold"
	case Warm:
		return "warm"
	case Hot:
		return "hot"
	default:
		return "unknown"
	}
}

// CacheEntry represents a cached deployment version.
type CacheEntry struct {
	Key         string    // namespace/name/version
	Fingerprint string    // xxhash64 fingerprint
	State       State     // Current thermal state
	Size        int64     // Size on disk
	FileCount   int       // Number of files
	LastAccess  time.Time // For LRU
	AccessCount int64     // For frequency scoring
	HotPath     string    // Path to extracted files (if hot)
	WarmPath    string    // Path to compressed archive (if warm)
	AssignedHere bool     // Is this deployment assigned to run on this node?

	// Heap index for priority queue
	index int
}

// Score calculates the cache priority score.
// Higher score = more likely to stay cached.
func (e *CacheEntry) Score() float64 {
	// Assigned deployments always score highest
	if e.AssignedHere {
		return 1e12
	}

	// Score based on:
	// - Recency (40%): How recently accessed
	// - Frequency (30%): How often accessed
	// - State (30%): Hot > Warm

	now := time.Now()
	recencyScore := 1.0 / (1.0 + now.Sub(e.LastAccess).Hours())
	frequencyScore := float64(e.AccessCount) / 100.0
	stateScore := 0.0
	if e.State == Hot {
		stateScore = 1.0
	} else if e.State == Warm {
		stateScore = 0.5
	}

	return recencyScore*0.4 + frequencyScore*0.3 + stateScore*0.3
}

// Cache manages local deployment storage.
type Cache struct {
	mu sync.RWMutex

	baseDir     string           // Base directory for storage
	maxSize     int64            // Maximum total size
	currentSize int64            // Current total size
	entries     map[string]*CacheEntry
	pq          priorityQueue    // For LRU eviction

	// For async operations
	primeQueue chan primeRequest
	stopCh     chan struct{}
	wg         sync.WaitGroup
}

// primeRequest is a request to prime a deployment.
type primeRequest struct {
	key         string
	fingerprint string
	sourcePath  string
	targetState State
	done        chan error
}

// Config holds cache configuration.
type Config struct {
	BaseDir     string
	MaxSizeGB   float64 // Maximum cache size in GB
	PrimeWorkers int    // Number of background priming workers
}

func (c *Config) defaults() {
	if c.MaxSizeGB == 0 {
		c.MaxSizeGB = 10.0 // 10GB default
	}
	if c.PrimeWorkers == 0 {
		c.PrimeWorkers = 2
	}
}

// NewCache creates a new local cache.
func NewCache(cfg Config) (*Cache, error) {
	cfg.defaults()

	if err := os.MkdirAll(cfg.BaseDir, 0755); err != nil {
		return nil, fmt.Errorf("create cache dir: %w", err)
	}

	// Create subdirectories
	for _, sub := range []string{"hot", "warm"} {
		if err := os.MkdirAll(filepath.Join(cfg.BaseDir, sub), 0755); err != nil {
			return nil, fmt.Errorf("create %s dir: %w", sub, err)
		}
	}

	c := &Cache{
		baseDir:     cfg.BaseDir,
		maxSize:     int64(cfg.MaxSizeGB * 1024 * 1024 * 1024),
		entries:     make(map[string]*CacheEntry),
		pq:          make(priorityQueue, 0),
		primeQueue:  make(chan primeRequest, 100),
		stopCh:      make(chan struct{}),
	}

	// Load existing entries
	if err := c.scan(); err != nil {
		return nil, fmt.Errorf("scan cache: %w", err)
	}

	// Start priming workers
	for i := 0; i < cfg.PrimeWorkers; i++ {
		c.wg.Add(1)
		go c.primeWorker()
	}

	return c, nil
}

// scan loads existing cache entries from disk.
func (c *Cache) scan() error {
	// Scan hot directory
	hotDir := filepath.Join(c.baseDir, "hot")
	filepath.Walk(hotDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || !info.IsDir() || path == hotDir {
			return nil
		}

		rel, _ := filepath.Rel(hotDir, path)
		// Only process top-level (namespace/name/version)
		parts := filepath.SplitList(rel)
		if len(parts) != 3 {
			return filepath.SkipDir
		}

		key := rel
		size := dirSize(path)

		entry := &CacheEntry{
			Key:        key,
			State:      Hot,
			Size:       size,
			LastAccess: info.ModTime(),
			HotPath:    path,
		}

		c.entries[key] = entry
		c.currentSize += size
		heap.Push(&c.pq, entry)

		return filepath.SkipDir
	})

	// Scan warm directory
	warmDir := filepath.Join(c.baseDir, "warm")
	filepath.Walk(warmDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}

		// Skip non-lz4 files
		if filepath.Ext(path) != ".lz4" {
			return nil
		}

		rel, _ := filepath.Rel(warmDir, path)
		key := rel[:len(rel)-4] // Remove .lz4

		// Skip if already hot
		if _, exists := c.entries[key]; exists {
			return nil
		}

		entry := &CacheEntry{
			Key:        key,
			State:      Warm,
			Size:       info.Size(),
			LastAccess: info.ModTime(),
			WarmPath:   path,
		}

		c.entries[key] = entry
		c.currentSize += info.Size()
		heap.Push(&c.pq, entry)

		return nil
	})

	return nil
}

// Close shuts down the cache.
func (c *Cache) Close() error {
	close(c.stopCh)
	c.wg.Wait()
	return nil
}

// Get returns the path to use for executing a deployment.
// Returns the hot path if available, otherwise extracts from warm.
// Returns error if cold (not cached).
func (c *Cache) Get(key string) (string, error) {
	c.mu.Lock()
	entry, exists := c.entries[key]
	if !exists {
		c.mu.Unlock()
		return "", fmt.Errorf("not cached: %s (cold)", key)
	}

	// Update access stats
	entry.LastAccess = time.Now()
	entry.AccessCount++
	heap.Fix(&c.pq, entry.index)
	c.mu.Unlock()

	switch entry.State {
	case Hot:
		return entry.HotPath, nil
	case Warm:
		// Extract synchronously (fast, ~10-50ms)
		hotPath, err := c.extractWarm(entry)
		if err != nil {
			return "", fmt.Errorf("extract warm: %w", err)
		}
		return hotPath, nil
	default:
		return "", fmt.Errorf("invalid state: %s", entry.State)
	}
}

// GetState returns the current state of a deployment.
func (c *Cache) GetState(key string) State {
	c.mu.RLock()
	defer c.mu.RUnlock()

	entry, exists := c.entries[key]
	if !exists {
		return Cold
	}
	return entry.State
}

// Prime ensures a deployment is at least at the given state.
// This is async - use PrimeSync for blocking.
func (c *Cache) Prime(key, fingerprint, sourcePath string, targetState State) {
	select {
	case c.primeQueue <- primeRequest{
		key:         key,
		fingerprint: fingerprint,
		sourcePath:  sourcePath,
		targetState: targetState,
	}:
	default:
		// Queue full, drop request
	}
}

// PrimeSync ensures a deployment is at least at the given state (blocking).
func (c *Cache) PrimeSync(key, fingerprint, sourcePath string, targetState State) error {
	done := make(chan error, 1)
	c.primeQueue <- primeRequest{
		key:         key,
		fingerprint: fingerprint,
		sourcePath:  sourcePath,
		targetState: targetState,
		done:        done,
	}
	return <-done
}

// primeWorker processes prime requests.
func (c *Cache) primeWorker() {
	defer c.wg.Done()

	for {
		select {
		case <-c.stopCh:
			return
		case req := <-c.primeQueue:
			err := c.doPrime(req)
			if req.done != nil {
				req.done <- err
			}
		}
	}
}

// doPrime performs the actual priming.
func (c *Cache) doPrime(req primeRequest) error {
	c.mu.RLock()
	entry, exists := c.entries[req.key]
	currentState := Cold
	if exists {
		currentState = entry.State
	}
	c.mu.RUnlock()

	// Already at or above target state
	if currentState >= req.targetState {
		return nil
	}

	// Make room if needed
	if err := c.ensureSpace(estimateSize(req.sourcePath)); err != nil {
		return fmt.Errorf("ensure space: %w", err)
	}

	// Cold -> Warm: Create compressed archive
	if currentState == Cold && req.targetState >= Warm {
		warmPath, size, err := c.createWarmArchive(req.key, req.sourcePath)
		if err != nil {
			return fmt.Errorf("create warm archive: %w", err)
		}

		c.mu.Lock()
		entry = &CacheEntry{
			Key:        req.key,
			Fingerprint: req.fingerprint,
			State:      Warm,
			Size:       size,
			LastAccess: time.Now(),
			WarmPath:   warmPath,
		}
		c.entries[req.key] = entry
		c.currentSize += size
		heap.Push(&c.pq, entry)
		c.mu.Unlock()

		currentState = Warm
	}

	// Warm -> Hot: Extract archive
	if currentState == Warm && req.targetState >= Hot {
		hotPath, err := c.extractWarm(entry)
		if err != nil {
			return fmt.Errorf("extract warm: %w", err)
		}

		c.mu.Lock()
		oldSize := entry.Size
		entry.State = Hot
		entry.HotPath = hotPath
		entry.Size = dirSize(hotPath)
		c.currentSize += entry.Size - oldSize
		heap.Fix(&c.pq, entry.index)
		c.mu.Unlock()
	}

	return nil
}

// createWarmArchive compresses a deployment to the warm cache.
func (c *Cache) createWarmArchive(key, sourcePath string) (string, int64, error) {
	warmPath := filepath.Join(c.baseDir, "warm", key+".lz4")

	// Ensure parent directory exists
	if err := os.MkdirAll(filepath.Dir(warmPath), 0755); err != nil {
		return "", 0, err
	}

	// Create compressed archive
	file, err := os.Create(warmPath)
	if err != nil {
		return "", 0, err
	}
	defer file.Close()

	// LZ4 writer with good compression
	zw := lz4.NewWriter(file)
	zw.Apply(lz4.CompressionLevelOption(lz4.Level9))
	defer zw.Close()

	// Write files as simple concatenation with headers
	// Format: [path_len:4][path][size:8][data]...
	err = filepath.Walk(sourcePath, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}

		relPath, _ := filepath.Rel(sourcePath, path)

		// Write path length and path
		pathBytes := []byte(relPath)
		writeUint32(zw, uint32(len(pathBytes)))
		zw.Write(pathBytes)

		// Write size
		writeUint64(zw, uint64(info.Size()))

		// Write content
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = io.Copy(zw, f)
		return err
	})

	if err != nil {
		os.Remove(warmPath)
		return "", 0, err
	}

	// Get final size
	stat, _ := file.Stat()
	return warmPath, stat.Size(), nil
}

// extractWarm extracts a warm archive to hot.
func (c *Cache) extractWarm(entry *CacheEntry) (string, error) {
	if entry.State == Hot {
		return entry.HotPath, nil
	}

	hotPath := filepath.Join(c.baseDir, "hot", entry.Key)

	// Ensure parent directory exists
	if err := os.MkdirAll(hotPath, 0755); err != nil {
		return "", err
	}

	// Open compressed archive
	file, err := os.Open(entry.WarmPath)
	if err != nil {
		return "", err
	}
	defer file.Close()

	zr := lz4.NewReader(file)

	// Read files
	for {
		// Read path length
		pathLen, err := readUint32(zr)
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", fmt.Errorf("read path len: %w", err)
		}

		// Read path
		pathBytes := make([]byte, pathLen)
		if _, err := io.ReadFull(zr, pathBytes); err != nil {
			return "", fmt.Errorf("read path: %w", err)
		}
		relPath := string(pathBytes)

		// Read size
		size, err := readUint64(zr)
		if err != nil {
			return "", fmt.Errorf("read size: %w", err)
		}

		// Create file
		filePath := filepath.Join(hotPath, relPath)
		if err := os.MkdirAll(filepath.Dir(filePath), 0755); err != nil {
			return "", err
		}

		outFile, err := os.Create(filePath)
		if err != nil {
			return "", err
		}

		if _, err := io.CopyN(outFile, zr, int64(size)); err != nil {
			outFile.Close()
			return "", fmt.Errorf("copy content: %w", err)
		}
		outFile.Close()
	}

	// Update entry
	c.mu.Lock()
	entry.State = Hot
	entry.HotPath = hotPath
	c.mu.Unlock()

	return hotPath, nil
}

// ensureSpace evicts entries until we have enough space.
func (c *Cache) ensureSpace(needed int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	for c.currentSize+needed > c.maxSize && c.pq.Len() > 0 {
		// Get lowest priority entry
		entry := heap.Pop(&c.pq).(*CacheEntry)

		// Don't evict assigned deployments
		if entry.AssignedHere {
			heap.Push(&c.pq, entry)
			return fmt.Errorf("cache full, cannot evict assigned deployments")
		}

		// Evict
		if entry.State == Hot {
			os.RemoveAll(entry.HotPath)
		}
		if entry.WarmPath != "" {
			os.Remove(entry.WarmPath)
		}

		c.currentSize -= entry.Size
		delete(c.entries, entry.Key)
	}

	return nil
}

// SetAssigned marks a deployment as assigned to this node.
func (c *Cache) SetAssigned(key string, assigned bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if entry, exists := c.entries[key]; exists {
		entry.AssignedHere = assigned
		heap.Fix(&c.pq, entry.index)
	}
}

// Stats returns cache statistics.
type Stats struct {
	TotalSize   int64
	MaxSize     int64
	EntryCount  int
	HotCount    int
	WarmCount   int
	HitRate     float64
}

func (c *Cache) Stats() Stats {
	c.mu.RLock()
	defer c.mu.RUnlock()

	stats := Stats{
		TotalSize:  c.currentSize,
		MaxSize:    c.maxSize,
		EntryCount: len(c.entries),
	}

	for _, e := range c.entries {
		switch e.State {
		case Hot:
			stats.HotCount++
		case Warm:
			stats.WarmCount++
		}
	}

	return stats
}

// Helper functions

func dirSize(path string) int64 {
	var size int64
	filepath.Walk(path, func(_ string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			size += info.Size()
		}
		return nil
	})
	return size
}

func estimateSize(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	if !info.IsDir() {
		return info.Size()
	}
	return dirSize(path)
}

func writeUint32(w io.Writer, v uint32) error {
	buf := []byte{byte(v), byte(v >> 8), byte(v >> 16), byte(v >> 24)}
	_, err := w.Write(buf)
	return err
}

func writeUint64(w io.Writer, v uint64) error {
	buf := make([]byte, 8)
	for i := 0; i < 8; i++ {
		buf[i] = byte(v >> (i * 8))
	}
	_, err := w.Write(buf)
	return err
}

func readUint32(r io.Reader) (uint32, error) {
	buf := make([]byte, 4)
	if _, err := io.ReadFull(r, buf); err != nil {
		return 0, err
	}
	return uint32(buf[0]) | uint32(buf[1])<<8 | uint32(buf[2])<<16 | uint32(buf[3])<<24, nil
}

func readUint64(r io.Reader) (uint64, error) {
	buf := make([]byte, 8)
	if _, err := io.ReadFull(r, buf); err != nil {
		return 0, err
	}
	var v uint64
	for i := 0; i < 8; i++ {
		v |= uint64(buf[i]) << (i * 8)
	}
	return v, nil
}

// Priority queue implementation for LRU eviction

type priorityQueue []*CacheEntry

func (pq priorityQueue) Len() int { return len(pq) }

func (pq priorityQueue) Less(i, j int) bool {
	// Lower score = evict first
	return pq[i].Score() < pq[j].Score()
}

func (pq priorityQueue) Swap(i, j int) {
	pq[i], pq[j] = pq[j], pq[i]
	pq[i].index = i
	pq[j].index = j
}

func (pq *priorityQueue) Push(x any) {
	n := len(*pq)
	entry := x.(*CacheEntry)
	entry.index = n
	*pq = append(*pq, entry)
}

func (pq *priorityQueue) Pop() any {
	old := *pq
	n := len(old)
	entry := old[n-1]
	old[n-1] = nil
	entry.index = -1
	*pq = old[0 : n-1]
	return entry
}
