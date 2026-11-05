package controller

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/httperr"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/service"
	"github.com/lazu-ai/lazu/internal/types"
	"gorm.io/gorm"
)

// The upstream check: on a schedule each source re-reads its upstream's model
// list and compares it with what it serves. A model the upstream starts
// listing waits for the operator (or is served at once, if they asked); a
// served model the upstream stops listing is stopped (or only flagged). Every
// check leaves a line in the source's log.

const (
	upstreamSyncLogLimit     = 30
	upstreamSyncStoppedLimit = 100
	// A check never stops more than this share of what a source serves in
	// one go: an upstream that answers with half its list is more likely
	// broken than retiring half its models.
	upstreamSyncMassGoneMin = 3
)

var errUpstreamListEmpty = errors.New("上游返回的模型列表是空的")

// upstreamSyncSummary is the slice of the state the source list shows.
type upstreamSyncSummary struct {
	IntervalHours int    `json:"interval_hours"`
	CheckedAt     int64  `json:"checked_at,omitempty"`
	Error         string `json:"error,omitempty"`
	New           int    `json:"new"`
	Gone          int    `json:"gone"`
}

func newUpstreamSyncSummary(channel *model.Channel) *upstreamSyncSummary {
	settings := channel.GetOtherSettings()
	cfg := settings.UpstreamSync.Normalized()
	state := reconcileUpstreamState(settings.UpstreamSyncState, cfg, channel)
	if cfg.IntervalHours == 0 && state == nil {
		return nil
	}
	summary := &upstreamSyncSummary{IntervalHours: cfg.IntervalHours}
	if state != nil {
		summary.CheckedAt = state.CheckedAt
		summary.Error = state.Error
		summary.New = len(state.New)
		summary.Gone = len(state.Gone) + len(state.Stopped)
	}
	return summary
}

// upstreamServed is what a source serves: public model names, and the
// upstream ID each one is requested as.
type upstreamServed struct {
	names      []string
	upstreamOf map[string]string
}

func newUpstreamServed(channel *model.Channel) upstreamServed {
	served := upstreamServed{upstreamOf: map[string]string{}}
	mapping := map[string]string{}
	if raw := strings.TrimSpace(channel.GetModelMapping()); raw != "" && raw != "{}" {
		_ = common.Unmarshal([]byte(raw), &mapping)
	}
	for public, upstream := range mapping {
		if upstream = strings.TrimSpace(upstream); upstream != "" {
			served.upstreamOf[strings.TrimSpace(public)] = upstream
		}
	}
	for _, name := range channel.GetModels() {
		if name = strings.TrimSpace(name); name != "" {
			served.names = append(served.names, name)
		}
	}
	return served
}

// upstreamID is the ID a public name is requested as upstream.
func (s upstreamServed) upstreamID(name string) string {
	if id := s.upstreamOf[name]; id != "" {
		return id
	}
	return name
}

func (s upstreamServed) servesUpstream() map[string]bool {
	out := make(map[string]bool, len(s.names)*2)
	for _, name := range s.names {
		out[name] = true
		out[s.upstreamID(name)] = true
	}
	return out
}

func (s upstreamServed) serves(name string) bool {
	for _, served := range s.names {
		if served == name {
			return true
		}
	}
	return false
}

// upstreamDiff is one check's outcome.
type upstreamDiff struct {
	state dto.UpstreamSyncState
	add   []string // public names to start serving
	stop  []string // public names to stop serving
	entry dto.UpstreamSyncLogEntry
}

// diffUpstream compares a fresh upstream list with the previous state and what
// the source serves, and decides what changes. It has no side effects.
func diffUpstream(cfg dto.UpstreamSyncSettings, prev *dto.UpstreamSyncState, served upstreamServed, listed []string, now int64, manual bool) upstreamDiff {
	if prev == nil {
		prev = &dto.UpstreamSyncState{}
	}
	ignore, _ := dto.CompileUpstreamIgnore(cfg.Ignore)
	listedSet := toSet(listed)
	servedUpstream := served.servesUpstream()
	firstList := prev.ListedAt == 0
	prevListed := toSet(prev.Upstream)

	// New: listed now, not listed last time, not served, not ignored. The
	// first list has nothing to compare with, so it flags nothing.
	var appeared []string
	if !firstList {
		for _, id := range listed {
			if !prevListed[id] && !servedUpstream[id] && !ignore.Match(id) {
				appeared = append(appeared, id)
			}
		}
	}

	// Gone: served, but neither its upstream ID nor its own name is listed.
	var gone []string
	for _, name := range served.names {
		if !listedSet[served.upstreamID(name)] && !listedSet[name] {
			gone = append(gone, name)
		}
	}
	prevGone := toSet(append(append([]string{}, prev.Gone...), prev.Stopped...))
	var newlyGone []string
	for _, name := range gone {
		if !prevGone[name] {
			newlyGone = append(newlyGone, name)
		}
	}

	out := upstreamDiff{entry: dto.UpstreamSyncLogEntry{
		At:     now,
		Manual: manual,
		Listed: len(listed),
		New:    appeared,
		Gone:   newlyGone,
	}}

	pending := map[string]bool{}
	for _, id := range append(append([]string{}, prev.New...), appeared...) {
		if listedSet[id] && !servedUpstream[id] && !ignore.Match(id) {
			pending[id] = true
		}
	}
	if cfg.OnNew == dto.UpstreamOnNewAuto {
		for _, id := range appeared {
			out.add = append(out.add, id)
			delete(pending, id)
		}
		out.entry.Added = out.add
	}

	stopping := map[string]bool{}
	if cfg.OnGone == dto.UpstreamOnGoneStop && len(gone) > 0 {
		if len(gone) >= upstreamSyncMassGoneMin && len(gone)*2 > len(served.names) {
			out.entry.Note = fmt.Sprintf("一次有 %d 个在供货的模型不在上游列表里，超过一半，没有自动停止，请核对", len(gone))
		} else {
			for _, name := range gone {
				stopping[name] = true
				out.stop = append(out.stop, name)
			}
			out.entry.Stopped = out.stop
		}
	}

	out.state = dto.UpstreamSyncState{
		CheckedAt: now,
		ListedAt:  now,
		Upstream:  append([]string{}, listed...),
		New:       sortedKeys(pending),
	}
	for _, name := range gone {
		if !stopping[name] {
			out.state.Gone = append(out.state.Gone, name)
		}
	}
	// Stopped models stay on record until the upstream lists them again
	// (then they come back as new) or someone serves them again.
	stopped := map[string]bool{}
	for _, name := range prev.Stopped {
		if !listedSet[served.upstreamID(name)] && !listedSet[name] && !served.serves(name) {
			stopped[name] = true
		}
	}
	for name := range stopping {
		stopped[name] = true
	}
	out.state.Stopped = sortedKeys(stopped)
	if len(out.state.Stopped) > upstreamSyncStoppedLimit {
		out.state.Stopped = out.state.Stopped[:upstreamSyncStoppedLimit]
	}
	// The first list may itself be the moment an upstream turned out to be
	// missing models; those are worth saying, but nothing appeared.
	if firstList && len(out.entry.Note) == 0 && len(listed) > 0 {
		out.entry.Note = "第一次读取上游列表，之后的检查会和它比"
	}
	out.state.Log = appendUpstreamLog(prev.Log, out.entry)
	return out
}

// reconcileUpstreamState drops what the operator has already dealt with since
// the last check: a pending model they now serve, a flagged one they stopped
// serving, a stopped one they serve again.
func reconcileUpstreamState(state *dto.UpstreamSyncState, cfg dto.UpstreamSyncSettings, channel *model.Channel) *dto.UpstreamSyncState {
	if state == nil {
		return nil
	}
	served := newUpstreamServed(channel)
	servedUpstream := served.servesUpstream()
	ignore, _ := dto.CompileUpstreamIgnore(cfg.Ignore)
	out := *state
	out.New = nil
	for _, id := range state.New {
		if !servedUpstream[id] && !ignore.Match(id) {
			out.New = append(out.New, id)
		}
	}
	out.Gone = nil
	for _, name := range state.Gone {
		if served.serves(name) {
			out.Gone = append(out.Gone, name)
		}
	}
	out.Stopped = nil
	for _, name := range state.Stopped {
		if !served.serves(name) {
			out.Stopped = append(out.Stopped, name)
		}
	}
	return &out
}

func appendUpstreamLog(log []dto.UpstreamSyncLogEntry, entry dto.UpstreamSyncLogEntry) []dto.UpstreamSyncLogEntry {
	out := make([]dto.UpstreamSyncLogEntry, 0, upstreamSyncLogLimit)
	out = append(out, entry)
	for _, previous := range log {
		if len(out) >= upstreamSyncLogLimit {
			break
		}
		out = append(out, previous)
	}
	return out
}

func toSet(values []string) map[string]bool {
	out := make(map[string]bool, len(values))
	for _, value := range values {
		out[value] = true
	}
	return out
}

func sortedKeys(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for key := range set {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

// upstreamCheckError is the one line the log shows for a failed read.
func upstreamCheckError(err error) string {
	if errors.Is(err, errUpstreamListEmpty) {
		return err.Error()
	}
	var statusErr *upstreamStatusError
	if errors.As(err, &statusErr) {
		switch statusErr.status {
		case http.StatusUnauthorized, http.StatusForbidden:
			return fmt.Sprintf("%d 密钥无效或没有权限", statusErr.status)
		case http.StatusPaymentRequired:
			return "402 上游余额不足"
		case http.StatusNotFound:
			return "404 上游没有模型列表接口"
		case http.StatusTooManyRequests:
			return "429 上游限流"
		}
		return fmt.Sprintf("上游返回 %d", statusErr.status)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "上游超时没有回应"
	}
	message := strings.TrimSpace(err.Error())
	if utf8.RuneCountInString(message) > 160 {
		message = string([]rune(message)[:160]) + "…"
	}
	return message
}

// runUpstreamCheck reads one source's upstream list now and applies the
// result. It returns the state after the check.
func runUpstreamCheck(ctx context.Context, channelID int, manual bool) (*dto.UpstreamSyncState, error) {
	channel, err := model.GetChannelById(channelID, true)
	if err != nil {
		return nil, err
	}
	settings := channel.GetOtherSettings()
	cfg := settings.UpstreamSync.Normalized()

	baseURL := constant.ChannelBaseURLs[channel.Type]
	if channel.GetBaseURL() != "" {
		baseURL = channel.GetBaseURL()
	}
	key := strings.Split(channel.Key, "\n")[0]
	listed, fetchErr := fetchUpstreamModelIDs(baseURL, channel.Type, key, channel)
	if fetchErr == nil && len(listed) == 0 {
		fetchErr = errUpstreamListEmpty
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	now := time.Now().Unix()

	var diff upstreamDiff
	var after *dto.UpstreamSyncState
	err = model.SaveUpstreamSyncState(channelID, func(stored *dto.ChannelOtherSettings) error {
		prev := stored.UpstreamSyncState
		if fetchErr != nil {
			next := dto.UpstreamSyncState{}
			if prev != nil {
				next = *prev
			}
			next.CheckedAt = now
			next.Error = upstreamCheckError(fetchErr)
			next.Log = appendUpstreamLog(next.Log, dto.UpstreamSyncLogEntry{At: now, Manual: manual, Error: next.Error})
			stored.UpstreamSyncState = &next
			after = &next
			return nil
		}
		diff = diffUpstream(cfg, prev, newUpstreamServed(channel), listed, now, manual)
		stored.UpstreamSyncState = &diff.state
		after = &diff.state
		return nil
	})
	if err != nil {
		return nil, err
	}
	if fetchErr == nil && (len(diff.add) > 0 || len(diff.stop) > 0) {
		if err := applyUpstreamModelChanges(channelID, diff.add, diff.stop); err != nil {
			return after, err
		}
	}
	if !manual && cfg.NotifyOps {
		notifyUpstreamChange(channel, after, fetchErr, diff.entry)
	}
	return after, nil
}

// applyUpstreamModelChanges rewrites the source's model list the way the
// console's own save would, abilities included.
func applyUpstreamModelChanges(channelID int, add []string, stop []string) error {
	channel, err := model.GetChannelById(channelID, false)
	if err != nil {
		return err
	}
	removing := toSet(stop)
	seen := map[string]bool{}
	kept := make([]string, 0, len(channel.GetModels())+len(add))
	for _, name := range channel.GetModels() {
		name = strings.TrimSpace(name)
		if name == "" || removing[name] || seen[name] {
			continue
		}
		seen[name] = true
		kept = append(kept, name)
	}
	for _, name := range add {
		if !seen[name] {
			seen[name] = true
			kept = append(kept, name)
		}
	}
	channel.Models = strings.Join(kept, ",")
	err = model.CoreDB().Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&model.Channel{}).Where("id = ?", channelID).Update("models", channel.Models).Error; err != nil {
			return err
		}
		return channel.UpdateAbilities(tx)
	})
	if err != nil {
		return err
	}
	model.InitChannelCache()
	InvalidateChannelHealthCache()
	return nil
}

func notifyUpstreamChange(channel *model.Channel, state *dto.UpstreamSyncState, fetchErr error, entry dto.UpstreamSyncLogEntry) {
	var subject string
	facts := []common.MailFact{{Label: "来源", Value: fmt.Sprintf("%s（#%d）", channel.Name, channel.Id)}}
	switch {
	case fetchErr != nil:
		// One failed read is noise; say so once it has failed twice running.
		if state == nil || len(state.Log) < 2 || state.Log[1].Error == "" {
			return
		}
		subject = "上游模型列表读不到：" + channel.Name
		facts = append(facts, common.MailFact{Label: "原因", Value: state.Error})
	case len(entry.New) > 0 || len(entry.Gone) > 0:
		subject = "上游模型有变化：" + channel.Name
		if len(entry.New) > 0 {
			facts = append(facts, common.MailFact{Label: "新出现", Value: strings.Join(entry.New, "、"), Mono: true})
		}
		if len(entry.Gone) > 0 {
			facts = append(facts, common.MailFact{Label: "不再列出", Value: strings.Join(entry.Gone, "、"), Mono: true})
		}
		if len(entry.Added) > 0 {
			facts = append(facts, common.MailFact{Label: "已开始供货", Value: strings.Join(entry.Added, "、"), Mono: true})
		}
		if len(entry.Stopped) > 0 {
			facts = append(facts, common.MailFact{Label: "已停止供货", Value: strings.Join(entry.Stopped, "、"), Mono: true})
		}
		if entry.Note != "" {
			facts = append(facts, common.MailFact{Label: "说明", Value: entry.Note})
		}
	default:
		return
	}
	mail := common.Mail{
		Severity: "NOTICE",
		Source:   "channel",
		Title:    subject,
		Lead:     "来源的「同步」页有完整记录，待核对的模型在「模型」页的「上游新增」和「上游已无」里。",
		Blocks:   []common.MailBlock{{Facts: facts}},
	}
	if err := service.SendOpsMail(subject, mail); err != nil {
		common.SysLog(fmt.Sprintf("upstream sync notice for channel %d: %v", channel.Id, err))
	}
}

// RunDueUpstreamChecks checks every source whose interval has passed, one at a
// time. The worker task calls it.
func RunDueUpstreamChecks(ctx context.Context) error {
	var channels []*model.Channel
	if err := model.CoreDB().Model(&model.Channel{}).
		Select("id, status, settings").
		Where("settings LIKE ?", "%upstream_sync%").
		Find(&channels).Error; err != nil {
		return err
	}
	now := time.Now().Unix()
	var failures []error
	for _, channel := range channels {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if channel.Status == common.ChannelStatusManuallyDisabled {
			continue
		}
		settings := channel.GetOtherSettings()
		cfg := settings.UpstreamSync.Normalized()
		if cfg.IntervalHours <= 0 {
			continue
		}
		if state := settings.UpstreamSyncState; state != nil && now-state.CheckedAt < int64(cfg.IntervalHours)*3600 {
			continue
		}
		if _, err := runUpstreamCheck(ctx, channel.Id, false); err != nil {
			failures = append(failures, fmt.Errorf("channel %d: %w", channel.Id, err))
		}
	}
	return errors.Join(failures...)
}

// upstreamSyncView is what the source page's 模型 and 同步 tabs read.
type upstreamSyncView struct {
	Settings dto.UpstreamSyncSettings `json:"settings"`
	State    *dto.UpstreamSyncState   `json:"state"`
	// NextCheckAt is when the schedule will check next; 0 when it is off.
	NextCheckAt int64 `json:"next_check_at"`
	// Requests7d is each served model's request count on this source over
	// the last seven days, when routing telemetry is available.
	Requests7d map[string]int64 `json:"requests_7d"`
}

func buildUpstreamSyncView(channel *model.Channel) upstreamSyncView {
	settings := channel.GetOtherSettings()
	cfg := settings.UpstreamSync.Normalized()
	view := upstreamSyncView{
		Settings:   cfg,
		State:      reconcileUpstreamState(settings.UpstreamSyncState, cfg, channel),
		Requests7d: map[string]int64{},
	}
	if cfg.IntervalHours > 0 {
		view.NextCheckAt = time.Now().Unix()
		if view.State != nil && view.State.CheckedAt > 0 {
			view.NextCheckAt = view.State.CheckedAt + int64(cfg.IntervalHours)*3600
		}
	}
	names := channel.GetModels()
	if len(names) > 0 {
		since := modelOpsWindowStart(modelOpsMaxWindowHours)
		if health, ok, err := modelOpsModelChannelHealth(names, []int{channel.Id}, since, modelOpsMaxWindowHours); err == nil && ok {
			for _, name := range names {
				if record, found := health[modelOpsCostKey(name, channel.Id)]; found {
					view.Requests7d[name] = int64(record.RequestCount)
				}
			}
		}
	}
	return view
}

// GetModelOpsSourceUpstream returns a source's upstream check settings, state
// and the per-model traffic its 模型 tab shows.
func GetModelOpsSourceUpstream(c *gin.Context) {
	channelID, err := resolveChannelID(c.Param("id"))
	if err != nil {
		httperr.Write(c, err)
		return
	}
	channel, err := model.GetChannelById(channelID, false)
	if err != nil {
		httperr.Write(c, err)
		return
	}
	common.ApiSuccess(c, buildUpstreamSyncView(channel))
}

// CheckModelOpsSourceUpstream reads the upstream list now, whatever the
// schedule says, and applies the result as a scheduled check would.
func CheckModelOpsSourceUpstream(c *gin.Context) {
	channelID, err := resolveChannelID(c.Param("id"))
	if err != nil {
		httperr.Write(c, err)
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 90*time.Second)
	defer cancel()
	if _, err := runUpstreamCheck(ctx, channelID, true); err != nil {
		httperr.Write(c, err)
		return
	}
	channel, err := model.GetChannelById(channelID, false)
	if err != nil {
		httperr.Write(c, err)
		return
	}
	model.WriteAdminAuditLog(c, "model_ops.source.upstream_check", "channel", channel.PublicID, channel.Name, nil, nil)
	common.ApiSuccess(c, buildUpstreamSyncView(channel))
}

type resolveUpstreamRequest struct {
	// New dismisses upstream IDs from the 上游新增 list without serving them.
	New []string `json:"new"`
	// Gone clears models from the 上游已无 list.
	Gone []string `json:"gone"`
}

// ResolveModelOpsSourceUpstream clears flagged models the operator has looked
// at and decided to leave as they are.
func ResolveModelOpsSourceUpstream(c *gin.Context) {
	channelID, err := resolveChannelID(c.Param("id"))
	if err != nil {
		httperr.Write(c, err)
		return
	}
	var req resolveUpstreamRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httperr.Code(c, types.ErrorCodeBadRequestBody)
		return
	}
	if len(req.New) == 0 && len(req.Gone) == 0 {
		httperr.Invalid(c, "new")
		return
	}
	dropNew, dropGone := toSet(req.New), toSet(req.Gone)
	err = model.SaveUpstreamSyncState(channelID, func(stored *dto.ChannelOtherSettings) error {
		state := stored.UpstreamSyncState
		if state == nil {
			return nil
		}
		keep := func(values []string, drop map[string]bool) []string {
			var out []string
			for _, value := range values {
				if !drop[value] {
					out = append(out, value)
				}
			}
			return out
		}
		state.New = keep(state.New, dropNew)
		state.Gone = keep(state.Gone, dropGone)
		state.Stopped = keep(state.Stopped, dropGone)
		return nil
	})
	if err != nil {
		httperr.Write(c, err)
		return
	}
	channel, err := model.GetChannelById(channelID, false)
	if err != nil {
		httperr.Write(c, err)
		return
	}
	model.WriteAdminAuditLog(c, "model_ops.source.upstream_resolve", "channel", channel.PublicID, channel.Name, nil, req)
	common.ApiSuccess(c, buildUpstreamSyncView(channel))
}
