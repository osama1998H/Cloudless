package agent

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/osama1998H/Cloudless/pkg/storage"
	"go.uber.org/zap"
)

// StorageAgent manages storage operations on the agent node
type StorageAgent struct {
	config *Config
	logger *zap.Logger

	// Storage components
	volumeManager *storage.VolumeManager
	chunkStore    *storage.ChunkStore

	nodeID string
}

// NewStorageAgent creates a new storage agent
func NewStorageAgent(config *Config) (*StorageAgent, error) {
	sa := &StorageAgent{
		config: config,
		logger: config.Logger,
		nodeID: config.NodeID,
	}

	// Create storage config
	storageConfig := storage.StorageConfig{
		DataDir:           filepath.Join(config.DataDir, "storage"),
		ReplicationFactor: 3,                     // Default to R=3
		ChunkSize:         4 * 1024 * 1024,       // 4MB chunks
		RepairInterval:    1 * 3600 * 1000000000, // 1 hour in nanoseconds
		EnableCompression: false,
		EnableReadRepair:  true,
		QuorumWrites:      true,
	}

	// Initialize chunk store
	chunkStore, err := storage.NewChunkStore(storageConfig, config.Logger)
	if err != nil {
		return nil, fmt.Errorf("failed to create chunk store: %w", err)
	}
	sa.chunkStore = chunkStore

	// Initialize volume manager
	volumeManager, err := storage.NewVolumeManager(storageConfig, config.NodeID, config.Logger)
	if err != nil {
		return nil, fmt.Errorf("failed to create volume manager: %w", err)
	}
	sa.volumeManager = volumeManager

	config.Logger.Info("Storage agent initialized",
		zap.String("node_id", config.NodeID),
		zap.String("data_dir", storageConfig.DataDir),
	)

	return sa, nil
}

// Start starts the storage agent
func (sa *StorageAgent) Start() error {
	sa.logger.Info("Starting storage agent")

	// Nothing to start for now - components are passive
	// In production, would start:
	// - Chunk serving endpoint
	// - Health monitoring
	// - Metrics collection

	sa.logger.Info("Storage agent started")
	return nil
}

// Stop stops the storage agent
func (sa *StorageAgent) Stop() error {
	sa.logger.Info("Stopping storage agent")

	// Run garbage collection
	if sa.chunkStore != nil {
		if count, err := sa.chunkStore.GarbageCollect(); err != nil {
			sa.logger.Error("Failed to run garbage collection", zap.Error(err))
		} else {
			sa.logger.Info("Garbage collection completed",
				zap.Int("chunks_removed", count),
			)
		}
	}

	sa.logger.Info("Storage agent stopped")
	return nil
}

// Volume Operations

// CreateVolume creates a new ephemeral volume
func (sa *StorageAgent) CreateVolume(ctx context.Context, req *storage.CreateVolumeRequest) (*storage.Volume, error) {
	sa.logger.Info("Creating volume",
		zap.String("volume_id", req.VolumeID),
		zap.String("workload_id", req.WorkloadID),
		zap.Int64("size_bytes", req.SizeBytes),
	)

	volume, err := sa.volumeManager.CreateVolume(req)
	if err != nil {
		return nil, fmt.Errorf("failed to create volume: %w", err)
	}

	sa.logger.Info("Volume created",
		zap.String("volume_id", req.VolumeID),
		zap.String("mount_path", volume.MountPath),
	)

	return volume, nil
}

// MountVolume mounts a volume for use
// workloadID is required for access control validation (CLD-REQ-052)
func (sa *StorageAgent) MountVolume(ctx context.Context, volumeID, workloadID string) (string, error) {
	sa.logger.Info("Mounting volume",
		zap.String("volume_id", volumeID),
		zap.String("workload_id", workloadID),
	)

	mountPath, err := sa.volumeManager.MountVolume(volumeID, workloadID)
	if err != nil {
		return "", fmt.Errorf("failed to mount volume: %w", err)
	}

	sa.logger.Info("Volume mounted",
		zap.String("volume_id", volumeID),
		zap.String("workload_id", workloadID),
		zap.String("mount_path", mountPath),
	)

	return mountPath, nil
}

// UnmountVolume unmounts a volume
// workloadID is required for access control validation (CLD-REQ-052)
func (sa *StorageAgent) UnmountVolume(ctx context.Context, volumeID, workloadID string) error {
	sa.logger.Info("Unmounting volume",
		zap.String("volume_id", volumeID),
		zap.String("workload_id", workloadID),
	)

	if err := sa.volumeManager.UnmountVolume(volumeID, workloadID); err != nil {
		return fmt.Errorf("failed to unmount volume: %w", err)
	}

	sa.logger.Info("Volume unmounted",
		zap.String("volume_id", volumeID),
		zap.String("workload_id", workloadID),
	)

	return nil
}

// DeleteVolume deletes a volume
// workloadID is required for access control validation (CLD-REQ-052)
func (sa *StorageAgent) DeleteVolume(ctx context.Context, volumeID, workloadID string) error {
	sa.logger.Info("Deleting volume",
		zap.String("volume_id", volumeID),
		zap.String("workload_id", workloadID),
	)

	if err := sa.volumeManager.DeleteVolume(volumeID, workloadID); err != nil {
		return fmt.Errorf("failed to delete volume: %w", err)
	}

	sa.logger.Info("Volume deleted",
		zap.String("volume_id", volumeID),
		zap.String("workload_id", workloadID),
	)

	return nil
}

// ListVolumes lists all volumes on this node
func (sa *StorageAgent) ListVolumes(ctx context.Context) ([]*storage.Volume, error) {
	return sa.volumeManager.ListVolumes()
}

// GetVolume retrieves volume information
func (sa *StorageAgent) GetVolume(ctx context.Context, volumeID string) (*storage.Volume, error) {
	return sa.volumeManager.GetVolume(volumeID)
}

// ResizeVolume resizes a volume
// workloadID is required for access control validation (CLD-REQ-052)
func (sa *StorageAgent) ResizeVolume(ctx context.Context, volumeID, workloadID string, newSize int64) error {
	sa.logger.Info("Resizing volume",
		zap.String("volume_id", volumeID),
		zap.String("workload_id", workloadID),
		zap.Int64("new_size", newSize),
	)

	if err := sa.volumeManager.ResizeVolume(volumeID, workloadID, newSize); err != nil {
		return fmt.Errorf("failed to resize volume: %w", err)
	}

	sa.logger.Info("Volume resized",
		zap.String("volume_id", volumeID),
		zap.String("workload_id", workloadID),
		zap.Int64("new_size", newSize),
	)

	return nil
}

// CleanupWorkloadVolumes deletes all volumes for a workload
func (sa *StorageAgent) CleanupWorkloadVolumes(ctx context.Context, workloadID string) error {
	sa.logger.Info("Cleaning up workload volumes",
		zap.String("workload_id", workloadID),
	)

	if err := sa.volumeManager.CleanupWorkloadVolumes(workloadID); err != nil {
		return fmt.Errorf("failed to cleanup workload volumes: %w", err)
	}

	sa.logger.Info("Workload volumes cleaned up",
		zap.String("workload_id", workloadID),
	)

	return nil
}

// Chunk Operations

// WriteChunk writes a chunk to local storage
func (sa *StorageAgent) WriteChunk(ctx context.Context, data []byte) (*storage.Chunk, error) {
	chunk, err := sa.chunkStore.WriteChunk(data)
	if err != nil {
		return nil, fmt.Errorf("failed to write chunk: %w", err)
	}

	sa.logger.Debug("Chunk written",
		zap.String("chunk_id", chunk.ID),
		zap.Int64("size", chunk.Size),
	)

	return chunk, nil
}

// ReadChunk reads a chunk from local storage
func (sa *StorageAgent) ReadChunk(ctx context.Context, chunkID string) ([]byte, error) {
	data, err := sa.chunkStore.ReadChunk(chunkID)
	if err != nil {
		return nil, fmt.Errorf("failed to read chunk: %w", err)
	}

	sa.logger.Debug("Chunk read",
		zap.String("chunk_id", chunkID),
		zap.Int("size", len(data)),
	)

	return data, nil
}

// DeleteChunk deletes a chunk from local storage
func (sa *StorageAgent) DeleteChunk(ctx context.Context, chunkID string) error {
	if err := sa.chunkStore.DeleteChunk(chunkID); err != nil {
		return fmt.Errorf("failed to delete chunk: %w", err)
	}

	sa.logger.Debug("Chunk deleted",
		zap.String("chunk_id", chunkID),
	)

	return nil
}

// VerifyChunk verifies the integrity of a chunk
func (sa *StorageAgent) VerifyChunk(ctx context.Context, chunkID string) error {
	if err := sa.chunkStore.VerifyChunk(chunkID); err != nil {
		return fmt.Errorf("chunk verification failed: %w", err)
	}

	sa.logger.Debug("Chunk verified",
		zap.String("chunk_id", chunkID),
	)

	return nil
}

// Statistics and Monitoring

// GetStorageStats returns storage statistics for this node
func (sa *StorageAgent) GetStorageStats() *storage.NodeStorageStats {
	// Get chunk store usage
	chunkUsage, err := sa.chunkStore.GetStorageUsage()
	if err != nil {
		sa.logger.Error("Failed to get chunk storage usage", zap.Error(err))
		chunkUsage = 0
	}

	// Get volume stats
	volumeStats := sa.volumeManager.GetVolumeStats()

	// Calculate total usage
	totalUsed := chunkUsage + volumeStats.TotalUsedBytes

	// In production, would get actual disk capacity
	// For now, estimate based on config
	totalCapacity := int64(500 * 1024 * 1024 * 1024) // 500GB default

	stats := &storage.NodeStorageStats{
		TotalBytes:     totalCapacity,
		UsedBytes:      totalUsed,
		AvailableBytes: totalCapacity - totalUsed,
		ChunkCount:     int64(len(sa.chunkStore.ListChunks())),
		VolumeCount:    int64(volumeStats.TotalVolumes),
	}

	return stats
}

// GetVolumeStats returns volume statistics
func (sa *StorageAgent) GetVolumeStats() storage.VolumeStats {
	return sa.volumeManager.GetVolumeStats()
}

// GetChunkStore returns the chunk store
func (sa *StorageAgent) GetChunkStore() *storage.ChunkStore {
	return sa.chunkStore
}

// GetVolumeManager returns the volume manager
func (sa *StorageAgent) GetVolumeManager() *storage.VolumeManager {
	return sa.volumeManager
}

// UpdateVolumeUsage updates usage statistics for all volumes
func (sa *StorageAgent) UpdateVolumeUsage(ctx context.Context) error {
	volumes, err := sa.volumeManager.ListVolumes()
	if err != nil {
		return fmt.Errorf("failed to list volumes: %w", err)
	}

	for _, volume := range volumes {
		if err := sa.volumeManager.UpdateVolumeUsage(volume.ID, volume.WorkloadID); err != nil {
			sa.logger.Warn("Failed to update volume usage",
				zap.String("volume_id", volume.ID),
				zap.String("workload_id", volume.WorkloadID),
				zap.Error(err),
			)
		}
	}

	return nil
}

// Integration with Agent

// AddStorageToAgent adds storage components to an existing agent
func AddStorageToAgent(agent *Agent) error {
	storageAgent, err := NewStorageAgent(agent.config)
	if err != nil {
		return fmt.Errorf("failed to create storage agent: %w", err)
	}

	// Store storage agent reference in the agent
	// In production, would add a field to Agent struct
	agent.config.Logger.Info("Storage components added to agent")

	return storageAgent.Start()
}

// GetStorageCapacity returns storage capacity information for the agent
func (a *Agent) GetStorageCapacity() *StorageCapacity {
	// In production, would query actual storage devices
	// For now, return estimated capacity based on config

	capacity := &StorageCapacity{
		TotalBytes:     500 * 1024 * 1024 * 1024, // 500GB
		AvailableBytes: 400 * 1024 * 1024 * 1024, // 400GB
		IOPSClass:      storage.IOPSClassMedium,
		StorageClass:   storage.StorageClassHot,
	}

	return capacity
}

// StorageCapacity represents storage capacity information
type StorageCapacity struct {
	TotalBytes     int64
	AvailableBytes int64
	IOPSClass      storage.IOPSClass
	StorageClass   storage.StorageClass
}
