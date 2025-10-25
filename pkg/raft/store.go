package raft

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/hashicorp/raft"
	raftboltdb "github.com/hashicorp/raft-boltdb/v2"
	"go.uber.org/zap"
)

const (
	retainSnapshotCount = 2
	raftTimeout         = 10 * time.Second
	leaderWaitDelay     = 100 * time.Millisecond
	appliedWaitDelay    = 100 * time.Millisecond
)

// Store provides a distributed key-value store using Raft consensus
type Store struct {
	raft   *raft.Raft
	fsm    *FSM
	config *Config
	logger *zap.Logger

	// Leadership tracking
	leaderCh   chan bool
	shutdownCh chan struct{}

	mu sync.RWMutex
}

// Config contains configuration for the Raft store
type Config struct {
	RaftDir       string
	RaftBind      string
	RaftID        string
	Bootstrap     bool
	Logger        *zap.Logger
	LocalID       raft.ServerID
	EnableSingle  bool
	SnapshotRetain int
}

// NewStore creates a new Raft-backed store
func NewStore(config *Config) (*Store, error) {
	if config.Logger == nil {
		return nil, fmt.Errorf("logger is required")
	}
	if config.RaftDir == "" {
		return nil, fmt.Errorf("raft directory is required")
	}
	if config.RaftBind == "" {
		return nil, fmt.Errorf("raft bind address is required")
	}
	if config.LocalID == "" {
		config.LocalID = raft.ServerID(config.RaftID)
	}
	if config.SnapshotRetain <= 0 {
		config.SnapshotRetain = retainSnapshotCount
	}

	// Create Raft directory
	if err := os.MkdirAll(config.RaftDir, 0700); err != nil {
		return nil, fmt.Errorf("failed to create raft directory: %w", err)
	}

	s := &Store{
		config:     config,
		logger:     config.Logger,
		leaderCh:   make(chan bool, 1),
		shutdownCh: make(chan struct{}),
	}

	// Initialize Raft
	if err := s.initRaft(); err != nil {
		return nil, fmt.Errorf("failed to initialize raft: %w", err)
	}

	return s, nil
}

// initRaft initializes the Raft subsystem
func (s *Store) initRaft() error {
	// Create FSM
	s.fsm = NewFSM(s.logger)

	// Create Raft configuration
	config := raft.DefaultConfig()
	config.LocalID = s.config.LocalID
	config.Logger = newHashiLogger(s.logger)
	config.SnapshotThreshold = 1024
	config.SnapshotInterval = 120 * time.Second
	config.LeaderLeaseTimeout = 500 * time.Millisecond
	config.HeartbeatTimeout = 1000 * time.Millisecond
	config.ElectionTimeout = 1000 * time.Millisecond
	config.CommitTimeout = 50 * time.Millisecond

	// Setup Raft communication
	addr, err := net.ResolveTCPAddr("tcp", s.config.RaftBind)
	if err != nil {
		return fmt.Errorf("failed to resolve bind address: %w", err)
	}

	// Create transport
	transport, err := raft.NewTCPTransport(s.config.RaftBind, addr, 3, 10*time.Second, os.Stderr)
	if err != nil {
		return fmt.Errorf("failed to create transport: %w", err)
	}

	// Create snapshot store
	snapshotStore, err := raft.NewFileSnapshotStore(s.config.RaftDir, s.config.SnapshotRetain, os.Stderr)
	if err != nil {
		return fmt.Errorf("failed to create snapshot store: %w", err)
	}

	// Create log store
	logStore, err := raftboltdb.NewBoltStore(filepath.Join(s.config.RaftDir, "raft.db"))
	if err != nil {
		return fmt.Errorf("failed to create log store: %w", err)
	}

	// Create stable store
	stableStore, err := raftboltdb.NewBoltStore(filepath.Join(s.config.RaftDir, "stable.db"))
	if err != nil {
		return fmt.Errorf("failed to create stable store: %w", err)
	}

	// Create Raft instance
	ra, err := raft.NewRaft(config, s.fsm, logStore, stableStore, snapshotStore, transport)
	if err != nil {
		return fmt.Errorf("failed to create raft: %w", err)
	}
	s.raft = ra

	// Bootstrap if needed
	if s.config.Bootstrap {
		configuration := raft.Configuration{
			Servers: []raft.Server{
				{
					ID:      config.LocalID,
					Address: transport.LocalAddr(),
				},
			},
		}
		ra.BootstrapCluster(configuration)
		s.logger.Info("Bootstrapped Raft cluster",
			zap.String("id", string(config.LocalID)),
			zap.String("addr", string(transport.LocalAddr())),
		)
	}

	// Start monitoring leadership changes
	go s.monitorLeadership()

	return nil
}

// monitorLeadership monitors leadership changes
func (s *Store) monitorLeadership() {
	for {
		select {
		case leader := <-s.raft.LeaderCh():
			s.mu.Lock()
			if leader {
				s.logger.Info("Became leader")
			} else {
				s.logger.Info("Lost leadership")
			}
			s.mu.Unlock()

			select {
			case s.leaderCh <- leader:
			default:
			}
		case <-s.shutdownCh:
			return
		}
	}
}

// Get retrieves a value for a given key
func (s *Store) Get(key string) ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.fsm.Get(key)
}

// Set stores a key-value pair
func (s *Store) Set(key string, value []byte) error {
	if s.raft.State() != raft.Leader {
		return fmt.Errorf("not leader")
	}

	cmd := &Command{
		Op:    "set",
		Key:   key,
		Value: value,
	}

	data, err := json.Marshal(cmd)
	if err != nil {
		return fmt.Errorf("failed to marshal command: %w", err)
	}

	future := s.raft.Apply(data, raftTimeout)
	if err := future.Error(); err != nil {
		return fmt.Errorf("failed to apply command: %w", err)
	}

	response := future.Response()
	if err, ok := response.(error); ok {
		return err
	}

	return nil
}

// Delete removes a key
func (s *Store) Delete(key string) error {
	if s.raft.State() != raft.Leader {
		return fmt.Errorf("not leader")
	}

	cmd := &Command{
		Op:  "delete",
		Key: key,
	}

	data, err := json.Marshal(cmd)
	if err != nil {
		return fmt.Errorf("failed to marshal command: %w", err)
	}

	future := s.raft.Apply(data, raftTimeout)
	if err := future.Error(); err != nil {
		return fmt.Errorf("failed to apply command: %w", err)
	}

	response := future.Response()
	if err, ok := response.(error); ok {
		return err
	}

	return nil
}

// GetAll returns all key-value pairs
func (s *Store) GetAll() (map[string][]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.fsm.GetAll()
}

// ListKeys returns all keys with the given prefix
func (s *Store) ListKeys(prefix string) ([]string, error) {
	all, err := s.GetAll()
	if err != nil {
		return nil, err
	}

	keys := make([]string, 0)
	for key := range all {
		if strings.HasPrefix(key, prefix) {
			keys = append(keys, key)
		}
	}

	return keys, nil
}

// Join adds a server to the cluster
func (s *Store) Join(nodeID, addr string) error {
	s.logger.Info("Received join request",
		zap.String("node_id", nodeID),
		zap.String("addr", addr),
	)

	configFuture := s.raft.GetConfiguration()
	if err := configFuture.Error(); err != nil {
		return fmt.Errorf("failed to get configuration: %w", err)
	}

	for _, srv := range configFuture.Configuration().Servers {
		if srv.ID == raft.ServerID(nodeID) {
			if srv.Address == raft.ServerAddress(addr) {
				s.logger.Info("Node already member of cluster",
					zap.String("node_id", nodeID),
				)
				return nil
			}

			// Remove the existing node first
			removeFuture := s.raft.RemoveServer(srv.ID, 0, 0)
			if err := removeFuture.Error(); err != nil {
				return fmt.Errorf("failed to remove existing node: %w", err)
			}
		}
	}

	// Add as voter
	f := s.raft.AddVoter(raft.ServerID(nodeID), raft.ServerAddress(addr), 0, 0)
	if f.Error() != nil {
		return f.Error()
	}

	s.logger.Info("Node joined cluster successfully",
		zap.String("node_id", nodeID),
		zap.String("addr", addr),
	)

	return nil
}

// Remove removes a server from the cluster
func (s *Store) Remove(nodeID string) error {
	s.logger.Info("Removing node from cluster",
		zap.String("node_id", nodeID),
	)

	f := s.raft.RemoveServer(raft.ServerID(nodeID), 0, 0)
	if err := f.Error(); err != nil {
		return fmt.Errorf("failed to remove server: %w", err)
	}

	s.logger.Info("Node removed from cluster",
		zap.String("node_id", nodeID),
	)

	return nil
}

// IsLeader returns true if this node is the cluster leader
func (s *Store) IsLeader() bool {
	return s.raft.State() == raft.Leader
}

// GetLeader returns the address of the cluster leader
func (s *Store) GetLeader() string {
	return string(s.raft.Leader())
}

// GetPeers returns the list of cluster peers
func (s *Store) GetPeers() ([]string, error) {
	configFuture := s.raft.GetConfiguration()
	if err := configFuture.Error(); err != nil {
		return nil, err
	}

	var peers []string
	for _, server := range configFuture.Configuration().Servers {
		peers = append(peers, string(server.Address))
	}

	return peers, nil
}

// WaitForLeader waits for a leader to be elected
func (s *Store) WaitForLeader(timeout time.Duration) error {
	ticker := time.NewTicker(leaderWaitDelay)
	defer ticker.Stop()

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	for {
		select {
		case <-ticker.C:
			if leader := s.GetLeader(); leader != "" {
				s.logger.Info("Leader detected", zap.String("leader", leader))
				return nil
			}
		case <-timer.C:
			return fmt.Errorf("timeout waiting for leader")
		}
	}
}

// WaitForAppliedIndex waits for a specific index to be applied
func (s *Store) WaitForAppliedIndex(index uint64, timeout time.Duration) error {
	ticker := time.NewTicker(appliedWaitDelay)
	defer ticker.Stop()

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	for {
		select {
		case <-ticker.C:
			if s.raft.AppliedIndex() >= index {
				return nil
			}
		case <-timer.C:
			return fmt.Errorf("timeout waiting for applied index")
		}
	}
}

// Snapshot triggers a manual snapshot
func (s *Store) Snapshot() error {
	future := s.raft.Snapshot()
	return future.Error()
}

// Restore restores from a snapshot
func (s *Store) Restore(rc io.ReadCloser) error {
	return s.raft.Restore(nil, rc, 0)
}

// Stats returns Raft statistics
func (s *Store) Stats() map[string]string {
	return s.raft.Stats()
}

// Close shuts down the Raft store
func (s *Store) Close() error {
	s.logger.Info("Shutting down Raft store")

	close(s.shutdownCh)

	if s.raft != nil {
		future := s.raft.Shutdown()
		if err := future.Error(); err != nil {
			return fmt.Errorf("failed to shutdown raft: %w", err)
		}
	}

	s.logger.Info("Raft store shutdown complete")
	return nil
}

// ApplyBatch applies multiple commands in a single Raft log entry
func (s *Store) ApplyBatch(commands []*Command) error {
	if s.raft.State() != raft.Leader {
		return fmt.Errorf("not leader")
	}

	batch := &BatchCommand{
		Commands: commands,
	}

	data, err := json.Marshal(batch)
	if err != nil {
		return fmt.Errorf("failed to marshal batch: %w", err)
	}

	future := s.raft.Apply(data, raftTimeout)
	if err := future.Error(); err != nil {
		return fmt.Errorf("failed to apply batch: %w", err)
	}

	response := future.Response()
	if err, ok := response.(error); ok {
		return err
	}

	return nil
}

// SetWithTTL sets a key-value pair with expiration
func (s *Store) SetWithTTL(key string, value []byte, ttl time.Duration) error {
	if s.raft.State() != raft.Leader {
		return fmt.Errorf("not leader")
	}

	cmd := &Command{
		Op:        "set",
		Key:       key,
		Value:     value,
		ExpiresAt: time.Now().Add(ttl).Unix(),
	}

	data, err := json.Marshal(cmd)
	if err != nil {
		return fmt.Errorf("failed to marshal command: %w", err)
	}

	future := s.raft.Apply(data, raftTimeout)
	if err := future.Error(); err != nil {
		return fmt.Errorf("failed to apply command: %w", err)
	}

	response := future.Response()
	if err, ok := response.(error); ok {
		return err
	}

	return nil
}

// LeadershipTransfer transfers leadership to another node
func (s *Store) LeadershipTransfer() error {
	if s.raft.State() != raft.Leader {
		return fmt.Errorf("not leader")
	}

	s.logger.Info("Initiating leadership transfer")
	future := s.raft.LeadershipTransfer()
	return future.Error()
}

// Barrier blocks until all preceding operations have been applied
func (s *Store) Barrier(timeout time.Duration) error {
	if s.raft.State() != raft.Leader {
		return fmt.Errorf("not leader")
	}

	barrier := s.raft.Barrier(timeout)
	return barrier.Error()
}