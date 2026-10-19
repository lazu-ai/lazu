package storage

import "fmt"

// NewClient creates an ObjectStorage backed by an S3-compatible service.
func NewClient(s3Cfg S3Config) (ObjectStorage, error) {
	if !s3Cfg.IsConfigured() {
		return nil, fmt.Errorf("no storage backend configured: set S3-compatible credentials")
	}
	return NewS3(s3Cfg)
}
