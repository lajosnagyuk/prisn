package raft

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Discovery handles automatic node discovery.
type Discovery struct {
	mu sync.RWMutex

	config   DiscoveryConfig
	peers    map[NodeID]string // id -> address
	onChange func([]string)     // Callback when peers change
	stopCh   chan struct{}
}

// DiscoveryConfig configures node discovery.
type DiscoveryConfig struct {
	// Method: "static", "dns", "file", "gossip"
	Method string

	// Static: list of peer addresses
	StaticPeers []string

	// DNS: headless service name for K8s
	DNSName string
	DNSPort int

	// File: directory for peer announcements (shared storage)
	FileDir string

	// Common
	Interval time.Duration // How often to check
	NodeID   NodeID        // This node's ID
	Address  string        // This node's address
}

func (c *DiscoveryConfig) defaults() {
	if c.Method == "" {
		c.Method = "static"
	}
	if c.Interval == 0 {
		c.Interval = 10 * time.Second
	}
	if c.DNSPort == 0 {
		c.DNSPort = 7332
	}
}

// NewDiscovery creates a new discovery service.
func NewDiscovery(cfg DiscoveryConfig) *Discovery {
	cfg.defaults()

	d := &Discovery{
		config: cfg,
		peers:  make(map[NodeID]string),
		stopCh: make(chan struct{}),
	}

	// Add static peers immediately
	for _, addr := range cfg.StaticPeers {
		// Use address as ID if no explicit ID
		id := NodeID(addr)
		d.peers[id] = addr
	}

	return d
}

// Start starts the discovery process.
func (d *Discovery) Start(ctx context.Context) {
	switch d.config.Method {
	case "static":
		// Nothing to do, peers are already set
		return
	case "dns":
		go d.dnsDiscoveryLoop(ctx)
	case "file":
		go d.fileDiscoveryLoop(ctx)
	}
}

// Stop stops the discovery process.
func (d *Discovery) Stop() {
	close(d.stopCh)
}

// OnChange sets a callback for when peers change.
func (d *Discovery) OnChange(fn func([]string)) {
	d.mu.Lock()
	d.onChange = fn
	d.mu.Unlock()
}

// Peers returns the current list of peer addresses.
func (d *Discovery) Peers() []string {
	d.mu.RLock()
	defer d.mu.RUnlock()

	result := make([]string, 0, len(d.peers))
	for _, addr := range d.peers {
		result = append(result, addr)
	}
	return result
}

// GetPeer returns the address of a specific peer.
func (d *Discovery) GetPeer(id NodeID) (string, bool) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	addr, ok := d.peers[id]
	return addr, ok
}

// dnsDiscoveryLoop periodically resolves DNS for peers.
func (d *Discovery) dnsDiscoveryLoop(ctx context.Context) {
	ticker := time.NewTicker(d.config.Interval)
	defer ticker.Stop()

	// Initial discovery
	d.dnsDiscover()

	for {
		select {
		case <-ctx.Done():
			return
		case <-d.stopCh:
			return
		case <-ticker.C:
			d.dnsDiscover()
		}
	}
}

// dnsDiscover resolves DNS SRV or A records.
func (d *Discovery) dnsDiscover() {
	// Try SRV first (for proper service discovery)
	_, addrs, err := net.LookupSRV("", "", d.config.DNSName)
	if err == nil && len(addrs) > 0 {
		d.mu.Lock()
		changed := false
		seen := make(map[NodeID]bool)

		for _, srv := range addrs {
			addr := fmt.Sprintf("%s:%d", strings.TrimSuffix(srv.Target, "."), srv.Port)
			id := NodeID(addr)
			seen[id] = true

			if _, exists := d.peers[id]; !exists {
				d.peers[id] = addr
				changed = true
			}
		}

		// Remove peers that are gone
		for id := range d.peers {
			if !seen[id] {
				delete(d.peers, id)
				changed = true
			}
		}

		onChange := d.onChange
		d.mu.Unlock()

		if changed && onChange != nil {
			onChange(d.Peers())
		}
		return
	}

	// Fall back to A records
	ips, err := net.LookupHost(d.config.DNSName)
	if err != nil {
		return
	}

	d.mu.Lock()
	changed := false
	seen := make(map[NodeID]bool)

	for _, ip := range ips {
		addr := fmt.Sprintf("%s:%d", ip, d.config.DNSPort)
		id := NodeID(addr)
		seen[id] = true

		if _, exists := d.peers[id]; !exists {
			d.peers[id] = addr
			changed = true
		}
	}

	for id := range d.peers {
		if !seen[id] {
			delete(d.peers, id)
			changed = true
		}
	}

	onChange := d.onChange
	d.mu.Unlock()

	if changed && onChange != nil {
		onChange(d.Peers())
	}
}

// fileDiscoveryLoop watches a directory for peer announcements.
func (d *Discovery) fileDiscoveryLoop(ctx context.Context) {
	// Ensure directory exists
	os.MkdirAll(d.config.FileDir, 0755)

	ticker := time.NewTicker(d.config.Interval)
	defer ticker.Stop()

	// Announce self
	d.fileAnnounce()

	// Initial discovery
	d.fileDiscover()

	for {
		select {
		case <-ctx.Done():
			d.fileRemove()
			return
		case <-d.stopCh:
			d.fileRemove()
			return
		case <-ticker.C:
			d.fileAnnounce() // Refresh heartbeat
			d.fileDiscover()
		}
	}
}

// fileAnnounce writes this node's info to the discovery directory.
func (d *Discovery) fileAnnounce() {
	info := struct {
		ID        NodeID    `json:"id"`
		Address   string    `json:"address"`
		UpdatedAt time.Time `json:"updated_at"`
	}{
		ID:        d.config.NodeID,
		Address:   d.config.Address,
		UpdatedAt: time.Now(),
	}

	data, _ := json.Marshal(info)
	path := filepath.Join(d.config.FileDir, string(d.config.NodeID)+".json")
	os.WriteFile(path, data, 0644)
}

// fileRemove removes this node's announcement file.
func (d *Discovery) fileRemove() {
	path := filepath.Join(d.config.FileDir, string(d.config.NodeID)+".json")
	os.Remove(path)
}

// fileDiscover reads peer announcements from the directory.
func (d *Discovery) fileDiscover() {
	entries, err := os.ReadDir(d.config.FileDir)
	if err != nil {
		return
	}

	d.mu.Lock()
	changed := false
	seen := make(map[NodeID]bool)
	staleThreshold := 3 * d.config.Interval // Consider stale after 3 missed intervals

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}

		path := filepath.Join(d.config.FileDir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}

		var info struct {
			ID        NodeID    `json:"id"`
			Address   string    `json:"address"`
			UpdatedAt time.Time `json:"updated_at"`
		}

		if err := json.Unmarshal(data, &info); err != nil {
			continue
		}

		// Skip stale entries
		if time.Since(info.UpdatedAt) > staleThreshold {
			os.Remove(path) // Clean up stale file
			continue
		}

		// Skip self
		if info.ID == d.config.NodeID {
			continue
		}

		seen[info.ID] = true

		if existing, exists := d.peers[info.ID]; !exists || existing != info.Address {
			d.peers[info.ID] = info.Address
			changed = true
		}
	}

	// Remove peers that are gone
	for id := range d.peers {
		if !seen[id] && id != d.config.NodeID {
			delete(d.peers, id)
			changed = true
		}
	}

	onChange := d.onChange
	d.mu.Unlock()

	if changed && onChange != nil {
		onChange(d.Peers())
	}
}

// BootstrapCluster initializes a new single-node cluster.
// Call this on the first node to start the cluster.
func (c *Cluster) BootstrapCluster() error {
	// Add self to cluster
	cmd := NodeCommand{
		NodeID:  c.node.config.ID,
		Address: c.transport.Address(),
	}

	_, _, err := c.node.Propose(LogNodeJoin, cmd)
	return err
}

// JoinWithDiscovery joins a cluster using discovered peers.
func (c *Cluster) JoinWithDiscovery(discovery *Discovery, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	peers := discovery.Peers()
	if len(peers) == 0 {
		return fmt.Errorf("no peers discovered")
	}

	// Prepare join request
	joinReq := struct {
		NodeID  string `json:"node_id"`
		Address string `json:"address"`
	}{
		NodeID:  string(c.node.config.ID),
		Address: c.transport.Address(),
	}

	reqBody, err := json.Marshal(joinReq)
	if err != nil {
		return fmt.Errorf("marshal join request: %w", err)
	}

	client := &http.Client{
		Timeout: 5 * time.Second,
	}

	var lastErr error
	maxRetries := 3
	baseBackoff := 100 * time.Millisecond

	// Try to join through each peer with retries
	for _, peerAddr := range peers {
		for attempt := 0; attempt < maxRetries; attempt++ {
			select {
			case <-ctx.Done():
				return fmt.Errorf("timeout joining cluster: %w", ctx.Err())
			default:
			}

			// Build the join URL - peerAddr might be host:port for Raft, use port 7331 for API
			apiAddr := peerAddr
			if host, _, err := net.SplitHostPort(peerAddr); err == nil {
				// If we got a Raft port (7332), use the API port (7331)
				apiAddr = net.JoinHostPort(host, "7331")
			}

			url := fmt.Sprintf("http://%s/api/v1/cluster/join", apiAddr)

			req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(reqBody))
			if err != nil {
				lastErr = fmt.Errorf("create request for %s: %w", peerAddr, err)
				continue
			}
			req.Header.Set("Content-Type", "application/json")

			resp, err := client.Do(req)
			if err != nil {
				lastErr = fmt.Errorf("join via %s: %w", peerAddr, err)
				// Exponential backoff before retry
				backoff := baseBackoff * time.Duration(1<<attempt)
				select {
				case <-ctx.Done():
					return fmt.Errorf("timeout joining cluster: %w", ctx.Err())
				case <-time.After(backoff):
				}
				continue
			}

			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()

			if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusCreated {
				// Successfully joined
				return nil
			}

			// Check if this peer isn't the leader
			if resp.StatusCode == http.StatusServiceUnavailable {
				// Not the leader, try to extract leader hint from response
				var errResp struct {
					Error string `json:"error"`
				}
				if json.Unmarshal(body, &errResp) == nil && strings.Contains(errResp.Error, "not leader") {
					// Move on to next peer
					lastErr = fmt.Errorf("%s is not leader", peerAddr)
					break
				}
			}

			lastErr = fmt.Errorf("join via %s failed (status %d): %s", peerAddr, resp.StatusCode, string(body))

			// Backoff before retry
			backoff := baseBackoff * time.Duration(1<<attempt)
			select {
			case <-ctx.Done():
				return fmt.Errorf("timeout joining cluster: %w", ctx.Err())
			case <-time.After(backoff):
			}
		}
	}

	if lastErr != nil {
		return fmt.Errorf("failed to join cluster after trying all peers: %w", lastErr)
	}
	return fmt.Errorf("failed to join cluster: no peers responded")
}
