package service

import (
	"context"
	"time"

	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/storage"
)

// FileLinkTTL is how long a signed download link handed out in a job result
// works. Each read signs a fresh one, so a client that keeps a job id can
// always get a working link until the file itself expires.
const FileLinkTTL = time.Hour

// FileLink is a temporary download link to a stored file.
type FileLink struct {
	URL       string
	ExpiresAt int64
}

// SignFileLink signs a GET link to file straight from the bucket. The link
// expires after ttl, or when the file does, whichever is first. It returns
// ok=false when the storage cannot sign links or the file has expired.
func SignFileLink(ctx context.Context, file *model.File, ttl time.Duration, now time.Time) (FileLink, bool) {
	if file == nil || file.StorageKey == "" || ttl <= 0 {
		return FileLink{}, false
	}
	expiresAt := now.Add(ttl).Unix()
	if file.ExpiresAt != nil && *file.ExpiresAt > 0 {
		if *file.ExpiresAt <= now.Unix() {
			return FileLink{}, false
		}
		if *file.ExpiresAt < expiresAt {
			expiresAt = *file.ExpiresAt
		}
	}
	client, err := EnsureUserFileStorageClient()
	if err != nil {
		return FileLink{}, false
	}
	signer, ok := client.(storage.ObjectPresignStorage)
	if !ok {
		return FileLink{}, false
	}
	url, err := signer.PresignGet(ctx, file.StorageKey, time.Unix(expiresAt, 0).Sub(now), file.Filename, file.MimeType)
	if err != nil || url == "" {
		return FileLink{}, false
	}
	return FileLink{URL: url, ExpiresAt: expiresAt}, true
}
