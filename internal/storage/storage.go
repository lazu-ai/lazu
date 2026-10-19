package storage

import (
	"context"
	"io"
	"time"
)

// ObjectMeta contains metadata returned by a Head request.
type ObjectMeta struct {
	Exists        bool
	ContentLength int64
}

// ObjectInfo is one row in a list response.
type ObjectInfo struct {
	Key          string
	Size         int64
	LastModified time.Time
}

// ObjectStorage is the interface for S3-compatible object storage backends.
type ObjectStorage interface {
	// Put uploads data from a reader with an optional content type.
	Put(ctx context.Context, key string, r io.Reader, contentType string) error
	// PutFromFile uploads a local file.
	PutFromFile(ctx context.Context, key string, filePath string) error
	// Get downloads an object as a stream. Callers must close the returned reader.
	Get(ctx context.Context, key string) (io.ReadCloser, error)
	// Head checks whether an object exists and returns its metadata.
	Head(ctx context.Context, key string) (ObjectMeta, error)
	// Delete removes an object.
	Delete(ctx context.Context, key string) error
	// ListByPrefix returns objects whose keys start with prefix. When the
	// backend paginates, all pages are walked and concatenated. The
	// compactor is the only current caller; ordering is implementation-
	// defined.
	ListByPrefix(ctx context.Context, prefix string) ([]ObjectInfo, error)
}

// ObjectRangeStorage is implemented by backends that can fetch an inclusive byte
// range without streaming and discarding the prefix.
type ObjectRangeStorage interface {
	GetRange(ctx context.Context, key string, start int64, end int64) (io.ReadCloser, error)
}

// ObjectCacheStorage is implemented by backends that can store an object
// with the Cache-Control header it is later served with (a public bucket
// behind a CDN).
type ObjectCacheStorage interface {
	PutWithCacheControl(ctx context.Context, key string, r io.Reader, contentType string, cacheControl string) error
}

// S3Config holds configuration for S3-compatible storage (including Cloudflare R2).
type S3Config struct {
	Endpoint        string `yaml:"endpoint"`
	Region          string `yaml:"region"`
	Bucket          string `yaml:"bucket"`
	AccessKeyID     string `yaml:"access_key_id"`
	SecretAccessKey string `yaml:"secret_access_key"`
}

// IsConfigured returns true when the minimum required fields are set.
func (c S3Config) IsConfigured() bool {
	return c.Bucket != "" && c.AccessKeyID != "" && c.SecretAccessKey != ""
}
