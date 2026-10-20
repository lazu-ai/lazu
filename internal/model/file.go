package model

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/types"

	"gorm.io/gorm"
)

const (
	FileObjectType      = "file"
	FileStatusProcessed = "processed"

	// FileStorageQuotaBytesOption caps the bytes one paying account may keep
	// in file storage. Storage carries no charge: uploads and generated
	// artifacts expire, and the cap bounds what an account can hold meanwhile.
	FileStorageQuotaBytesOption  = "FileStorageQuotaBytes"
	defaultFileStorageQuotaBytes = int64(10 << 30)
)

// FileStorageQuotaBytes is the per-account storage cap from the Options table.
func FileStorageQuotaBytes() int64 {
	n, err := strconv.ParseInt(GetOptionValue(FileStorageQuotaBytesOption), 10, 64)
	if err != nil || n <= 0 {
		return defaultFileStorageQuotaBytes
	}
	return n
}

func validateFileStorageOption(key, value string) error {
	if key != FileStorageQuotaBytesOption {
		return nil
	}
	n, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil || n <= 0 {
		return errors.New("file storage quota must be a positive number of bytes")
	}
	return nil
}

type File struct {
	ProjectID   int `json:"-" gorm:"index;not null;default:0"`
	PayerUserID int `json:"-" gorm:"not null;default:0"`
	ActorUserID int `json:"-" gorm:"not null;default:0"`

	ID         string `json:"id" gorm:"type:varchar(64);primaryKey"`
	PublicID   string `json:"-" gorm:"type:varchar(64);uniqueIndex"`
	UserID     int    `json:"-" gorm:"not null;index:idx_files_user_created,priority:1"`
	TokenID    *int   `json:"-" gorm:"index"`
	Filename   string `json:"filename" gorm:"type:varchar(255);not null"`
	Purpose    string `json:"purpose" gorm:"type:varchar(32);not null;index"`
	MimeType   string `json:"mime_type" gorm:"type:varchar(127);not null"`
	Bytes      int64  `json:"bytes" gorm:"not null"`
	StorageKey string `json:"storage_key" gorm:"type:varchar(512);not null"`
	Status     string `json:"status" gorm:"type:varchar(16);not null;default:'processed'"`
	ExpiresAt  *int64 `json:"expires_at,omitempty" gorm:"index:idx_files_expires"`
	CreatedAt  int64  `json:"created_at" gorm:"not null;index:idx_files_user_created,priority:2,sort:desc"`
}

func (file File) ToDTO() dto.FileObject {
	return dto.FileObject{
		ID:        file.ID,
		Object:    FileObjectType,
		Bytes:     file.Bytes,
		CreatedAt: file.CreatedAt,
		Filename:  file.Filename,
		Purpose:   file.Purpose,
		Status:    file.Status,
	}
}

type CreateFileAccountingInput struct {
	File *File
	// EnforceQuota rejects the file when it would take the paying account past
	// FileStorageQuotaBytes. Customer uploads enforce it. A generated artifact
	// was already paid for as a generation, so it is counted but never refused.
	EnforceQuota bool
	// An already admitted media job may finish its artifact after a new hold.
	AllowHeldSettlement bool
}

// CreateFileWithAccounting records a stored file and adds its bytes to the
// paying account's storage counter. Storage is not billed, so this never
// touches the wallet, a Key or a spend budget.
func CreateFileWithAccounting(input CreateFileAccountingInput) error {
	if CoreDB() == nil {
		return fmt.Errorf("database is not initialized")
	}
	if input.File == nil {
		return fmt.Errorf("file is required")
	}
	if input.File.BillingUserID() <= 0 {
		return fmt.Errorf("user id is required")
	}
	return CoreDB().Transaction(func(tx *gorm.DB) error {
		if input.File.ProjectID > 0 && !input.AllowHeldSettlement {
			if err := checkProjectPaymentHoldTx(tx, input.File.ProjectID); err != nil {
				return err
			}
		}
		if err := tx.Create(input.File).Error; err != nil {
			return err
		}
		limit := int64(0)
		if input.EnforceQuota {
			limit = FileStorageQuotaBytes()
		}
		return incrementUserStorageBytes(tx, input.File.BillingUserID(), input.File.Bytes, limit)
	})
}

// DeleteFileWithAccounting removes a file record, lets extensions release
// anything that showed it, and returns its bytes to the paying account's
// counter.
func DeleteFileWithAccounting(file *File) error {
	if CoreDB() == nil {
		return fmt.Errorf("database is not initialized")
	}
	if file == nil {
		return fmt.Errorf("file is required")
	}
	return CoreDB().Transaction(func(tx *gorm.DB) error {
		if err := runFileDeletingHooks(tx, file); err != nil {
			return err
		}
		result := tx.Where("id = ? AND user_id = ?", file.ID, file.UserID).Delete(&File{})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		return decrementUserStorageBytes(tx, file.BillingUserID(), file.Bytes)
	})
}

func DeleteExpiredFileRecord(file *File) error {
	if CoreDB() == nil {
		return fmt.Errorf("database is not initialized")
	}
	if file == nil {
		return fmt.Errorf("file is required")
	}
	return CoreDB().Transaction(func(tx *gorm.DB) error {
		result := tx.Where("id = ?", file.ID).Delete(&File{})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return nil
		}
		return decrementUserStorageBytes(tx, file.BillingUserID(), file.Bytes)
	})
}

func GetFileByIDAndOwner(id string, userID int, principals ...ProjectPrincipal) (*File, error) {
	var file File
	err := requestResourceScope(CoreDB(), userID, principals).Where("id = ?", id).First(&file).Error
	if err != nil {
		return nil, err
	}
	return &file, nil
}

// GetFilesByIDs loads files by id in one query, keyed by id. Callers pass ids
// they already hold through an owned row (a job's artifact), so it does not
// scope by owner; ids that no longer exist are simply absent.
func GetFilesByIDs(ids []string) (map[string]File, error) {
	out := make(map[string]File, len(ids))
	if len(ids) == 0 || CoreDB() == nil {
		return out, nil
	}
	var rows []File
	if err := CoreDB().Where("id IN ?", ids).Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		out[row.ID] = row
	}
	return out, nil
}

func ListFilesByOwner(userID int, purpose string, limit int, order string, principals ...ProjectPrincipal) ([]File, error) {
	files := make([]File, 0)
	if limit <= 0 || limit > 10000 {
		limit = 10000
	}
	order = strings.ToLower(strings.TrimSpace(order))
	if order != "asc" {
		order = "desc"
	}
	query := requestResourceScope(CoreDB(), userID, principals)
	if purpose != "" {
		query = query.Where("purpose = ?", purpose)
	}
	err := query.Order("created_at " + order).Limit(limit).Find(&files).Error
	return files, err
}

func ListExpiredFiles(now int64, limit int) ([]File, error) {
	files := make([]File, 0)
	if CoreDB() == nil || !CoreDB().Migrator().HasTable(&File{}) {
		return files, nil
	}
	if limit <= 0 {
		limit = 500
	}
	err := CoreDB().Where("expires_at IS NOT NULL AND expires_at < ?", now).Limit(limit).Find(&files).Error
	return files, err
}

func CountFilesByOwner(userID int) (int64, error) {
	var count int64
	err := CoreDB().Model(&File{}).Where("user_id = ?", userID).Count(&count).Error
	return count, err
}

// incrementUserStorageBytes adds bytes to the account's counter. With a
// positive limit the update is conditional, so concurrent uploads can never
// take the counter past it.
func incrementUserStorageBytes(tx *gorm.DB, userID int, bytes int64, limit int64) error {
	if bytes <= 0 {
		return nil
	}
	query := tx.Model(&User{}).Where("id = ?", userID)
	if limit > 0 {
		query = query.Where("storage_bytes_used + ? <= ?", bytes, limit)
	}
	result := query.Update("storage_bytes_used", gorm.Expr("storage_bytes_used + ?", bytes))
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		if limit > 0 {
			return types.Err(types.ErrorCodeStorageQuotaExceeded, types.D{"limit": FormatStorageBytes(limit)})
		}
		return gorm.ErrRecordNotFound
	}
	return nil
}

// FormatStorageBytes renders a byte count the way customers read storage.
func FormatStorageBytes(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	value, suffix := float64(bytes), "B"
	for _, next := range []string{"KB", "MB", "GB", "TB"} {
		if value < unit {
			break
		}
		value, suffix = value/unit, next
	}
	if value == float64(int64(value)) {
		return fmt.Sprintf("%d %s", int64(value), suffix)
	}
	return fmt.Sprintf("%.1f %s", value, suffix)
}

func decrementUserStorageBytes(tx *gorm.DB, userID int, bytes int64) error {
	if bytes <= 0 {
		return nil
	}
	return tx.Model(&User{}).
		Where("id = ?", userID).
		Update("storage_bytes_used", gorm.Expr("CASE WHEN storage_bytes_used >= ? THEN storage_bytes_used - ? ELSE 0 END", bytes, bytes)).
		Error
}

func (file File) BillingUserID() int {
	if file.ProjectID > 0 {
		return file.PayerUserID
	}
	return file.UserID
}
