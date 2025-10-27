package storage

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"
)

// MetadataClient provides automatic leader redirect and retry for metadata operations
// CLD-REQ-051: Clients automatically redirect writes to RAFT leader
type MetadataClient struct {
	stores       map[string]*MetadataStore // nodeID -> MetadataStore
	currentStore *MetadataStore            // Currently connected store
	maxRetries   int
	retryDelay   time.Duration
	logger       *zap.Logger
}

// MetadataClientConfig configures the metadata client
type MetadataClientConfig struct {
	MaxRetries int           // Maximum number of retries on NotLeaderError
	RetryDelay time.Duration // Delay between retries
	Logger     *zap.Logger
}

// DefaultMetadataClientConfig returns default client configuration
func DefaultMetadataClientConfig() *MetadataClientConfig {
	return &MetadataClientConfig{
		MaxRetries: 3,
		RetryDelay: 100 * time.Millisecond,
		Logger:     zap.NewNop(),
	}
}

// NewMetadataClient creates a new metadata client
func NewMetadataClient(initialStore *MetadataStore, config *MetadataClientConfig) *MetadataClient {
	if config == nil {
		config = DefaultMetadataClientConfig()
	}

	return &MetadataClient{
		stores:       make(map[string]*MetadataStore),
		currentStore: initialStore,
		maxRetries:   config.MaxRetries,
		retryDelay:   config.RetryDelay,
		logger:       config.Logger,
	}
}

// AddStore adds a metadata store to the client's connection pool
// This allows the client to switch between stores when redirected
func (c *MetadataClient) AddStore(nodeID string, store *MetadataStore) {
	c.stores[nodeID] = store
}

// CreateBucketWithRetry creates a bucket with automatic leader redirect
func (c *MetadataClient) CreateBucketWithRetry(ctx context.Context, bucket *Bucket) error {
	return c.retryOnLeaderChange(ctx, func() error {
		return c.currentStore.CreateBucket(bucket)
	})
}

// DeleteBucketWithRetry deletes a bucket with automatic leader redirect
func (c *MetadataClient) DeleteBucketWithRetry(ctx context.Context, name string) error {
	return c.retryOnLeaderChange(ctx, func() error {
		return c.currentStore.DeleteBucket(name)
	})
}

// PutObjectWithRetry puts object metadata with automatic leader redirect
func (c *MetadataClient) PutObjectWithRetry(ctx context.Context, object *Object) error {
	return c.retryOnLeaderChange(ctx, func() error {
		return c.currentStore.PutObject(object)
	})
}

// DeleteObjectWithRetry deletes object metadata with automatic leader redirect
func (c *MetadataClient) DeleteObjectWithRetry(ctx context.Context, bucket, key string) error {
	return c.retryOnLeaderChange(ctx, func() error {
		return c.currentStore.DeleteObject(bucket, key)
	})
}

// GetBucket retrieves a bucket (reads don't need retry, any node can serve)
func (c *MetadataClient) GetBucket(name string) (*Bucket, error) {
	return c.currentStore.GetBucket(name)
}

// ListObjects lists objects (reads don't need retry)
func (c *MetadataClient) ListObjects(bucket, prefix string, limit int) ([]*ObjectMetadata, error) {
	return c.currentStore.ListObjects(bucket, prefix, limit)
}

// retryOnLeaderChange executes an operation with automatic retry on NotLeaderError
func (c *MetadataClient) retryOnLeaderChange(ctx context.Context, operation func() error) error {
	var lastErr error

	for attempt := 0; attempt <= c.maxRetries; attempt++ {
		// Check context cancellation
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// Execute operation
		err := operation()
		if err == nil {
			// Success!
			if attempt > 0 {
				c.logger.Info("Operation succeeded after retry",
					zap.Int("attempts", attempt+1))
			}
			return nil
		}

		// Check if it's a NotLeaderError
		var notLeaderErr *NotLeaderError
		if IsNotLeaderError(err) {
			notLeaderErr = err.(*NotLeaderError)
			lastErr = err

			// Try to switch to leader if we have it in our store pool
			leaderAddr := notLeaderErr.LeaderAddr
			if leaderAddr == "" {
				// No leader known, wait and retry
				c.logger.Warn("No leader known, retrying",
					zap.Int("attempt", attempt+1),
					zap.Int("max_retries", c.maxRetries))

				time.Sleep(c.retryDelay)
				continue
			}

			// Try to find leader in our store pool
			leaderFound := false
			for nodeID, store := range c.stores {
				if store.raftStore != nil && store.raftStore.GetRaftAddr() == leaderAddr {
					c.logger.Info("Redirecting to leader",
						zap.String("leader_node", nodeID),
						zap.String("leader_addr", leaderAddr),
						zap.Int("attempt", attempt+1))

					c.currentStore = store
					leaderFound = true
					break
				}
			}

			if !leaderFound {
				c.logger.Warn("Leader not found in store pool",
					zap.String("leader_addr", leaderAddr),
					zap.Int("available_stores", len(c.stores)))

				// Leader not in our pool, return error with redirect info
				return fmt.Errorf("leader at %s not available in client store pool: %w", leaderAddr, err)
			}

			// Small delay before retrying with new leader
			time.Sleep(c.retryDelay)
			continue
		}

		// Not a NotLeaderError, return immediately
		return err
	}

	// Max retries exceeded
	return fmt.Errorf("max retries (%d) exceeded: %w", c.maxRetries, lastErr)
}

// GetCurrentStore returns the currently connected store
func (c *MetadataClient) GetCurrentStore() *MetadataStore {
	return c.currentStore
}

// GetStoreCount returns the number of stores in the client's pool
func (c *MetadataClient) GetStoreCount() int {
	return len(c.stores)
}
