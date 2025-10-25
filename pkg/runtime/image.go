package runtime

import (
	"context"
	"fmt"

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/images"
	"github.com/containerd/errdefs"
	"go.uber.org/zap"
)

// PullImage pulls an image from a registry
func (r *ContainerdRuntime) PullImage(ctx context.Context, imageRef string) error {
	ctx = r.withNamespace(ctx)

	r.logger.Info("Pulling image", zap.String("image", imageRef))

	// Pull image
	image, err := r.client.Pull(ctx, imageRef,
		containerd.WithPullUnpack,
	)
	if err != nil {
		return fmt.Errorf("failed to pull image: %w", err)
	}

	r.logger.Info("Image pulled successfully",
		zap.String("image", imageRef),
		zap.String("digest", image.Target().Digest.String()),
	)

	return nil
}

// ListImages lists all images
func (r *ContainerdRuntime) ListImages(ctx context.Context) ([]*Image, error) {
	ctx = r.withNamespace(ctx)

	imageList, err := r.client.ListImages(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list images: %w", err)
	}

	result := make([]*Image, 0, len(imageList))
	for _, img := range imageList {
		info := &Image{
			Name:      img.Name(),
			Digest:    img.Target().Digest.String(),
			CreatedAt: img.Metadata().CreatedAt,
		}

		// Get image size
		size, err := img.Size(ctx)
		if err == nil {
			info.Size = size
		}

		// Parse name and tag
		// Simple parsing, in production use proper OCI reference parser
		if len(img.Name()) > 0 {
			info.Name = img.Name()
			// Extract tag if present
			// This is simplified - real implementation should use proper parsing
		}

		result = append(result, info)
	}

	return result, nil
}

// DeleteImage deletes an image
func (r *ContainerdRuntime) DeleteImage(ctx context.Context, imageRef string) error {
	ctx = r.withNamespace(ctx)

	r.logger.Info("Deleting image", zap.String("image", imageRef))

	image, err := r.client.ImageService().Get(ctx, imageRef)
	if err != nil {
		return fmt.Errorf("failed to get image: %w", err)
	}

	err = r.client.ImageService().Delete(ctx, image.Name, images.SynchronousDelete())
	if err != nil {
		return fmt.Errorf("failed to delete image: %w", err)
	}

	r.logger.Info("Image deleted successfully", zap.String("image", imageRef))

	return nil
}

// GetImage gets information about an image
func (r *ContainerdRuntime) GetImage(ctx context.Context, imageRef string) (*Image, error) {
	ctx = r.withNamespace(ctx)

	img, err := r.client.GetImage(ctx, imageRef)
	if err != nil {
		return nil, fmt.Errorf("failed to get image: %w", err)
	}

	info := &Image{
		Name:      img.Name(),
		Digest:    img.Target().Digest.String(),
		CreatedAt: img.Metadata().CreatedAt,
	}

	// Get image size
	size, err := img.Size(ctx)
	if err == nil {
		info.Size = size
	}

	return info, nil
}

// ImageExists checks if an image exists
func (r *ContainerdRuntime) ImageExists(ctx context.Context, imageRef string) (bool, error) {
	ctx = r.withNamespace(ctx)

	_, err := r.client.GetImage(ctx, imageRef)
	if err != nil {
		if errdefs.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}
