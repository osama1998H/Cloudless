package storage

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"time"

	"go.uber.org/zap"
)

// Client provides a high-level API for interacting with the storage system
type Client struct {
	config ClientConfig
	logger *zap.Logger

	// Backend can be either StorageManager (coordinator) or ObjectStore (direct)
	manager     *StorageManager
	objectStore *ObjectStore
}

// ClientConfig contains client configuration
type ClientConfig struct {
	// Connection
	Endpoint string
	Timeout  time.Duration

	// Retry configuration
	MaxRetries int
	RetryDelay time.Duration

	// Logger
	Logger *zap.Logger
}

// NewClient creates a new storage client
func NewClient(config ClientConfig) (*Client, error) {
	if config.Logger == nil {
		var err error
		config.Logger, err = zap.NewProduction()
		if err != nil {
			return nil, fmt.Errorf("failed to create logger: %w", err)
		}
	}

	if config.Timeout == 0 {
		config.Timeout = 30 * time.Second
	}

	if config.MaxRetries == 0 {
		config.MaxRetries = 3
	}

	if config.RetryDelay == 0 {
		config.RetryDelay = time.Second
	}

	client := &Client{
		config: config,
		logger: config.Logger,
	}

	// In production, would establish connection to coordinator/storage system
	// For now, client is initialized without connection

	return client, nil
}

// ConnectToManager connects the client to a storage manager (coordinator)
func (c *Client) ConnectToManager(manager *StorageManager) {
	c.manager = manager
}

// ConnectToObjectStore connects the client directly to an object store
func (c *Client) ConnectToObjectStore(store *ObjectStore) {
	c.objectStore = store
}

// Bucket Operations

// CreateBucket creates a new bucket
func (c *Client) CreateBucket(ctx context.Context, name string, opts ...BucketOption) error {
	req := &CreateBucketRequest{
		Name:         name,
		StorageClass: StorageClassHot, // Default
		Labels:       make(map[string]string),
	}

	// Apply options
	for _, opt := range opts {
		opt(req)
	}

	return c.withRetry(ctx, func() error {
		if c.manager != nil {
			return c.manager.CreateBucket(ctx, req)
		} else if c.objectStore != nil {
			return c.objectStore.CreateBucket(ctx, req)
		}
		return fmt.Errorf("no storage backend connected")
	})
}

// DeleteBucket deletes a bucket
func (c *Client) DeleteBucket(ctx context.Context, name string) error {
	return c.withRetry(ctx, func() error {
		if c.manager != nil {
			return c.manager.DeleteBucket(ctx, name)
		} else if c.objectStore != nil {
			return c.objectStore.DeleteBucket(ctx, name)
		}
		return fmt.Errorf("no storage backend connected")
	})
}

// ListBuckets lists all buckets
func (c *Client) ListBuckets(ctx context.Context) ([]*BucketMetadata, error) {
	var buckets []*BucketMetadata

	err := c.withRetry(ctx, func() error {
		var err error
		if c.manager != nil {
			buckets, err = c.manager.ListBuckets(ctx)
		} else if c.objectStore != nil {
			buckets, err = c.objectStore.ListBuckets(ctx)
		} else {
			return fmt.Errorf("no storage backend connected")
		}
		return err
	})

	return buckets, err
}

// GetBucket retrieves bucket information
func (c *Client) GetBucket(ctx context.Context, name string) (*Bucket, error) {
	var bucket *Bucket

	err := c.withRetry(ctx, func() error {
		var err error
		if c.manager != nil {
			// Manager doesn't expose GetBucket directly, would need to add
			return fmt.Errorf("GetBucket not implemented for manager")
		} else if c.objectStore != nil {
			bucket, err = c.objectStore.GetBucket(ctx, name)
		} else {
			return fmt.Errorf("no storage backend connected")
		}
		return err
	})

	return bucket, err
}

// Object Operations

// PutObject stores an object
func (c *Client) PutObject(ctx context.Context, bucket, key string, data []byte, opts ...ObjectOption) error {
	req := &PutObjectRequest{
		Bucket:       bucket,
		Key:          key,
		Data:         data,
		StorageClass: StorageClassHot, // Default
		Metadata:     make(map[string]string),
	}

	// Apply options
	for _, opt := range opts {
		opt.apply(req)
	}

	return c.withRetry(ctx, func() error {
		if c.manager != nil {
			return c.manager.PutObject(ctx, req)
		} else if c.objectStore != nil {
			return c.objectStore.PutObject(ctx, req)
		}
		return fmt.Errorf("no storage backend connected")
	})
}

// GetObject retrieves an object
func (c *Client) GetObject(ctx context.Context, bucket, key string) ([]byte, error) {
	var response *GetObjectResponse

	err := c.withRetry(ctx, func() error {
		var err error
		if c.manager != nil {
			response, err = c.manager.GetObject(ctx, bucket, key)
		} else if c.objectStore != nil {
			response, err = c.objectStore.GetObject(ctx, bucket, key)
		} else {
			return fmt.Errorf("no storage backend connected")
		}
		return err
	})

	if err != nil {
		return nil, err
	}

	return response.Data, nil
}

// GetObjectWithMetadata retrieves an object with its metadata
func (c *Client) GetObjectWithMetadata(ctx context.Context, bucket, key string) (*GetObjectResponse, error) {
	var response *GetObjectResponse

	err := c.withRetry(ctx, func() error {
		var err error
		if c.manager != nil {
			response, err = c.manager.GetObject(ctx, bucket, key)
		} else if c.objectStore != nil {
			response, err = c.objectStore.GetObject(ctx, bucket, key)
		} else {
			return fmt.Errorf("no storage backend connected")
		}
		return err
	})

	return response, err
}

// DeleteObject deletes an object
func (c *Client) DeleteObject(ctx context.Context, bucket, key string) error {
	return c.withRetry(ctx, func() error {
		if c.manager != nil {
			return c.manager.DeleteObject(ctx, bucket, key)
		} else if c.objectStore != nil {
			return c.objectStore.DeleteObject(ctx, bucket, key)
		}
		return fmt.Errorf("no storage backend connected")
	})
}

// ListObjects lists objects in a bucket
func (c *Client) ListObjects(ctx context.Context, bucket string, opts ...ListOption) ([]*ObjectMetadata, error) {
	req := &ListObjectsRequest{
		Bucket:  bucket,
		Prefix:  "",
		MaxKeys: 1000, // Default
	}

	// Apply options
	for _, opt := range opts {
		opt(req)
	}

	var objects []*ObjectMetadata

	err := c.withRetry(ctx, func() error {
		var err error
		if c.manager != nil {
			objects, err = c.manager.ListObjects(ctx, req)
		} else if c.objectStore != nil {
			objects, err = c.objectStore.ListObjects(ctx, req)
		} else {
			return fmt.Errorf("no storage backend connected")
		}
		return err
	})

	return objects, err
}

// Streaming Operations

// PutObjectStream stores an object from a stream
func (c *Client) PutObjectStream(ctx context.Context, bucket, key string, reader io.Reader, opts ...ObjectOption) error {
	req := &PutObjectStreamRequest{
		Bucket:       bucket,
		Key:          key,
		Reader:       reader,
		StorageClass: StorageClassHot,
		Metadata:     make(map[string]string),
	}

	// Apply options
	for _, opt := range opts {
		opt.applyStream(req)
	}

	return c.withRetry(ctx, func() error {
		if c.manager != nil {
			// Would need to add streaming support to manager
			return fmt.Errorf("streaming not implemented for manager")
		} else if c.objectStore != nil {
			return c.objectStore.PutObjectStream(ctx, req)
		}
		return fmt.Errorf("no storage backend connected")
	})
}

// GetObjectStream retrieves an object as a stream
func (c *Client) GetObjectStream(ctx context.Context, bucket, key string, writer io.Writer) error {
	return c.withRetry(ctx, func() error {
		if c.manager != nil {
			// Would need to add streaming support to manager
			return fmt.Errorf("streaming not implemented for manager")
		} else if c.objectStore != nil {
			return c.objectStore.GetObjectStream(ctx, bucket, key, writer)
		}
		return fmt.Errorf("no storage backend connected")
	})
}

// Multipart Upload Operations

// InitiateMultipartUpload starts a multipart upload
func (c *Client) InitiateMultipartUpload(ctx context.Context, bucket, key string, opts ...ObjectOption) (string, error) {
	req := &InitiateMultipartUploadRequest{
		Bucket:       bucket,
		Key:          key,
		StorageClass: StorageClassHot,
		Metadata:     make(map[string]string),
	}

	// Apply options
	for _, opt := range opts {
		opt.applyMultipart(req)
	}

	var uploadID string

	err := c.withRetry(ctx, func() error {
		if c.objectStore != nil {
			resp, err := c.objectStore.InitiateMultipartUpload(ctx, req)
			if err != nil {
				return err
			}
			uploadID = resp.UploadID
			return nil
		}
		return fmt.Errorf("multipart upload not supported")
	})

	return uploadID, err
}

// UploadPart uploads a part of a multipart upload
func (c *Client) UploadPart(ctx context.Context, uploadID string, partNumber int, data []byte) (string, error) {
	req := &UploadPartRequest{
		UploadID:   uploadID,
		PartNumber: partNumber,
		Data:       data,
	}

	var checksum string

	err := c.withRetry(ctx, func() error {
		if c.objectStore != nil {
			resp, err := c.objectStore.UploadPart(ctx, req)
			if err != nil {
				return err
			}
			checksum = resp.Checksum
			return nil
		}
		return fmt.Errorf("multipart upload not supported")
	})

	return checksum, err
}

// CompleteMultipartUpload completes a multipart upload
func (c *Client) CompleteMultipartUpload(ctx context.Context, uploadID string) error {
	req := &CompleteMultipartUploadRequest{
		UploadID: uploadID,
	}

	return c.withRetry(ctx, func() error {
		if c.objectStore != nil {
			return c.objectStore.CompleteMultipartUpload(ctx, req)
		}
		return fmt.Errorf("multipart upload not supported")
	})
}

// AbortMultipartUpload aborts a multipart upload
func (c *Client) AbortMultipartUpload(ctx context.Context, uploadID string) error {
	return c.withRetry(ctx, func() error {
		if c.objectStore != nil {
			return c.objectStore.AbortMultipartUpload(ctx, uploadID)
		}
		return fmt.Errorf("multipart upload not supported")
	})
}

// Helper Methods

// withRetry executes a function with retry logic
func (c *Client) withRetry(ctx context.Context, fn func() error) error {
	var lastErr error

	for attempt := 0; attempt <= c.config.MaxRetries; attempt++ {
		if attempt > 0 {
			// Wait before retry
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(c.config.RetryDelay):
			}

			c.logger.Debug("Retrying operation",
				zap.Int("attempt", attempt),
				zap.Int("max_retries", c.config.MaxRetries),
			)
		}

		err := fn()
		if err == nil {
			return nil
		}

		lastErr = err

		// Check if error is retryable
		if !c.isRetryable(err) {
			return err
		}
	}

	return fmt.Errorf("operation failed after %d retries: %w", c.config.MaxRetries, lastErr)
}

// isRetryable determines if an error is retryable
func (c *Client) isRetryable(err error) bool {
	// In production, would check for specific error types:
	// - Network errors
	// - Timeout errors
	// - Temporary failures
	// - 5xx HTTP errors

	// For now, assume most errors are retryable
	return true
}

// Options

// BucketOption is a function that configures a bucket request
type BucketOption func(*CreateBucketRequest)

// WithStorageClass sets the storage class for a bucket
func WithStorageClass(class StorageClass) BucketOption {
	return func(req *CreateBucketRequest) {
		req.StorageClass = class
	}
}

// WithQuota sets the quota for a bucket
func WithQuota(bytes int64) BucketOption {
	return func(req *CreateBucketRequest) {
		req.QuotaBytes = bytes
	}
}

// WithBucketLabels sets labels for a bucket
func WithBucketLabels(labels map[string]string) BucketOption {
	return func(req *CreateBucketRequest) {
		req.Labels = labels
	}
}

// WithOwner sets the owner for a bucket
func WithOwner(owner string) BucketOption {
	return func(req *CreateBucketRequest) {
		req.Owner = owner
	}
}

// ObjectOption is a function that configures an object request
type ObjectOption interface {
	apply(*PutObjectRequest)
	applyStream(*PutObjectStreamRequest)
	applyMultipart(*InitiateMultipartUploadRequest)
}

type objectOption struct {
	applyFn          func(*PutObjectRequest)
	applyStreamFn    func(*PutObjectStreamRequest)
	applyMultipartFn func(*InitiateMultipartUploadRequest)
}

func (o objectOption) apply(req *PutObjectRequest) {
	if o.applyFn != nil {
		o.applyFn(req)
	}
}

func (o objectOption) applyStream(req *PutObjectStreamRequest) {
	if o.applyStreamFn != nil {
		o.applyStreamFn(req)
	}
}

func (o objectOption) applyMultipart(req *InitiateMultipartUploadRequest) {
	if o.applyMultipartFn != nil {
		o.applyMultipartFn(req)
	}
}

// WithObjectStorageClass sets the storage class for an object
func WithObjectStorageClass(class StorageClass) ObjectOption {
	return objectOption{
		applyFn: func(req *PutObjectRequest) {
			req.StorageClass = class
		},
		applyStreamFn: func(req *PutObjectStreamRequest) {
			req.StorageClass = class
		},
		applyMultipartFn: func(req *InitiateMultipartUploadRequest) {
			req.StorageClass = class
		},
	}
}

// WithContentType sets the content type for an object
func WithContentType(contentType string) ObjectOption {
	return objectOption{
		applyFn: func(req *PutObjectRequest) {
			req.ContentType = contentType
		},
		applyStreamFn: func(req *PutObjectStreamRequest) {
			req.ContentType = contentType
		},
		applyMultipartFn: func(req *InitiateMultipartUploadRequest) {
			req.ContentType = contentType
		},
	}
}

// WithMetadata sets metadata for an object
func WithMetadata(metadata map[string]string) ObjectOption {
	return objectOption{
		applyFn: func(req *PutObjectRequest) {
			req.Metadata = metadata
		},
		applyStreamFn: func(req *PutObjectStreamRequest) {
			req.Metadata = metadata
		},
		applyMultipartFn: func(req *InitiateMultipartUploadRequest) {
			req.Metadata = metadata
		},
	}
}

// ListOption is a function that configures a list request
type ListOption func(*ListObjectsRequest)

// WithPrefix sets the prefix filter for listing
func WithPrefix(prefix string) ListOption {
	return func(req *ListObjectsRequest) {
		req.Prefix = prefix
	}
}

// WithMaxKeys sets the maximum number of keys to return
func WithMaxKeys(maxKeys int) ListOption {
	return func(req *ListObjectsRequest) {
		req.MaxKeys = maxKeys
	}
}

// Convenience Methods

// UploadFile uploads a file-like data with automatic chunking
func (c *Client) UploadFile(ctx context.Context, bucket, key string, data []byte, opts ...ObjectOption) error {
	// For small files, use simple put
	if len(data) < 5*1024*1024 { // 5MB
		return c.PutObject(ctx, bucket, key, data, opts...)
	}

	// For large files, use multipart upload
	uploadID, err := c.InitiateMultipartUpload(ctx, bucket, key, opts...)
	if err != nil {
		return fmt.Errorf("failed to initiate multipart upload: %w", err)
	}

	// Split into 5MB parts
	partSize := 5 * 1024 * 1024
	partNumber := 1

	for offset := 0; offset < len(data); offset += partSize {
		end := offset + partSize
		if end > len(data) {
			end = len(data)
		}

		partData := data[offset:end]

		if _, err := c.UploadPart(ctx, uploadID, partNumber, partData); err != nil {
			// Abort upload on error
			c.AbortMultipartUpload(ctx, uploadID)
			return fmt.Errorf("failed to upload part %d: %w", partNumber, err)
		}

		partNumber++
	}

	// Complete upload
	if err := c.CompleteMultipartUpload(ctx, uploadID); err != nil {
		return fmt.Errorf("failed to complete multipart upload: %w", err)
	}

	return nil
}

// DownloadFile downloads an object to a buffer
func (c *Client) DownloadFile(ctx context.Context, bucket, key string) ([]byte, error) {
	var buf bytes.Buffer

	if err := c.GetObjectStream(ctx, bucket, key, &buf); err != nil {
		// Fallback to simple get
		return c.GetObject(ctx, bucket, key)
	}

	return buf.Bytes(), nil
}

// CopyObject copies an object within or between buckets
func (c *Client) CopyObject(ctx context.Context, sourceBucket, sourceKey, destBucket, destKey string, opts ...ObjectOption) error {
	// Download source object
	data, err := c.GetObject(ctx, sourceBucket, sourceKey)
	if err != nil {
		return fmt.Errorf("failed to get source object: %w", err)
	}

	// Upload to destination
	if err := c.PutObject(ctx, destBucket, destKey, data, opts...); err != nil {
		return fmt.Errorf("failed to put destination object: %w", err)
	}

	return nil
}
