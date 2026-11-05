package upload

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// Store is the object-storage port. Keys are chosen by the caller (NewKey).
type Store interface {
	Put(ctx context.Context, key string, data []byte, contentType string) error
	Delete(ctx context.Context, key string) error
}

// NewKey returns an unguessable object key (Architecture 9.5). The user's file
// name is never part of the key, so it cannot cause path or collision issues.
func NewKey() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generating object key: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// MinIO implements Store with minio-go.
type MinIO struct {
	client *minio.Client
	bucket string
}

func NewMinIO(endpoint, accessKey, secretKey, bucket string, useSSL bool) (*MinIO, error) {
	c, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(accessKey, secretKey, ""),
		Secure: useSSL,
	})
	if err != nil {
		return nil, fmt.Errorf("creating minio client: %w", err)
	}
	return &MinIO{client: c, bucket: bucket}, nil
}

// EnsureBucket creates the bucket if missing. Idempotent; the bucket is private
// by default, which is what we want since the browser never talks to MinIO.
func (m *MinIO) EnsureBucket(ctx context.Context) error {
	ok, err := m.client.BucketExists(ctx, m.bucket)
	if err != nil {
		return fmt.Errorf("checking bucket: %w", err)
	}
	if ok {
		return nil
	}
	if err := m.client.MakeBucket(ctx, m.bucket, minio.MakeBucketOptions{}); err != nil {
		// Lost a creation race with another instance: that is fine.
		if minio.ToErrorResponse(err).Code == "BucketAlreadyOwnedByYou" {
			return nil
		}
		return fmt.Errorf("creating bucket: %w", err)
	}
	return nil
}

func (m *MinIO) Put(ctx context.Context, key string, data []byte, contentType string) error {
	_, err := m.client.PutObject(ctx, m.bucket, key, bytes.NewReader(data), int64(len(data)),
		minio.PutObjectOptions{ContentType: contentType})
	if err != nil {
		return fmt.Errorf("put object: %w", err)
	}
	return nil
}

// Delete is idempotent: removing a missing object is not an error in S3.
func (m *MinIO) Delete(ctx context.Context, key string) error {
	if err := m.client.RemoveObject(ctx, m.bucket, key, minio.RemoveObjectOptions{}); err != nil {
		return fmt.Errorf("remove object: %w", err)
	}
	return nil
}

// Exists is used by tests and by future orphan sweeps.
func (m *MinIO) Exists(ctx context.Context, key string) (bool, error) {
	_, err := m.client.StatObject(ctx, m.bucket, key, minio.StatObjectOptions{})
	if err == nil {
		return true, nil
	}
	if minio.ToErrorResponse(err).Code == "NoSuchKey" {
		return false, nil
	}
	return false, err
}
