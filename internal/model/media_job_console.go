package model

import (
	"strings"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/ledger"
	"github.com/lazu-ai/lazu/pkg/billing"
	"gorm.io/gorm"
)

// The console's Tasks view lists media jobs (videos, images) the way the
// usage log lists requests: one row per job, with its hold, charge, refund
// and the stored file's lifetime.

const (
	MediaJobConsoleStatusActive = "active"
	// mediaJobConsoleSummaryCap bounds the rows a summary reads; a range with
	// more jobs than this summarizes its newest ones.
	mediaJobConsoleSummaryCap = 5000
)

// MediaJobConsoleFilter narrows the Tasks view. Zero values do not filter.
type MediaJobConsoleFilter struct {
	Status    string // "", active, succeeded or failed
	Kind      string // "", image or video
	Start     int64  // unix seconds, inclusive
	End       int64  // unix seconds, inclusive
	ModelName string
	TokenID   int
	Query     string // a job id, a request id, or words of the prompt
	UserID    int    // admin view only: one person's jobs
	// AllAccounts is the admin view: every account's jobs, the principal
	// unused.
	AllAccounts bool
}

// MediaJobConsoleSummary is the strip above the Tasks table.
type MediaJobConsoleSummary struct {
	Tasks            int   `json:"tasks"`
	SpendMicrousd    int64 `json:"spend_microusd"`
	Active           int   `json:"active"`
	ActiveHoldMicro  int64 `json:"active_hold_microusd"`
	Failed           int   `json:"failed"`
	RefundedMicrousd int64 `json:"refunded_microusd"`
	ExpiringSoon     int   `json:"expiring_soon"`
}

func mediaJobConsoleQuery(principal ProjectPrincipal, filter MediaJobConsoleFilter) *gorm.DB {
	query := CoreDB().Model(&MediaJob{})
	if !filter.AllAccounts {
		query = requestResourceScope(query, principal.ActorUserID, []ProjectPrincipal{principal})
	}
	if filter.UserID > 0 {
		query = query.Where("actor_user_id = ? OR (actor_user_id = 0 AND user_id = ?)", filter.UserID, filter.UserID)
	}
	switch filter.Status {
	case MediaJobConsoleStatusActive:
		query = query.Where("status IN ?", []string{MediaJobStatusPending, MediaJobStatusRunning})
	case MediaJobStatusSucceeded, MediaJobStatusFailed:
		query = query.Where("status = ?", filter.Status)
	}
	if kind := normalizeMediaJobKind(filter.Kind); IsValidMediaJobKind(kind) {
		query = query.Where("kind = ?", kind)
	}
	if filter.Start > 0 {
		query = query.Where("created_at >= ?", time.Unix(filter.Start, 0).UTC())
	}
	if filter.End > 0 {
		query = query.Where("created_at <= ?", time.Unix(filter.End, 0).UTC())
	}
	if name := strings.TrimSpace(filter.ModelName); name != "" {
		query = query.Where("model_name = ?", name)
	}
	if filter.TokenID > 0 {
		query = query.Where("token_id = ?", filter.TokenID)
	}
	if q := strings.TrimSpace(filter.Query); q != "" {
		like := "%" + escapeLike(strings.ToLower(q)) + "%"
		query = query.Where("(LOWER(public_id) LIKE ? ESCAPE '\\' OR LOWER(request_id) LIKE ? ESCAPE '\\' OR LOWER(CAST(request_snapshot AS TEXT)) LIKE ? ESCAPE '\\')", like, like, like)
	}
	return query
}

func escapeLike(value string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(value)
}

// ListMediaJobsForConsole pages the jobs a console principal may see, newest
// first, with the total that matches.
func ListMediaJobsForConsole(principal ProjectPrincipal, filter MediaJobConsoleFilter, offset int, limit int) ([]MediaJob, int64, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}
	var total int64
	if err := mediaJobConsoleQuery(principal, filter).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []MediaJob
	err := mediaJobConsoleQuery(principal, filter).Order("created_at DESC, id DESC").Offset(offset).Limit(limit).Find(&rows).Error
	return rows, total, err
}

// GetMediaJobForConsole finds one job a console principal may see.
func GetMediaJobForConsole(principal ProjectPrincipal, publicID string) (*MediaJob, error) {
	var row MediaJob
	err := requestResourceScope(CoreDB(), principal.ActorUserID, []ProjectPrincipal{principal}).
		Where("public_id = ?", strings.TrimSpace(publicID)).First(&row).Error
	if err != nil {
		return nil, err
	}
	return &row, nil
}

// SummarizeMediaJobsForConsole adds up the jobs that match filter, ignoring
// its status so the strip reads the same whichever status tab is open.
func SummarizeMediaJobsForConsole(principal ProjectPrincipal, filter MediaJobConsoleFilter, now time.Time) (MediaJobConsoleSummary, error) {
	filter.Status = ""
	var rows []MediaJob
	err := mediaJobConsoleQuery(principal, filter).
		Select("id", "status", "cost_snapshot", "ledger_input_json", "request_snapshot", "artifact_file_id", "billing_settled_at", "user_id", "project_id", "token_id", "actor_user_id", "payer_user_id", "request_id").
		Order("created_at DESC, id DESC").Limit(mediaJobConsoleSummaryCap).Find(&rows).Error
	if err != nil {
		return MediaJobConsoleSummary{}, err
	}
	charges, err := MediaJobChargedAmounts(rows)
	if err != nil {
		// The request lookup is secondary: the strip falls back to the
		// charges the jobs carry themselves.
		common.SysError("console tasks summary: read request charges: " + err.Error())
	}
	summary := MediaJobConsoleSummary{Tasks: len(rows)}
	artifactIDs := make([]string, 0)
	for i, row := range rows {
		hold := int64(MediaJobHoldAmount(row))
		switch row.Status {
		case MediaJobStatusPending, MediaJobStatusRunning:
			summary.Active++
			summary.ActiveHoldMicro += hold
		case MediaJobStatusFailed:
			summary.Failed++
			summary.RefundedMicrousd += hold
		case MediaJobStatusSucceeded:
			charge := int64(charges[i])
			summary.SpendMicrousd += charge
			if hold > charge {
				summary.RefundedMicrousd += hold - charge
			}
			if row.ArtifactFileID != "" {
				artifactIDs = append(artifactIDs, row.ArtifactFileID)
			}
		}
	}
	if len(artifactIDs) > 0 {
		var count int64
		soon := now.Add(24 * time.Hour).Unix()
		if err := CoreDB().Model(&File{}).Where("id IN ? AND expires_at > ? AND expires_at <= ?", artifactIDs, now.Unix(), soon).Count(&count).Error; err != nil {
			return summary, err
		}
		summary.ExpiringSoon = int(count)
	}
	return summary, nil
}

// MediaJobHoldAmount is what the job held from the balance at submission.
// The gateway ledger keeps it in the job's ledger input; the legacy ledger
// in its request snapshot.
func MediaJobHoldAmount(job MediaJob) billing.MicroUSD {
	if job.LedgerInputJSON != "" {
		var input ledger.Input
		if err := common.Unmarshal([]byte(job.LedgerInputJSON), &input); err == nil && input.Amount > 0 {
			return billing.MicroUSD(input.Amount)
		}
	}
	if amount := MediaJobBillingReservationAmount(job); amount > 0 {
		return amount
	}
	if job.Status == MediaJobStatusPending || job.Status == MediaJobStatusRunning || job.Status == MediaJobStatusFailed {
		return mediaJobSnapshotCharge(job)
	}
	return 0
}

// MediaJobChargedAmount is what a settled job charged; 0 before settlement.
func MediaJobChargedAmount(job MediaJob) billing.MicroUSD {
	if job.Status != MediaJobStatusSucceeded {
		return 0
	}
	return mediaJobSnapshotCharge(job)
}

// MediaJobChargedAmounts is MediaJobChargedAmount for each job, in order. A
// succeeded job that never settled itself (no hold, no settlement, no
// charge in its snapshot) was billed as the ordinary gateway request it
// records, so its charge is that request's settled financial operations for
// the job's payer.
func MediaJobChargedAmounts(jobs []MediaJob) ([]billing.MicroUSD, error) {
	charges := make([]billing.MicroUSD, len(jobs))
	requestIDs := make([]string, 0)
	for i, job := range jobs {
		charges[i] = MediaJobChargedAmount(job)
		if mediaJobBilledByRequest(job, charges[i]) {
			requestIDs = append(requestIDs, job.RequestID)
		}
	}
	if len(requestIDs) == 0 || CoreDB() == nil {
		return charges, nil
	}
	type requestCharge struct {
		RequestID string
		PayerID   int
		Amount    int64
	}
	byRequest := map[string]map[int]int64{}
	const chunk = 500
	for start := 0; start < len(requestIDs); start += chunk {
		var rows []requestCharge
		if err := CoreDB().Model(&RedisLedgerOperation{}).
			Select("request_id, payer_id, SUM(amount) AS amount").
			Where("kind IN ? AND request_id IN ?", []string{"settle", "refund"}, requestIDs[start:min(start+chunk, len(requestIDs))]).
			Group("request_id, payer_id").Scan(&rows).Error; err != nil {
			return charges, err
		}
		for _, row := range rows {
			if byRequest[row.RequestID] == nil {
				byRequest[row.RequestID] = map[int]int64{}
			}
			byRequest[row.RequestID][row.PayerID] = row.Amount
		}
	}
	for i, job := range jobs {
		if !mediaJobBilledByRequest(job, charges[i]) {
			continue
		}
		if amount := byRequest[job.RequestID][job.BillingUserID()]; amount > 0 {
			charges[i] = billing.MicroUSD(amount)
		}
	}
	return charges, nil
}

func mediaJobBilledByRequest(job MediaJob, snapshotCharge billing.MicroUSD) bool {
	return job.Status == MediaJobStatusSucceeded && snapshotCharge == 0 && job.BillingSettledAt == 0 &&
		job.LedgerInputJSON == "" && MediaJobBillingReservationAmount(job) == 0 && job.RequestID != ""
}

func mediaJobSnapshotCharge(job MediaJob) billing.MicroUSD {
	fields := map[string]any{}
	if len(job.CostSnapshot) == 0 || common.Unmarshal(job.CostSnapshot, &fields) != nil {
		return 0
	}
	amount, _ := int64FromAny(fields["customer_charge_microusd"])
	return billing.MicroUSD(amount)
}

// TokenNamesByID resolves Key names for display, keyed by token id.
func TokenNamesByID(ids []int) (map[int]string, error) {
	out := make(map[int]string, len(ids))
	if len(ids) == 0 || CoreDB() == nil {
		return out, nil
	}
	var rows []Token
	if err := CoreDB().Select("id", "name").Where("id IN ?", ids).Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		out[row.Id] = row.Name
	}
	return out, nil
}

// UserNamesByID resolves account names for the admin Tasks view.
func UserNamesByID(ids []int) (map[int]string, error) {
	out := make(map[int]string, len(ids))
	if len(ids) == 0 || CoreDB() == nil {
		return out, nil
	}
	var rows []User
	if err := CoreDB().Select("id", "username").Where("id IN ?", ids).Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		out[row.Id] = row.Username
	}
	return out, nil
}
