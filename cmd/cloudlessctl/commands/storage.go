package commands

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/cloudless/cloudless/pkg/api"
	cmdconfig "github.com/cloudless/cloudless/cmd/cloudlessctl/config"
	"github.com/spf13/cobra"
	"google.golang.org/protobuf/types/known/emptypb"
)

// NewStorageCommand creates the storage command
func NewStorageCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "storage",
		Short: "Manage storage resources",
		Long:  "Manage storage resources including buckets, objects, and volumes",
	}

	cmd.AddCommand(newStorageBucketCommand())
	cmd.AddCommand(newStorageObjectCommand())
	cmd.AddCommand(newStorageVolumeCommand())

	return cmd
}

// newStorageBucketCommand creates the bucket command
func newStorageBucketCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "bucket",
		Short: "Manage storage buckets",
	}

	listCmd := &cobra.Command{
		Use:   "list",
		Short: "List buckets",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runBucketList(cmd, args)
		},
	}
	listCmd.Flags().StringP("output", "o", "table", "Output format (table, json, yaml)")
	cmd.AddCommand(listCmd)

	createCmd := &cobra.Command{
		Use:   "create NAME",
		Short: "Create a bucket",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runBucketCreate(cmd, args)
		},
	}
	createCmd.Flags().String("region", "", "Bucket region")
	createCmd.Flags().String("storage-class", "standard", "Storage class (standard, infrequent, archive)")
	cmd.AddCommand(createCmd)

	deleteCmd := &cobra.Command{
		Use:   "delete NAME",
		Short: "Delete a bucket",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runBucketDelete(cmd, args)
		},
	}
	deleteCmd.Flags().BoolP("force", "f", false, "Force delete without confirmation")
	cmd.AddCommand(deleteCmd)

	return cmd
}

// newStorageObjectCommand creates the object command
func newStorageObjectCommand() *cobra.Command{
	cmd := &cobra.Command{
		Use:   "object",
		Short: "Manage storage objects",
	}

	listObjCmd := &cobra.Command{
		Use:   "list BUCKET",
		Short: "List objects in bucket",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runObjectList(cmd, args)
		},
	}
	listObjCmd.Flags().StringP("output", "o", "table", "Output format (table, json, yaml)")
	listObjCmd.Flags().String("prefix", "", "Filter objects by prefix")
	cmd.AddCommand(listObjCmd)

	getCmd := &cobra.Command{
		Use:   "get BUCKET/OBJECT",
		Short: "Get an object",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runObjectGet(cmd, args)
		},
	}
	getCmd.Flags().StringP("output", "o", "", "Output file path (defaults to object name)")
	cmd.AddCommand(getCmd)

	putCmd := &cobra.Command{
		Use:   "put BUCKET/OBJECT FILE",
		Short: "Put an object",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runObjectPut(cmd, args)
		},
	}
	putCmd.Flags().String("content-type", "", "Content type")
	cmd.AddCommand(putCmd)

	return cmd
}

// newStorageVolumeCommand creates the volume command
func newStorageVolumeCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "volume",
		Short: "Manage volumes",
	}

	listVolCmd := &cobra.Command{
		Use:   "list",
		Short: "List volumes",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runVolumeList(cmd, args)
		},
	}
	listVolCmd.Flags().StringP("output", "o", "table", "Output format (table, json, yaml)")
	cmd.AddCommand(listVolCmd)

	createVolCmd := &cobra.Command{
		Use:   "create NAME",
		Short: "Create a volume",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runVolumeCreate(cmd, args)
		},
	}
	createVolCmd.Flags().Int64("size", 10*1024*1024*1024, "Volume size in bytes (default 10GB)")
	createVolCmd.Flags().String("type", "standard", "Volume type (standard, ssd, nvme)")
	cmd.AddCommand(createVolCmd)

	return cmd
}
// Bucket command implementations

func runBucketList(cmd *cobra.Command, args []string) error {
	cfg, err := cmdconfig.LoadConfig(cmd)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	client, conn, err := cfg.NewStorageClient()
	if err != nil {
		return fmt.Errorf("failed to create client: %w", err)
	}
	defer conn.Close()

	ctx := context.Background()
	resp, err := client.ListBuckets(ctx, &api.ListBucketsRequest{})
	if err != nil {
		return fmt.Errorf("failed to list buckets: %w", err)
	}

	output, _ := cmd.Flags().GetString("output")
	outputter := cmdconfig.NewOutputter(output)

	if output == "table" {
		if len(resp.Buckets) == 0 {
			fmt.Println("No buckets found")
			return nil
		}

		headers := []string{"NAME", "REGION", "STORAGE CLASS", "CREATED"}
		var rows [][]string
		for _, bucket := range resp.Buckets {
			rows = append(rows, []string{
				bucket.Name,
				bucket.Region,
				bucket.StorageClass,
				bucket.CreatedAt.AsTime().Format("2006-01-02 15:04:05"),
			})
		}
		outputter.PrintTable(headers, rows)
	} else {
		outputter.Print(resp.Buckets)
	}

	return nil
}

func runBucketCreate(cmd *cobra.Command, args []string) error {
	cfg, err := cmdconfig.LoadConfig(cmd)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	client, conn, err := cfg.NewStorageClient()
	if err != nil {
		return fmt.Errorf("failed to create client: %w", err)
	}
	defer conn.Close()

	region, _ := cmd.Flags().GetString("region")
	storageClass, _ := cmd.Flags().GetString("storage-class")

	ctx := context.Background()
	bucket, err := client.CreateBucket(ctx, &api.CreateBucketRequest{
		Name:         args[0],
		Region:       region,
		StorageClass: storageClass,
	})
	if err != nil {
		return fmt.Errorf("failed to create bucket: %w", err)
	}

	fmt.Printf("Bucket '%s' created successfully\n", bucket.Name)
	return nil
}

func runBucketDelete(cmd *cobra.Command, args []string) error {
	force, _ := cmd.Flags().GetBool("force")
	bucketName := args[0]

	if !force {
		fmt.Printf("Are you sure you want to delete bucket '%s'? (y/N): ", bucketName)
		var response string
		fmt.Scanln(&response)
		if strings.ToLower(response) != "y" {
			fmt.Println("Aborted")
			return nil
		}
	}

	cfg, err := cmdconfig.LoadConfig(cmd)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	client, conn, err := cfg.NewStorageClient()
	if err != nil {
		return fmt.Errorf("failed to create client: %w", err)
	}
	defer conn.Close()

	ctx := context.Background()
	_, err = client.DeleteBucket(ctx, &api.DeleteBucketRequest{
		Name: bucketName,
	})
	if err != nil {
		return fmt.Errorf("failed to delete bucket: %w", err)
	}

	fmt.Printf("Bucket '%s' deleted successfully\n", bucketName)
	return nil
}

// Object command implementations

func runObjectList(cmd *cobra.Command, args []string) error {
	cfg, err := cmdconfig.LoadConfig(cmd)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	client, conn, err := cfg.NewStorageClient()
	if err != nil {
		return fmt.Errorf("failed to create client: %w", err)
	}
	defer conn.Close()

	prefix, _ := cmd.Flags().GetString("prefix")

	ctx := context.Background()
	resp, err := client.ListObjects(ctx, &api.ListObjectsRequest{
		Bucket: args[0],
		Prefix: prefix,
	})
	if err != nil {
		return fmt.Errorf("failed to list objects: %w", err)
	}

	output, _ := cmd.Flags().GetString("output")
	outputter := cmdconfig.NewOutputter(output)

	if output == "table" {
		if len(resp.Objects) == 0 {
			fmt.Println("No objects found")
			return nil
		}

		headers := []string{"KEY", "SIZE", "MODIFIED"}
		var rows [][]string
		for _, obj := range resp.Objects {
			rows = append(rows, []string{
				obj.Key,
				fmt.Sprintf("%d", obj.Size),
				obj.LastModified.AsTime().Format("2006-01-02 15:04:05"),
			})
		}
		outputter.PrintTable(headers, rows)
	} else {
		outputter.Print(resp.Objects)
	}

	return nil
}

func runObjectGet(cmd *cobra.Command, args []string) error {
	// Parse bucket/object
	parts := strings.SplitN(args[0], "/", 2)
	if len(parts) != 2 {
		return fmt.Errorf("invalid format, expected BUCKET/OBJECT")
	}
	bucket, key := parts[0], parts[1]

	outputPath, _ := cmd.Flags().GetString("output")
	if outputPath == "" {
		outputPath = filepath.Base(key)
	}

	cfg, err := cmdconfig.LoadConfig(cmd)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	client, conn, err := cfg.NewStorageClient()
	if err != nil {
		return fmt.Errorf("failed to create client: %w", err)
	}
	defer conn.Close()

	ctx := context.Background()
	stream, err := client.GetObject(ctx, &api.GetObjectRequest{
		Bucket: bucket,
		Key:    key,
	})
	if err != nil {
		return fmt.Errorf("failed to get object: %w", err)
	}

	// Create output file
	outFile, err := os.Create(outputPath)
	if err != nil {
		return fmt.Errorf("failed to create output file: %w", err)
	}
	defer outFile.Close()

	// Stream object data
	for {
		chunk, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("failed to receive chunk: %w", err)
		}

		if _, err := outFile.Write(chunk.Data); err != nil {
			return fmt.Errorf("failed to write data: %w", err)
		}
	}

	fmt.Printf("Object downloaded to '%s'\n", outputPath)
	return nil
}

func runObjectPut(cmd *cobra.Command, args []string) error {
	// Parse bucket/object
	parts := strings.SplitN(args[0], "/", 2)
	if len(parts) != 2 {
		return fmt.Errorf("invalid format, expected BUCKET/OBJECT")
	}
	bucket, key := parts[0], parts[1]
	filePath := args[1]

	contentType, _ := cmd.Flags().GetString("content-type")

	// Open input file
	inFile, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("failed to open file: %w", err)
	}
	defer inFile.Close()

	fileInfo, err := inFile.Stat()
	if err != nil {
		return fmt.Errorf("failed to stat file: %w", err)
	}

	cfg, err := cmdconfig.LoadConfig(cmd)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	client, conn, err := cfg.NewStorageClient()
	if err != nil {
		return fmt.Errorf("failed to create client: %w", err)
	}
	defer conn.Close()

	ctx := context.Background()
	stream, err := client.PutObject(ctx)
	if err != nil {
		return fmt.Errorf("failed to start upload: %w", err)
	}

	// Send metadata
	err = stream.Send(&api.PutObjectRequest{
		Data: &api.PutObjectRequest_Metadata{
			Metadata: &api.ObjectMetadata{
				Bucket:      bucket,
				Key:         key,
				Size:        fileInfo.Size(),
				ContentType: contentType,
			},
		},
	})
	if err != nil {
		return fmt.Errorf("failed to send metadata: %w", err)
	}

	// Stream file data in chunks
	buf := make([]byte, 64*1024) // 64KB chunks
	for {
		n, err := inFile.Read(buf)
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("failed to read file: %w", err)
		}

		err = stream.Send(&api.PutObjectRequest{
			Data: &api.PutObjectRequest_Chunk{
				Chunk: buf[:n],
			},
		})
		if err != nil {
			return fmt.Errorf("failed to send chunk: %w", err)
		}
	}

	_, err = stream.CloseAndRecv()
	if err != nil {
		return fmt.Errorf("failed to complete upload: %w", err)
	}

	fmt.Printf("Object '%s/%s' uploaded successfully\n", bucket, key)
	return nil
}

// Volume command implementations

func runVolumeList(cmd *cobra.Command, args []string) error {
	cfg, err := cmdconfig.LoadConfig(cmd)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	client, conn, err := cfg.NewStorageClient()
	if err != nil {
		return fmt.Errorf("failed to create client: %w", err)
	}
	defer conn.Close()

	ctx := context.Background()
	resp, err := client.ListVolumes(ctx, &api.ListVolumesRequest{})
	if err != nil {
		return fmt.Errorf("failed to list volumes: %w", err)
	}

	output, _ := cmd.Flags().GetString("output")
	outputter := cmdconfig.NewOutputter(output)

	if output == "table" {
		if len(resp.Volumes) == 0 {
			fmt.Println("No volumes found")
			return nil
		}

		headers := []string{"NAME", "SIZE", "TYPE", "STATE", "CREATED"}
		var rows [][]string
		for _, vol := range resp.Volumes {
			rows = append(rows, []string{
				vol.Name,
				fmt.Sprintf("%d GB", vol.Size/(1024*1024*1024)),
				vol.VolumeType,
				vol.State.String(),
				vol.CreatedAt.AsTime().Format("2006-01-02 15:04:05"),
			})
		}
		outputter.PrintTable(headers, rows)
	} else {
		outputter.Print(resp.Volumes)
	}

	return nil
}

func runVolumeCreate(cmd *cobra.Command, args []string) error {
	cfg, err := cmdconfig.LoadConfig(cmd)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	client, conn, err := cfg.NewStorageClient()
	if err != nil {
		return fmt.Errorf("failed to create client: %w", err)
	}
	defer conn.Close()

	size, _ := cmd.Flags().GetInt64("size")
	volType, _ := cmd.Flags().GetString("type")

	ctx := context.Background()
	volume, err := client.CreateVolume(ctx, &api.CreateVolumeRequest{
		Name:       args[0],
		Size:       size,
		VolumeType: volType,
	})
	if err != nil {
		return fmt.Errorf("failed to create volume: %w", err)
	}

	fmt.Printf("Volume '%s' created successfully (%d GB)\n", volume.Name, volume.Size/(1024*1024*1024))
	return nil
}
