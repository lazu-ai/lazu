package uploader

import (
	"context"
	"fmt"
	"path"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/storage"
)

// archivePrefixes hold the full request / response captures. Everything under
// them is deleted once it is older than ArchiveRetentionDays. A bucket
// lifecycle rule may do the same; this sweep makes the promise hold even
// when that rule is missing or was never applied.
var archivePrefixes = []string{"archive/requests/", "archive/errors/"}

// ArchiveRetentionDays is how long captured request text is kept in object
// storage (R2_ARCHIVE_RETENTION_DAYS, default 180).
func ArchiveRetentionDays() int {
	return common.RequestContentRetentionDays()
}

// SweepArchive deletes captures older than the retention window, dating each
// object by the day in its file name.
func SweepArchive(ctx context.Context, client storage.ObjectStorage, days int, now time.Time) (int, error) {
	if client == nil || days <= 0 {
		return 0, nil
	}
	cutoff := now.UTC().AddDate(0, 0, -days).Format("2006-01-02")
	deleted := 0
	for _, prefix := range archivePrefixes {
		objects, err := client.ListByPrefix(ctx, prefix)
		if err != nil {
			return deleted, fmt.Errorf("list %s: %w", prefix, err)
		}
		for _, object := range objects {
			match := dateInFilename.FindString(path.Base(object.Key))
			if match == "" || match >= cutoff {
				continue
			}
			if err := client.Delete(ctx, object.Key); err != nil {
				return deleted, fmt.Errorf("delete %s: %w", object.Key, err)
			}
			deleted++
		}
	}
	return deleted, nil
}

func runArchiveSweep() {
	cfg := common.AppConfig
	client, err := newStorageClient(cfg.Upload.S3)
	if err != nil {
		common.SysError("archive retention: " + err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	deleted, err := SweepArchive(ctx, client, ArchiveRetentionDays(), time.Now())
	if err != nil {
		common.SysError("archive retention: " + err.Error())
	}
	common.SysLog(fmt.Sprintf("archive retention removed %d objects older than %d days", deleted, ArchiveRetentionDays()))
}
