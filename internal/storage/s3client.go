package storage

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

type s3Storage struct {
	client *s3.Client
	bucket string
}

// NewS3 creates an ObjectStorage backed by any S3-compatible service (AWS S3, Cloudflare R2, MinIO, etc.).
func NewS3(cfg S3Config) (ObjectStorage, error) {
	if !cfg.IsConfigured() {
		return nil, fmt.Errorf("s3 config incomplete: bucket, access_key_id and secret_access_key are required")
	}

	region := cfg.Region
	if region == "" {
		region = "auto" // Cloudflare R2 default
	}

	awsCfg := aws.Config{
		Region:      region,
		Credentials: credentials.NewStaticCredentialsProvider(cfg.AccessKeyID, cfg.SecretAccessKey, ""),
	}

	var s3Opts []func(*s3.Options)
	if cfg.Endpoint != "" {
		s3Opts = append(s3Opts, func(o *s3.Options) {
			o.BaseEndpoint = aws.String(cfg.Endpoint)
			// A plain-http endpoint is a local S3 stand-in (MinIO in tests),
			// which serves buckets by path, not by subdomain. R2 is https.
			o.UsePathStyle = strings.HasPrefix(cfg.Endpoint, "http://")
		})
	}

	client := s3.NewFromConfig(awsCfg, s3Opts...)
	return &s3Storage{client: client, bucket: cfg.Bucket}, nil
}

func (s *s3Storage) Put(ctx context.Context, key string, r io.Reader, contentType string) error {
	input := &s3.PutObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
		Body:   r,
	}
	if contentType != "" {
		input.ContentType = aws.String(contentType)
	}
	_, err := s.client.PutObject(ctx, input)
	return err
}

func (s *s3Storage) PutWithCacheControl(ctx context.Context, key string, r io.Reader, contentType string, cacheControl string) error {
	input := &s3.PutObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
		Body:   r,
	}
	if contentType != "" {
		input.ContentType = aws.String(contentType)
	}
	if cacheControl != "" {
		input.CacheControl = aws.String(cacheControl)
	}
	_, err := s.client.PutObject(ctx, input)
	return err
}

func (s *s3Storage) PutFromFile(ctx context.Context, key string, filePath string) error {
	f, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
		Body:   f,
	})
	return err
}

func (s *s3Storage) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, err
	}
	return out.Body, nil
}

func (s *s3Storage) GetRange(ctx context.Context, key string, start int64, end int64) (io.ReadCloser, error) {
	if start < 0 || end < start {
		return nil, fmt.Errorf("invalid object range %d-%d", start, end)
	}
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
		Range:  aws.String(fmt.Sprintf("bytes=%d-%d", start, end)),
	})
	if err != nil {
		return nil, err
	}
	return out.Body, nil
}

func (s *s3Storage) Head(ctx context.Context, key string) (ObjectMeta, error) {
	out, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		// HeadObject returns an error for non-existent objects; treat as "not found".
		return ObjectMeta{Exists: false}, nil
	}
	var contentLength int64
	if out.ContentLength != nil {
		contentLength = *out.ContentLength
	}
	return ObjectMeta{Exists: true, ContentLength: contentLength}, nil
}

func (s *s3Storage) Delete(ctx context.Context, key string) error {
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	return err
}

func (s *s3Storage) ListByPrefix(ctx context.Context, prefix string) ([]ObjectInfo, error) {
	var out []ObjectInfo
	var token *string
	for {
		page, err := s.client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
			Bucket:            aws.String(s.bucket),
			Prefix:            aws.String(prefix),
			ContinuationToken: token,
		})
		if err != nil {
			return nil, err
		}
		for _, item := range page.Contents {
			info := ObjectInfo{}
			if item.Key != nil {
				info.Key = *item.Key
			}
			if item.Size != nil {
				info.Size = *item.Size
			}
			if item.LastModified != nil {
				info.LastModified = *item.LastModified
			}
			out = append(out, info)
		}
		if page.IsTruncated == nil || !*page.IsTruncated {
			break
		}
		token = page.NextContinuationToken
	}
	return out, nil
}
