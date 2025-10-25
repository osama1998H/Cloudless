package storage

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"go.uber.org/zap"
)

// MetadataStore manages bucket and object metadata with strong consistency
type MetadataStore struct {
	logger *zap.Logger

	buckets sync.Map // bucketName -> *Bucket
	objects sync.Map // bucketName:objectKey -> *Object
	indices sync.Map // Various indices for fast lookups

	mu sync.RWMutex
}

// NewMetadataStore creates a new metadata store
func NewMetadataStore(logger *zap.Logger) *MetadataStore {
	return &MetadataStore{
		logger: logger,
	}
}

// Bucket Operations

// CreateBucket creates a new bucket
func (ms *MetadataStore) CreateBucket(bucket *Bucket) error {
	ms.mu.Lock()
	defer ms.mu.Unlock()

	// Check if bucket already exists
	if _, exists := ms.buckets.Load(bucket.Name); exists {
		return fmt.Errorf("bucket already exists: %s", bucket.Name)
	}

	// Set timestamps
	now := time.Now()
	bucket.CreatedAt = now
	bucket.UpdatedAt = now

	// Initialize fields
	if bucket.Labels == nil {
		bucket.Labels = make(map[string]string)
	}

	// Store bucket
	ms.buckets.Store(bucket.Name, bucket)

	ms.logger.Info("Created bucket",
		zap.String("bucket", bucket.Name),
		zap.String("storage_class", string(bucket.StorageClass)),
	)

	return nil
}

// GetBucket retrieves a bucket by name
func (ms *MetadataStore) GetBucket(name string) (*Bucket, error) {
	if b, ok := ms.buckets.Load(name); ok {
		bucket := b.(*Bucket)
		return bucket, nil
	}

	return nil, fmt.Errorf("bucket not found: %s", name)
}

// UpdateBucket updates bucket metadata
func (ms *MetadataStore) UpdateBucket(bucket *Bucket) error {
	ms.mu.Lock()
	defer ms.mu.Unlock()

	// Check if bucket exists
	if _, exists := ms.buckets.Load(bucket.Name); !exists {
		return fmt.Errorf("bucket not found: %s", bucket.Name)
	}

	// Update timestamp
	bucket.UpdatedAt = time.Now()

	// Store updated bucket
	ms.buckets.Store(bucket.Name, bucket)

	ms.logger.Debug("Updated bucket",
		zap.String("bucket", bucket.Name),
	)

	return nil
}

// DeleteBucket deletes a bucket (must be empty)
func (ms *MetadataStore) DeleteBucket(name string) error {
	ms.mu.Lock()
	defer ms.mu.Unlock()

	// Check if bucket exists
	if _, exists := ms.buckets.Load(name); !exists {
		return fmt.Errorf("bucket not found: %s", name)
	}

	// Check if bucket is empty
	objectCount := 0
	ms.objects.Range(func(key, value interface{}) bool {
		obj := value.(*Object)
		if obj.Bucket == name {
			objectCount++
			return false // Stop iteration
		}
		return true
	})

	if objectCount > 0 {
		return fmt.Errorf("bucket not empty: %s (contains %d objects)", name, objectCount)
	}

	// Delete bucket
	ms.buckets.Delete(name)

	ms.logger.Info("Deleted bucket",
		zap.String("bucket", name),
	)

	return nil
}

// ListBuckets returns all buckets
func (ms *MetadataStore) ListBuckets() ([]*BucketMetadata, error) {
	var buckets []*BucketMetadata

	ms.buckets.Range(func(key, value interface{}) bool {
		bucket := value.(*Bucket)

		// Count objects in this bucket
		objectCount := 0
		ms.objects.Range(func(k, v interface{}) bool {
			obj := v.(*Object)
			if obj.Bucket == bucket.Name {
				objectCount++
			}
			return true
		})

		metadata := &BucketMetadata{
			Name:         bucket.Name,
			StorageClass: bucket.StorageClass,
			UsedBytes:    bucket.UsedBytes,
			ObjectCount:  objectCount,
			CreatedAt:    bucket.CreatedAt,
		}

		buckets = append(buckets, metadata)
		return true
	})

	return buckets, nil
}

// Object Operations

// PutObject stores object metadata
func (ms *MetadataStore) PutObject(object *Object) error {
	ms.mu.Lock()
	defer ms.mu.Unlock()

	// Verify bucket exists
	bucket, err := ms.GetBucket(object.Bucket)
	if err != nil {
		return err
	}

	// Set timestamps
	now := time.Now()
	objectKey := ms.getObjectKey(object.Bucket, object.Key)

	// Check if object exists (update vs create)
	var oldSize int64
	if existing, ok := ms.objects.Load(objectKey); ok {
		oldObj := existing.(*Object)
		oldSize = oldObj.Size
		object.CreatedAt = oldObj.CreatedAt
	} else {
		object.CreatedAt = now
	}

	object.UpdatedAt = now
	object.AccessedAt = now

	// Initialize fields
	if object.Metadata == nil {
		object.Metadata = make(map[string]string)
	}

	// Update bucket usage
	bucket.UsedBytes = bucket.UsedBytes - oldSize + object.Size
	ms.buckets.Store(bucket.Name, bucket)

	// Check quota
	if bucket.QuotaBytes > 0 && bucket.UsedBytes > bucket.QuotaBytes {
		return fmt.Errorf("bucket quota exceeded: %s (quota: %d, used: %d)",
			bucket.Name, bucket.QuotaBytes, bucket.UsedBytes)
	}

	// Increment version
	if oldSize > 0 {
		object.Version++
	}

	// Store object
	ms.objects.Store(objectKey, object)

	ms.logger.Debug("Put object",
		zap.String("bucket", object.Bucket),
		zap.String("key", object.Key),
		zap.Int64("size", object.Size),
		zap.Uint64("version", object.Version),
	)

	return nil
}

// GetObject retrieves object metadata
func (ms *MetadataStore) GetObject(bucket, key string) (*Object, error) {
	objectKey := ms.getObjectKey(bucket, key)

	if o, ok := ms.objects.Load(objectKey); ok {
		object := o.(*Object)

		// Update access time
		object.AccessedAt = time.Now()
		ms.objects.Store(objectKey, object)

		return object, nil
	}

	return nil, fmt.Errorf("object not found: %s/%s", bucket, key)
}

// DeleteObject deletes object metadata
func (ms *MetadataStore) DeleteObject(bucket, key string) error {
	ms.mu.Lock()
	defer ms.mu.Unlock()

	objectKey := ms.getObjectKey(bucket, key)

	// Get object
	o, ok := ms.objects.Load(objectKey)
	if !ok {
		return fmt.Errorf("object not found: %s/%s", bucket, key)
	}

	object := o.(*Object)

	// Update bucket usage
	if b, ok := ms.buckets.Load(bucket); ok {
		bkt := b.(*Bucket)
		bkt.UsedBytes -= object.Size
		ms.buckets.Store(bucket, bkt)
	}

	// Delete object
	ms.objects.Delete(objectKey)

	ms.logger.Info("Deleted object",
		zap.String("bucket", bucket),
		zap.String("key", key),
	)

	return nil
}

// ListObjects lists objects in a bucket with pagination
func (ms *MetadataStore) ListObjects(bucket, prefix string, maxKeys int) ([]*ObjectMetadata, error) {
	// Verify bucket exists
	if _, err := ms.GetBucket(bucket); err != nil {
		return nil, err
	}

	var objects []*ObjectMetadata
	count := 0

	ms.objects.Range(func(key, value interface{}) bool {
		obj := value.(*Object)

		// Filter by bucket and prefix
		if obj.Bucket != bucket {
			return true
		}

		if prefix != "" && len(obj.Key) < len(prefix) {
			return true
		}

		if prefix != "" && obj.Key[:len(prefix)] != prefix {
			return true
		}

		// Create metadata
		metadata := &ObjectMetadata{
			Key:          obj.Key,
			Size:         obj.Size,
			Checksum:     obj.Checksum,
			StorageClass: obj.StorageClass,
			CreatedAt:    obj.CreatedAt,
			UpdatedAt:    obj.UpdatedAt,
		}

		objects = append(objects, metadata)
		count++

		// Check max keys
		if maxKeys > 0 && count >= maxKeys {
			return false
		}

		return true
	})

	return objects, nil
}

// UpdateObjectAccess updates the last accessed time
func (ms *MetadataStore) UpdateObjectAccess(bucket, key string) error {
	objectKey := ms.getObjectKey(bucket, key)

	if o, ok := ms.objects.Load(objectKey); ok {
		object := o.(*Object)
		object.AccessedAt = time.Now()
		ms.objects.Store(objectKey, object)
		return nil
	}

	return fmt.Errorf("object not found: %s/%s", bucket, key)
}

// GetObjectsByChecksum finds objects with a specific checksum (deduplication)
func (ms *MetadataStore) GetObjectsByChecksum(checksum string) []*Object {
	var objects []*Object

	ms.objects.Range(func(key, value interface{}) bool {
		obj := value.(*Object)
		if obj.Checksum == checksum {
			objects = append(objects, obj)
		}
		return true
	})

	return objects
}

// GetStats returns metadata statistics
func (ms *MetadataStore) GetStats() StorageStats {
	stats := StorageStats{
		LastUpdated: time.Now(),
	}

	// Count buckets
	ms.buckets.Range(func(key, value interface{}) bool {
		stats.BucketCount++
		return true
	})

	// Count objects and bytes
	ms.objects.Range(func(key, value interface{}) bool {
		obj := value.(*Object)
		stats.TotalObjects++
		stats.TotalBytes += obj.Size
		stats.TotalChunks += int64(len(obj.ChunkIDs))
		return true
	})

	return stats
}

// getObjectKey creates a composite key for object storage
func (ms *MetadataStore) getObjectKey(bucket, key string) string {
	return fmt.Sprintf("%s:%s", bucket, key)
}

// Serialization for RAFT (if needed)

// SerializeMetadata serializes metadata for RAFT storage
func (ms *MetadataStore) SerializeMetadata() ([]byte, error) {
	ms.mu.RLock()
	defer ms.mu.RUnlock()

	data := struct {
		Buckets map[string]*Bucket
		Objects map[string]*Object
	}{
		Buckets: make(map[string]*Bucket),
		Objects: make(map[string]*Object),
	}

	// Collect buckets
	ms.buckets.Range(func(key, value interface{}) bool {
		name := key.(string)
		bucket := value.(*Bucket)
		data.Buckets[name] = bucket
		return true
	})

	// Collect objects
	ms.objects.Range(func(key, value interface{}) bool {
		objKey := key.(string)
		object := value.(*Object)
		data.Objects[objKey] = object
		return true
	})

	return json.Marshal(data)
}

// DeserializeMetadata deserializes metadata from RAFT storage
func (ms *MetadataStore) DeserializeMetadata(serialized []byte) error {
	ms.mu.Lock()
	defer ms.mu.Unlock()

	var data struct {
		Buckets map[string]*Bucket
		Objects map[string]*Object
	}

	if err := json.Unmarshal(serialized, &data); err != nil {
		return fmt.Errorf("failed to deserialize metadata: %w", err)
	}

	// Clear existing data
	ms.buckets = sync.Map{}
	ms.objects = sync.Map{}

	// Load buckets
	for name, bucket := range data.Buckets {
		ms.buckets.Store(name, bucket)
	}

	// Load objects
	for key, object := range data.Objects {
		ms.objects.Store(key, object)
	}

	ms.logger.Info("Deserialized metadata",
		zap.Int("buckets", len(data.Buckets)),
		zap.Int("objects", len(data.Objects)),
	)

	return nil
}
