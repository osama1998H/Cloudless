package runtime

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/osama1998H/Cloudless/pkg/api"
	"go.uber.org/zap"
)

// CLD-REQ-063: Secret Injection for Workloads
// This file implements secret injection into container environments

// SecretsRetriever defines the interface for retrieving secrets
// This allows the runtime to be decoupled from the agent's secrets client
type SecretsRetriever interface {
	GetSecret(ctx context.Context, namespace, name, audience string) (map[string][]byte, error)
}

// InjectSecretsIntoSpec injects secrets into a ContainerSpec based on workload configuration
// This processes both environment variable injection and volume mounts
func InjectSecretsIntoSpec(
	ctx context.Context,
	spec *ContainerSpec,
	secretEnvSources []*api.SecretEnvSource,
	secretVolumeSources []*api.SecretVolumeSource,
	retriever SecretsRetriever,
	workloadNamespace string,
	logger *zap.Logger,
) error {
	// Process secret environment variables
	if len(secretEnvSources) > 0 {
		if err := injectSecretEnvVars(ctx, spec, secretEnvSources, retriever, workloadNamespace, logger); err != nil {
			return fmt.Errorf("failed to inject secret environment variables: %w", err)
		}
	}

	// Process secret volume mounts
	if len(secretVolumeSources) > 0 {
		if err := injectSecretVolumes(ctx, spec, secretVolumeSources, retriever, workloadNamespace, logger); err != nil {
			return fmt.Errorf("failed to inject secret volumes: %w", err)
		}
	}

	return nil
}

// injectSecretEnvVars retrieves secrets and injects them as environment variables
func injectSecretEnvVars(
	ctx context.Context,
	spec *ContainerSpec,
	secretEnvSources []*api.SecretEnvSource,
	retriever SecretsRetriever,
	defaultNamespace string,
	logger *zap.Logger,
) error {
	if spec.Env == nil {
		spec.Env = make(map[string]string)
	}

	for _, secretEnv := range secretEnvSources {
		namespace := secretEnv.Namespace
		if namespace == "" {
			namespace = defaultNamespace
		}

		logger.Info("Injecting secret as environment variables",
			zap.String("secret", secretEnv.SecretName),
			zap.String("namespace", namespace),
			zap.String("audience", secretEnv.Audience),
		)

		// Retrieve secret data
		secretData, err := retriever.GetSecret(ctx, namespace, secretEnv.SecretName, secretEnv.Audience)
		if err != nil {
			return fmt.Errorf("failed to retrieve secret %s/%s: %w", namespace, secretEnv.SecretName, err)
		}

		// Map secret keys to environment variables
		if len(secretEnv.KeyToEnv) > 0 {
			// Use explicit key-to-env mapping
			for secretKey, envVar := range secretEnv.KeyToEnv {
				if data, exists := secretData[secretKey]; exists {
					spec.Env[envVar] = string(data)
					logger.Debug("Mapped secret key to env var",
						zap.String("secret_key", secretKey),
						zap.String("env_var", envVar),
					)
				} else {
					logger.Warn("Secret key not found in secret data",
						zap.String("secret", secretEnv.SecretName),
						zap.String("key", secretKey),
					)
				}
			}
		} else {
			// Inject all keys with their original names
			for key, value := range secretData {
				spec.Env[key] = string(value)
				logger.Debug("Injected secret key as env var",
					zap.String("key", key),
				)
			}
		}
	}

	return nil
}

// injectSecretVolumes retrieves secrets and mounts them as files
func injectSecretVolumes(
	ctx context.Context,
	spec *ContainerSpec,
	secretVolumeSources []*api.SecretVolumeSource,
	retriever SecretsRetriever,
	defaultNamespace string,
	logger *zap.Logger,
) error {
	for _, secretVolume := range secretVolumeSources {
		namespace := secretVolume.Namespace
		if namespace == "" {
			namespace = defaultNamespace
		}

		logger.Info("Mounting secret as volume",
			zap.String("secret", secretVolume.SecretName),
			zap.String("namespace", namespace),
			zap.String("mount_path", secretVolume.MountPath),
		)

		// Retrieve secret data
		secretData, err := retriever.GetSecret(ctx, namespace, secretVolume.SecretName, secretVolume.Audience)
		if err != nil {
			return fmt.Errorf("failed to retrieve secret %s/%s: %w", namespace, secretVolume.SecretName, err)
		}

		// Create temporary directory for secret files
		// In production, this should be a secure tmpfs mount with proper permissions
		secretDir := fmt.Sprintf("/tmp/cloudless-secrets/%s-%s", namespace, secretVolume.SecretName)
		if err := os.MkdirAll(secretDir, 0700); err != nil {
			return fmt.Errorf("failed to create secret directory: %w", err)
		}

		// Write secret data to files
		filesToMount := make(map[string]string) // secretKey -> filePath

		if len(secretVolume.KeyToFile) > 0 {
			// Use explicit key-to-file mapping
			for secretKey, fileName := range secretVolume.KeyToFile {
				if data, exists := secretData[secretKey]; exists {
					filePath := filepath.Join(secretDir, fileName)
					if err := writeSecretFile(filePath, data, secretVolume.Mode); err != nil {
						return fmt.Errorf("failed to write secret file %s: %w", fileName, err)
					}
					filesToMount[secretKey] = filePath
					logger.Debug("Wrote secret key to file",
						zap.String("secret_key", secretKey),
						zap.String("file", fileName),
					)
				} else {
					logger.Warn("Secret key not found in secret data",
						zap.String("secret", secretVolume.SecretName),
						zap.String("key", secretKey),
					)
				}
			}
		} else {
			// Mount all keys with their original names
			for key, value := range secretData {
				filePath := filepath.Join(secretDir, key)
				if err := writeSecretFile(filePath, value, secretVolume.Mode); err != nil {
					return fmt.Errorf("failed to write secret file %s: %w", key, err)
				}
				filesToMount[key] = filePath
				logger.Debug("Wrote secret key to file",
					zap.String("key", key),
				)
			}
		}

		// Add bind mount for the secret directory
		// The entire directory is mounted at the specified mount path
		mount := Mount{
			Source:      secretDir,
			Destination: secretVolume.MountPath,
			Type:        "bind",
			Options:     []string{"ro", "rbind"}, // Read-only bind mount
		}
		spec.Mounts = append(spec.Mounts, mount)

		logger.Info("Added secret volume mount",
			zap.String("source", secretDir),
			zap.String("destination", secretVolume.MountPath),
			zap.Int("files", len(filesToMount)),
		)
	}

	return nil
}

// writeSecretFile writes secret data to a file with the specified mode
func writeSecretFile(path string, data []byte, mode int32) error {
	// Use 0400 (read-only) as default if mode is not specified or invalid
	fileMode := os.FileMode(0400)
	if mode > 0 && mode <= 0777 {
		fileMode = os.FileMode(mode)
	}

	// Write file with restricted permissions
	if err := os.WriteFile(path, data, fileMode); err != nil {
		return fmt.Errorf("failed to write file: %w", err)
	}

	return nil
}

// CleanupSecretVolumes removes temporary secret directories after container stops
// This should be called when cleaning up a container
func CleanupSecretVolumes(secretVolumeSources []*api.SecretVolumeSource, namespace string, logger *zap.Logger) error {
	for _, secretVolume := range secretVolumeSources {
		ns := secretVolume.Namespace
		if ns == "" {
			ns = namespace
		}

		secretDir := fmt.Sprintf("/tmp/cloudless-secrets/%s-%s", ns, secretVolume.SecretName)
		if err := os.RemoveAll(secretDir); err != nil {
			logger.Warn("Failed to cleanup secret directory",
				zap.String("dir", secretDir),
				zap.Error(err),
			)
			// Don't fail the cleanup if one directory fails
			continue
		}

		logger.Debug("Cleaned up secret directory",
			zap.String("dir", secretDir),
		)
	}

	return nil
}
