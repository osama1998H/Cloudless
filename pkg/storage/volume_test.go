package storage

import (
	"os"
	"path/filepath"
	"testing"

	"go.uber.org/zap"
)

// setupVolumeManager creates a volume manager for testing
func setupVolumeManager(t *testing.T) (*VolumeManager, string, func()) {
	t.Helper()

	tempDir, err := os.MkdirTemp("", "cloudless-volume-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}

	config := StorageConfig{
		DataDir:             tempDir,
		EnableCompression:   false,
		ReplicationFactor:   ReplicationFactorThree,
		MaxDiskUsagePercent: 90,
	}

	logger := zap.NewNop()
	vm, err := NewVolumeManager(config, "test-node-1", logger)
	if err != nil {
		os.RemoveAll(tempDir)
		t.Fatalf("Failed to create volume manager: %v", err)
	}

	cleanup := func() {
		os.RemoveAll(tempDir)
	}

	return vm, tempDir, cleanup
}

// TestVolumeManager_CreateVolume_Success tests successful volume creation
// Satisfies CLD-REQ-052: Node-local ephemeral volumes
func TestVolumeManager_CreateVolume_Success(t *testing.T) {
	vm, _, cleanup := setupVolumeManager(t)
	defer cleanup()

	req := &CreateVolumeRequest{
		VolumeID:   "vol-001",
		Name:       "test-volume",
		WorkloadID: "workload-1",
		SizeBytes:  1024 * 1024 * 100, // 100MB
		IOPSClass:  IOPSClassMedium,
		Labels: map[string]string{
			"environment": "test",
		},
	}

	volume, err := vm.CreateVolume(req)
	if err != nil {
		t.Fatalf("CreateVolume failed: %v", err)
	}

	// Verify volume metadata
	if volume.ID != req.VolumeID {
		t.Errorf("Expected volume ID %s, got %s", req.VolumeID, volume.ID)
	}
	if volume.WorkloadID != req.WorkloadID {
		t.Errorf("Expected workload ID %s, got %s", req.WorkloadID, volume.WorkloadID)
	}
	if volume.SizeBytes != req.SizeBytes {
		t.Errorf("Expected size %d, got %d", req.SizeBytes, volume.SizeBytes)
	}
	if volume.State != VolumeStateCreated {
		t.Errorf("Expected state %s, got %s", VolumeStateCreated, volume.State)
	}
	if volume.IOPSClass != req.IOPSClass {
		t.Errorf("Expected IOPS class %s, got %s", req.IOPSClass, volume.IOPSClass)
	}

	// Verify volume directory exists
	if _, err := os.Stat(volume.Path); os.IsNotExist(err) {
		t.Error("Volume directory should exist")
	}

	// Verify mount point exists
	if _, err := os.Stat(volume.MountPath); os.IsNotExist(err) {
		t.Error("Mount point should exist")
	}
}

// TestVolumeManager_CreateVolume_AlreadyExists tests duplicate volume creation
func TestVolumeManager_CreateVolume_AlreadyExists(t *testing.T) {
	vm, _, cleanup := setupVolumeManager(t)
	defer cleanup()

	req := &CreateVolumeRequest{
		VolumeID:   "vol-duplicate",
		Name:       "duplicate-test",
		WorkloadID: "workload-1",
		SizeBytes:  1024 * 1024,
		IOPSClass:  IOPSClassLow,
	}

	// Create volume first time
	_, err := vm.CreateVolume(req)
	if err != nil {
		t.Fatalf("First CreateVolume failed: %v", err)
	}

	// Try to create again - should fail
	_, err = vm.CreateVolume(req)
	if err == nil {
		t.Error("Expected error when creating duplicate volume, got nil")
	}
}

// TestVolumeManager_CreateVolume_InvalidSize tests invalid size handling
func TestVolumeManager_CreateVolume_InvalidSize(t *testing.T) {
	vm, _, cleanup := setupVolumeManager(t)
	defer cleanup()

	tests := []struct {
		name      string
		sizeBytes int64
		wantErr   bool
	}{
		{"zero size", 0, true},
		{"negative size", -1024, true},
		{"valid size", 1024, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := &CreateVolumeRequest{
				VolumeID:   "vol-" + tt.name,
				Name:       tt.name,
				WorkloadID: "workload-1",
				SizeBytes:  tt.sizeBytes,
				IOPSClass:  IOPSClassLow,
			}

			_, err := vm.CreateVolume(req)
			if (err != nil) != tt.wantErr {
				t.Errorf("CreateVolume() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// TestVolumeManager_MountVolume_Success tests successful volume mounting
func TestVolumeManager_MountVolume_Success(t *testing.T) {
	vm, _, cleanup := setupVolumeManager(t)
	defer cleanup()

	// Create volume
	req := &CreateVolumeRequest{
		VolumeID:   "vol-mount-test",
		Name:       "mount-test",
		WorkloadID: "workload-1",
		SizeBytes:  1024 * 1024,
		IOPSClass:  IOPSClassMedium,
	}

	volume, err := vm.CreateVolume(req)
	if err != nil {
		t.Fatalf("CreateVolume failed: %v", err)
	}

	// Mount volume
	mountPath, err := vm.MountVolume(volume.ID, req.WorkloadID)
	if err != nil {
		t.Fatalf("MountVolume failed: %v", err)
	}

	if mountPath != volume.MountPath {
		t.Errorf("Expected mount path %s, got %s", volume.MountPath, mountPath)
	}

	// Verify volume state updated
	vol, err := vm.GetVolume(volume.ID)
	if err != nil {
		t.Fatalf("GetVolume failed: %v", err)
	}

	if vol.State != VolumeStateMounted {
		t.Errorf("Expected state %s, got %s", VolumeStateMounted, vol.State)
	}

	if vol.MountedAt.IsZero() {
		t.Error("MountedAt timestamp should be set")
	}
}

// TestVolumeManager_MountVolume_AlreadyMounted tests mounting already mounted volume
func TestVolumeManager_MountVolume_AlreadyMounted(t *testing.T) {
	vm, _, cleanup := setupVolumeManager(t)
	defer cleanup()

	// Create and mount volume
	req := &CreateVolumeRequest{
		VolumeID:   "vol-already-mounted",
		Name:       "already-mounted",
		WorkloadID: "workload-1",
		SizeBytes:  1024 * 1024,
		IOPSClass:  IOPSClassLow,
	}

	volume, err := vm.CreateVolume(req)
	if err != nil {
		t.Fatalf("CreateVolume failed: %v", err)
	}

	// First mount
	path1, err := vm.MountVolume(volume.ID, req.WorkloadID)
	if err != nil {
		t.Fatalf("First MountVolume failed: %v", err)
	}

	// Second mount (should be idempotent and return same path)
	path2, err := vm.MountVolume(volume.ID, req.WorkloadID)
	if err != nil {
		t.Fatalf("Second MountVolume failed: %v", err)
	}

	if path1 != path2 {
		t.Errorf("Mount paths should match: %s != %s", path1, path2)
	}
}

// TestVolumeManager_UnmountVolume_Success tests successful volume unmounting
func TestVolumeManager_UnmountVolume_Success(t *testing.T) {
	vm, _, cleanup := setupVolumeManager(t)
	defer cleanup()

	// Create and mount volume
	req := &CreateVolumeRequest{
		VolumeID:   "vol-unmount-test",
		Name:       "unmount-test",
		WorkloadID: "workload-1",
		SizeBytes:  1024 * 1024,
		IOPSClass:  IOPSClassMedium,
	}

	volume, err := vm.CreateVolume(req)
	if err != nil {
		t.Fatalf("CreateVolume failed: %v", err)
	}

	_, err = vm.MountVolume(volume.ID, req.WorkloadID)
	if err != nil {
		t.Fatalf("MountVolume failed: %v", err)
	}

	// Unmount
	err = vm.UnmountVolume(volume.ID, req.WorkloadID)
	if err != nil {
		t.Fatalf("UnmountVolume failed: %v", err)
	}

	// Verify state updated
	vol, err := vm.GetVolume(volume.ID)
	if err != nil {
		t.Fatalf("GetVolume failed: %v", err)
	}

	if vol.State != VolumeStateCreated {
		t.Errorf("Expected state %s after unmount, got %s", VolumeStateCreated, vol.State)
	}
}

// TestVolumeManager_UnmountVolume_NotMounted tests unmounting non-mounted volume
func TestVolumeManager_UnmountVolume_NotMounted(t *testing.T) {
	vm, _, cleanup := setupVolumeManager(t)
	defer cleanup()

	// Create volume without mounting
	req := &CreateVolumeRequest{
		VolumeID:   "vol-not-mounted",
		Name:       "not-mounted",
		WorkloadID: "workload-1",
		SizeBytes:  1024 * 1024,
		IOPSClass:  IOPSClassLow,
	}

	volume, err := vm.CreateVolume(req)
	if err != nil {
		t.Fatalf("CreateVolume failed: %v", err)
	}

	// Try to unmount - should fail
	err = vm.UnmountVolume(volume.ID, req.WorkloadID)
	if err == nil {
		t.Error("Expected error when unmounting non-mounted volume, got nil")
	}
}

// TestVolumeManager_DeleteVolume_Success tests successful volume deletion
func TestVolumeManager_DeleteVolume_Success(t *testing.T) {
	vm, _, cleanup := setupVolumeManager(t)
	defer cleanup()

	// Create volume
	req := &CreateVolumeRequest{
		VolumeID:   "vol-delete-test",
		Name:       "delete-test",
		WorkloadID: "workload-1",
		SizeBytes:  1024 * 1024,
		IOPSClass:  IOPSClassLow,
	}

	volume, err := vm.CreateVolume(req)
	if err != nil {
		t.Fatalf("CreateVolume failed: %v", err)
	}

	volumePath := volume.Path

	// Delete volume
	err = vm.DeleteVolume(volume.ID, req.WorkloadID)
	if err != nil {
		t.Fatalf("DeleteVolume failed: %v", err)
	}

	// Verify volume removed from tracking
	_, err = vm.GetVolume(volume.ID)
	if err == nil {
		t.Error("Expected error when getting deleted volume, got nil")
	}

	// Verify volume directory removed
	if _, err := os.Stat(volumePath); !os.IsNotExist(err) {
		t.Error("Volume directory should be removed")
	}
}

// TestVolumeManager_DeleteVolume_WhileMounted tests deleting mounted volume
func TestVolumeManager_DeleteVolume_WhileMounted(t *testing.T) {
	vm, _, cleanup := setupVolumeManager(t)
	defer cleanup()

	// Create and mount volume
	req := &CreateVolumeRequest{
		VolumeID:   "vol-delete-mounted",
		Name:       "delete-mounted",
		WorkloadID: "workload-1",
		SizeBytes:  1024 * 1024,
		IOPSClass:  IOPSClassMedium,
	}

	volume, err := vm.CreateVolume(req)
	if err != nil {
		t.Fatalf("CreateVolume failed: %v", err)
	}

	_, err = vm.MountVolume(volume.ID, req.WorkloadID)
	if err != nil {
		t.Fatalf("MountVolume failed: %v", err)
	}

	// Try to delete mounted volume - should fail
	err = vm.DeleteVolume(volume.ID, req.WorkloadID)
	if err == nil {
		t.Error("Expected error when deleting mounted volume, got nil")
	}
}

// TestVolumeManager_ListVolumes_Success tests listing all volumes
func TestVolumeManager_ListVolumes_Success(t *testing.T) {
	vm, _, cleanup := setupVolumeManager(t)
	defer cleanup()

	// Create multiple volumes
	for i := 1; i <= 3; i++ {
		req := &CreateVolumeRequest{
			VolumeID:   "vol-list-" + string(rune('0'+i)),
			Name:       "list-test",
			WorkloadID: "workload-1",
			SizeBytes:  1024 * 1024,
			IOPSClass:  IOPSClassLow,
		}

		_, err := vm.CreateVolume(req)
		if err != nil {
			t.Fatalf("CreateVolume %d failed: %v", i, err)
		}
	}

	// List volumes
	volumes, err := vm.ListVolumes()
	if err != nil {
		t.Fatalf("ListVolumes failed: %v", err)
	}

	if len(volumes) != 3 {
		t.Errorf("Expected 3 volumes, got %d", len(volumes))
	}
}

// TestVolumeManager_ListVolumesByWorkload_Isolation tests workload isolation
// CRITICAL: Satisfies CLD-REQ-052 isolation requirement
func TestVolumeManager_ListVolumesByWorkload_Isolation(t *testing.T) {
	vm, _, cleanup := setupVolumeManager(t)
	defer cleanup()

	// Create volumes for workload-1
	for i := 1; i <= 2; i++ {
		req := &CreateVolumeRequest{
			VolumeID:   "vol-w1-" + string(rune('0'+i)),
			Name:       "workload1-vol",
			WorkloadID: "workload-1",
			SizeBytes:  1024 * 1024,
			IOPSClass:  IOPSClassMedium,
		}

		_, err := vm.CreateVolume(req)
		if err != nil {
			t.Fatalf("CreateVolume for workload-1 failed: %v", err)
		}
	}

	// Create volumes for workload-2
	for i := 1; i <= 3; i++ {
		req := &CreateVolumeRequest{
			VolumeID:   "vol-w2-" + string(rune('0'+i)),
			Name:       "workload2-vol",
			WorkloadID: "workload-2",
			SizeBytes:  1024 * 1024,
			IOPSClass:  IOPSClassHigh,
		}

		_, err := vm.CreateVolume(req)
		if err != nil {
			t.Fatalf("CreateVolume for workload-2 failed: %v", err)
		}
	}

	// List volumes for workload-1
	volumes1, err := vm.ListVolumesByWorkload("workload-1")
	if err != nil {
		t.Fatalf("ListVolumesByWorkload(workload-1) failed: %v", err)
	}

	if len(volumes1) != 2 {
		t.Errorf("Expected 2 volumes for workload-1, got %d", len(volumes1))
	}

	// Verify all volumes belong to workload-1
	for _, vol := range volumes1 {
		if vol.WorkloadID != "workload-1" {
			t.Errorf("Found volume %s with wrong workload ID: %s", vol.ID, vol.WorkloadID)
		}
	}

	// List volumes for workload-2
	volumes2, err := vm.ListVolumesByWorkload("workload-2")
	if err != nil {
		t.Fatalf("ListVolumesByWorkload(workload-2) failed: %v", err)
	}

	if len(volumes2) != 3 {
		t.Errorf("Expected 3 volumes for workload-2, got %d", len(volumes2))
	}

	// Verify all volumes belong to workload-2
	for _, vol := range volumes2 {
		if vol.WorkloadID != "workload-2" {
			t.Errorf("Found volume %s with wrong workload ID: %s", vol.ID, vol.WorkloadID)
		}
	}
}

// TestVolumeManager_ResizeVolume_Expansion tests volume expansion
func TestVolumeManager_ResizeVolume_Expansion(t *testing.T) {
	vm, _, cleanup := setupVolumeManager(t)
	defer cleanup()

	// Create volume
	req := &CreateVolumeRequest{
		VolumeID:   "vol-resize-expand",
		Name:       "resize-expand",
		WorkloadID: "workload-1",
		SizeBytes:  1024 * 1024, // 1MB
		IOPSClass:  IOPSClassMedium,
	}

	volume, err := vm.CreateVolume(req)
	if err != nil {
		t.Fatalf("CreateVolume failed: %v", err)
	}

	// Resize to 2MB
	newSize := int64(1024 * 1024 * 2)
	err = vm.ResizeVolume(volume.ID, req.WorkloadID, newSize)
	if err != nil {
		t.Fatalf("ResizeVolume failed: %v", err)
	}

	// Verify new size
	vol, err := vm.GetVolume(volume.ID)
	if err != nil {
		t.Fatalf("GetVolume failed: %v", err)
	}

	if vol.SizeBytes != newSize {
		t.Errorf("Expected size %d, got %d", newSize, vol.SizeBytes)
	}
}

// TestVolumeManager_ResizeVolume_BelowUsedSpace tests invalid shrinking
func TestVolumeManager_ResizeVolume_BelowUsedSpace(t *testing.T) {
	vm, _, cleanup := setupVolumeManager(t)
	defer cleanup()

	// Create volume
	req := &CreateVolumeRequest{
		VolumeID:   "vol-resize-invalid",
		Name:       "resize-invalid",
		WorkloadID: "workload-1",
		SizeBytes:  1024 * 1024 * 10, // 10MB
		IOPSClass:  IOPSClassMedium,
	}

	volume, err := vm.CreateVolume(req)
	if err != nil {
		t.Fatalf("CreateVolume failed: %v", err)
	}

	// Mount and write some data
	mountPath, err := vm.MountVolume(volume.ID, req.WorkloadID)
	if err != nil {
		t.Fatalf("MountVolume failed: %v", err)
	}

	testData := make([]byte, 1024*1024*5) // 5MB
	testFile := filepath.Join(mountPath, "test.dat")
	err = os.WriteFile(testFile, testData, 0644)
	if err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	// Update usage
	err = vm.UpdateVolumeUsage(volume.ID, req.WorkloadID)
	if err != nil {
		t.Fatalf("UpdateVolumeUsage failed: %v", err)
	}

	// Try to resize below used space (5MB -> 1MB) - should fail
	err = vm.ResizeVolume(volume.ID, req.WorkloadID, 1024*1024)
	if err == nil {
		t.Error("Expected error when resizing below used space, got nil")
	}
}

// TestVolumeManager_UpdateVolumeUsage_Success tests usage tracking
func TestVolumeManager_UpdateVolumeUsage_Success(t *testing.T) {
	vm, _, cleanup := setupVolumeManager(t)
	defer cleanup()

	// Create and mount volume
	req := &CreateVolumeRequest{
		VolumeID:   "vol-usage-test",
		Name:       "usage-test",
		WorkloadID: "workload-1",
		SizeBytes:  1024 * 1024 * 10,
		IOPSClass:  IOPSClassMedium,
	}

	volume, err := vm.CreateVolume(req)
	if err != nil {
		t.Fatalf("CreateVolume failed: %v", err)
	}

	mountPath, err := vm.MountVolume(volume.ID, req.WorkloadID)
	if err != nil {
		t.Fatalf("MountVolume failed: %v", err)
	}

	// Write test data
	testData := []byte("Test data for usage tracking")
	testFile := filepath.Join(mountPath, "test.txt")
	err = os.WriteFile(testFile, testData, 0644)
	if err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	// Update usage
	err = vm.UpdateVolumeUsage(volume.ID, req.WorkloadID)
	if err != nil {
		t.Fatalf("UpdateVolumeUsage failed: %v", err)
	}

	// Verify usage updated
	vol, err := vm.GetVolume(volume.ID)
	if err != nil {
		t.Fatalf("GetVolume failed: %v", err)
	}

	if vol.UsedBytes == 0 {
		t.Error("UsedBytes should be greater than 0")
	}

	if vol.UsedBytes < int64(len(testData)) {
		t.Errorf("UsedBytes %d should be at least %d", vol.UsedBytes, len(testData))
	}
}

// TestVolumeManager_CreateSnapshot_Success tests volume snapshots
func TestVolumeManager_CreateSnapshot_Success(t *testing.T) {
	vm, _, cleanup := setupVolumeManager(t)
	defer cleanup()

	// Create and mount volume
	req := &CreateVolumeRequest{
		VolumeID:   "vol-snapshot-test",
		Name:       "snapshot-test",
		WorkloadID: "workload-1",
		SizeBytes:  1024 * 1024,
		IOPSClass:  IOPSClassMedium,
	}

	volume, err := vm.CreateVolume(req)
	if err != nil {
		t.Fatalf("CreateVolume failed: %v", err)
	}

	mountPath, err := vm.MountVolume(volume.ID, req.WorkloadID)
	if err != nil {
		t.Fatalf("MountVolume failed: %v", err)
	}

	// Write test data
	testData := []byte("Snapshot test data")
	testFile := filepath.Join(mountPath, "snapshot.txt")
	err = os.WriteFile(testFile, testData, 0644)
	if err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	// Update usage
	err = vm.UpdateVolumeUsage(volume.ID, req.WorkloadID)
	if err != nil {
		t.Fatalf("UpdateVolumeUsage failed: %v", err)
	}

	// Create snapshot
	snapshot, err := vm.CreateSnapshot(volume.ID, "snap-001")
	if err != nil {
		t.Fatalf("CreateSnapshot failed: %v", err)
	}

	// Verify snapshot
	if snapshot.ID != "snap-001" {
		t.Errorf("Expected snapshot ID snap-001, got %s", snapshot.ID)
	}
	if snapshot.VolumeID != volume.ID {
		t.Errorf("Expected volume ID %s, got %s", volume.ID, snapshot.VolumeID)
	}
	if snapshot.WorkloadID != volume.WorkloadID {
		t.Errorf("Expected workload ID %s, got %s", volume.WorkloadID, snapshot.WorkloadID)
	}

	// Verify snapshot directory exists
	if _, err := os.Stat(snapshot.Path); os.IsNotExist(err) {
		t.Error("Snapshot directory should exist")
	}

	// Verify snapshot contains data
	snapshotFile := filepath.Join(snapshot.Path, "snapshot.txt")
	snapshotData, err := os.ReadFile(snapshotFile)
	if err != nil {
		t.Errorf("Failed to read snapshot file: %v", err)
	} else if string(snapshotData) != string(testData) {
		t.Errorf("Snapshot data mismatch: expected %s, got %s", testData, snapshotData)
	}
}

// TestVolumeManager_CleanupWorkloadVolumes_Success tests workload cleanup
// CRITICAL: Satisfies CLD-REQ-052 ephemeral requirement
func TestVolumeManager_CleanupWorkloadVolumes_Success(t *testing.T) {
	vm, _, cleanup := setupVolumeManager(t)
	defer cleanup()

	// Create volumes for workload-1
	for i := 1; i <= 3; i++ {
		req := &CreateVolumeRequest{
			VolumeID:   "vol-cleanup-" + string(rune('0'+i)),
			Name:       "cleanup-test",
			WorkloadID: "workload-cleanup",
			SizeBytes:  1024 * 1024,
			IOPSClass:  IOPSClassLow,
		}

		volume, err := vm.CreateVolume(req)
		if err != nil {
			t.Fatalf("CreateVolume %d failed: %v", i, err)
		}

		// Mount some volumes
		if i <= 2 {
			_, err = vm.MountVolume(volume.ID, req.WorkloadID)
			if err != nil {
				t.Fatalf("MountVolume %d failed: %v", i, err)
			}
		}
	}

	// Verify volumes exist
	volumesBefore, err := vm.ListVolumesByWorkload("workload-cleanup")
	if err != nil {
		t.Fatalf("ListVolumesByWorkload failed: %v", err)
	}
	if len(volumesBefore) != 3 {
		t.Errorf("Expected 3 volumes before cleanup, got %d", len(volumesBefore))
	}

	// Cleanup workload volumes
	err = vm.CleanupWorkloadVolumes("workload-cleanup")
	if err != nil {
		t.Fatalf("CleanupWorkloadVolumes failed: %v", err)
	}

	// Verify volumes removed
	volumesAfter, err := vm.ListVolumesByWorkload("workload-cleanup")
	if err != nil {
		t.Fatalf("ListVolumesByWorkload after cleanup failed: %v", err)
	}
	if len(volumesAfter) != 0 {
		t.Errorf("Expected 0 volumes after cleanup, got %d", len(volumesAfter))
	}
}

// TestVolumeManager_GetVolumeStats_Accuracy tests statistics tracking
func TestVolumeManager_GetVolumeStats_Accuracy(t *testing.T) {
	vm, _, cleanup := setupVolumeManager(t)
	defer cleanup()

	// Create volumes with different IOPS classes
	volumes := []struct {
		id        string
		size      int64
		iopsClass IOPSClass
	}{
		{"vol-stats-1", 1024 * 1024, IOPSClassHigh},
		{"vol-stats-2", 2 * 1024 * 1024, IOPSClassMedium},
		{"vol-stats-3", 3 * 1024 * 1024, IOPSClassLow},
	}

	for _, v := range volumes {
		req := &CreateVolumeRequest{
			VolumeID:   v.id,
			Name:       "stats-test",
			WorkloadID: "workload-1",
			SizeBytes:  v.size,
			IOPSClass:  v.iopsClass,
		}

		_, err := vm.CreateVolume(req)
		if err != nil {
			t.Fatalf("CreateVolume %s failed: %v", v.id, err)
		}
	}

	// Mount one volume
	_, err := vm.MountVolume("vol-stats-1", "workload-1")
	if err != nil {
		t.Fatalf("MountVolume failed: %v", err)
	}

	// Get stats
	stats := vm.GetVolumeStats()

	// Verify stats
	if stats.TotalVolumes != 3 {
		t.Errorf("Expected 3 total volumes, got %d", stats.TotalVolumes)
	}
	if stats.MountedVolumes != 1 {
		t.Errorf("Expected 1 mounted volume, got %d", stats.MountedVolumes)
	}
	if stats.HighIOPSVolumes != 1 {
		t.Errorf("Expected 1 high IOPS volume, got %d", stats.HighIOPSVolumes)
	}
	if stats.MediumIOPSVolumes != 1 {
		t.Errorf("Expected 1 medium IOPS volume, got %d", stats.MediumIOPSVolumes)
	}
	if stats.LowIOPSVolumes != 1 {
		t.Errorf("Expected 1 low IOPS volume, got %d", stats.LowIOPSVolumes)
	}

	expectedSize := int64(6 * 1024 * 1024) // Sum of all volumes
	if stats.TotalAllocatedBytes != expectedSize {
		t.Errorf("Expected allocated bytes %d, got %d", expectedSize, stats.TotalAllocatedBytes)
	}
}

// TestVolumeManager_ConcurrentAccess_RaceConditions tests thread safety
// Satisfies GO_ENGINEERING_SOP.md §5 (Concurrency Policy)
// Run with: go test -race
func TestVolumeManager_ConcurrentAccess_RaceConditions(t *testing.T) {
	vm, _, cleanup := setupVolumeManager(t)
	defer cleanup()

	// Create initial volumes
	for i := 1; i <= 5; i++ {
		req := &CreateVolumeRequest{
			VolumeID:   "vol-race-" + string(rune('0'+i)),
			Name:       "race-test",
			WorkloadID: "workload-1",
			SizeBytes:  1024 * 1024,
			IOPSClass:  IOPSClassMedium,
		}

		_, err := vm.CreateVolume(req)
		if err != nil {
			t.Fatalf("CreateVolume %d failed: %v", i, err)
		}
	}

	// Concurrent operations
	done := make(chan bool)

	// Goroutine 1: List volumes
	go func() {
		for i := 0; i < 100; i++ {
			_, _ = vm.ListVolumes()
		}
		done <- true
	}()

	// Goroutine 2: Get volumes
	go func() {
		for i := 0; i < 100; i++ {
			_, _ = vm.GetVolume("vol-race-1")
		}
		done <- true
	}()

	// Goroutine 3: Mount/unmount
	go func() {
		for i := 0; i < 50; i++ {
			_, _ = vm.MountVolume("vol-race-2", "workload-1")
			_ = vm.UnmountVolume("vol-race-2", "workload-1")
		}
		done <- true
	}()

	// Goroutine 4: Get stats
	go func() {
		for i := 0; i < 100; i++ {
			_ = vm.GetVolumeStats()
		}
		done <- true
	}()

	// Wait for all goroutines
	for i := 0; i < 4; i++ {
		<-done
	}

	// If we get here without race detector failures, test passes
}

// TestVolumeManager_LoadVolumes_Recovery tests volume recovery on restart
func TestVolumeManager_LoadVolumes_Recovery(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "cloudless-volume-recovery-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	config := StorageConfig{
		DataDir:             tempDir,
		EnableCompression:   false,
		ReplicationFactor:   ReplicationFactorThree,
		MaxDiskUsagePercent: 90,
	}

	logger := zap.NewNop()

	// Create first volume manager and add volumes
	vm1, err := NewVolumeManager(config, "test-node-1", logger)
	if err != nil {
		t.Fatalf("Failed to create first volume manager: %v", err)
	}

	// Create test volumes
	for i := 1; i <= 3; i++ {
		req := &CreateVolumeRequest{
			VolumeID:   "vol-recovery-" + string(rune('0'+i)),
			Name:       "recovery-test",
			WorkloadID: "workload-1",
			SizeBytes:  1024 * 1024,
			IOPSClass:  IOPSClassMedium,
		}

		_, err := vm1.CreateVolume(req)
		if err != nil {
			t.Fatalf("CreateVolume %d failed: %v", i, err)
		}
	}

	// Simulate restart by creating new volume manager
	vm2, err := NewVolumeManager(config, "test-node-1", logger)
	if err != nil {
		t.Fatalf("Failed to create second volume manager: %v", err)
	}

	// Verify volumes recovered
	volumes, err := vm2.ListVolumes()
	if err != nil {
		t.Fatalf("ListVolumes after recovery failed: %v", err)
	}

	if len(volumes) != 3 {
		t.Errorf("Expected 3 recovered volumes, got %d", len(volumes))
	}

	// Verify we can still access recovered volumes
	for i := 1; i <= 3; i++ {
		volumeID := "vol-recovery-" + string(rune('0'+i))
		_, err := vm2.GetVolume(volumeID)
		if err != nil {
			t.Errorf("Failed to get recovered volume %s: %v", volumeID, err)
		}
	}
}

// TestVolumeManager_GetVolume_NotFound tests error handling for missing volumes
func TestVolumeManager_GetVolume_NotFound(t *testing.T) {
	vm, _, cleanup := setupVolumeManager(t)
	defer cleanup()

	_, err := vm.GetVolume("non-existent-volume")
	if err == nil {
		t.Error("Expected error when getting non-existent volume, got nil")
	}
}

// TestVolumeManager_WorkloadIsolation_CannotAccessOtherWorkload tests access control
// Satisfies CLD-REQ-052: Node-local ephemeral volumes are isolated per workload
// Satisfies GO_ENGINEERING_SOP.md §8.2: Access control validation
func TestVolumeManager_WorkloadIsolation_CannotAccessOtherWorkload(t *testing.T) {
	vm, _, cleanup := setupVolumeManager(t)
	defer cleanup()

	// Create volume for workload-1
	req := &CreateVolumeRequest{
		VolumeID:   "vol-workload1",
		Name:       "workload1-private",
		WorkloadID: "workload-1",
		SizeBytes:  1024 * 1024,
		IOPSClass:  IOPSClassMedium,
	}

	volume, err := vm.CreateVolume(req)
	if err != nil {
		t.Fatalf("CreateVolume failed: %v", err)
	}

	// Verify workload-1 CAN mount its own volume
	_, err = vm.MountVolume(volume.ID, "workload-1")
	if err != nil {
		t.Fatalf("workload-1 should be able to mount its own volume: %v", err)
	}

	// Verify workload-2 CANNOT mount workload-1's volume
	_, err = vm.MountVolume(volume.ID, "workload-2")
	if err == nil {
		t.Error("workload-2 should NOT be able to mount workload-1's volume")
	}
	if !IsVolumeAccessError(err) {
		t.Errorf("Expected VolumeAccessError, got: %v", err)
	}

	// Verify workload-2 CANNOT unmount workload-1's volume
	err = vm.UnmountVolume(volume.ID, "workload-2")
	if err == nil {
		t.Error("workload-2 should NOT be able to unmount workload-1's volume")
	}
	if !IsVolumeAccessError(err) {
		t.Errorf("Expected VolumeAccessError, got: %v", err)
	}

	// Verify workload-2 CANNOT delete workload-1's volume
	err = vm.DeleteVolume(volume.ID, "workload-2")
	if err == nil {
		t.Error("workload-2 should NOT be able to delete workload-1's volume")
	}
	if !IsVolumeAccessError(err) {
		t.Errorf("Expected VolumeAccessError, got: %v", err)
	}

	// Verify workload-2 CANNOT resize workload-1's volume
	err = vm.ResizeVolume(volume.ID, "workload-2", 2*1024*1024)
	if err == nil {
		t.Error("workload-2 should NOT be able to resize workload-1's volume")
	}
	if !IsVolumeAccessError(err) {
		t.Errorf("Expected VolumeAccessError, got: %v", err)
	}

	// Verify workload-2 CANNOT update usage for workload-1's volume
	err = vm.UpdateVolumeUsage(volume.ID, "workload-2")
	if err == nil {
		t.Error("workload-2 should NOT be able to update usage for workload-1's volume")
	}
	if !IsVolumeAccessError(err) {
		t.Errorf("Expected VolumeAccessError, got: %v", err)
	}

	t.Log("✓ Access control validation successfully enforced for all operations")
}

// TestVolumeManager_AccessControl_DenyUnauthorized tests comprehensive access denial
// Satisfies CLD-REQ-052 and GO_ENGINEERING_SOP.md §8.2
func TestVolumeManager_AccessControl_DenyUnauthorized(t *testing.T) {
	vm, _, cleanup := setupVolumeManager(t)
	defer cleanup()

	// Create volumes for multiple workloads
	workloads := []string{"workload-alpha", "workload-beta", "workload-gamma"}
	volumeIDs := make(map[string]string)

	for i, workloadID := range workloads {
		req := &CreateVolumeRequest{
			VolumeID:   "vol-access-" + string(rune('a'+i)),
			Name:       "access-test",
			WorkloadID: workloadID,
			SizeBytes:  1024 * 1024,
			IOPSClass:  IOPSClassMedium,
		}

		volume, err := vm.CreateVolume(req)
		if err != nil {
			t.Fatalf("CreateVolume for %s failed: %v", workloadID, err)
		}
		volumeIDs[workloadID] = volume.ID
	}

	// Test cross-workload access denial for all operations
	operations := []struct {
		name string
		fn   func(volumeID, workloadID string) error
	}{
		{"MountVolume", func(vid, wid string) error {
			_, err := vm.MountVolume(vid, wid)
			return err
		}},
		{"UnmountVolume", func(vid, wid string) error {
			return vm.UnmountVolume(vid, wid)
		}},
		{"DeleteVolume", func(vid, wid string) error {
			return vm.DeleteVolume(vid, wid)
		}},
		{"ResizeVolume", func(vid, wid string) error {
			return vm.ResizeVolume(vid, wid, 2*1024*1024)
		}},
		{"UpdateVolumeUsage", func(vid, wid string) error {
			return vm.UpdateVolumeUsage(vid, wid)
		}},
	}

	// Test that each workload CANNOT access other workloads' volumes
	for ownerWorkload, volumeID := range volumeIDs {
		for _, callerWorkload := range workloads {
			if ownerWorkload == callerWorkload {
				continue // Skip owner's own volume
			}

			for _, op := range operations {
				err := op.fn(volumeID, callerWorkload)
				if err == nil {
					t.Errorf("%s: %s should NOT be able to access %s's volume",
						op.name, callerWorkload, ownerWorkload)
				}
				if !IsVolumeAccessError(err) {
					t.Errorf("%s: Expected VolumeAccessError for %s accessing %s's volume, got: %v",
						op.name, callerWorkload, ownerWorkload, err)
				}
			}
		}
	}

	t.Log("✓ All cross-workload access attempts correctly denied")
}

// TestVolumeManager_AccessControl_WorkloadLifecycle tests full lifecycle with access control
// Satisfies CLD-REQ-052 ephemeral volume lifecycle with security
func TestVolumeManager_AccessControl_WorkloadLifecycle(t *testing.T) {
	vm, _, cleanup := setupVolumeManager(t)
	defer cleanup()

	// Workload lifecycle simulation
	workloadID := "workload-lifecycle"

	// 1. Create volume
	req := &CreateVolumeRequest{
		VolumeID:   "vol-lifecycle",
		Name:       "lifecycle-test",
		WorkloadID: workloadID,
		SizeBytes:  5 * 1024 * 1024,
		IOPSClass:  IOPSClassHigh,
	}

	volume, err := vm.CreateVolume(req)
	if err != nil {
		t.Fatalf("CreateVolume failed: %v", err)
	}

	// 2. Mount volume
	mountPath, err := vm.MountVolume(volume.ID, workloadID)
	if err != nil {
		t.Fatalf("MountVolume failed: %v", err)
	}
	if mountPath == "" {
		t.Error("MountPath should not be empty")
	}

	// 3. Verify another workload cannot access
	_, err = vm.MountVolume(volume.ID, "workload-intruder")
	if err == nil || !IsVolumeAccessError(err) {
		t.Error("Intruder should not be able to access volume during lifecycle")
	}

	// 4. Resize volume (authorized)
	newSize := int64(10 * 1024 * 1024)
	err = vm.ResizeVolume(volume.ID, workloadID, newSize)
	if err != nil {
		t.Fatalf("ResizeVolume (authorized) failed: %v", err)
	}

	vol, _ := vm.GetVolume(volume.ID)
	if vol.SizeBytes != newSize {
		t.Errorf("Expected size %d, got %d", newSize, vol.SizeBytes)
	}

	// 5. Verify intruder cannot resize
	err = vm.ResizeVolume(volume.ID, "workload-intruder", 20*1024*1024)
	if err == nil || !IsVolumeAccessError(err) {
		t.Error("Intruder should not be able to resize volume")
	}

	// 6. Update usage (authorized)
	err = vm.UpdateVolumeUsage(volume.ID, workloadID)
	if err != nil {
		t.Fatalf("UpdateVolumeUsage (authorized) failed: %v", err)
	}

	// 7. Unmount volume (authorized)
	err = vm.UnmountVolume(volume.ID, workloadID)
	if err != nil {
		t.Fatalf("UnmountVolume (authorized) failed: %v", err)
	}

	// 8. Verify intruder cannot delete
	err = vm.DeleteVolume(volume.ID, "workload-intruder")
	if err == nil || !IsVolumeAccessError(err) {
		t.Error("Intruder should not be able to delete volume")
	}

	// 9. Delete volume (authorized)
	err = vm.DeleteVolume(volume.ID, workloadID)
	if err != nil {
		t.Fatalf("DeleteVolume (authorized) failed: %v", err)
	}

	// 10. Verify volume fully removed
	_, err = vm.GetVolume(volume.ID)
	if err == nil {
		t.Error("Volume should be removed after deletion")
	}

	t.Log("✓ Complete workload lifecycle with access control validation successful")
}
