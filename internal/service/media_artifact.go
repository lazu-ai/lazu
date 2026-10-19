package service

import (
	"bytes"
	"context"
	"fmt"
	"mime"
	"net/url"
	"path"
	"strings"
	"time"
	"unicode"

	"github.com/lazu-ai/lazu/internal/model"
)

func StoreMediaJobArtifact(
	ctx context.Context,
	job model.MediaJob,
	data []byte,
	mimeType string,
	sourceURL string,
	now time.Time,
) (*model.File, error) {
	user, err := model.GetUserById(job.BillingUserID(), false)
	if err != nil {
		return nil, err
	}
	storageClient, err := EnsureUserFileStorageClient()
	if err != nil {
		return nil, err
	}
	if !IsSupportedMediaArtifactMIME(job.Kind, mimeType) {
		return nil, fmt.Errorf("unsupported %s artifact mime type %q", job.Kind, mimeType)
	}
	purpose := mediaJobArtifactPurpose(job)
	now = now.UTC()
	nowUnix := now.Unix()
	expiresAtPointer := mediaJobArtifactExpiresAt(job, nowUnix)
	storageKey := BuildUserFileStorageKey(user.PublicID, purpose, now)
	if err := storageClient.Put(ctx, storageKey, bytes.NewReader(data), mimeType); err != nil {
		return nil, err
	}
	row := &model.File{
		ProjectID: job.ProjectID, PayerUserID: job.BillingUserID(), ActorUserID: job.ActorID(),
		UserID:     job.UserID,
		TokenID:    job.TokenID,
		Filename:   mediaJobArtifactFilename(sourceURL, mimeType),
		Purpose:    purpose,
		MimeType:   mimeType,
		Bytes:      int64(len(data)),
		StorageKey: storageKey,
		Status:     model.FileStatusProcessed,
		ExpiresAt:  expiresAtPointer,
		CreatedAt:  nowUnix,
	}
	// The generation was paid for; its artifact counts toward the account's
	// storage but is never refused for it.
	if err := model.CreateFileWithAccounting(model.CreateFileAccountingInput{
		File:                row,
		AllowHeldSettlement: true,
	}); err != nil {
		_ = storageClient.Delete(ctx, storageKey)
		return nil, err
	}
	return row, nil
}

// DeleteMediaJobArtifact removes an artifact a media job stored but must not
// keep, such as one whose generation could not be billed.
func DeleteMediaJobArtifact(ctx context.Context, file *model.File) error {
	if file == nil || file.ID == "" {
		return nil
	}
	storageClient, err := EnsureUserFileStorageClient()
	if err != nil {
		return err
	}
	if file.StorageKey != "" {
		if err := storageClient.Delete(ctx, file.StorageKey); err != nil {
			return fmt.Errorf("delete artifact object: %w", err)
		}
	}
	return model.DeleteFileWithAccounting(file)
}

func mediaJobArtifactExpiresAt(_ model.MediaJob, nowUnix int64) *int64 {
	expiresAt := DefaultFileExpiresAt(nowUnix)
	return &expiresAt
}

func IsSupportedMediaArtifactMIME(kind string, rawMIME string) bool {
	mimeType := strings.ToLower(strings.TrimSpace(strings.Split(rawMIME, ";")[0]))
	switch kind {
	case model.MediaJobKindImage:
		switch mimeType {
		case "image/avif", "image/gif", "image/jpeg", "image/png", "image/webp":
			return true
		}
	case model.MediaJobKindVideo:
		switch mimeType {
		case "video/mp4", "video/ogg", "video/quicktime", "video/webm":
			return true
		}
	}
	return false
}

func mediaJobArtifactPurpose(job model.MediaJob) string {
	if job.Kind == model.MediaJobKindImage {
		return "vision"
	}
	return "user_data"
}

func mediaJobArtifactFilename(rawURL string, mimeType string) string {
	filename := ""
	if parsed, err := url.Parse(rawURL); err == nil {
		filename = path.Base(parsed.Path)
	}
	if filename == "" || filename == "." || filename == "/" {
		filename = "media-artifact"
		if extensions, err := mime.ExtensionsByType(mimeType); err == nil && len(extensions) > 0 {
			filename += extensions[0]
		}
	}
	var builder strings.Builder
	for _, char := range filename {
		if char == '/' || char == '\\' || unicode.IsControl(char) {
			continue
		}
		builder.WriteRune(char)
		if builder.Len() >= 255 {
			break
		}
	}
	if builder.Len() == 0 {
		return "media-artifact"
	}
	return builder.String()
}
