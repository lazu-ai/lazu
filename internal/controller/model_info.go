package controller

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/httperr"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/service"
	"github.com/lazu-ai/lazu/internal/types"

	"github.com/gin-gonic/gin"
)

// modelMakerView is one row of the 厂商顺序 dialog.
type modelMakerView struct {
	Key    string `json:"key"`
	Label  string `json:"label"`
	Rank   *int   `json:"rank"`
	Models int    `json:"models"`
}

// GetModelMakers lists every known maker with its place in the order and how
// many listed models it has.
func GetModelMakers(c *gin.Context) {
	counts := map[string]int{}
	for _, pricing := range model.GetPricing() {
		if pricing.Maker != "" {
			counts[pricing.Maker]++
		}
	}
	order := model.ModelMakerOrder()
	ranks := make(map[string]int, len(order))
	for index, key := range order {
		ranks[key] = index
	}
	views := make([]modelMakerView, 0)
	for _, key := range order {
		rank := ranks[key]
		views = append(views, modelMakerView{Key: key, Label: model.ModelMakerLabel(key), Rank: &rank, Models: counts[key]})
	}
	for _, maker := range model.KnownModelMakers() {
		if _, ranked := ranks[maker.Key]; ranked {
			continue
		}
		views = append(views, modelMakerView{Key: maker.Key, Label: maker.Label, Models: counts[maker.Key]})
	}
	common.ApiSuccess(c, gin.H{"order": order, "makers": views})
}

// UpdateModelMakerOrder saves the ranked maker keys.
func UpdateModelMakerOrder(c *gin.Context) {
	var body struct {
		Order []string `json:"order"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		httperr.Invalid(c, "body")
		return
	}
	value := model.EncodeModelMakerOrder(body.Order)
	if err := model.UpdateOption(model.ModelMakerOrderOptionKey, value); err != nil {
		httperr.Write(c, err)
		return
	}
	refreshModelMarketplaceCaches()
	InvalidateModelDiscoveryCache()
	InvalidateChannelHealthCache()
	common.ApiSuccess(c, gin.H{"order": model.ModelMakerOrder()})
}

// modelInfoReviewView is a queue row with the catalog id of its model, so
// the console can open that model's panel (0 for an unregistered release).
type modelInfoReviewView struct {
	model.ModelInfoReview
	ModelID int `json:"model_id"`
}

type missingModelReleaseDate struct {
	ModelID   int    `json:"model_id"`
	ModelName string `json:"model_name"`
	Maker     string `json:"maker"`
}

// Inspect the full offered catalog, not just the paginated review queue.
// Pricing already excludes unconfirmed legacy listing dates.
func missingModelReleaseDates(pricing []model.Pricing, ids map[string]int) []missingModelReleaseDate {
	missing := make([]missingModelReleaseDate, 0)
	for _, row := range pricing {
		if model.NormalizeISODate(row.ReleaseDate) == "" {
			missing = append(missing, missingModelReleaseDate{ModelID: ids[row.ModelName], ModelName: row.ModelName, Maker: row.Maker})
		}
	}
	return missing
}

// GetModelInfoReviews returns the 资料更新 queue.
func GetModelInfoReviews(c *gin.Context) {
	rows, err := model.ListModelInfoReviews()
	if err != nil {
		httperr.Write(c, err)
		return
	}
	pending, err := model.CountPendingModelInfoReviews()
	if err != nil {
		httperr.Write(c, err)
		return
	}
	lastRun, err := model.ModelsDevSyncLastRun()
	if err != nil {
		httperr.Write(c, err)
		return
	}
	pricing := model.GetPricing()
	missingNames := make([]string, 0)
	names := make([]string, 0, len(rows))
	for _, row := range pricing {
		if model.NormalizeISODate(row.ReleaseDate) == "" {
			missingNames = append(missingNames, row.ModelName)
		}
	}
	for _, row := range rows {
		names = append(names, row.ModelName)
	}
	idByName := map[string]int{}
	if len(names) > 0 {
		var found []model.Model
		if err := model.CoreDB().Select("id, model_name").Where("model_name IN ?", names).Order("id ASC").Find(&found).Error; err != nil {
			httperr.Write(c, err)
			return
		}
		for _, m := range found {
			if _, exists := idByName[m.ModelName]; !exists {
				idByName[m.ModelName] = m.Id
			}
		}
	}
	missingIDs, err := model.CatalogModelIDs(missingNames)
	if err != nil {
		httperr.Write(c, err)
		return
	}
	items := make([]modelInfoReviewView, 0, len(rows))
	for _, row := range rows {
		items = append(items, modelInfoReviewView{ModelInfoReview: row, ModelID: idByName[row.ModelName]})
	}
	common.ApiSuccess(c, gin.H{
		"items":                 items,
		"pending_count":         pending,
		"last_run_at":           lastRun,
		"missing_release_dates": missingModelReleaseDates(pricing, missingIDs),
	})
}

// GetModelInfoReviewCount is the tab badge.
func GetModelInfoReviewCount(c *gin.Context) {
	pending, err := model.CountPendingModelInfoReviews()
	if err != nil {
		httperr.Write(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"pending_count": pending})
}

// RunModelInfoSyncNow runs the models.dev sync on demand (现在同步).
func RunModelInfoSyncNow(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Minute)
	defer cancel()
	result, err := RunModelInfoSyncAndRefresh(ctx, false)
	if err != nil {
		httperr.Write(c, err)
		return
	}
	common.ApiSuccess(c, result)
}

// RunModelInfoSyncAndRefresh runs the sync and clears every cache that reads
// model facts. The daily worker task calls it too.
func RunModelInfoSyncAndRefresh(ctx context.Context, scheduled bool) (service.ModelInfoSyncResult, error) {
	result, err := service.RunModelInfoSync(ctx, scheduled)
	if err != nil {
		return result, err
	}
	refreshModelMarketplaceCaches()
	InvalidateModelDiscoveryCache()
	InvalidateChannelHealthCache()
	return result, nil
}

// ResolveModelInfoReview applies an operator decision: accept, keep or undo.
func ResolveModelInfoReview(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		httperr.Invalid(c, "id")
		return
	}
	row, err := model.GetModelInfoReview(id)
	if err != nil {
		httperr.Write(c, err)
		return
	}
	action := c.Param("action")
	switch action {
	case "accept":
		if row.Status != model.ModelInfoReviewPending ||
			(row.Kind != model.ModelInfoReviewDiff && row.Kind != model.ModelInfoReviewFirstRun) {
			httperr.Code(c, types.ErrorCodeStateConflict)
			return
		}
		err = service.ApplyModelInfoReview(row)
	case "keep":
		if row.Status != model.ModelInfoReviewPending {
			httperr.Code(c, types.ErrorCodeStateConflict)
			return
		}
		if row.Kind == model.ModelInfoReviewPriceFollowed {
			// A followed price is already applied; 知道了 only files it.
			err = model.SetModelInfoReviewStatus(row.Id, model.ModelInfoReviewKept)
		} else {
			err = model.KeepModelInfoReview(row.Id)
		}
	case "undo":
		if row.Kind != model.ModelInfoReviewFilled || row.Status != model.ModelInfoReviewApplied {
			httperr.Code(c, types.ErrorCodeStateConflict)
			return
		}
		err = service.UndoModelInfoFill(row)
	default:
		httperr.Invalid(c, "action")
		return
	}
	if err != nil {
		httperr.Write(c, err)
		return
	}
	model.RecomputeFollowingSellPricesLogged([]string{row.ModelName}, nil, "model info review")
	refreshModelMarketplaceCaches()
	InvalidateModelDiscoveryCache()
	InvalidateChannelHealthCache()
	common.ApiSuccess(c, nil)
}

// AcceptAllModelInfoFirstRun applies the first-run revisions selected by the browser.
func AcceptAllModelInfoFirstRun(c *gin.Context) {
	var body struct {
		IDs []int64 `json:"ids"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || len(body.IDs) == 0 {
		httperr.Invalid(c, "ids")
		return
	}
	var pending []model.ModelInfoReview
	if err := model.CoreDB().Where("id IN ? AND kind = ? AND status = ?", body.IDs, model.ModelInfoReviewFirstRun, model.ModelInfoReviewPending).Order("id ASC").Find(&pending).Error; err != nil {
		httperr.Write(c, err)
		return
	}
	if len(pending) != len(body.IDs) {
		httperr.Code(c, types.ErrorCodeVersionConflict)
		return
	}
	if err := service.ApplyModelInfoReviews(pending); err != nil {
		httperr.Write(c, err)
		return
	}
	refreshModelMarketplaceCaches()
	InvalidateModelDiscoveryCache()
	InvalidateChannelHealthCache()
	common.ApiSuccess(c, gin.H{"applied": len(pending)})
}

// LookupModelInfo returns registration defaults for a model id.
func LookupModelInfo(c *gin.Context) {
	name := strings.TrimSpace(c.Query("model"))
	if name == "" {
		httperr.Code(c, types.ErrorCodeMissingRequiredParameter, types.D{"field": "model"})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()
	prefill, found, err := service.LookupModelInfoPrefill(ctx, name)
	if err != nil {
		httperr.Write(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"found": found, "prefill": prefill})
}

// UpdateModelInfo edits the facts the 资料 page exposes — maker, release
// date, lock — without the whole-record model update, which would also
// rewrite columns the page never showed.
func UpdateModelInfo(c *gin.Context) {
	raw, err := c.GetRawData()
	if err != nil {
		httperr.Write(c, err)
		return
	}
	var sent map[string]json.RawMessage
	if err := common.Unmarshal(raw, &sent); err != nil {
		httperr.Invalid(c, "body")
		return
	}
	var body struct {
		ID          int     `json:"id"`
		Maker       *string `json:"maker"`
		ReleaseDate *string `json:"release_date"`
		InfoLocked  *bool   `json:"info_locked"`
	}
	if err := common.Unmarshal(raw, &body); err != nil {
		httperr.Invalid(c, "body")
		return
	}
	var before model.Model
	if err := model.CoreDB().First(&before, body.ID).Error; err != nil {
		httperr.Write(c, err)
		return
	}
	after := before
	fields := map[string]json.RawMessage{}
	if body.Maker != nil {
		after.Maker = *body.Maker
		fields["maker"] = sent["maker"]
	}
	if body.ReleaseDate != nil {
		after.ReleaseDate = *body.ReleaseDate
		fields["release_date"] = sent["release_date"]
	}
	if body.InfoLocked != nil {
		after.InfoLocked = *body.InfoLocked
		fields["info_locked"] = sent["info_locked"]
	}
	if err := model.NormalizeModelInfo(&after); err != nil {
		httperr.Invalid(c, "model_info")
		return
	}
	if err := model.ApplyModelInfoEdits(before, after, fields); err != nil {
		httperr.Write(c, err)
		return
	}
	refreshModelMarketplaceCaches()
	InvalidateModelDiscoveryCache()
	InvalidateChannelHealthCache()
	common.ApiSuccess(c, nil)
}
