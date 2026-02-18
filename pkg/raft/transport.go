package raft

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

// HTTPTransport implements Transport using HTTP/JSON.
type HTTPTransport struct {
	addr   string
	client *http.Client
	node   *Node

	mu       sync.RWMutex
	handlers map[string]http.HandlerFunc
}

// NewHTTPTransport creates a new HTTP transport.
func NewHTTPTransport(addr string) *HTTPTransport {
	return &HTTPTransport{
		addr: addr,
		client: &http.Client{
			Timeout: 100 * time.Millisecond,
			Transport: &http.Transport{
				MaxIdleConnsPerHost: 10,
				IdleConnTimeout:     60 * time.Second,
			},
		},
		handlers: make(map[string]http.HandlerFunc),
	}
}

// SetNode sets the node for handling incoming RPCs.
func (t *HTTPTransport) SetNode(n *Node) {
	t.mu.Lock()
	t.node = n
	t.mu.Unlock()
}

// Address returns this transport's address.
func (t *HTTPTransport) Address() string {
	return t.addr
}

// PreVote sends a PreVote RPC to the given address.
func (t *HTTPTransport) PreVote(ctx context.Context, addr string, args *PreVoteArgs) (*PreVoteReply, error) {
	data, err := json.Marshal(args)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", "http://"+addr+"/raft/prevote", bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := t.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("prevote failed: %s", body)
	}

	var reply PreVoteReply
	if err := json.NewDecoder(resp.Body).Decode(&reply); err != nil {
		return nil, err
	}

	return &reply, nil
}

// RequestVote sends a RequestVote RPC to the given address.
func (t *HTTPTransport) RequestVote(ctx context.Context, addr string, args *RequestVoteArgs) (*RequestVoteReply, error) {
	data, err := json.Marshal(args)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", "http://"+addr+"/raft/vote", bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := t.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("request vote failed: %s", body)
	}

	var reply RequestVoteReply
	if err := json.NewDecoder(resp.Body).Decode(&reply); err != nil {
		return nil, err
	}

	return &reply, nil
}

// AppendEntries sends an AppendEntries RPC to the given address.
func (t *HTTPTransport) AppendEntries(ctx context.Context, addr string, args *AppendEntriesArgs) (*AppendEntriesReply, error) {
	data, err := json.Marshal(args)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", "http://"+addr+"/raft/append", bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := t.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("append entries failed: %s", body)
	}

	var reply AppendEntriesReply
	if err := json.NewDecoder(resp.Body).Decode(&reply); err != nil {
		return nil, err
	}

	return &reply, nil
}

// InstallSnapshot sends an InstallSnapshot RPC to the given address.
func (t *HTTPTransport) InstallSnapshot(ctx context.Context, addr string, args *InstallSnapshotArgs) (*InstallSnapshotReply, error) {
	data, err := json.Marshal(args)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", "http://"+addr+"/raft/snapshot", bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := t.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("install snapshot failed: %s", body)
	}

	var reply InstallSnapshotReply
	if err := json.NewDecoder(resp.Body).Decode(&reply); err != nil {
		return nil, err
	}

	return &reply, nil
}

// TimeoutNow sends a TimeoutNow RPC for leadership transfer.
func (t *HTTPTransport) TimeoutNow(ctx context.Context, addr string, args *TimeoutNowArgs) (*TimeoutNowReply, error) {
	data, err := json.Marshal(args)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", "http://"+addr+"/raft/timeoutnow", bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := t.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("timeoutnow failed: %s", body)
	}

	var reply TimeoutNowReply
	if err := json.NewDecoder(resp.Body).Decode(&reply); err != nil {
		return nil, err
	}

	return &reply, nil
}

// HandlePreVote handles incoming PreVote RPCs.
func (t *HTTPTransport) HandlePreVote(w http.ResponseWriter, r *http.Request) {
	t.mu.RLock()
	node := t.node
	t.mu.RUnlock()

	if node == nil {
		http.Error(w, "node not ready", http.StatusServiceUnavailable)
		return
	}

	var args PreVoteArgs
	if err := json.NewDecoder(r.Body).Decode(&args); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	reply := node.HandlePreVote(&args)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(reply)
}

// HandleVote handles incoming RequestVote RPCs.
func (t *HTTPTransport) HandleVote(w http.ResponseWriter, r *http.Request) {
	t.mu.RLock()
	node := t.node
	t.mu.RUnlock()

	if node == nil {
		http.Error(w, "node not ready", http.StatusServiceUnavailable)
		return
	}

	var args RequestVoteArgs
	if err := json.NewDecoder(r.Body).Decode(&args); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	reply := node.HandleRequestVote(&args)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(reply)
}

// HandleAppend handles incoming AppendEntries RPCs.
func (t *HTTPTransport) HandleAppend(w http.ResponseWriter, r *http.Request) {
	t.mu.RLock()
	node := t.node
	t.mu.RUnlock()

	if node == nil {
		http.Error(w, "node not ready", http.StatusServiceUnavailable)
		return
	}

	var args AppendEntriesArgs
	if err := json.NewDecoder(r.Body).Decode(&args); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	reply := node.HandleAppendEntries(&args)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(reply)
}

// HandleSnapshot handles incoming InstallSnapshot RPCs.
func (t *HTTPTransport) HandleSnapshot(w http.ResponseWriter, r *http.Request) {
	t.mu.RLock()
	node := t.node
	t.mu.RUnlock()

	if node == nil {
		http.Error(w, "node not ready", http.StatusServiceUnavailable)
		return
	}

	var args InstallSnapshotArgs
	if err := json.NewDecoder(r.Body).Decode(&args); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	reply := node.HandleInstallSnapshot(&args)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(reply)
}

// HandleTimeoutNow handles incoming TimeoutNow RPCs for leadership transfer.
func (t *HTTPTransport) HandleTimeoutNow(w http.ResponseWriter, r *http.Request) {
	t.mu.RLock()
	node := t.node
	t.mu.RUnlock()

	if node == nil {
		http.Error(w, "node not ready", http.StatusServiceUnavailable)
		return
	}

	var args TimeoutNowArgs
	if err := json.NewDecoder(r.Body).Decode(&args); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	reply := node.HandleTimeoutNow(&args)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(reply)
}

// RegisterHandlers registers the Raft HTTP handlers on a mux.
func (t *HTTPTransport) RegisterHandlers(mux *http.ServeMux) {
	mux.HandleFunc("POST /raft/prevote", t.HandlePreVote)
	mux.HandleFunc("POST /raft/vote", t.HandleVote)
	mux.HandleFunc("POST /raft/append", t.HandleAppend)
	mux.HandleFunc("POST /raft/snapshot", t.HandleSnapshot)
	mux.HandleFunc("POST /raft/timeoutnow", t.HandleTimeoutNow)
}

// MemoryTransport is an in-memory transport for testing.
type MemoryTransport struct {
	addr      string
	node      *Node
	mu        sync.RWMutex
	peers     map[string]*MemoryTransport
	networkMu *sync.RWMutex // Shared across all transports in a test
}

// MemoryNetwork manages a cluster of MemoryTransports for testing.
type MemoryNetwork struct {
	mu         sync.RWMutex
	transports map[string]*MemoryTransport
}

// NewMemoryNetwork creates a new in-memory network for testing.
func NewMemoryNetwork() *MemoryNetwork {
	return &MemoryNetwork{
		transports: make(map[string]*MemoryTransport),
	}
}

// AddTransport adds a transport to the network.
func (n *MemoryNetwork) AddTransport(addr string) *MemoryTransport {
	n.mu.Lock()
	defer n.mu.Unlock()

	t := &MemoryTransport{
		addr:      addr,
		peers:     make(map[string]*MemoryTransport),
		networkMu: &n.mu,
	}
	n.transports[addr] = t

	// Connect to all existing transports
	for a, existing := range n.transports {
		if a != addr {
			t.peers[a] = existing
			existing.mu.Lock()
			existing.peers[addr] = t
			existing.mu.Unlock()
		}
	}

	return t
}

// SetNode sets the node for this transport.
func (t *MemoryTransport) SetNode(n *Node) {
	t.mu.Lock()
	t.node = n
	t.mu.Unlock()
}

// Address returns the transport's address.
func (t *MemoryTransport) Address() string {
	return t.addr
}

// PreVote sends a PreVote RPC to the given address.
func (t *MemoryTransport) PreVote(ctx context.Context, addr string, args *PreVoteArgs) (*PreVoteReply, error) {
	t.mu.RLock()
	peer, ok := t.peers[addr]
	t.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("peer %s not found", addr)
	}

	peer.mu.RLock()
	node := peer.node
	peer.mu.RUnlock()

	if node == nil {
		return nil, fmt.Errorf("peer %s node not ready", addr)
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	return node.HandlePreVote(args), nil
}

// RequestVote sends a RequestVote RPC to the given address.
func (t *MemoryTransport) RequestVote(ctx context.Context, addr string, args *RequestVoteArgs) (*RequestVoteReply, error) {
	t.mu.RLock()
	peer, ok := t.peers[addr]
	t.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("peer %s not found", addr)
	}

	peer.mu.RLock()
	node := peer.node
	peer.mu.RUnlock()

	if node == nil {
		return nil, fmt.Errorf("peer %s node not ready", addr)
	}

	// Check context
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	return node.HandleRequestVote(args), nil
}

// AppendEntries sends an AppendEntries RPC to the given address.
func (t *MemoryTransport) AppendEntries(ctx context.Context, addr string, args *AppendEntriesArgs) (*AppendEntriesReply, error) {
	t.mu.RLock()
	peer, ok := t.peers[addr]
	t.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("peer %s not found", addr)
	}

	peer.mu.RLock()
	node := peer.node
	peer.mu.RUnlock()

	if node == nil {
		return nil, fmt.Errorf("peer %s node not ready", addr)
	}

	// Check context
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	return node.HandleAppendEntries(args), nil
}

// InstallSnapshot sends an InstallSnapshot RPC to the given address.
func (t *MemoryTransport) InstallSnapshot(ctx context.Context, addr string, args *InstallSnapshotArgs) (*InstallSnapshotReply, error) {
	t.mu.RLock()
	peer, ok := t.peers[addr]
	t.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("peer %s not found", addr)
	}

	peer.mu.RLock()
	node := peer.node
	peer.mu.RUnlock()

	if node == nil {
		return nil, fmt.Errorf("peer %s node not ready", addr)
	}

	// Check context
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	return node.HandleInstallSnapshot(args), nil
}

// TimeoutNow sends a TimeoutNow RPC for leadership transfer.
func (t *MemoryTransport) TimeoutNow(ctx context.Context, addr string, args *TimeoutNowArgs) (*TimeoutNowReply, error) {
	t.mu.RLock()
	peer, ok := t.peers[addr]
	t.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("peer %s not found", addr)
	}

	peer.mu.RLock()
	node := peer.node
	peer.mu.RUnlock()

	if node == nil {
		return nil, fmt.Errorf("peer %s node not ready", addr)
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	return node.HandleTimeoutNow(args), nil
}

// Disconnect simulates network partition by removing a peer.
func (t *MemoryTransport) Disconnect(addr string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.peers, addr)
}

// Reconnect re-establishes connection to a peer.
func (t *MemoryTransport) Reconnect(peer *MemoryTransport) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.peers[peer.addr] = peer
}

// Disconnect simulates a network partition between two nodes.
// Both directions are disconnected.
func (n *MemoryNetwork) Disconnect(addr1, addr2 string) {
	n.mu.Lock()
	defer n.mu.Unlock()

	t1, ok1 := n.transports[addr1]
	t2, ok2 := n.transports[addr2]

	if ok1 && ok2 {
		t1.Disconnect(addr2)
		t2.Disconnect(addr1)
	}
}

// Reconnect heals a network partition between two nodes.
// Both directions are reconnected.
func (n *MemoryNetwork) Reconnect(addr1, addr2 string) {
	n.mu.Lock()
	defer n.mu.Unlock()

	t1, ok1 := n.transports[addr1]
	t2, ok2 := n.transports[addr2]

	if ok1 && ok2 {
		t1.Reconnect(t2)
		t2.Reconnect(t1)
	}
}
