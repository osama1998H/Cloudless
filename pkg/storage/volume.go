package storage

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"go.uber.org/zap"
)

// VolumeManager manages node-local ephemeral volumes
type VolumeManager struct {
	config     StorageConfig
	logger     *zap.Logger
	baseDir    string
	nodeID     string
	volumes    sync.Map // volumeID -> *Volume
	volumeLock sync.Map // volumeID -> *sync.RWMutex
	mu         sync.RWMutex
}

// NewVolumeManager creates a new volume manager
func NewVolumeManager(config StorageConfig, nodeID string, logger *zap.Logger) (*VolumeManager, error) {
	baseDir := filepath.Join(config.DataDir, "volumes")

	// Create volumes directory with secure permissions (owner-only)
	if err := os.MkdirAll(baseDir, 0700); err != nil {
		return nil, fmt.Errorf("failed to create volumes directory: %w", err)
	}

	vm := &VolumeManager{
		config:  config,
		logger:  logger,
		baseDir: baseDir,
		nodeID:  nodeID,
	}

	// Load existing volumes
	if err := vm.loadVolumes(); err != nil {
		logger.Warn("Failed to load existing volumes", zap.Error(err))
	}

	return vm, nil
}

// CreateVolume creates a new ephemeral volume
func (vm *VolumeManager) CreateVolume(req *CreateVolumeRequest) (*Volume, error) {
	vm.logger.Info("Creating volume",
		zap.String("volume_id", req.VolumeID),
		zap.String("workload_id", req.WorkloadID),
		zap.Int64("size_bytes", req.SizeBytes),
		zap.String("iops_class", string(req.IOPSClass)),
	)

	vm.mu.Lock()
	defer vm.mu.Unlock()

	// Check if volume already exists
	if _, exists := vm.volumes.Load(req.VolumeID); exists {
		return nil, fmt.Errorf("volume already exists: %s", req.VolumeID)
	}

	// Validate size
	if req.SizeBytes <= 0 {
		return nil, fmt.Errorf("invalid volume size: %d", req.SizeBytes)
	}

	// Check available space
	if err := vm.checkAvailableSpace(req.SizeBytes); err != nil {
		return nil, fmt.Errorf("insufficient space: %w", err)
	}

	// Create volume directory with secure permissions (owner-only)
	volumePath := vm.getVolumePath(req.VolumeID)
	if err := os.MkdirAll(volumePath, 0700); err != nil {
		return nil, fmt.Errorf("failed to create volume directory: %w", err)
	}

	// Create mount point with secure permissions (owner-only)
	mountPath := filepath.Join(volumePath, "mount")
	if err := os.MkdirAll(mountPath, 0700); err != nil {
		os.RemoveAll(volumePath)
		return nil, fmt.Errorf("failed to create mount point: %w", err)
	}

	// Create volume
	volume := &Volume{
		ID:          req.VolumeID,
		Name:        req.Name,
		WorkloadID:  req.WorkloadID,
		NodeID:      vm.nodeID,
		SizeBytes:   req.SizeBytes,
		UsedBytes:   0,
		IOPSClass:   req.IOPSClass,
		Path:        volumePath,
		MountPath:   mountPath,
		State:       VolumeStateCreated,
		CreatedAt:   time.Now(),
		LastUsed:    time.Now(),
		Labels:      req.Labels,
		Annotations: req.Annotations,
	}

	// Store volume
	vm.volumes.Store(req.VolumeID, volume)
	vm.volumeLock.Store(req.VolumeID, &sync.RWMutex{})

	vm.logger.Info("Volume created successfully",
		zap.String("volume_id", req.VolumeID),
		zap.String("path", volumePath),
	)

	return volume, nil
}

// GetVolume retrieves volume information
func (vm *VolumeManager) GetVolume(volumeID string) (*Volume, error) {
	v, ok := vm.volumes.Load(volumeID)
	if !ok {
		return nil, fmt.Errorf("volume not found: %s", volumeID)
	}

	volume := v.(*Volume)
	return volume, nil
}

// MountVolume mounts a volume for use by the specified workload.
// Access control is enforced: only the workload that created the volume can mount it.
// Returns VolumeAccessError if workloadID doesn't match the volume's owner.
//
// CLD-REQ-052: Enforces node-local ephemeral volume isolation per workload
// GO_ENGINEERING_SOP.md §8.2: Implements access control validation
func (vm *VolumeManager) MountVolume(volumeID, workloadID string) (string, error) {
	vm.logger.Info("Mounting volume",
		zap.String("volume_id", volumeID),
		zap.String("workload_id", workloadID),
	)

	// Get volume lock FIRST to prevent race conditions
	lock := vm.getVolumeLock(volumeID)
	lock.Lock()
	defer lock.Unlock()

	// Validate access control
	if err := vm.validateWorkloadAccess(volumeID, workloadID); err != nil {
		vm.logger.Warn("Volume access denied",
			zap.String("volume_id", volumeID),
			zap.String("caller_workload_id", workloadID),
			zap.Error(err),
		)
		return "", err
	}

	// Get volume (after lock acquired and access validated)
	volume, err := vm.GetVolume(volumeID)
	if err != nil {
		return "", err
	}

	// Check if already mounted
	if volume.State == VolumeStateMounted {
		vm.logger.Debug("Volume already mounted",
			zap.String("volume_id", volumeID),
			zap.String("mount_path", volume.MountPath),
		)
		return volume.MountPath, nil
	}

	// Set up volume based on IOPS class
	if err := vm.setupVolumeIOPS(volume); err != nil {
		return "", fmt.Errorf("failed to setup volume IOPS: %w", err)
	}

	// Update volume state
	volume.State = VolumeStateMounted
	volume.MountedAt = time.Now()
	volume.LastUsed = time.Now()
	vm.volumes.Store(volumeID, volume)

	vm.logger.Info("Volume mounted successfully",
		zap.String("volume_id", volumeID),
		zap.String("mount_path", volume.MountPath),
	)

	return volume.MountPath, nil
}

// UnmountVolume unmounts a volume. Access control is enforced.
//
// CLD-REQ-052: Enforces node-local ephemeral volume isolation per workload
// GO_ENGINEERING_SOP.md §8.2: Implements access control validation
func (vm *VolumeManager) UnmountVolume(volumeID, workloadID string) error {
	vm.logger.Info("Unmounting volume",
		zap.String("volume_id", volumeID),
		zap.String("workload_id", workloadID),
	)

	// Get volume lock FIRST to prevent race conditions
	lock := vm.getVolumeLock(volumeID)
	lock.Lock()
	defer lock.Unlock()

	// Validate access control
	if err := vm.validateWorkloadAccess(volumeID, workloadID); err != nil {
		vm.logger.Warn("Volume access denied",
			zap.String("volume_id", volumeID),
			zap.String("caller_workload_id", workloadID),
			zap.Error(err),
		)
		return err
	}

	// Get volume (after lock acquired and access validated)
	volume, err := vm.GetVolume(volumeID)
	if err != nil {
		return err
	}

	// Check if mounted
	if volume.State != VolumeStateMounted {
		return fmt.Errorf("volume not mounted: %s", volumeID)
	}

	// Update volume state
	volume.State = VolumeStateCreated
	volume.LastUsed = time.Now()
	vm.volumes.Store(volumeID, volume)

	vm.logger.Info("Volume unmounted successfully",
		zap.String("volume_id", volumeID),
	)

	return nil
}

// DeleteVolume deletes a volume. Access control is enforced.
//
// CLD-REQ-052: Enforces node-local ephemeral volume isolation per workload
// GO_ENGINEERING_SOP.md §8.2: Implements access control validation
func (vm *VolumeManager) DeleteVolume(volumeID, workloadID string) error {
	vm.logger.Info("Deleting volume",
		zap.String("volume_id", volumeID),
		zap.String("workload_id", workloadID),
	)

	vm.mu.Lock()
	defer vm.mu.Unlock()

	// Get volume lock FIRST to prevent race conditions
	lock := vm.getVolumeLock(volumeID)
	lock.Lock()
	defer lock.Unlock()

	// Validate access control
	if err := vm.validateWorkloadAccess(volumeID, workloadID); err != nil {
		vm.logger.Warn("Volume access denied",
			zap.String("volume_id", volumeID),
			zap.String("caller_workload_id", workloadID),
			zap.Error(err),
		)
		return err
	}

	// Get volume (after lock acquired and access validated)
	volume, err := vm.GetVolume(volumeID)
	if err != nil {
		return err
	}

	// Check if mounted
	if volume.State == VolumeStateMounted {
		return fmt.Errorf("cannot delete mounted volume: %s", volumeID)
	}

	// Delete volume directory
	if err := os.RemoveAll(volume.Path); err != nil {
		return fmt.Errorf("failed to delete volume directory: %w", err)
	}

	// Remove from tracking
	vm.volumes.Delete(volumeID)
	vm.volumeLock.Delete(volumeID)

	vm.logger.Info("Volume deleted successfully",
		zap.String("volume_id", volumeID),
	)

	return nil
}

// ListVolumes lists all volumes
func (vm *VolumeManager) ListVolumes() ([]*Volume, error) {
	var volumes []*Volume

	vm.volumes.Range(func(key, value interface{}) bool {
		volume := value.(*Volume)
		volumes = append(volumes, volume)
		return true
	})

	return volumes, nil
}

// ListVolumesByWorkload lists volumes for a specific workload
func (vm *VolumeManager) ListVolumesByWorkload(workloadID string) ([]*Volume, error) {
	var volumes []*Volume

	vm.volumes.Range(func(key, value interface{}) bool {
		volume := value.(*Volume)
		if volume.WorkloadID == workloadID {
			volumes = append(volumes, volume)
		}
		return true
	})

	return volumes, nil
}

// ResizeVolume resizes a volume. Access control is enforced.
//
// CLD-REQ-052: Enforces node-local ephemeral volume isolation per workload
// GO_ENGINEERING_SOP.md §8.2: Implements access control validation
func (vm *VolumeManager) ResizeVolume(volumeID, workloadID string, newSize int64) error {
	vm.logger.Info("Resizing volume",
		zap.String("volume_id", volumeID),
		zap.String("workload_id", workloadID),
		zap.Int64("new_size", newSize),
	)

	// Get volume lock FIRST to prevent race conditions
	lock := vm.getVolumeLock(volumeID)
	lock.Lock()
	defer lock.Unlock()

	// Validate access control
	if err := vm.validateWorkloadAccess(volumeID, workloadID); err != nil {
		vm.logger.Warn("Volume access denied",
			zap.String("volume_id", volumeID),
			zap.String("caller_workload_id", workloadID),
			zap.Error(err),
		)
		return err
	}

	// Get volume (after lock acquired and access validated)
	volume, err := vm.GetVolume(volumeID)
	if err != nil {
		return err
	}

	// Validate new size
	if newSize < volume.UsedBytes {
		return fmt.Errorf("cannot shrink volume below used space: used=%d, requested=%d",
			volume.UsedBytes, newSize)
	}

	// Check available space for expansion
	if newSize > volume.SizeBytes {
		delta := newSize - volume.SizeBytes
		if err := vm.checkAvailableSpace(delta); err != nil {
			return fmt.Errorf("insufficient space for resize: %w", err)
		}
	}

	// Update volume size
	volume.SizeBytes = newSize
	volume.LastUsed = time.Now()
	vm.volumes.Store(volumeID, volume)

	vm.logger.Info("Volume resized successfully",
		zap.String("volume_id", volumeID),
		zap.Int64("new_size", newSize),
	)

	return nil
}

// UpdateVolumeUsage updates the used space for a volume. Access control is enforced.
//
// CLD-REQ-052: Enforces node-local ephemeral volume isolation per workload
// GO_ENGINEERING_SOP.md §8.2: Implements access control validation
func (vm *VolumeManager) UpdateVolumeUsage(volumeID, workloadID string) error {
	// Get volume lock FIRST to prevent race conditions
	lock := vm.getVolumeLock(volumeID)
	lock.Lock()
	defer lock.Unlock()

	// Validate access control
	if err := vm.validateWorkloadAccess(volumeID, workloadID); err != nil {
		vm.logger.Warn("Volume access denied",
			zap.String("volume_id", volumeID),
			zap.String("caller_workload_id", workloadID),
			zap.Error(err),
		)
		return err
	}

	// Get volume (after lock acquired and access validated)
	volume, err := vm.GetVolume(volumeID)
	if err != nil {
		return err
	}

	// Calculate used space
	usedBytes, err := vm.calculateVolumeUsage(volume.MountPath)
	if err != nil {
		return fmt.Errorf("failed to calculate volume usage: %w", err)
	}

	// Update volume
	volume.UsedBytes = usedBytes
	volume.LastUsed = time.Now()
	vm.volumes.Store(volumeID, volume)

	vm.logger.Debug("Updated volume usage",
		zap.String("volume_id", volumeID),
		zap.Int64("used_bytes", usedBytes),
	)

	return nil
}

// CreateSnapshot creates a snapshot of a volume
func (vm *VolumeManager) CreateSnapshot(volumeID, snapshotID string) (*VolumeSnapshot, error) {
	vm.logger.Info("Creating volume snapshot",
		zap.String("volume_id", volumeID),
		zap.String("snapshot_id", snapshotID),
	)

	// Get volume
	volume, err := vm.GetVolume(volumeID)
	if err != nil {
		return nil, err
	}

	// Get volume lock (read lock since we're only reading)
	lock := vm.getVolumeLock(volumeID)
	lock.RLock()
	defer lock.RUnlock()

	// Create snapshot directory with secure permissions (owner-only)
	snapshotPath := filepath.Join(vm.baseDir, "snapshots", snapshotID)
	if err := os.MkdirAll(snapshotPath, 0700); err != nil {
		return nil, fmt.Errorf("failed to create snapshot directory: %w", err)
	}

	// Copy volume contents (simplified - in production would use COW or hardlinks)
	if err := vm.copyDirectory(volume.MountPath, snapshotPath); err != nil {
		os.RemoveAll(snapshotPath)
		return nil, fmt.Errorf("failed to copy volume: %w", err)
	}

	// Create snapshot metadata
	snapshot := &VolumeSnapshot{
		ID:         snapshotID,
		VolumeID:   volumeID,
		SizeBytes:  volume.UsedBytes,
		Path:       snapshotPath,
		CreatedAt:  time.Now(),
		WorkloadID: volume.WorkloadID,
	}

	vm.logger.Info("Volume snapshot created",
		zap.String("volume_id", volumeID),
		zap.String("snapshot_id", snapshotID),
		zap.Int64("size_bytes", snapshot.SizeBytes),
	)

	return snapshot, nil
}

// CleanupWorkloadVolumes deletes all volumes for a workload
func (vm *VolumeManager) CleanupWorkloadVolumes(workloadID string) error {
	vm.logger.Info("Cleaning up workload volumes",
		zap.String("workload_id", workloadID),
	)

	// List volumes for workload
	volumes, err := vm.ListVolumesByWorkload(workloadID)
	if err != nil {
		return err
	}

	// Delete each volume
	deletedCount := 0
	for _, volume := range volumes {
		// Unmount if mounted
		if volume.State == VolumeStateMounted {
			if err := vm.UnmountVolume(volume.ID, workloadID); err != nil {
				vm.logger.Error("Failed to unmount volume",
					zap.String("volume_id", volume.ID),
					zap.Error(err),
				)
				continue
			}
		}

		// Delete volume
		if err := vm.DeleteVolume(volume.ID, workloadID); err != nil {
			vm.logger.Error("Failed to delete volume",
				zap.String("volume_id", volume.ID),
				zap.Error(err),
			)
			continue
		}

		deletedCount++
	}

	vm.logger.Info("Workload volumes cleaned up",
		zap.String("workload_id", workloadID),
		zap.Int("deleted_count", deletedCount),
		zap.Int("total_count", len(volumes)),
	)

	return nil
}

// GetVolumeStats returns volume statistics
func (vm *VolumeManager) GetVolumeStats() VolumeStats {
	stats := VolumeStats{
		NodeID:      vm.nodeID,
		LastUpdated: time.Now(),
	}

	vm.volumes.Range(func(key, value interface{}) bool {
		volume := value.(*Volume)
		volumeID := volume.ID

		// Acquire read lock to prevent race conditions
		lock := vm.getVolumeLock(volumeID)
		lock.RLock()
		defer lock.RUnlock()

		stats.TotalVolumes++
		stats.TotalAllocatedBytes += volume.SizeBytes
		stats.TotalUsedBytes += volume.UsedBytes

		if volume.State == VolumeStateMounted {
			stats.MountedVolumes++
		}

		// Count by IOPS class
		switch volume.IOPSClass {
		case IOPSClassHigh:
			stats.HighIOPSVolumes++
		case IOPSClassMedium:
			stats.MediumIOPSVolumes++
		case IOPSClassLow:
			stats.LowIOPSVolumes++
		}

		return true
	})

	return stats
}

// Helper Methods

// getVolumePath returns the filesystem path for a volume
func (vm *VolumeManager) getVolumePath(volumeID string) string {
	return filepath.Join(vm.baseDir, volumeID)
}

// getVolumeLock returns the lock for a volume
func (vm *VolumeManager) getVolumeLock(volumeID string) *sync.RWMutex {
	if l, ok := vm.volumeLock.Load(volumeID); ok {
		return l.(*sync.RWMutex)
	}

	// Create new lock if not exists
	lock := &sync.RWMutex{}
	vm.volumeLock.Store(volumeID, lock)
	return lock
}

// validateWorkloadAccess checks if callerWorkloadID can access the volume
// Satisfies CLD-REQ-052 isolation requirement and GO_ENGINEERING_SOP.md §8.2 (access control)
func (vm *VolumeManager) validateWorkloadAccess(volumeID, callerWorkloadID string) error {
	volume, err := vm.GetVolume(volumeID)
	if err != nil {
		return err
	}

	if volume.WorkloadID != callerWorkloadID {
		return &VolumeAccessError{
			VolumeID:  volumeID,
			CallerID:  callerWorkloadID,
			OwnerID:   volume.WorkloadID,
			Operation: "access",
		}
	}

	return nil
}

// checkAvailableSpace checks if there's enough space available
func (vm *VolumeManager) checkAvailableSpace(requiredBytes int64) error {
	// Get filesystem info
	// In production, would check actual filesystem capacity
	// For now, just return nil (assume space available)
	return nil
}

// calculateVolumeUsage calculates the used space in a volume
func (vm *VolumeManager) calculateVolumeUsage(path string) (int64, error) {
	var totalSize int64

	err := filepath.Walk(path, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			totalSize += info.Size()
		}
		return nil
	})

	if err != nil {
		return 0, err
	}

	return totalSize, nil
}

// setupVolumeIOPS sets up volume for requested IOPS class
func (vm *VolumeManager) setupVolumeIOPS(volume *Volume) error {
	// In production, would:
	// - Configure I/O scheduling
	// - Set I/O limits (cgroups)
	// - Configure filesystem options
	// - Set up direct I/O if needed

	switch volume.IOPSClass {
	case IOPSClassHigh:
		vm.logger.Debug("Setting up high IOPS volume",
			zap.String("volume_id", volume.ID),
		)
		// Configure for SSD/NVMe performance
	case IOPSClassMedium:
		vm.logger.Debug("Setting up medium IOPS volume",
			zap.String("volume_id", volume.ID),
		)
		// Balanced configuration
	case IOPSClassLow:
		vm.logger.Debug("Setting up low IOPS volume",
			zap.String("volume_id", volume.ID),
		)
		// Configure for HDD/cold storage
	}

	return nil
}

// copyDirectory copies a directory recursively
func (vm *VolumeManager) copyDirectory(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Get relative path
		relPath, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}

		targetPath := filepath.Join(dst, relPath)

		if info.IsDir() {
			return os.MkdirAll(targetPath, info.Mode())
		}

		// Copy file
		return vm.copyFile(path, targetPath)
	})
}

// copyFile copies a single file
func (vm *VolumeManager) copyFile(src, dst string) error {
	sourceData, err := os.ReadFile(src)
	if err != nil {
		return err
	}

	// Write with secure permissions (owner-only read/write)
	return os.WriteFile(dst, sourceData, 0600)
}

// loadVolumes loads existing volumes from disk
func (vm *VolumeManager) loadVolumes() error {
	vm.logger.Info("Loading existing volumes")

	count := 0
	entries, err := os.ReadDir(vm.baseDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		volumeID := entry.Name()
		if volumeID == "snapshots" {
			continue
		}

		volumePath := vm.getVolumePath(volumeID)
		mountPath := filepath.Join(volumePath, "mount")

		// Get directory info
		info, err := entry.Info()
		if err != nil {
			vm.logger.Warn("Failed to get volume info",
				zap.String("volume_id", volumeID),
				zap.Error(err),
			)
			continue
		}

		// Calculate volume size
		usedBytes, _ := vm.calculateVolumeUsage(mountPath)

		// Create volume metadata
		volume := &Volume{
			ID:        volumeID,
			NodeID:    vm.nodeID,
			Path:      volumePath,
			MountPath: mountPath,
			UsedBytes: usedBytes,
			State:     VolumeStateCreated,
			CreatedAt: info.ModTime(),
			LastUsed:  info.ModTime(),
		}

		vm.volumes.Store(volumeID, volume)
		vm.volumeLock.Store(volumeID, &sync.RWMutex{})
		count++
	}

	vm.logger.Info("Loaded existing volumes",
		zap.Int("count", count),
	)

	return nil
}

// Request Types

// CreateVolumeRequest contains parameters for creating a volume
type CreateVolumeRequest struct {
	VolumeID    string
	Name        string
	WorkloadID  string
	SizeBytes   int64
	IOPSClass   IOPSClass
	Labels      map[string]string
	Annotations map[string]string
}

// VolumeStats contains volume statistics
type VolumeStats struct {
	NodeID              string
	TotalVolumes        int
	MountedVolumes      int
	TotalAllocatedBytes int64
	TotalUsedBytes      int64
	HighIOPSVolumes     int
	MediumIOPSVolumes   int
	LowIOPSVolumes      int
	LastUpdated         time.Time
}

// VolumeSnapshot represents a volume snapshot
type VolumeSnapshot struct {
	ID         string
	VolumeID   string
	SizeBytes  int64
	Path       string
	CreatedAt  time.Time
	WorkloadID string
}
