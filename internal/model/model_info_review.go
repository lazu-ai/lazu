package model

import (
	"strings"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/types"
)

// Kinds of rows the model-info review queue (资料更新) holds.
const (
	// A lab released a model we do not list yet, or a channel serves one the
	// catalog has never registered.
	ModelInfoReviewNewRelease = "new_release"
	// A fact we hold disagrees with models.dev and was not changed.
	ModelInfoReviewDiff = "diff"
	// First-sync correction of release dates that were really listing days.
	ModelInfoReviewFirstRun = "first_run"
	// An empty field was filled; kept for undo.
	ModelInfoReviewFilled = "filled"
	// The lab prices long inputs higher and our sell price has no tier.
	ModelInfoReviewTierGap = "tier_gap"
	// The official price moved and was taken; sell prices that follow it
	// moved with it. A notice, not a decision: it is not counted as pending.
	ModelInfoReviewPriceFollowed = "price_followed"
)

const (
	ModelInfoReviewPending  = "pending"
	ModelInfoReviewAccepted = "accepted"
	ModelInfoReviewKept     = "kept"
	ModelInfoReviewApplied  = "applied"
	ModelInfoReviewUndone   = "undone"
	ModelInfoReviewResolved = "resolved"
)

// ModelInfoReview is one row of the 资料更新 queue.
type ModelInfoReview struct {
	Id            int64  `json:"id" gorm:"primaryKey"`
	Kind          string `json:"kind" gorm:"type:varchar(24);not null;index:idx_model_info_review_kind_status,priority:1"`
	Status        string `json:"status" gorm:"type:varchar(16);not null;default:'pending';index:idx_model_info_review_kind_status,priority:2"`
	ModelName     string `json:"model_name" gorm:"type:varchar(160);not null;index"`
	Maker         string `json:"maker" gorm:"type:varchar(32);not null;default:''"`
	Field         string `json:"field" gorm:"type:varchar(32);not null;default:''"`
	LocalValue    string `json:"local_value" gorm:"type:text"`
	UpstreamValue string `json:"upstream_value" gorm:"type:text"`
	Note          string `json:"note" gorm:"type:text"`
	// Payload carries what an action needs: the prefill for a new release,
	// the previous value for an undo, the upstream tier for a tier gap.
	Payload      string `json:"payload,omitempty" gorm:"type:text"`
	SyncRun      int64  `json:"sync_run" gorm:"bigint;default:0;index"`
	CreatedTime  int64  `json:"created_time" gorm:"bigint"`
	ResolvedTime int64  `json:"resolved_time,omitempty" gorm:"bigint;default:0"`
}

// RecordModelInfoReview retains unchanged proposals and replaces changed ones
// with new IDs. A row the operator already chose to keep is not
// re-raised while the upstream value, explanatory note and complete payload stay the same.
func RecordModelInfoReview(row ModelInfoReview) error {
	return RecordModelInfoReviewDB(CoreDB(), row)
}

func RecordModelInfoReviewDB(db *gorm.DB, row ModelInfoReview) error {
	row.ModelName = strings.TrimSpace(row.ModelName)
	if row.ModelName == "" || row.Kind == "" {
		return nil
	}
	var kept int64
	kinds := []string{row.Kind}
	query := db.Model(&ModelInfoReview{}).Where("model_name = ? AND field = ? AND status = ? AND upstream_value = ? AND COALESCE(payload, '') = ?",
		row.ModelName, row.Field, ModelInfoReviewKept, row.UpstreamValue, row.Payload)
	if row.Field == ModelInfoFieldReleaseDate && (row.Kind == ModelInfoReviewFirstRun || row.Kind == ModelInfoReviewDiff) {
		// Keeping a legacy date confirms it as manual. That changes the next
		// proposal's kind and note, but not the date pair the operator rejected.
		kinds = []string{ModelInfoReviewFirstRun, ModelInfoReviewDiff}
		query = query.Where("local_value = ?", row.LocalValue)
	} else {
		query = query.Where("COALESCE(note, '') = ?", row.Note)
	}
	if err := query.Where("kind IN ?", kinds).Count(&kept).Error; err != nil {
		return err
	}
	if kept > 0 {
		// Retire superseded pending revisions across the provenance transition.
		return db.Model(&ModelInfoReview{}).
			Where("kind IN ? AND model_name = ? AND field = ? AND status = ?", kinds, row.ModelName, row.Field, ModelInfoReviewPending).
			Updates(map[string]interface{}{"status": ModelInfoReviewResolved, "resolved_time": common.GetTimestamp()}).Error
	}
	var existing []ModelInfoReview
	if err := db.Where("kind = ? AND model_name = ? AND field = ? AND status = ?",
		row.Kind, row.ModelName, row.Field, ModelInfoReviewPending).
		Limit(1).Find(&existing).Error; err != nil {
		return err
	}
	if len(existing) > 0 {
		previous := existing[0]
		if previous.LocalValue == row.LocalValue && previous.UpstreamValue == row.UpstreamValue && previous.Note == row.Note && previous.Payload == row.Payload && previous.Maker == row.Maker {
			return db.Model(&previous).Update("sync_run", row.SyncRun).Error
		}
		// IDs identify immutable proposals: an old browser must never accept
		// a price or limit that a newer synchronization silently replaced.
		return db.Transaction(func(tx *gorm.DB) error {
			if err := ResolvePendingModelInfoReviewsDB(tx, row.Kind, row.ModelName, row.Field); err != nil {
				return err
			}
			row.Id, row.Status, row.CreatedTime, row.ResolvedTime = 0, ModelInfoReviewPending, common.GetTimestamp(), 0
			return tx.Create(&row).Error
		})
	}
	row.Id = 0
	if row.Status == "" {
		row.Status = ModelInfoReviewPending
	}
	row.CreatedTime = common.GetTimestamp()
	return db.Create(&row).Error
}

func RecordModelInfoFillDB(db *gorm.DB, modelName, maker, field, previous, value string, syncRun int64) error {
	var count int64
	if err := db.Model(&ModelInfoReview{}).Where("kind = ? AND status = ? AND model_name = ? AND field = ? AND sync_run = ? AND local_value = ? AND upstream_value = ?",
		ModelInfoReviewFilled, ModelInfoReviewApplied, modelName, field, syncRun, previous, value).Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	return db.Create(&ModelInfoReview{
		Kind:          ModelInfoReviewFilled,
		Status:        ModelInfoReviewApplied,
		ModelName:     modelName,
		Maker:         maker,
		Field:         field,
		LocalValue:    previous,
		UpstreamValue: value,
		SyncRun:       syncRun,
		CreatedTime:   common.GetTimestamp(),
	}).Error
}

// RecordModelInfoPriceFollowDB files an official price the sync took. An
// older notice for the same field is closed: only the latest move is shown.
func RecordModelInfoPriceFollowDB(db *gorm.DB, modelName, maker, field, previous, value string, syncRun int64) error {
	if err := ResolvePendingModelInfoReviewsDB(db, ModelInfoReviewPriceFollowed, modelName, field); err != nil {
		return err
	}
	return db.Create(&ModelInfoReview{
		Kind:          ModelInfoReviewPriceFollowed,
		Status:        ModelInfoReviewPending,
		ModelName:     modelName,
		Maker:         maker,
		Field:         field,
		LocalValue:    previous,
		UpstreamValue: value,
		SyncRun:       syncRun,
		CreatedTime:   common.GetTimestamp(),
	}).Error
}

// ResolvePendingModelInfoReviews closes pending rows that no longer apply —
// the disagreement went away, or the model got registered.
func ResolvePendingModelInfoReviews(kind, modelName, field string) error {
	return ResolvePendingModelInfoReviewsDB(CoreDB(), kind, modelName, field)
}

func ResolvePendingModelInfoReviewsDB(db *gorm.DB, kind, modelName, field string) error {
	query := db.Model(&ModelInfoReview{}).
		Where("kind = ? AND model_name = ? AND status = ?", kind, modelName, ModelInfoReviewPending)
	if field != "" {
		query = query.Where("field = ?", field)
	}
	return query.Updates(map[string]interface{}{
		"status":        ModelInfoReviewResolved,
		"resolved_time": common.GetTimestamp(),
	}).Error
}

// ResolveOrphanedModelInfoReviews retires proposals whose model was deleted
// or renamed. New-release suggestions intentionally need no registered model.
func ResolveOrphanedModelInfoReviews() error {
	return CoreDB().Model(&ModelInfoReview{}).
		Where("status = ? AND kind IN ?", ModelInfoReviewPending, []string{ModelInfoReviewDiff, ModelInfoReviewFirstRun, ModelInfoReviewTierGap, ModelInfoReviewPriceFollowed}).
		Where("model_name NOT IN (?)", CoreDB().Model(&Model{}).Select("model_name")).
		Updates(map[string]interface{}{"status": ModelInfoReviewResolved, "resolved_time": common.GetTimestamp()}).Error
}

// ListModelInfoReviews returns the queue: every pending row, plus the fills
// of the latest sync run so they can still be undone.
func ListModelInfoReviews() ([]ModelInfoReview, error) {
	if err := ResolveOrphanedModelInfoReviews(); err != nil {
		return nil, err
	}
	var rows []ModelInfoReview
	if err := CoreDB().Where("status = ?", ModelInfoReviewPending).
		Order("id ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	latest, err := ModelsDevSyncLastRun()
	if err != nil {
		return nil, err
	}
	if latest > 0 {
		var fills []ModelInfoReview
		if err := CoreDB().Where("kind = ? AND sync_run = ? AND status = ?",
			ModelInfoReviewFilled, latest, ModelInfoReviewApplied).
			Order("id ASC").Find(&fills).Error; err != nil {
			return nil, err
		}
		rows = append(rows, fills...)
	}
	return rows, nil
}

// CountPendingModelInfoReviews is the number on the 资料更新 tab: the rows
// that need a decision. The one-time first-run batch is not counted.
func CountPendingModelInfoReviews() (int64, error) {
	if err := ResolveOrphanedModelInfoReviews(); err != nil {
		return 0, err
	}
	var count int64
	err := CoreDB().Model(&ModelInfoReview{}).
		Where("status = ? AND kind IN ?", ModelInfoReviewPending,
			[]string{ModelInfoReviewNewRelease, ModelInfoReviewDiff, ModelInfoReviewTierGap}).
		Count(&count).Error
	return count, err
}

// GetModelInfoReview loads one row.
func GetModelInfoReview(id int64) (*ModelInfoReview, error) {
	var row ModelInfoReview
	if err := CoreDB().First(&row, id).Error; err != nil {
		return nil, err
	}
	return &row, nil
}

// SetModelInfoReviewStatus records the operator's decision.
func SetModelInfoReviewStatus(id int64, status string) error {
	return CoreDB().Model(&ModelInfoReview{}).Where("id = ?", id).Updates(map[string]interface{}{
		"status":        status,
		"resolved_time": common.GetTimestamp(),
	}).Error
}

// KeepModelInfoReview dismisses a pending proposal and confirms a retained
// release date, provided the model still has the value the operator reviewed.
func KeepModelInfoReview(id int64) error {
	return CoreDB().Transaction(func(tx *gorm.DB) error {
		var row ModelInfoReview
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&row, id).Error; err != nil {
			return err
		}
		if row.Status != ModelInfoReviewPending {
			return types.Err(types.ErrorCodeVersionConflict)
		}
		// Keeping a reviewed release date is an explicit confirmation. Otherwise
		// legacy dates remain invisible after their only review has been dismissed.
		if row.Field == ModelInfoFieldReleaseDate && (row.Kind == ModelInfoReviewFirstRun || row.Kind == ModelInfoReviewDiff) {
			var models []Model
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("model_name = ?", row.ModelName).Find(&models).Error; err != nil {
				return err
			}
			if len(models) == 0 {
				return types.Err(types.ErrorCodeVersionConflict)
			}
			for _, m := range models {
				if m.ReleaseDate != row.LocalValue {
					return types.Err(types.ErrorCodeVersionConflict)
				}
				if NormalizeISODate(m.ReleaseDate) == "" {
					continue
				}
				sources := ModelInfoSources{}
				for key, value := range m.InfoSources {
					sources[key] = value
				}
				sources[ModelInfoFieldReleaseDate] = ModelInfoSourceManual
				if err := tx.Model(&m).Update("info_sources", sources).Error; err != nil {
					return err
				}
			}
		}
		return tx.Model(&row).Updates(map[string]interface{}{"status": ModelInfoReviewKept, "resolved_time": common.GetTimestamp()}).Error
	})
}
