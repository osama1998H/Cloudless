package storage

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"sync"
	"time"

	"go.uber.org/zap"
)

// ObjectStore is the main interface for object storage operations
type ObjectStore struct {
	config             StorageConfig
	logger             *zap.Logger
	metadataStore      *MetadataStore
	chunkStore         *ChunkStore
	contentEngine      *ContentEngine
	replicationManager *ReplicationManager
	repairEngine       *RepairEngine
	placementStrategy  *PlacementStrategy

	multipartUploads sync.Map // uploadID -> *MultipartUpload
	mu               sync.RWMutex
}

// NewObjectStore creates a new object store
func NewObjectStore(config StorageConfig, logger *zap.Logger) (*ObjectStore, error) {
	// Create chunk store
	chunkStore, err := NewChunkStore(config, logger)
	if err != nil {
		return nil, fmt.Errorf("failed to create chunk store: %w", err)
	}

	// Create content engine
	contentEngine := NewContentEngine(chunkStore, config, logger)

	// Create metadata store
	metadataStore := NewMetadataStore(logger)

	// Create replication manager
	replicationManager := NewReplicationManager(config, chunkStore, logger)

	// Create repair engine
	repairEngine := NewRepairEngine(config, replicationManager, chunkStore, logger)

	// Create placement strategy
	placementStrategy := NewPlacementStrategy(config, logger)

	store := &ObjectStore{
		config:             config,
		logger:             logger,
		metadataStore:      metadataStore,
		chunkStore:         chunkStore,
		contentEngine:      contentEngine,
		replicationManager: replicationManager,
		repairEngine:       repairEngine,
		placementStrategy:  placementStrategy,
	}

	return store, nil
}

// Start starts the object store and its components
func (os *ObjectStore) Start() error {
	os.logger.Info("Starting object store")

	// Start repair engine
	if err := os.repairEngine.Start(); err != nil {
		return fmt.Errorf("failed to start repair engine: %w", err)
	}

	os.logger.Info("Object store started successfully")
	return nil
}

// Stop gracefully stops the object store
func (os *ObjectStore) Stop() error {
	os.logger.Info("Stopping object store")

	// Stop repair engine
	if err := os.repairEngine.Stop(); err != nil {
		os.logger.Error("Failed to stop repair engine", zap.Error(err))
	}

	// Run garbage collection
	if _, err := os.chunkStore.GarbageCollect(); err != nil {
		os.logger.Error("Failed to run garbage collection", zap.Error(err))
	}

	os.logger.Info("Object store stopped")
	return nil
}

// Bucket Operations

// CreateBucket creates a new bucket
func (os *ObjectStore) CreateBucket(ctx context.Context, req *CreateBucketRequest) error {
	os.logger.Info("Creating bucket",
		zap.String("bucket", req.Name),
		zap.String("storage_class", string(req.StorageClass)),
	)

	// Validate request
	if req.Name == "" {
		return fmt.Errorf("bucket name cannot be empty")
	}

	// Create bucket
	bucket := &Bucket{
		Name:         req.Name,
		StorageClass: req.StorageClass,
		QuotaBytes:   req.QuotaBytes,
		Labels:       req.Labels,
		Owner:        req.Owner,
	}

	if err := os.metadataStore.CreateBucket(bucket); err != nil {
		return fmt.Errorf("failed to create bucket: %w", err)
	}

	os.logger.Info("Bucket created successfully",
		zap.String("bucket", req.Name),
	)

	return nil
}

// GetBucket retrieves bucket information
func (os *ObjectStore) GetBucket(ctx context.Context, name string) (*Bucket, error) {
	return os.metadataStore.GetBucket(name)
}

// DeleteBucket deletes a bucket (must be empty)
func (os *ObjectStore) DeleteBucket(ctx context.Context, name string) error {
	os.logger.Info("Deleting bucket",
		zap.String("bucket", name),
	)

	if err := os.metadataStore.DeleteBucket(name); err != nil {
		return fmt.Errorf("failed to delete bucket: %w", err)
	}

	os.logger.Info("Bucket deleted successfully",
		zap.String("bucket", name),
	)

	return nil
}

// ListBuckets lists all buckets
func (os *ObjectStore) ListBuckets(ctx context.Context) ([]*BucketMetadata, error) {
	return os.metadataStore.ListBuckets()
}

// Object Operations

// PutObject stores an object
func (os *ObjectStore) PutObject(ctx context.Context, req *PutObjectRequest) error {
	os.logger.Info("Putting object",
		zap.String("bucket", req.Bucket),
		zap.String("key", req.Key),
		zap.Int("size", len(req.Data)),
	)

	// Verify bucket exists
	bucket, err := os.metadataStore.GetBucket(req.Bucket)
	if err != nil {
		return fmt.Errorf("bucket not found: %w", err)
	}

	// Check quota
	if bucket.QuotaBytes > 0 && bucket.UsedBytes+int64(len(req.Data)) > bucket.QuotaBytes {
		return fmt.Errorf("bucket quota exceeded")
	}

	// Store content
	chunkIDs, checksum, err := os.contentEngine.StoreContent(req.Data)
	if err != nil {
		return fmt.Errorf("failed to store content: %w", err)
	}

	// Replicate chunks
	if err := os.replicateChunks(ctx, chunkIDs, req.StorageClass); err != nil {
		// Cleanup chunks on replication failure
		os.contentEngine.DeleteContent(chunkIDs)
		return fmt.Errorf("failed to replicate chunks: %w", err)
	}

	// Create object metadata
	object := &Object{
		Bucket:       req.Bucket,
		Key:          req.Key,
		Size:         int64(len(req.Data)),
		Checksum:     checksum,
		ChunkIDs:     chunkIDs,
		StorageClass: req.StorageClass,
		ContentType:  req.ContentType,
		Metadata:     req.Metadata,
	}

	// Store metadata
	if err := os.metadataStore.PutObject(object); err != nil {
		// Cleanup on metadata failure
		os.contentEngine.DeleteContent(chunkIDs)
		return fmt.Errorf("failed to store metadata: %w", err)
	}

	os.logger.Info("Object stored successfully",
		zap.String("bucket", req.Bucket),
		zap.String("key", req.Key),
		zap.String("checksum", checksum),
		zap.Int("chunk_count", len(chunkIDs)),
	)

	return nil
}

// GetObject retrieves an object
func (os *ObjectStore) GetObject(ctx context.Context, bucket, key string) (*GetObjectResponse, error) {
	os.logger.Debug("Getting object",
		zap.String("bucket", bucket),
		zap.String("key", key),
	)

	// Get metadata
	object, err := os.metadataStore.GetObject(bucket, key)
	if err != nil {
		return nil, fmt.Errorf("object not found: %w", err)
	}

	// Retrieve content
	data, err := os.contentEngine.RetrieveContent(object.ChunkIDs)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve content: %w", err)
	}

	// Verify checksum
	if err := os.contentEngine.VerifyContent(object.ChunkIDs, object.Checksum); err != nil {
		os.logger.Error("Checksum verification failed",
			zap.String("bucket", bucket),
			zap.String("key", key),
			zap.Error(err),
		)
		return nil, fmt.Errorf("data integrity check failed: %w", err)
	}

	// Perform read repair if enabled
	if os.config.EnableReadRepair {
		go func() {
			for _, chunkID := range object.ChunkIDs {
				if err := os.replicationManager.ReadRepair(context.Background(), chunkID); err != nil {
					os.logger.Debug("Read repair failed",
						zap.String("chunk_id", chunkID),
						zap.Error(err),
					)
				}
			}
		}()
	}

	response := &GetObjectResponse{
		Data:         data,
		Size:         object.Size,
		Checksum:     object.Checksum,
		StorageClass: object.StorageClass,
		ContentType:  object.ContentType,
		Metadata:     object.Metadata,
		CreatedAt:    object.CreatedAt,
		UpdatedAt:    object.UpdatedAt,
	}

	return response, nil
}

// DeleteObject deletes an object
func (os *ObjectStore) DeleteObject(ctx context.Context, bucket, key string) error {
	os.logger.Info("Deleting object",
		zap.String("bucket", bucket),
		zap.String("key", key),
	)

	// Get object metadata
	object, err := os.metadataStore.GetObject(bucket, key)
	if err != nil {
		return fmt.Errorf("object not found: %w", err)
	}

	// Delete metadata first
	if err := os.metadataStore.DeleteObject(bucket, key); err != nil {
		return fmt.Errorf("failed to delete metadata: %w", err)
	}

	// Delete chunks (async)
	go func() {
		if err := os.contentEngine.DeleteContent(object.ChunkIDs); err != nil {
			os.logger.Error("Failed to delete chunks",
				zap.String("bucket", bucket),
				zap.String("key", key),
				zap.Error(err),
			)
		}
	}()

	os.logger.Info("Object deleted successfully",
		zap.String("bucket", bucket),
		zap.String("key", key),
	)

	return nil
}

// ListObjects lists objects in a bucket
func (os *ObjectStore) ListObjects(ctx context.Context, req *ListObjectsRequest) ([]*ObjectMetadata, error) {
	return os.metadataStore.ListObjects(req.Bucket, req.Prefix, req.MaxKeys)
}

// Streaming Operations

// PutObjectStream stores an object from a stream
func (os *ObjectStore) PutObjectStream(ctx context.Context, req *PutObjectStreamRequest) error {
	os.logger.Info("Putting object from stream",
		zap.String("bucket", req.Bucket),
		zap.String("key", req.Key),
	)

	// Verify bucket exists
	bucket, err := os.metadataStore.GetBucket(req.Bucket)
	if err != nil {
		return fmt.Errorf("bucket not found: %w", err)
	}

	// Store content from stream
	chunkIDs, checksum, err := os.contentEngine.StoreContentStream(req.Reader)
	if err != nil {
		return fmt.Errorf("failed to store content: %w", err)
	}

	// Get total size
	size, err := os.contentEngine.GetContentSize(chunkIDs)
	if err != nil {
		os.contentEngine.DeleteContent(chunkIDs)
		return fmt.Errorf("failed to get content size: %w", err)
	}

	// Check quota
	if bucket.QuotaBytes > 0 && bucket.UsedBytes+size > bucket.QuotaBytes {
		os.contentEngine.DeleteContent(chunkIDs)
		return fmt.Errorf("bucket quota exceeded")
	}

	// Replicate chunks
	if err := os.replicateChunks(ctx, chunkIDs, req.StorageClass); err != nil {
		os.contentEngine.DeleteContent(chunkIDs)
		return fmt.Errorf("failed to replicate chunks: %w", err)
	}

	// Create object metadata
	object := &Object{
		Bucket:       req.Bucket,
		Key:          req.Key,
		Size:         size,
		Checksum:     checksum,
		ChunkIDs:     chunkIDs,
		StorageClass: req.StorageClass,
		ContentType:  req.ContentType,
		Metadata:     req.Metadata,
	}

	// Store metadata
	if err := os.metadataStore.PutObject(object); err != nil {
		os.contentEngine.DeleteContent(chunkIDs)
		return fmt.Errorf("failed to store metadata: %w", err)
	}

	os.logger.Info("Object stored successfully from stream",
		zap.String("bucket", req.Bucket),
		zap.String("key", req.Key),
		zap.Int64("size", size),
		zap.String("checksum", checksum),
	)

	return nil
}

// GetObjectStream retrieves an object as a stream
func (os *ObjectStore) GetObjectStream(ctx context.Context, bucket, key string, writer io.Writer) error {
	os.logger.Debug("Getting object stream",
		zap.String("bucket", bucket),
		zap.String("key", key),
	)

	// Get metadata
	object, err := os.metadataStore.GetObject(bucket, key)
	if err != nil {
		return fmt.Errorf("object not found: %w", err)
	}

	// Stream content
	if err := os.contentEngine.RetrieveContentStream(object.ChunkIDs, writer); err != nil {
		return fmt.Errorf("failed to retrieve content: %w", err)
	}

	// Perform read repair if enabled
	if os.config.EnableReadRepair {
		go func() {
			for _, chunkID := range object.ChunkIDs {
				if err := os.replicationManager.ReadRepair(context.Background(), chunkID); err != nil {
					os.logger.Debug("Read repair failed",
						zap.String("chunk_id", chunkID),
						zap.Error(err),
					)
				}
			}
		}()
	}

	return nil
}

// Multipart Upload Operations

// InitiateMultipartUpload starts a multipart upload
func (os *ObjectStore) InitiateMultipartUpload(ctx context.Context, req *InitiateMultipartUploadRequest) (*InitiateMultipartUploadResponse, error) {
	os.logger.Info("Initiating multipart upload",
		zap.String("bucket", req.Bucket),
		zap.String("key", req.Key),
	)

	// Verify bucket exists
	if _, err := os.metadataStore.GetBucket(req.Bucket); err != nil {
		return nil, fmt.Errorf("bucket not found: %w", err)
	}

	// Generate upload ID
	uploadID := fmt.Sprintf("upload-%d-%s", time.Now().UnixNano(), req.Key)

	// Create multipart upload
	upload := &MultipartUpload{
		UploadID:     uploadID,
		Bucket:       req.Bucket,
		Key:          req.Key,
		StorageClass: req.StorageClass,
		ContentType:  req.ContentType,
		Metadata:     req.Metadata,
		Parts:        make(map[int]*UploadPart),
		CreatedAt:    time.Now(),
	}

	os.multipartUploads.Store(uploadID, upload)

	os.logger.Info("Multipart upload initiated",
		zap.String("upload_id", uploadID),
		zap.String("bucket", req.Bucket),
		zap.String("key", req.Key),
	)

	return &InitiateMultipartUploadResponse{
		UploadID: uploadID,
		Bucket:   req.Bucket,
		Key:      req.Key,
	}, nil
}

// UploadPart uploads a part of a multipart upload
func (os *ObjectStore) UploadPart(ctx context.Context, req *UploadPartRequest) (*UploadPartResponse, error) {
	os.logger.Debug("Uploading part",
		zap.String("upload_id", req.UploadID),
		zap.Int("part_number", req.PartNumber),
		zap.Int("size", len(req.Data)),
	)

	// Get multipart upload
	u, ok := os.multipartUploads.Load(req.UploadID)
	if !ok {
		return nil, fmt.Errorf("upload not found: %s", req.UploadID)
	}
	upload := u.(*MultipartUpload)

	upload.mu.Lock()
	defer upload.mu.Unlock()

	// Store part content
	chunkIDs, checksum, err := os.contentEngine.StoreContent(req.Data)
	if err != nil {
		return nil, fmt.Errorf("failed to store part: %w", err)
	}

	// Replicate chunks (use storage class from upload)
	if err := os.replicateChunks(ctx, chunkIDs, upload.StorageClass); err != nil {
		os.contentEngine.DeleteContent(chunkIDs)
		return nil, fmt.Errorf("failed to replicate chunks: %w", err)
	}

	// Create part
	part := &UploadPart{
		PartNumber: req.PartNumber,
		Size:       int64(len(req.Data)),
		Checksum:   checksum,
		ChunkIDs:   chunkIDs,
		UploadedAt: time.Now(),
	}

	upload.Parts[req.PartNumber] = part
	upload.TotalSize += part.Size

	os.logger.Debug("Part uploaded successfully",
		zap.String("upload_id", req.UploadID),
		zap.Int("part_number", req.PartNumber),
		zap.String("checksum", checksum),
	)

	return &UploadPartResponse{
		PartNumber: req.PartNumber,
		Checksum:   checksum,
	}, nil
}

// CompleteMultipartUpload completes a multipart upload
func (os *ObjectStore) CompleteMultipartUpload(ctx context.Context, req *CompleteMultipartUploadRequest) error {
	os.logger.Info("Completing multipart upload",
		zap.String("upload_id", req.UploadID),
	)

	// Get multipart upload
	u, ok := os.multipartUploads.Load(req.UploadID)
	if !ok {
		return fmt.Errorf("upload not found: %s", req.UploadID)
	}
	upload := u.(*MultipartUpload)

	upload.mu.Lock()
	defer upload.mu.Unlock()

	// Verify bucket quota
	bucket, err := os.metadataStore.GetBucket(upload.Bucket)
	if err != nil {
		return fmt.Errorf("bucket not found: %w", err)
	}

	if bucket.QuotaBytes > 0 && bucket.UsedBytes+upload.TotalSize > bucket.QuotaBytes {
		return fmt.Errorf("bucket quota exceeded")
	}

	// Assemble all parts
	var allChunkIDs []string
	var buffer bytes.Buffer

	for i := 1; i <= len(upload.Parts); i++ {
		part, ok := upload.Parts[i]
		if !ok {
			return fmt.Errorf("missing part %d", i)
		}

		allChunkIDs = append(allChunkIDs, part.ChunkIDs...)

		// Retrieve part data for checksum calculation
		data, err := os.contentEngine.RetrieveContent(part.ChunkIDs)
		if err != nil {
			return fmt.Errorf("failed to retrieve part %d: %w", i, err)
		}
		buffer.Write(data)
	}

	// Calculate overall checksum
	overallChecksum := os.contentEngine.calculateChecksum(buffer.Bytes())

	// Create object metadata
	object := &Object{
		Bucket:       upload.Bucket,
		Key:          upload.Key,
		Size:         upload.TotalSize,
		Checksum:     overallChecksum,
		ChunkIDs:     allChunkIDs,
		StorageClass: upload.StorageClass,
		ContentType:  upload.ContentType,
		Metadata:     upload.Metadata,
	}

	// Store metadata
	if err := os.metadataStore.PutObject(object); err != nil {
		return fmt.Errorf("failed to store metadata: %w", err)
	}

	// Remove multipart upload
	os.multipartUploads.Delete(req.UploadID)

	os.logger.Info("Multipart upload completed",
		zap.String("upload_id", req.UploadID),
		zap.String("bucket", upload.Bucket),
		zap.String("key", upload.Key),
		zap.Int64("size", upload.TotalSize),
		zap.Int("parts", len(upload.Parts)),
	)

	return nil
}

// AbortMultipartUpload aborts a multipart upload
func (os *ObjectStore) AbortMultipartUpload(ctx context.Context, uploadID string) error {
	os.logger.Info("Aborting multipart upload",
		zap.String("upload_id", uploadID),
	)

	// Get multipart upload
	u, ok := os.multipartUploads.Load(uploadID)
	if !ok {
		return fmt.Errorf("upload not found: %s", uploadID)
	}
	upload := u.(*MultipartUpload)

	upload.mu.Lock()
	defer upload.mu.Unlock()

	// Delete all uploaded parts
	for _, part := range upload.Parts {
		go os.contentEngine.DeleteContent(part.ChunkIDs)
	}

	// Remove multipart upload
	os.multipartUploads.Delete(uploadID)

	os.logger.Info("Multipart upload aborted",
		zap.String("upload_id", uploadID),
	)

	return nil
}

// Helper Methods

// replicateChunks replicates chunks to R nodes
func (os *ObjectStore) replicateChunks(ctx context.Context, chunkIDs []string, storageClass StorageClass) error {
	// Select target nodes using placement strategy
	targetNodes := os.getTargetNodes(storageClass)

	for _, chunkID := range chunkIDs {
		if err := os.replicationManager.ReplicateChunk(ctx, chunkID, targetNodes); err != nil {
			return fmt.Errorf("failed to replicate chunk %s: %w", chunkID, err)
		}
	}

	return nil
}

// getTargetNodes returns target nodes for replication using placement strategy
func (os *ObjectStore) getTargetNodes(storageClass StorageClass) []string {
	// Determine IOPS class based on storage class
	iopsClass := IOPSClassMedium // Default
	if storageClass == StorageClassHot {
		iopsClass = IOPSClassHigh
	} else if storageClass == StorageClassCold {
		iopsClass = IOPSClassLow
	}

	// Create placement request
	req := &PlacementRequest{
		StorageClass:  storageClass,
		IOPSClass:     iopsClass,
		ReplicaCount:  int(os.config.ReplicationFactor),
		Size:          0, // Size check done at bucket level
		PreferredZone: "", // No zone preference for object storage
		ExcludeNodes:  []string{},
	}

	// Use placement strategy to select nodes
	nodes, err := os.placementStrategy.SelectNodes(req)
	if err != nil {
		os.logger.Warn("Failed to select nodes using placement strategy, using fallback",
			zap.String("storage_class", string(storageClass)),
			zap.String("iops_class", string(iopsClass)),
			zap.Error(err),
		)

		// Fallback to placeholder nodes when placement strategy fails
		fallbackNodes := []string{"node-1", "node-2", "node-3"}
		if int(os.config.ReplicationFactor) <= len(fallbackNodes) {
			return fallbackNodes[:os.config.ReplicationFactor]
		}
		return fallbackNodes
	}

	os.logger.Debug("Selected nodes for replication",
		zap.Strings("nodes", nodes),
		zap.String("storage_class", string(storageClass)),
		zap.String("iops_class", string(iopsClass)),
	)

	return nodes
}

// GetStats returns storage statistics
func (os *ObjectStore) GetStats() *StorageStats {
	stats := os.metadataStore.GetStats()
	return &stats
}

// GetReplicationStats returns replication statistics
func (os *ObjectStore) GetReplicationStats() ReplicationStats {
	return os.replicationManager.GetReplicationStats()
}

// GetRepairMetrics returns repair metrics
func (os *ObjectStore) GetRepairMetrics() RepairMetrics {
	return os.repairEngine.GetRepairMetrics()
}

// Request and Response Types

// CreateBucketRequest contains parameters for creating a bucket
type CreateBucketRequest struct {
	Name         string
	StorageClass StorageClass
	QuotaBytes   int64
	Labels       map[string]string
	Owner        string
}

// PutObjectRequest contains parameters for storing an object
type PutObjectRequest struct {
	Bucket       string
	Key          string
	Data         []byte
	StorageClass StorageClass
	ContentType  string
	Metadata     map[string]string
}

// GetObjectResponse contains object data and metadata
type GetObjectResponse struct {
	Data         []byte
	Size         int64
	Checksum     string
	StorageClass StorageClass
	ContentType  string
	Metadata     map[string]string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// ListObjectsRequest contains parameters for listing objects
type ListObjectsRequest struct {
	Bucket  string
	Prefix  string
	MaxKeys int
}

// PutObjectStreamRequest contains parameters for streaming upload
type PutObjectStreamRequest struct {
	Bucket       string
	Key          string
	Reader       io.Reader
	StorageClass StorageClass
	ContentType  string
	Metadata     map[string]string
}

// InitiateMultipartUploadRequest contains parameters for initiating multipart upload
type InitiateMultipartUploadRequest struct {
	Bucket       string
	Key          string
	StorageClass StorageClass
	ContentType  string
	Metadata     map[string]string
}

// InitiateMultipartUploadResponse contains upload ID
type InitiateMultipartUploadResponse struct {
	UploadID string
	Bucket   string
	Key      string
}

// UploadPartRequest contains parameters for uploading a part
type UploadPartRequest struct {
	UploadID   string
	PartNumber int
	Data       []byte
}

// UploadPartResponse contains part information
type UploadPartResponse struct {
	PartNumber int
	Checksum   string
}

// CompleteMultipartUploadRequest contains parameters for completing upload
type CompleteMultipartUploadRequest struct {
	UploadID string
}

// MultipartUpload represents an in-progress multipart upload
type MultipartUpload struct {
	UploadID     string
	Bucket       string
	Key          string
	StorageClass StorageClass
	ContentType  string
	Metadata     map[string]string
	Parts        map[int]*UploadPart
	TotalSize    int64
	CreatedAt    time.Time

	mu sync.Mutex
}

// UploadPart represents a single part of a multipart upload
type UploadPart struct {
	PartNumber int
	Size       int64
	Checksum   string
	ChunkIDs   []string
	UploadedAt time.Time
}
