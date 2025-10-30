package coordinator

import (
	"context"
	"strings"
	"time"

	"github.com/osama1998H/Cloudless/pkg/api"
	"github.com/osama1998H/Cloudless/pkg/secrets"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// SecretsServiceServer implements the gRPC SecretsService
type SecretsServiceServer struct {
	api.UnimplementedSecretsServiceServer
	coordinator *Coordinator
	logger      *zap.Logger
}

// NewSecretsServiceServer creates a new secrets service server
func NewSecretsServiceServer(coordinator *Coordinator) *SecretsServiceServer {
	return &SecretsServiceServer{
		coordinator: coordinator,
		logger:      coordinator.logger,
	}
}

// CreateSecret creates a new secret
func (s *SecretsServiceServer) CreateSecret(ctx context.Context, req *api.CreateSecretRequest) (*api.SecretResponse, error) {
	// Check if secrets manager is enabled
	if s.coordinator.secretsManager == nil {
		return nil, status.Error(codes.Unavailable, "secrets service is not enabled")
	}

	// Only leader can write
	if !s.coordinator.IsLeader() {
		return nil, status.Errorf(codes.FailedPrecondition, "not the leader, redirect to: %s", s.coordinator.GetLeader())
	}

	// Validate request
	if req.Name == "" {
		return nil, status.Error(codes.InvalidArgument, "secret name is required")
	}
	if req.Namespace == "" {
		return nil, status.Error(codes.InvalidArgument, "namespace is required")
	}
	if len(req.Data) == 0 {
		return nil, status.Error(codes.InvalidArgument, "secret data cannot be empty")
	}

	// Convert proto data to internal format
	data := make(map[string][]byte, len(req.Data))
	for k, v := range req.Data {
		data[k] = v
	}

	// Build secret options
	var opts []secrets.SecretOption

	if len(req.Audiences) > 0 {
		opts = append(opts, secrets.WithAudiences(req.Audiences))
	}

	if len(req.Labels) > 0 {
		opts = append(opts, secrets.WithLabels(req.Labels))
	}

	if req.TtlSeconds > 0 {
		opts = append(opts, secrets.WithTTL(time.Duration(req.TtlSeconds)*time.Second))
	}

	if req.Immutable {
		opts = append(opts, secrets.WithImmutable())
	}

	if req.ExpiresAt != nil {
		opts = append(opts, secrets.WithExpiration(req.ExpiresAt.AsTime()))
	}

	// Create secret
	secret, err := s.coordinator.secretsManager.CreateSecret(req.Namespace, req.Name, data, opts...)
	if err != nil {
		s.logger.Error("Failed to create secret",
			zap.String("namespace", req.Namespace),
			zap.String("name", req.Name),
			zap.Error(err),
		)
		// Map specific errors to gRPC codes
		if strings.Contains(err.Error(), "already exists") {
			return nil, status.Errorf(codes.AlreadyExists, "secret already exists: %s/%s", req.Namespace, req.Name)
		}
		return nil, status.Errorf(codes.Internal, "failed to create secret: %v", err)
	}

	s.logger.Info("Secret created",
		zap.String("namespace", req.Namespace),
		zap.String("name", req.Name),
		zap.Uint64("version", secret.Version),
	)

	// Convert to proto response
	return secretToProto(secret), nil
}

// GetSecret retrieves a secret (requires access token)
func (s *SecretsServiceServer) GetSecret(ctx context.Context, req *api.GetSecretRequest) (*api.GetSecretResponse, error) {
	// Check if secrets manager is enabled
	if s.coordinator.secretsManager == nil {
		return nil, status.Error(codes.Unavailable, "secrets service is not enabled")
	}

	// Validate request
	if req.Name == "" {
		return nil, status.Error(codes.InvalidArgument, "secret name is required")
	}
	if req.Namespace == "" {
		return nil, status.Error(codes.InvalidArgument, "namespace is required")
	}
	if req.AccessToken == "" {
		return nil, status.Error(codes.Unauthenticated, "access token is required")
	}
	if req.Audience == "" {
		return nil, status.Error(codes.InvalidArgument, "audience is required")
	}

	// Build access request
	accessReq := secrets.SecretAccessRequest{
		SecretName:  req.Name,
		Namespace:   req.Namespace,
		Audience:    req.Audience,
		AccessToken: req.AccessToken,
	}

	// Get secret
	response, err := s.coordinator.secretsManager.GetSecret(accessReq)
	if err != nil {
		s.logger.Warn("Failed to get secret",
			zap.String("namespace", req.Namespace),
			zap.String("name", req.Name),
			zap.String("audience", req.Audience),
			zap.Error(err),
		)
		// Map specific errors to gRPC codes
		errMsg := err.Error()
		if strings.Contains(errMsg, "not found") {
			return nil, status.Errorf(codes.NotFound, "secret not found: %s/%s", req.Namespace, req.Name)
		}
		if strings.Contains(errMsg, "invalid token") || strings.Contains(errMsg, "expired") {
			return nil, status.Error(codes.Unauthenticated, "invalid or expired access token")
		}
		if strings.Contains(errMsg, "permission denied") || strings.Contains(errMsg, "audience") {
			return nil, status.Errorf(codes.PermissionDenied, "access denied: %v", err)
		}
		return nil, status.Errorf(codes.Internal, "failed to get secret: %v", err)
	}

	// Convert to proto response
	metadata := &api.SecretMetadata{
		Name:      response.Name,
		Namespace: response.Namespace,
		Version:   response.Version,
	}

	if response.ExpiresAt != nil {
		metadata.ExpiresAt = timestamppb.New(*response.ExpiresAt)
	}

	protoResp := &api.GetSecretResponse{
		Metadata: metadata,
		Data:     response.Data,
	}

	s.logger.Debug("Secret accessed",
		zap.String("namespace", req.Namespace),
		zap.String("name", req.Name),
		zap.String("audience", req.Audience),
	)

	return protoResp, nil
}

// UpdateSecret updates an existing secret
func (s *SecretsServiceServer) UpdateSecret(ctx context.Context, req *api.UpdateSecretRequest) (*api.SecretResponse, error) {
	// Check if secrets manager is enabled
	if s.coordinator.secretsManager == nil {
		return nil, status.Error(codes.Unavailable, "secrets service is not enabled")
	}

	// Only leader can write
	if !s.coordinator.IsLeader() {
		return nil, status.Errorf(codes.FailedPrecondition, "not the leader, redirect to: %s", s.coordinator.GetLeader())
	}

	// Validate request
	if req.Name == "" {
		return nil, status.Error(codes.InvalidArgument, "secret name is required")
	}
	if req.Namespace == "" {
		return nil, status.Error(codes.InvalidArgument, "namespace is required")
	}
	if len(req.Data) == 0 {
		return nil, status.Error(codes.InvalidArgument, "secret data cannot be empty")
	}

	// Convert proto data to internal format
	data := make(map[string][]byte, len(req.Data))
	for k, v := range req.Data {
		data[k] = v
	}

	// Update secret (note: UpdateSecret currently doesn't support options like audiences)
	// TODO(osama): Extend UpdateSecret to support audience and label updates. See issue #17.
	// Current implementation only updates secret data. To fully support secret lifecycle
	// management, we need UpdateSecretOptions with AudiencesToAdd/Remove and LabelsToUpdate.
	secret, err := s.coordinator.secretsManager.UpdateSecret(req.Namespace, req.Name, data)
	if err != nil {
		s.logger.Error("Failed to update secret",
			zap.String("namespace", req.Namespace),
			zap.String("name", req.Name),
			zap.Error(err),
		)
		// Map specific errors to gRPC codes
		errMsg := err.Error()
		if strings.Contains(errMsg, "not found") {
			return nil, status.Errorf(codes.NotFound, "secret not found: %s/%s", req.Namespace, req.Name)
		}
		if strings.Contains(errMsg, "immutable") {
			return nil, status.Errorf(codes.FailedPrecondition, "cannot update immutable secret: %s/%s", req.Namespace, req.Name)
		}
		return nil, status.Errorf(codes.Internal, "failed to update secret: %v", err)
	}

	s.logger.Info("Secret updated",
		zap.String("namespace", req.Namespace),
		zap.String("name", req.Name),
		zap.Uint64("version", secret.Version),
	)

	// Convert to proto response
	return secretToProto(secret), nil
}

// DeleteSecret deletes a secret
func (s *SecretsServiceServer) DeleteSecret(ctx context.Context, req *api.DeleteSecretRequest) (*emptypb.Empty, error) {
	// Check if secrets manager is enabled
	if s.coordinator.secretsManager == nil {
		return nil, status.Error(codes.Unavailable, "secrets service is not enabled")
	}

	// Only leader can write
	if !s.coordinator.IsLeader() {
		return nil, status.Errorf(codes.FailedPrecondition, "not the leader, redirect to: %s", s.coordinator.GetLeader())
	}

	// Validate request
	if req.Name == "" {
		return nil, status.Error(codes.InvalidArgument, "secret name is required")
	}
	if req.Namespace == "" {
		return nil, status.Error(codes.InvalidArgument, "namespace is required")
	}

	// Delete secret
	err := s.coordinator.secretsManager.DeleteSecret(req.Namespace, req.Name)
	if err != nil {
		s.logger.Error("Failed to delete secret",
			zap.String("namespace", req.Namespace),
			zap.String("name", req.Name),
			zap.Error(err),
		)
		// Map specific errors to gRPC codes
		if strings.Contains(err.Error(), "not found") {
			return nil, status.Errorf(codes.NotFound, "secret not found: %s/%s", req.Namespace, req.Name)
		}
		return nil, status.Errorf(codes.Internal, "failed to delete secret: %v", err)
	}

	s.logger.Info("Secret deleted",
		zap.String("namespace", req.Namespace),
		zap.String("name", req.Name),
	)

	return &emptypb.Empty{}, nil
}

// ListSecrets lists secrets in a namespace
func (s *SecretsServiceServer) ListSecrets(ctx context.Context, req *api.ListSecretsRequest) (*api.ListSecretsResponse, error) {
	// Check if secrets manager is enabled
	if s.coordinator.secretsManager == nil {
		return nil, status.Error(codes.Unavailable, "secrets service is not enabled")
	}

	// Validate request
	if req.Namespace == "" {
		return nil, status.Error(codes.InvalidArgument, "namespace is required")
	}

	// Build filter
	filter := secrets.SecretFilter{
		Namespace: req.Namespace,
		Prefix:    req.NamePrefix,
		Labels:    req.LabelSelector,
		Limit:     int(req.Limit),
		// TODO(osama): Implement continuation token support for pagination. See issue #17.
		// Need cursor-based pagination with opaque continuation tokens.
	}

	// List secrets
	secretsList, err := s.coordinator.secretsManager.ListSecrets(filter)
	if err != nil {
		s.logger.Error("Failed to list secrets",
			zap.String("namespace", req.Namespace),
			zap.Error(err),
		)
		return nil, status.Errorf(codes.Internal, "failed to list secrets: %v", err)
	}

	// Convert to proto response
	metadataList := make([]*api.SecretMetadata, len(secretsList))
	for i, metadata := range secretsList {
		protoMetadata := &api.SecretMetadata{
			Name:         metadata.Name,
			Namespace:    metadata.Namespace,
			Version:      metadata.Version,
			CreatedAt:    timestamppb.New(metadata.CreatedAt),
			UpdatedAt:    timestamppb.New(metadata.UpdatedAt),
			Labels:       metadata.Labels,
			DataKeyCount: int32(len(metadata.Keys)),
		}
		metadataList[i] = protoMetadata
	}

	// TODO(osama): Implement next_token for pagination. See issue #17.
	// Need to return continuation token for next page of results.
	return &api.ListSecretsResponse{
		Secrets: metadataList,
		// NextToken: "", // To be implemented
	}, nil
}

// GenerateAccessToken generates a short-lived access token for a secret
func (s *SecretsServiceServer) GenerateAccessToken(ctx context.Context, req *api.GenerateAccessTokenRequest) (*api.GenerateAccessTokenResponse, error) {
	// Check if secrets manager is enabled
	if s.coordinator.secretsManager == nil {
		return nil, status.Error(codes.Unavailable, "secrets service is not enabled")
	}

	// Only leader can write
	if !s.coordinator.IsLeader() {
		return nil, status.Errorf(codes.FailedPrecondition, "not the leader, redirect to: %s", s.coordinator.GetLeader())
	}

	// Validate request
	if req.Name == "" {
		return nil, status.Error(codes.InvalidArgument, "secret name is required")
	}
	if req.Namespace == "" {
		return nil, status.Error(codes.InvalidArgument, "namespace is required")
	}
	if req.Audience == "" {
		return nil, status.Error(codes.InvalidArgument, "audience is required")
	}

	// Default TTL and max uses
	ttl := 15 * time.Minute
	if req.TtlSeconds > 0 {
		ttl = time.Duration(req.TtlSeconds) * time.Second
	}

	maxUses := 1 // Single-use by default
	if req.MaxUses > 0 {
		maxUses = int(req.MaxUses)
	}

	// Generate token
	token, err := s.coordinator.secretsManager.GenerateAccessToken(req.Namespace, req.Name, req.Audience, ttl, maxUses)
	if err != nil {
		s.logger.Error("Failed to generate access token",
			zap.String("namespace", req.Namespace),
			zap.String("secret", req.Name),
			zap.String("audience", req.Audience),
			zap.Error(err),
		)
		// Map specific errors to gRPC codes
		errMsg := err.Error()
		if strings.Contains(errMsg, "not found") {
			return nil, status.Errorf(codes.NotFound, "secret not found: %s/%s", req.Namespace, req.Name)
		}
		if strings.Contains(errMsg, "audience") || strings.Contains(errMsg, "not allowed") {
			return nil, status.Errorf(codes.PermissionDenied, "audience %s not allowed for secret %s/%s", req.Audience, req.Namespace, req.Name)
		}
		return nil, status.Errorf(codes.Internal, "failed to generate access token: %v", err)
	}

	s.logger.Info("Access token generated",
		zap.String("namespace", req.Namespace),
		zap.String("secret", req.Name),
		zap.String("audience", req.Audience),
		zap.String("token_id", token.ID),
	)

	// Convert to proto response
	return &api.GenerateAccessTokenResponse{
		Token: accessTokenToProto(token),
	}, nil
}

// RevokeAccessToken revokes an access token
func (s *SecretsServiceServer) RevokeAccessToken(ctx context.Context, req *api.RevokeAccessTokenRequest) (*emptypb.Empty, error) {
	// Check if secrets manager is enabled
	if s.coordinator.secretsManager == nil {
		return nil, status.Error(codes.Unavailable, "secrets service is not enabled")
	}

	// Only leader can write
	if !s.coordinator.IsLeader() {
		return nil, status.Errorf(codes.FailedPrecondition, "not the leader, redirect to: %s", s.coordinator.GetLeader())
	}

	// Handle oneof target
	switch target := req.Target.(type) {
	case *api.RevokeAccessTokenRequest_TokenId:
		// Revoke specific token
		if target.TokenId == "" {
			return nil, status.Error(codes.InvalidArgument, "token ID is required")
		}

		// TODO(osama): Add RevokeAccessToken method to Manager or expose TokenManager. See issue #17.
		// Need to implement token blacklist in BoltDB with TTL-based cleanup.
		// For now, return unimplemented
		s.logger.Warn("Token revocation not yet implemented",
			zap.String("token_id", target.TokenId),
		)
		return nil, status.Error(codes.Unimplemented, "token revocation not yet implemented")

	case *api.RevokeAccessTokenRequest_SecretRef:
		// Revoke all tokens for secret
		if target.SecretRef == "" {
			return nil, status.Error(codes.InvalidArgument, "secret ref is required")
		}

		// TODO(osama): Add RevokeTokensForSecret method to Manager. See issue #17.
		// Need to revoke all tokens associated with a secret namespace/name.
		s.logger.Warn("Secret token revocation not yet implemented",
			zap.String("secret_ref", target.SecretRef),
		)
		return nil, status.Error(codes.Unimplemented, "secret token revocation not yet implemented")

	default:
		return nil, status.Error(codes.InvalidArgument, "must specify either token_id or secret_ref")
	}
}

// ValidateAccessToken validates an access token (without consuming it)
func (s *SecretsServiceServer) ValidateAccessToken(ctx context.Context, req *api.ValidateAccessTokenRequest) (*api.ValidateAccessTokenResponse, error) {
	// Check if secrets manager is enabled
	if s.coordinator.secretsManager == nil {
		return nil, status.Error(codes.Unavailable, "secrets service is not enabled")
	}

	// Validate request
	if req.Token == "" {
		return nil, status.Error(codes.InvalidArgument, "token is required")
	}

	// This is a validation-only check, not actual usage
	// We would need to add a ValidateTokenWithoutUsing method to the secrets manager
	// For now, return not implemented
	return nil, status.Error(codes.Unimplemented, "token validation without consumption is not yet implemented")
}

// RotateMasterKey rotates the master encryption key
func (s *SecretsServiceServer) RotateMasterKey(ctx context.Context, req *api.RotateMasterKeyRequest) (*api.RotateMasterKeyResponse, error) {
	// Check if secrets manager is enabled
	if s.coordinator.secretsManager == nil {
		return nil, status.Error(codes.Unavailable, "secrets service is not enabled")
	}

	// Only leader can write
	if !s.coordinator.IsLeader() {
		return nil, status.Errorf(codes.FailedPrecondition, "not the leader, redirect to: %s", s.coordinator.GetLeader())
	}

	s.logger.Info("Starting master key rotation")

	// Get old key info before rotation
	oldKeyInfo := s.coordinator.secretsManager.GetMasterKey()
	oldProtoKey := &api.MasterKeyInfo{
		KeyId:     oldKeyInfo.ID,
		Algorithm: oldKeyInfo.Algorithm,
		Active:    oldKeyInfo.Active,
		CreatedAt: timestamppb.New(oldKeyInfo.CreatedAt),
	}

	// Rotate master key
	err := s.coordinator.secretsManager.RotateMasterKey()
	if err != nil {
		s.logger.Error("Failed to rotate master key", zap.Error(err))
		return nil, status.Errorf(codes.Internal, "failed to rotate master key: %v", err)
	}

	// Get new key info after rotation
	newKeyInfo := s.coordinator.secretsManager.GetMasterKey()
	newProtoKey := &api.MasterKeyInfo{
		KeyId:     newKeyInfo.ID,
		Algorithm: newKeyInfo.Algorithm,
		Active:    newKeyInfo.Active,
		CreatedAt: timestamppb.New(newKeyInfo.CreatedAt),
	}

	s.logger.Info("Master key rotated successfully")

	// TODO(osama): Track number of secrets re-encrypted during rotation. See issue #17.
	// Add Prometheus metrics and progress logging for re-encryption operations.
	return &api.RotateMasterKeyResponse{
		OldKey:             oldProtoKey,
		NewKey:             newProtoKey,
		SecretsReEncrypted: 0, // To be implemented
	}, nil
}

// GetMasterKeyInfo returns information about the current master key (without key material)
func (s *SecretsServiceServer) GetMasterKeyInfo(ctx context.Context, req *api.GetMasterKeyInfoRequest) (*api.MasterKeyInfo, error) {
	// Check if secrets manager is enabled
	if s.coordinator.secretsManager == nil {
		return nil, status.Error(codes.Unavailable, "secrets service is not enabled")
	}

	// Get current master key info
	keyInfo := s.coordinator.secretsManager.GetMasterKey()

	return &api.MasterKeyInfo{
		KeyId:     keyInfo.ID,
		Algorithm: keyInfo.Algorithm,
		Active:    keyInfo.Active,
		CreatedAt: timestamppb.New(keyInfo.CreatedAt),
	}, nil
}

// GetSecretAuditLog retrieves audit log entries for secrets
func (s *SecretsServiceServer) GetSecretAuditLog(ctx context.Context, req *api.GetSecretAuditLogRequest) (*api.GetSecretAuditLogResponse, error) {
	// Check if secrets manager is enabled
	if s.coordinator.secretsManager == nil {
		return nil, status.Error(codes.Unavailable, "secrets service is not enabled")
	}

	// Determine limit
	limit := 100 // Default limit
	if req.Limit > 0 {
		limit = int(req.Limit)
	}

	// Get audit log (Manager.GetAuditLog only takes limit parameter)
	entries := s.coordinator.secretsManager.GetAuditLog(limit)

	// Filter by secret_ref if specified
	var filteredEntries []secrets.SecretAuditEntry
	if req.SecretRef != "" {
		for _, entry := range entries {
			if entry.SecretRef == req.SecretRef {
				filteredEntries = append(filteredEntries, entry)
			}
		}
	} else {
		filteredEntries = entries
	}

	// Filter by time range if specified
	if req.StartTime != nil || req.EndTime != nil {
		var timeFilteredEntries []secrets.SecretAuditEntry
		for _, entry := range filteredEntries {
			includeEntry := true
			if req.StartTime != nil && entry.Timestamp.Before(req.StartTime.AsTime()) {
				includeEntry = false
			}
			if req.EndTime != nil && entry.Timestamp.After(req.EndTime.AsTime()) {
				includeEntry = false
			}
			if includeEntry {
				timeFilteredEntries = append(timeFilteredEntries, entry)
			}
		}
		filteredEntries = timeFilteredEntries
	}

	// Convert to proto response
	protoEntries := make([]*api.SecretAuditEntry, len(filteredEntries))
	for i, entry := range filteredEntries {
		protoEntries[i] = &api.SecretAuditEntry{
			Timestamp: timestamppb.New(entry.Timestamp),
			Action:    string(entry.Action),
			SecretRef: entry.SecretRef,
			Actor:     entry.Actor,
			Success:   entry.Success,
			Reason:    entry.Reason,
		}
	}

	// TODO(osama): Implement pagination with continue_token and next_token. See issue #17.
	// Audit logs can grow large - need cursor-based pagination similar to ListSecrets.
	return &api.GetSecretAuditLogResponse{
		Entries: protoEntries,
		// NextToken: "", // To be implemented
		NextToken: "", // Not implemented yet
	}, nil
}

// Helper functions to convert between internal and proto types

func secretToProto(secret *secrets.Secret) *api.SecretResponse {
	metadata := &api.SecretMetadata{
		Name:         secret.Name,
		Namespace:    secret.Namespace,
		Version:      secret.Version,
		Audiences:    secret.Audiences,
		TtlSeconds:   secret.TTL,
		Immutable:    secret.Immutable,
		CreatedAt:    timestamppb.New(secret.CreatedAt),
		UpdatedAt:    timestamppb.New(secret.UpdatedAt),
		Labels:       secret.Labels,
		DataKeyCount: int32(len(secret.Data)),
	}

	if secret.ExpiresAt != nil {
		metadata.ExpiresAt = timestamppb.New(*secret.ExpiresAt)
	}

	// For CreateSecret/UpdateSecret responses, we return encrypted data
	// (clients should use GetSecret with an access token to decrypt)
	data := &api.SecretData{
		EncryptedData:    secret.Data,
		EncryptedDataKey: secret.EncryptedDataKey,
		KeyId:            secret.KeyID,
		Algorithm:        secret.Algorithm,
	}

	return &api.SecretResponse{
		Metadata: metadata,
		Data:     data,
	}
}

func secretMetadataToProto(secret *secrets.Secret) *api.SecretMetadata {
	metadata := &api.SecretMetadata{
		Name:         secret.Name,
		Namespace:    secret.Namespace,
		Version:      secret.Version,
		Audiences:    secret.Audiences,
		TtlSeconds:   secret.TTL,
		Immutable:    secret.Immutable,
		CreatedAt:    timestamppb.New(secret.CreatedAt),
		UpdatedAt:    timestamppb.New(secret.UpdatedAt),
		Labels:       secret.Labels,
		DataKeyCount: int32(len(secret.Data)),
	}

	if secret.ExpiresAt != nil {
		metadata.ExpiresAt = timestamppb.New(*secret.ExpiresAt)
	}

	return metadata
}

func accessTokenToProto(token *secrets.SecretAccessToken) *api.AccessToken {
	protoToken := &api.AccessToken{
		TokenId:       token.ID,
		SecretRef:     token.SecretRef,
		Audience:      token.Audience,
		Token:         token.Token,
		IssuedAt:      timestamppb.New(token.IssuedAt),
		ExpiresAt:     timestamppb.New(token.ExpiresAt),
		UsesRemaining: int32(token.UsesRemaining),
	}

	if !token.LastUsedAt.IsZero() {
		protoToken.LastUsedAt = timestamppb.New(token.LastUsedAt)
	}

	return protoToken
}
