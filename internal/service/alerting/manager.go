package alerting

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/robfig/cron/v3"
)

const (
	incidentKeyPrefix = "lazu:alerts:incident:v1:"
	healthKey         = "lazu:alerts:health:v1"
	incidentTTL       = 30 * 24 * time.Hour
	deliveryLeaseTTL  = 30 * time.Second
)

type Priority string

const (
	PriorityMaintenance Priority = "maintenance"
	PriorityHigh        Priority = "high"
)

type Incident struct {
	Key         string
	Fingerprint string
	Message     string
	Priority    Priority
}

type incidentState struct {
	Fingerprint        string `json:"fingerprint"`
	FirstSeen          int64  `json:"first_seen"`
	LastSeen           int64  `json:"last_seen"`
	LastNotified       int64  `json:"last_notified"`
	Occurrences        int64  `json:"occurrences"`
	ReminderStep       int    `json:"reminder_step"`
	CauseChanged       bool   `json:"cause_changed,omitempty"`
	DeliveryToken      string `json:"delivery_token,omitempty"`
	DeliveryReservedAt int64  `json:"delivery_reserved_at,omitempty"`
	RecoveryToken      string `json:"recovery_token,omitempty"`
	RecoveryReservedAt int64  `json:"recovery_reserved_at,omitempty"`
	// Title is the first line of the latest message, so the daily digest can
	// name an incident that is still open after its reminders ran out.
	Title string `json:"title,omitempty"`
}

type HealthSnapshot struct {
	Key       string `json:"key"`
	Label     string `json:"label"`
	Status    string `json:"status"`
	Summary   string `json:"summary"`
	UpdatedAt int64  `json:"updated_at"`
}

var (
	nowFunc  = time.Now
	postText = common.PostFeishuText

	localMu        sync.Mutex
	localIncidents = map[string]incidentState{}
	localResolved  = map[string]bool{}
	localHealth    = map[string]HealthSnapshot{}
	localDigestDay string
	lastRedisError time.Time
	digestCron     *cron.Cron
)

func Report(event Incident) {
	event.Key = strings.TrimSpace(event.Key)
	event.Fingerprint = strings.TrimSpace(event.Fingerprint)
	event.Message = strings.TrimSpace(event.Message)
	if event.Key == "" || event.Fingerprint == "" || event.Message == "" {
		common.SysError("alerting: key, fingerprint, and message are required")
		return
	}
	if event.Priority == "" {
		event.Priority = PriorityMaintenance
	}

	now := nowFunc()
	deliveryToken := newDeliveryToken()
	redisBacked := common.RedisEnabled && common.RDB != nil
	if !common.RedisEnabled || common.RDB == nil {
		state, send, changed := updateLocalIncident(event, now, deliveryToken)
		if !send {
			return
		}
		if postIncident(event.Message, state, changed) != nil {
			releaseLocalDelivery(event.Key, state)
			return
		}
		markLocalIncidentNotified(event.Key, state, now)
		return
	}
	state, send, changed, err := updateRedisIncident(event, now, deliveryToken)
	if err != nil {
		logRedisFallback(err)
		redisBacked = false
		state, send, changed = updateLocalIncident(event, now, deliveryToken)
	} else {
		cacheLocalIncident(event.Key, state)
	}
	if !send {
		return
	}
	if postIncident(event.Message, state, changed) != nil {
		releaseLocalDelivery(event.Key, state)
		if redisBacked {
			if err := releaseRedisDelivery(event.Key, state); err != nil {
				logRedisFallback(err)
			}
		}
		return
	}
	markLocalIncidentNotified(event.Key, state, now)
	if redisBacked {
		if err := markRedisIncidentNotified(event.Key, state, now); err != nil {
			logRedisFallback(err)
		}
	}
}

func postIncident(message string, state incidentState, changed bool) error {
	if changed {
		message += "\nincident: new or changed root cause"
	} else if state.LastNotified > 0 {
		message += fmt.Sprintf("\nreminder: unchanged; occurrences=%d", state.Occurrences)
	}
	if err := postText(message); err != nil {
		common.SysError("alerting: Feishu incident delivery failed: " + err.Error())
		return err
	}
	return nil
}

func Resolve(key string, message string) {
	key = strings.TrimSpace(key)
	message = strings.TrimSpace(message)
	if key == "" || message == "" {
		return
	}
	now := nowFunc()
	recoveryToken := newDeliveryToken()
	localState, localExists := peekLocalIncident(key)
	if !common.RedisEnabled || common.RDB == nil {
		resolveLocalIncident(key, message, localState, localExists, recoveryToken, now)
		return
	}
	redisState, redisExists, err := loadRedisIncidentByKey(key)
	if err != nil {
		logRedisFallback(err)
		resolveLocalIncident(key, message, localState, localExists, recoveryToken, now)
		return
	}

	wasResolvedLocally := localWasResolved(key)
	if wasResolvedLocally && !localExists {
		if err := deleteRedisIncident(key, redisState, redisExists); err != nil {
			logRedisFallback(err)
			return
		}
		clearLocalResolved(key)
		return
	}
	redisResolved := redisExists
	if wasResolvedLocally {
		// Redis may still contain the incident that was already recovered while
		// Redis was unavailable. A local incident at this point is a recurrence.
		redisResolved = false
	}
	state, resolved := mergeResolvedIncident(redisState, redisResolved, localState, localExists)
	if !resolved {
		return
	}
	state, acquired, err := reserveRedisRecovery(key, state, recoveryToken, now)
	if err != nil {
		logRedisFallback(err)
		return
	}
	if !acquired {
		return
	}
	cacheLocalIncident(key, state)
	if !postRecoveryIfNeeded(message, state, true) {
		releaseLocalRecovery(key, state)
		if err := releaseRedisRecovery(key, state); err != nil {
			logRedisFallback(err)
		}
		return
	}
	if err := deleteRedisRecovery(key, state); err != nil {
		logRedisFallback(err)
		return
	}
	deleteLocalIncident(key, state)
	clearLocalResolved(key)
}

func resolveLocalIncident(key string, message string, state incidentState, exists bool, recoveryToken string, now time.Time) {
	if !exists {
		return
	}
	state, acquired := reserveLocalRecovery(key, state, recoveryToken, now)
	if !acquired {
		return
	}
	if !postRecoveryIfNeeded(message, state, true) {
		releaseLocalRecovery(key, state)
		return
	}
	deleteLocalIncident(key, state)
	markLocalResolved(key)
}

func postRecoveryIfNeeded(message string, state incidentState, resolved bool) bool {
	if !resolved {
		return true
	}
	if state.LastNotified == 0 {
		// The failure was never accepted by Feishu, so a recovery-only message
		// would be misleading. The caller still clears the resolved state.
		return true
	}
	duration := nowFunc().Sub(time.Unix(state.FirstSeen, 0)).Truncate(time.Minute)
	if duration < 0 {
		duration = 0
	}
	if err := postText(fmt.Sprintf("%s\nduration=%s occurrences=%d", message, duration, state.Occurrences)); err != nil {
		common.SysError("alerting: Feishu recovery delivery failed: " + err.Error())
		return false
	}
	return true
}

func RecordHealth(key string, label string, status string, summary string) {
	snapshot := HealthSnapshot{
		Key:       strings.TrimSpace(key),
		Label:     strings.TrimSpace(label),
		Status:    strings.TrimSpace(status),
		Summary:   strings.TrimSpace(summary),
		UpdatedAt: nowFunc().Unix(),
	}
	if snapshot.Key == "" || snapshot.Label == "" || snapshot.Status == "" {
		return
	}
	raw, err := json.Marshal(snapshot)
	if err == nil && common.RedisEnabled && common.RDB != nil {
		if err = persistHealthSnapshot(snapshot.Key, raw); err != nil {
			common.SysError("alerting: failed to persist health snapshot after retries: " + err.Error())
		}
	}
	localMu.Lock()
	localHealth[snapshot.Key] = snapshot
	localMu.Unlock()
}

func StartDailyDigestScheduler() {
	loc, err := time.LoadLocation(common.AppConfig.Upload.Timezone)
	if err != nil {
		loc = time.FixedZone("Asia/Shanghai", 8*60*60)
	}
	if digestCron != nil {
		digestCron.Stop()
	}
	digestCron = cron.New(cron.WithLocation(loc))
	// 09:10 runs after the 09:00 hourly upload so the digest includes its
	// result and does not contend with the R2 connection burst. The later
	// ticks only retry: the daily claim lets one delivery through, and a
	// failed delivery or a process that was down at 09:10 tries again.
	if _, err = digestCron.AddFunc("10,25,40,55 9 * * *", sendDailyDigest); err != nil {
		common.SysError("alerting: failed to schedule daily digest: " + err.Error())
		return
	}
	digestCron.Start()
}

func sendDailyDigest() {
	now := nowFunc()
	if !claimDigest(now) {
		return
	}
	snapshots, err := loadHealthSnapshots()
	if err != nil {
		common.SysError("alerting: failed to load health snapshots: " + err.Error())
		releaseDigestClaim(now)
		return
	}
	incidents, err := loadOpenIncidents()
	if err != nil {
		// Health is the core of the digest; send it with the incidents this
		// process knows about rather than skip the day.
		common.SysError("alerting: failed to load open incidents for digest: " + err.Error())
	}
	sections := buildDigestSections(now)
	if err := postText(formatDailyDigest(now, snapshots, incidents, sections)); err != nil {
		common.SysError("alerting: Feishu digest delivery failed: " + err.Error())
		releaseDigestClaim(now)
	}
}

func persistHealthSnapshot(key string, raw []byte) error {
	return persistHealthSnapshotWithRetry(func(ctx context.Context) error {
		return common.RDB.HSet(ctx, healthKey, key, raw).Err()
	}, time.Sleep)
}

func persistHealthSnapshotWithRetry(write func(context.Context) error, sleep func(time.Duration)) error {
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		lastErr = write(ctx)
		cancel()
		if lastErr == nil {
			return nil
		}
		if attempt < 2 {
			sleep(time.Duration(attempt+1) * 100 * time.Millisecond)
		}
	}
	return lastErr
}

func updateRedisIncident(event Incident, now time.Time, deliveryToken string) (incidentState, bool, bool, error) {
	if !common.RedisEnabled || common.RDB == nil {
		return incidentState{}, false, false, errors.New("Redis is not initialized")
	}
	key := incidentKeyPrefix + event.Key
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	var next incidentState
	var send, changed bool
	localState, localExists := peekLocalIncident(event.Key)
	wasResolvedLocally := localWasResolved(event.Key)
	for attempt := 0; attempt < 4; attempt++ {
		err := common.RDB.Watch(ctx, func(tx *redis.Tx) error {
			current, exists, err := loadIncidentState(ctx, tx, key)
			if err != nil {
				return err
			}
			if wasResolvedLocally {
				// Discard stale Redis state left behind by a recovery delivered
				// during an outage. Any local state is a newer recurrence.
				current, exists = localState, localExists
			} else if localExists {
				current, exists = mergeIncidentState(current, exists, localState, true)
			}
			next, send, changed = nextIncidentState(current, exists, event, now, deliveryToken)
			raw, err := json.Marshal(next)
			if err != nil {
				return err
			}
			_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
				pipe.Set(ctx, key, raw, incidentTTL)
				return nil
			})
			return err
		}, key)
		if !errors.Is(err, redis.TxFailedErr) {
			if err == nil && wasResolvedLocally {
				clearLocalResolved(event.Key)
			}
			return next, send, changed, err
		}
	}
	return incidentState{}, false, false, errors.New("Redis incident update conflicted repeatedly")
}

func loadRedisIncidentByKey(key string) (incidentState, bool, error) {
	if !common.RedisEnabled || common.RDB == nil {
		return incidentState{}, false, errors.New("Redis is not initialized")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return loadIncidentState(ctx, common.RDB, incidentKeyPrefix+key)
}

func deleteRedisIncident(key string, expected incidentState, expectedExists bool) error {
	if !common.RedisEnabled || common.RDB == nil {
		return errors.New("Redis is not initialized")
	}
	if !expectedExists {
		return nil
	}
	redisKey := incidentKeyPrefix + key
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	for attempt := 0; attempt < 4; attempt++ {
		err := common.RDB.Watch(ctx, func(tx *redis.Tx) error {
			current, exists, err := loadIncidentState(ctx, tx, redisKey)
			if err != nil || !exists {
				return err
			}
			if current.Fingerprint != expected.Fingerprint || current.FirstSeen != expected.FirstSeen {
				return nil
			}
			_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
				pipe.Del(ctx, redisKey)
				return nil
			})
			return err
		}, redisKey)
		if !errors.Is(err, redis.TxFailedErr) {
			return err
		}
	}
	return errors.New("Redis incident recovery delete conflicted repeatedly")
}

func reserveRedisRecovery(key string, candidate incidentState, recoveryToken string, now time.Time) (incidentState, bool, error) {
	if !common.RedisEnabled || common.RDB == nil {
		return incidentState{}, false, errors.New("Redis is not initialized")
	}
	redisKey := incidentKeyPrefix + key
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var next incidentState
	var acquired bool
	for attempt := 0; attempt < 4; attempt++ {
		err := common.RDB.Watch(ctx, func(tx *redis.Tx) error {
			current, exists, err := loadIncidentState(ctx, tx, redisKey)
			if err != nil {
				return err
			}
			if exists && recoveryReservationActive(current, now) {
				next, acquired = current, false
				return nil
			}
			next, _ = mergeIncidentState(current, exists, candidate, true)
			next.RecoveryToken = recoveryToken
			next.RecoveryReservedAt = now.Unix()
			acquired = true
			raw, err := json.Marshal(next)
			if err != nil {
				return err
			}
			_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
				pipe.Set(ctx, redisKey, raw, incidentTTL)
				return nil
			})
			return err
		}, redisKey)
		if !errors.Is(err, redis.TxFailedErr) {
			return next, acquired, err
		}
	}
	return incidentState{}, false, errors.New("Redis recovery reservation conflicted repeatedly")
}

func releaseRedisRecovery(key string, observed incidentState) error {
	return updateRedisRecoveryReservation(key, observed, false)
}

func deleteRedisRecovery(key string, observed incidentState) error {
	return updateRedisRecoveryReservation(key, observed, true)
}

func updateRedisRecoveryReservation(key string, observed incidentState, deleteIncident bool) error {
	if !common.RedisEnabled || common.RDB == nil {
		return errors.New("Redis is not initialized")
	}
	redisKey := incidentKeyPrefix + key
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	for attempt := 0; attempt < 4; attempt++ {
		err := common.RDB.Watch(ctx, func(tx *redis.Tx) error {
			current, exists, err := loadIncidentState(ctx, tx, redisKey)
			if err != nil || !exists || current.RecoveryToken != observed.RecoveryToken || current.RecoveryToken == "" {
				return err
			}
			_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
				if deleteIncident {
					pipe.Del(ctx, redisKey)
					return nil
				}
				current.RecoveryToken = ""
				current.RecoveryReservedAt = 0
				raw, marshalErr := json.Marshal(current)
				if marshalErr != nil {
					return marshalErr
				}
				pipe.Set(ctx, redisKey, raw, incidentTTL)
				return nil
			})
			return err
		}, redisKey)
		if !errors.Is(err, redis.TxFailedErr) {
			return err
		}
	}
	return errors.New("Redis recovery reservation update conflicted repeatedly")
}

type redisGetter interface {
	Get(context.Context, string) *redis.StringCmd
}

func loadIncidentState(ctx context.Context, client redisGetter, key string) (incidentState, bool, error) {
	raw, err := client.Get(ctx, key).Bytes()
	if errors.Is(err, redis.Nil) {
		return incidentState{}, false, nil
	}
	if err != nil {
		return incidentState{}, false, err
	}
	var state incidentState
	if err := json.Unmarshal(raw, &state); err != nil {
		return incidentState{}, false, err
	}
	return state, true, nil
}

func nextIncidentState(current incidentState, exists bool, event Incident, now time.Time, deliveryToken string) (incidentState, bool, bool) {
	nowUnix := now.Unix()
	if !exists || current.Fingerprint != event.Fingerprint {
		return incidentState{
			Fingerprint:        event.Fingerprint,
			FirstSeen:          nowUnix,
			LastSeen:           nowUnix,
			Occurrences:        1,
			CauseChanged:       exists,
			DeliveryToken:      deliveryToken,
			DeliveryReservedAt: nowUnix,
			Title:              incidentTitle(event.Message),
		}, true, exists
	}

	current.LastSeen = nowUnix
	current.Title = incidentTitle(event.Message)
	current.Occurrences++
	if deliveryReservationActive(current, now) {
		return current, false, false
	}
	current.DeliveryToken = ""
	current.DeliveryReservedAt = 0
	if current.LastNotified == 0 {
		current.DeliveryToken = deliveryToken
		current.DeliveryReservedAt = nowUnix
		return current, true, current.CauseChanged
	}
	delays := reminderDelays(event.Priority)
	if current.ReminderStep >= len(delays) {
		// After the final 24-hour reminder, the daily operations digest is the
		// single continuing signal for an unchanged incident.
		return current, false, false
	}
	index := current.ReminderStep
	if now.Sub(time.Unix(current.LastNotified, 0)) < delays[index] {
		return current, false, false
	}
	current.DeliveryToken = deliveryToken
	current.DeliveryReservedAt = nowUnix
	return current, true, false
}

func markIncidentNotified(current incidentState, now time.Time) incidentState {
	if current.LastNotified == 0 {
		current.LastNotified = now.Unix()
		current.CauseChanged = false
	} else {
		current.LastNotified = now.Unix()
		current.ReminderStep++
	}
	current.DeliveryToken = ""
	current.DeliveryReservedAt = 0
	return current
}

func deliveryReservationActive(state incidentState, now time.Time) bool {
	if state.DeliveryToken == "" || state.DeliveryReservedAt == 0 {
		return false
	}
	return now.Sub(time.Unix(state.DeliveryReservedAt, 0)) < deliveryLeaseTTL
}

func recoveryReservationActive(state incidentState, now time.Time) bool {
	if state.RecoveryToken == "" || state.RecoveryReservedAt == 0 {
		return false
	}
	return now.Sub(time.Unix(state.RecoveryReservedAt, 0)) < deliveryLeaseTTL
}

func newDeliveryToken() string {
	var value [16]byte
	if _, err := rand.Read(value[:]); err == nil {
		return hex.EncodeToString(value[:])
	}
	return fmt.Sprintf("fallback-%d", nowFunc().UnixNano())
}

func markRedisIncidentNotified(key string, observed incidentState, now time.Time) error {
	if !common.RedisEnabled || common.RDB == nil {
		return errors.New("Redis is not initialized")
	}
	redisKey := incidentKeyPrefix + key
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	for attempt := 0; attempt < 4; attempt++ {
		err := common.RDB.Watch(ctx, func(tx *redis.Tx) error {
			current, exists, err := loadIncidentState(ctx, tx, redisKey)
			if err != nil || !exists || current.Fingerprint != observed.Fingerprint {
				return err
			}
			if current.DeliveryToken != observed.DeliveryToken || current.DeliveryToken == "" {
				return nil
			}
			current = markIncidentNotified(current, now)
			raw, err := json.Marshal(current)
			if err != nil {
				return err
			}
			_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
				pipe.Set(ctx, redisKey, raw, incidentTTL)
				return nil
			})
			return err
		}, redisKey)
		if !errors.Is(err, redis.TxFailedErr) {
			return err
		}
	}
	return errors.New("Redis incident notification update conflicted repeatedly")
}

func releaseRedisDelivery(key string, observed incidentState) error {
	if !common.RedisEnabled || common.RDB == nil {
		return errors.New("Redis is not initialized")
	}
	redisKey := incidentKeyPrefix + key
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	for attempt := 0; attempt < 4; attempt++ {
		err := common.RDB.Watch(ctx, func(tx *redis.Tx) error {
			current, exists, err := loadIncidentState(ctx, tx, redisKey)
			if err != nil || !exists || current.DeliveryToken != observed.DeliveryToken {
				return err
			}
			current.DeliveryToken = ""
			current.DeliveryReservedAt = 0
			raw, err := json.Marshal(current)
			if err != nil {
				return err
			}
			_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
				pipe.Set(ctx, redisKey, raw, incidentTTL)
				return nil
			})
			return err
		}, redisKey)
		if !errors.Is(err, redis.TxFailedErr) {
			return err
		}
	}
	return errors.New("Redis incident delivery release conflicted repeatedly")
}

func reminderDelays(priority Priority) []time.Duration {
	if priority == PriorityHigh {
		return []time.Duration{time.Hour, 5 * time.Hour, 18 * time.Hour}
	}
	return []time.Duration{6 * time.Hour, 18 * time.Hour}
}

func updateLocalIncident(event Incident, now time.Time, deliveryToken string) (incidentState, bool, bool) {
	localMu.Lock()
	defer localMu.Unlock()
	current, exists := localIncidents[event.Key]
	next, send, changed := nextIncidentState(current, exists, event, now, deliveryToken)
	localIncidents[event.Key] = next
	return next, send, changed
}

func markLocalIncidentNotified(key string, observed incidentState, now time.Time) {
	localMu.Lock()
	defer localMu.Unlock()
	current, exists := localIncidents[key]
	if !exists || current.Fingerprint != observed.Fingerprint {
		return
	}
	if current.DeliveryToken != observed.DeliveryToken || current.DeliveryToken == "" {
		return
	}
	localIncidents[key] = markIncidentNotified(current, now)
}

func releaseLocalDelivery(key string, observed incidentState) {
	localMu.Lock()
	defer localMu.Unlock()
	current, exists := localIncidents[key]
	if !exists || current.DeliveryToken != observed.DeliveryToken {
		return
	}
	current.DeliveryToken = ""
	current.DeliveryReservedAt = 0
	localIncidents[key] = current
}

func reserveLocalRecovery(key string, expected incidentState, recoveryToken string, now time.Time) (incidentState, bool) {
	localMu.Lock()
	defer localMu.Unlock()
	current, exists := localIncidents[key]
	if !exists || current.Fingerprint != expected.Fingerprint || current.FirstSeen != expected.FirstSeen {
		return incidentState{}, false
	}
	if recoveryReservationActive(current, now) {
		return current, false
	}
	current.RecoveryToken = recoveryToken
	current.RecoveryReservedAt = now.Unix()
	localIncidents[key] = current
	return current, true
}

func releaseLocalRecovery(key string, observed incidentState) {
	localMu.Lock()
	defer localMu.Unlock()
	current, exists := localIncidents[key]
	if !exists || current.RecoveryToken != observed.RecoveryToken {
		return
	}
	current.RecoveryToken = ""
	current.RecoveryReservedAt = 0
	localIncidents[key] = current
}

func cacheLocalIncident(key string, state incidentState) {
	localMu.Lock()
	localIncidents[key] = state
	localMu.Unlock()
}

func logRedisFallback(err error) {
	if err == nil {
		return
	}
	now := nowFunc()
	localMu.Lock()
	shouldLog := lastRedisError.IsZero() || now.Sub(lastRedisError) >= 5*time.Minute
	if shouldLog {
		lastRedisError = now
	}
	localMu.Unlock()
	if shouldLog {
		common.SysError("alerting: Redis state unavailable, using local fallback: " + err.Error())
	}
}

func peekLocalIncident(key string) (incidentState, bool) {
	localMu.Lock()
	defer localMu.Unlock()
	state, ok := localIncidents[key]
	return state, ok
}

func deleteLocalIncident(key string, expected incidentState) {
	localMu.Lock()
	defer localMu.Unlock()
	current, ok := localIncidents[key]
	if ok && current.Fingerprint == expected.Fingerprint && current.FirstSeen == expected.FirstSeen {
		delete(localIncidents, key)
	}
}

func markLocalResolved(key string) {
	localMu.Lock()
	localResolved[key] = true
	localMu.Unlock()
}

func localWasResolved(key string) bool {
	localMu.Lock()
	defer localMu.Unlock()
	return localResolved[key]
}

func clearLocalResolved(key string) {
	localMu.Lock()
	delete(localResolved, key)
	localMu.Unlock()
}

func mergeResolvedIncident(redisState incidentState, redisExists bool, localState incidentState, localExists bool) (incidentState, bool) {
	return mergeIncidentState(redisState, redisExists, localState, localExists)
}

func mergeIncidentState(primary incidentState, primaryExists bool, shadow incidentState, shadowExists bool) (incidentState, bool) {
	if !primaryExists {
		return shadow, shadowExists
	}
	if !shadowExists {
		return primary, true
	}
	if primary.Fingerprint != shadow.Fingerprint {
		if shadow.LastSeen >= primary.LastSeen {
			return shadow, true
		}
		return primary, true
	}
	if shadow.FirstSeen > 0 && (primary.FirstSeen == 0 || shadow.FirstSeen < primary.FirstSeen) {
		primary.FirstSeen = shadow.FirstSeen
	}
	if shadow.LastSeen > primary.LastSeen {
		primary.LastSeen = shadow.LastSeen
		if shadow.Title != "" {
			primary.Title = shadow.Title
		}
	}
	if shadow.LastNotified > primary.LastNotified {
		primary.LastNotified = shadow.LastNotified
	}
	if shadow.Occurrences > primary.Occurrences {
		primary.Occurrences = shadow.Occurrences
	}
	if shadow.ReminderStep > primary.ReminderStep {
		primary.ReminderStep = shadow.ReminderStep
	}
	primary.CauseChanged = primary.CauseChanged || shadow.CauseChanged
	if shadow.DeliveryReservedAt > primary.DeliveryReservedAt {
		primary.DeliveryToken = shadow.DeliveryToken
		primary.DeliveryReservedAt = shadow.DeliveryReservedAt
	}
	if primary.LastNotified >= primary.DeliveryReservedAt {
		primary.DeliveryToken = ""
		primary.DeliveryReservedAt = 0
	}
	if shadow.RecoveryReservedAt > primary.RecoveryReservedAt {
		primary.RecoveryToken = shadow.RecoveryToken
		primary.RecoveryReservedAt = shadow.RecoveryReservedAt
	}
	return primary, true
}

func loadHealthSnapshots() ([]HealthSnapshot, error) {
	byKey := map[string]HealthSnapshot{}
	if common.RedisEnabled && common.RDB != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		values, err := common.RDB.HGetAll(ctx, healthKey).Result()
		cancel()
		if err != nil {
			return nil, err
		}
		for key, raw := range values {
			var snapshot HealthSnapshot
			if err := json.Unmarshal([]byte(raw), &snapshot); err != nil {
				return nil, fmt.Errorf("decode health snapshot %q: %w", key, err)
			}
			byKey[key] = snapshot
		}
	}
	localMu.Lock()
	for key, snapshot := range localHealth {
		if existing, ok := byKey[key]; !ok || snapshot.UpdatedAt > existing.UpdatedAt {
			byKey[key] = snapshot
		}
	}
	localMu.Unlock()
	result := make([]HealthSnapshot, 0, len(byKey))
	for _, snapshot := range byKey {
		result = append(result, snapshot)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Label < result[j].Label })
	return result, nil
}

// loadOpenIncidents returns every incident that has not been resolved, keyed
// by incident key. Resolve deletes an incident, so whatever remains is open.
// When Redis answers, it is the whole answer: another process may have
// resolved an incident this process still caches, or recorded a recurrence
// whose lifecycle the cached one would wrongly extend. The local cache is
// used only when Redis is off or the scan failed.
func loadOpenIncidents() (map[string]incidentState, error) {
	open := map[string]incidentState{}
	var loadErr error
	redisAnswered := false
	if common.RedisEnabled && common.RDB != nil {
		redisAnswered = true
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var cursor uint64
		for {
			keys, next, err := common.RDB.Scan(ctx, cursor, incidentKeyPrefix+"*", 200).Result()
			if err != nil {
				loadErr, redisAnswered = err, false
				break
			}
			for _, redisKey := range keys {
				state, exists, err := loadIncidentState(ctx, common.RDB, redisKey)
				if err != nil {
					loadErr, redisAnswered = err, false
					continue
				}
				if exists {
					open[strings.TrimPrefix(redisKey, incidentKeyPrefix)] = state
				}
			}
			cursor = next
			if cursor == 0 {
				break
			}
		}
	}
	if redisAnswered {
		return open, nil
	}
	localMu.Lock()
	for key, state := range localIncidents {
		if existing, ok := open[key]; ok {
			state, _ = mergeIncidentState(existing, true, state, true)
		}
		open[key] = state
	}
	localMu.Unlock()
	return open, loadErr
}

func incidentTitle(message string) string {
	title, _, _ := strings.Cut(strings.TrimSpace(message), "\n")
	runes := []rune(strings.TrimSpace(title))
	if len(runes) > 120 {
		return string(runes[:120]) + "…"
	}
	return string(runes)
}

func claimDigest(now time.Time) bool {
	day := now.Format("2006-01-02")
	if common.RedisEnabled && common.RDB != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		claimed, err := common.RDB.SetNX(ctx, "lazu:alerts:digest:v1:"+day, "1", 25*time.Hour).Result()
		cancel()
		if err == nil {
			// Remember that the day is taken, by this process or another, so a
			// retry tick that finds Redis down does not send it again.
			localMu.Lock()
			localDigestDay = day
			localMu.Unlock()
			return claimed
		}
	}
	localMu.Lock()
	defer localMu.Unlock()
	if localDigestDay == day {
		return false
	}
	localDigestDay = day
	return true
}

func releaseDigestClaim(now time.Time) {
	day := now.Format("2006-01-02")
	if common.RedisEnabled && common.RDB != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = common.RDB.Del(ctx, "lazu:alerts:digest:v1:"+day).Err()
		cancel()
	}
	localMu.Lock()
	if localDigestDay == day {
		localDigestDay = ""
	}
	localMu.Unlock()
}
