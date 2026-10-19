package model

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/lazu-ai/lazu/internal/common"
)

const IdentitySnapshotContextKey = "gateway_identity_snapshot"
const identitySnapshotTTL = 60 * time.Second

type IdentitySnapshot struct {
	Token               *Token               `json:"token"`
	Actor               *UserBase            `json:"actor"`
	Payer               *UserBase            `json:"payer"`
	Principal           ProjectPrincipal     `json:"principal"`
	Budgets             []SpendBudget        `json:"budgets"`
	Generations         []IdentityGeneration `json:"generations"`
	CredentialExpiresAt int64                `json:"credential_expires_at"`
	CachedUntil         int64                `json:"cached_until"`
	ProjectUpgraded     bool                 `json:"project_upgraded"`
	PaymentRiskHold     bool                 `json:"payment_risk_hold"`
	MemberStatus        string               `json:"member_status"`
	DirectoryVersion    string               `json:"directory_version"`
	// Only the verified server/session credential constructor sets this. It
	// never survives serialization into the ordinary API-key cache.
	trustedCredential  bool
	playgroundIdentity bool
}

// Keep secrets out of serialized snapshots; the credential is restored from
// the current request only after its digest has selected the cached entry.
type identitySnapshotWire struct {
	Token               Token                `json:"token"`
	Actor               UserBase             `json:"actor"`
	Payer               UserBase             `json:"payer"`
	Principal           ProjectPrincipal     `json:"principal"`
	Budgets             []identityBudgetWire `json:"budgets"`
	Generations         []IdentityGeneration `json:"generations"`
	CredentialExpiresAt int64                `json:"credential_expires_at"`
	CachedUntil         int64                `json:"cached_until"`
	ProjectUpgraded     bool                 `json:"project_upgraded"`
	PaymentRiskHold     bool                 `json:"payment_risk_hold"`
	MemberStatus        string               `json:"member_status"`
	DirectoryVersion    string               `json:"directory_version"`
}

// SpendBudget's public API hides ProjectID. The private authorization cache
// must carry it so project caps never turn into platform-wide budget counters.
// A missing scope denotes a pre-upgrade cache entry and requires a SQL reload.
type identityBudgetWire struct {
	SpendBudget
	ProjectID *int `json:"project_id"`
}

func identityBudgetProjection(budgets []SpendBudget) []identityBudgetWire {
	if budgets == nil {
		return nil
	}
	result := make([]identityBudgetWire, len(budgets))
	for i, budget := range budgets {
		projectID := budget.ProjectID
		result[i] = identityBudgetWire{SpendBudget: budget, ProjectID: &projectID}
	}
	return result
}

func IdentitySnapshotsEnabled() bool {
	// The test database has no asynchronous writer and remains on SQL.
	if CoreDB() == nil || CoreDB().Dialector.Name() == "sqlite" || !common.RedisEnabled || common.RDB == nil {
		return false
	}
	return GatewayLedgerEnabled()
}

// IsPlayground is true only for the private, authenticated owner snapshot.
// Serialized cache entries and caller-created zero-ID tokens cannot set it.
func (snapshot *IdentitySnapshot) IsPlayground() bool {
	return snapshot != nil && snapshot.playgroundIdentity
}

var identitySnapshotL1 = newBoundedIdentityCache(identitySnapshotTTL, 20_000, 64*1024*1024)

var publishIdentitySnapshotScript = redis.NewScript(`
local now = redis.call('TIME')
local ms = now[1]*1000 + math.floor(now[2]/1000)
for i=2,#KEYS,2 do
  if redis.call('GET', KEYS[i]) ~= ARGV[(i+2)/2] then return 0 end
  if redis.call('ZCOUNT', KEYS[i+1], '('..ms, '+inf') > 0 then return 0 end
end
redis.call('SET', KEYS[1], ARGV[1], 'PX', ARGV[#ARGV])
return 1
`)

func (snapshot *IdentitySnapshot) MarshalJSON() ([]byte, error) {
	wire := identitySnapshotWire{Token: *snapshot.Token, Actor: *snapshot.Actor, Payer: *snapshot.Payer, Principal: snapshot.Principal, Budgets: identityBudgetProjection(snapshot.Budgets), Generations: snapshot.Generations, CredentialExpiresAt: snapshot.CredentialExpiresAt, CachedUntil: snapshot.CachedUntil, ProjectUpgraded: snapshot.ProjectUpgraded, PaymentRiskHold: snapshot.PaymentRiskHold, MemberStatus: snapshot.MemberStatus, DirectoryVersion: snapshot.DirectoryVersion}
	// Token's public JSON intentionally omits auth IDs/digests. The cache must
	// carry them explicitly without changing the external token API shape.
	wire.Token.Key = ""
	wire.Token.RemainQuota = 0
	wire.Token.UsedQuota = 0
	wire.Token.LastUsedAt = 0
	return json.Marshal(struct {
		identitySnapshotWire
		TokenID            int     `json:"token_id"`
		UserID             int     `json:"user_id"`
		ProjectID          int     `json:"project_id"`
		RemovalOperationID string  `json:"removal_operation_id"`
		KeyDigest          string  `json:"key_digest"`
		InternalScope      *string `json:"internal_scope"`
	}{wire, snapshot.Token.Id, snapshot.Token.UserId, snapshot.Token.ProjectID, snapshot.Token.RemovalOperationID, snapshot.Token.KeyDigest, snapshot.Token.InternalScope})
}

func (snapshot *IdentitySnapshot) UnmarshalJSON(data []byte) error {
	var value struct {
		identitySnapshotWire
		TokenID            int     `json:"token_id"`
		UserID             int     `json:"user_id"`
		ProjectID          int     `json:"project_id"`
		RemovalOperationID string  `json:"removal_operation_id"`
		KeyDigest          string  `json:"key_digest"`
		InternalScope      *string `json:"internal_scope"`
	}
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	w := value.identitySnapshotWire
	var budgets []SpendBudget
	if w.Budgets != nil {
		budgets = make([]SpendBudget, len(w.Budgets))
		for i, budget := range w.Budgets {
			if budget.ProjectID == nil {
				return ErrIdentityStale
			}
			budgets[i] = budget.SpendBudget
			budgets[i].ProjectID = *budget.ProjectID
		}
	}
	// Existing Redis entries can outlive an application upgrade. Directory
	// remains the cold-loader coherence marker, but must not globally fence
	// warm credentials from the older wire format either. Preserve policy and
	// every discovered object generation, along with the original expiry.
	generations := w.Generations[:0]
	for _, generation := range w.Generations {
		if generation.Key != "gen:directory" {
			generations = append(generations, generation)
		}
	}
	w.Token.Id = value.TokenID
	w.Token.UserId = value.UserID
	w.Token.ProjectID = value.ProjectID
	w.Token.RemovalOperationID = value.RemovalOperationID
	w.Token.KeyDigest = value.KeyDigest
	w.Token.InternalScope = value.InternalScope
	*snapshot = IdentitySnapshot{Token: &w.Token, Actor: &w.Actor, Payer: &w.Payer, Principal: w.Principal, Budgets: budgets, Generations: generations, CredentialExpiresAt: w.CredentialExpiresAt, CachedUntil: w.CachedUntil, ProjectUpgraded: w.ProjectUpgraded, PaymentRiskHold: w.PaymentRiskHold, MemberStatus: w.MemberStatus, DirectoryVersion: w.DirectoryVersion}
	return nil
}

func (snapshot *IdentitySnapshot) Validate(now time.Time) error {
	if snapshot == nil || snapshot.Token == nil || snapshot.Actor == nil || snapshot.Payer == nil || snapshot.Token.Id <= 0 && !snapshot.playgroundIdentity {
		return ErrInvalidAPIKey
	}
	if snapshot.CredentialExpiresAt > 0 && snapshot.CredentialExpiresAt <= now.Unix() {
		return ErrInvalidAPIKey
	}
	if snapshot.Token.IsInternal() && !snapshot.trustedCredential {
		return ErrInvalidAPIKey
	}
	if snapshot.Principal.ProjectID <= 0 {
		return ErrTokenAccessInactive
	}
	if snapshot.Token.Status == common.TokenStatusExpired || snapshot.Token.ExpiredTime != -1 && snapshot.Token.ExpiredTime <= now.Unix() {
		return ErrTokenExpired
	}
	if snapshot.Token.Status != common.TokenStatusEnabled && snapshot.Token.Status != common.TokenStatusExhausted {
		return ErrTokenDisabled
	}
	// Wallet and quota counters are authoritative in Redis admission. Preserve
	// explicit disabled/exhausted status but never use cached numeric balances.
	if snapshot.Token.Status == common.TokenStatusExhausted {
		return ErrTokenBudgetExhausted
	}
	if snapshot.PaymentRiskHold || snapshot.MemberStatus != ProjectMemberActive || snapshot.Token.RemovalOperationID != "" || snapshot.Actor.Status != common.UserStatusEnabled || snapshot.Payer.Status != common.UserStatusEnabled || !snapshot.ProjectUpgraded && snapshot.Actor.Id != snapshot.Payer.Id {
		return ErrProjectAccessSuspended
	}
	return nil
}

// The console playground is an authenticated owner session with no API key.
// It uses its owner's existing wallet and project budgets, never a synthetic
// key row or a serialized authorization flag.
func ResolvePlaygroundIdentitySnapshot(ctx context.Context, userID int) (*IdentitySnapshot, error) {
	before, err := readIdentityGenerations(ctx, []string{"directory", "policy", "user:" + strconv.Itoa(userID)}, true)
	if err != nil {
		return nil, err
	}
	var user User
	if err := CoreDB().WithContext(ctx).First(&user, userID).Error; err != nil {
		return nil, err
	}
	var project Project
	if err := CoreDB().WithContext(ctx).Where("owner_user_id = ?", userID).First(&project).Error; err != nil {
		return nil, err
	}
	budgets, err := applicableProjectSpendBudgetsWithDB(CoreDB().WithContext(ctx), SpendBudgetChargeInput{ProjectID: project.ID, ActorUserID: userID}, false)
	if err != nil {
		return nil, err
	}
	generations, err := readIdentityGenerations(ctx, []string{"directory", "policy", "user:" + strconv.Itoa(userID), "project:" + strconv.Itoa(project.ID)}, true)
	if err != nil {
		return nil, err
	}
	for i := range before {
		if before[i].Value != generations[i].Value {
			return nil, ErrIdentityStale
		}
	}
	actor := user.ToBaseUser()
	actor.Quota = 0
	snapshot := &IdentitySnapshot{Token: &Token{UserId: userID, ProjectID: project.ID, Group: ChannelLaneDirect, Status: common.TokenStatusEnabled, ExpiredTime: -1, UnlimitedQuota: true}, Actor: actor, Payer: actor, Principal: ProjectPrincipal{ProjectID: project.ID, ActorUserID: userID, PayerUserID: userID}, Budgets: budgets, Generations: generations[1:], ProjectUpgraded: project.UpgradedAt != nil, PaymentRiskHold: project.PaymentRiskHold, MemberStatus: ProjectMemberActive, trustedCredential: true, playgroundIdentity: true}
	return snapshot, snapshot.Validate(time.Now())
}

func ResolveIdentitySnapshot(ctx context.Context, key string) (*IdentitySnapshot, error) {
	if common.RDB == nil {
		return nil, errors.New("identity Redis unavailable")
	}
	deadline := time.Now().Add(200 * time.Millisecond)
	for {
		snapshot, err := resolveIdentitySnapshotOnce(ctx, key)
		if !errors.Is(err, ErrIdentityUpdating) && !errors.Is(err, ErrIdentityStale) {
			return snapshot, err
		}
		if time.Now().After(deadline) {
			return nil, err
		}
		timer := time.NewTimer(10 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

func resolveIdentitySnapshotOnce(ctx context.Context, key string) (*IdentitySnapshot, error) {
	digest := TokenKeyDigest(key)
	cacheKey := "identity:" + digest
	now := time.Now()
	if raw, ok := identitySnapshotL1.get(digest, now); ok {
		snapshot, err := decodeIdentityProjection(ctx, key, raw)
		if !errors.Is(err, ErrIdentityStale) {
			return snapshot, err
		}
		identitySnapshotL1.delete(digest)
	}
	raw, err := common.RDB.Get(ctx, cacheKey).Result()
	if err == nil {
		snapshot, decodeErr := decodeIdentityProjection(ctx, key, raw)
		if !errors.Is(decodeErr, ErrIdentityStale) {
			if snapshot != nil {
				identitySnapshotL1.set(digest, raw, now)
			}
			return snapshot, decodeErr
		}
	} else if !errors.Is(err, redis.Nil) {
		return nil, err
	}
	raw, err = identitySnapshotRefreshes.do(ctx, digest, func(readCtx context.Context) (string, error) {
		return refreshIdentityProjection(readCtx, key, digest)
	})
	if err != nil {
		return nil, err
	}
	return decodeIdentityProjection(ctx, key, raw)
}

func decodeIdentityProjection(ctx context.Context, key, raw string) (*IdentitySnapshot, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var snapshot IdentitySnapshot
	if err := json.Unmarshal([]byte(raw), &snapshot); err != nil {
		return nil, err
	}
	if snapshot.CachedUntil <= time.Now().UnixMilli() {
		return nil, ErrIdentityStale
	}
	if err := CheckIdentityGenerations(ctx, snapshot.Generations); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	now := time.Now()
	if snapshot.CachedUntil <= now.UnixMilli() {
		return nil, ErrIdentityStale
	}
	snapshot.Token.Key = common.NormalizeAPIKey(key)
	return &snapshot, snapshot.Validate(now)
}

func refreshIdentityProjection(ctx context.Context, key, digest string) (string, error) {
	cacheKey := "identity:" + digest
	// Another completed refresh may have filled a cache while this caller
	// waited to become the leader. Recheck before hitting SQL.
	if raw, ok := identitySnapshotL1.get(digest, time.Now()); ok {
		_, err := decodeIdentityProjection(ctx, key, raw)
		if !errors.Is(err, ErrIdentityStale) {
			return raw, nil
		}
		identitySnapshotL1.delete(digest)
	}
	if raw, err := common.RDB.Get(ctx, cacheKey).Result(); err == nil {
		if _, err := decodeIdentityProjection(ctx, key, raw); !errors.Is(err, ErrIdentityStale) {
			return raw, nil
		}
	} else if !errors.Is(err, redis.Nil) {
		return "", err
	}
	before, err := readIdentityGenerations(ctx, []string{"directory", "policy", "key:" + digest}, true)
	if err != nil {
		return "", err
	}
	// Absolute expiry is anchored before the SQL read, never at completion
	// or when a waiter receives the shared result.
	now := time.Now()
	snapshot, err := loadIdentitySnapshot(ctx, key, now)
	if err != nil {
		return "", err
	}
	scopes := []string{"directory", "policy", "key:" + digest, "token:" + strconv.Itoa(snapshot.Token.Id), "user:" + strconv.Itoa(snapshot.Actor.Id)} // secret-scan: allow -- Dynamic credential digest and numeric identity scope IDs, no embedded credential
	if snapshot.Payer.Id != snapshot.Actor.Id {
		scopes = append(scopes, "user:"+strconv.Itoa(snapshot.Payer.Id))
	}
	scopes = append(scopes, "project:"+strconv.Itoa(snapshot.Principal.ProjectID))
	generations, err := readIdentityGenerations(ctx, scopes, true)
	if err != nil {
		return "", err
	}
	for i := range before {
		if before[i].Value != generations[i].Value {
			return "", ErrIdentityStale
		}
	}
	// Directory protects discovery while IDs are still unknown to a cold
	// loader. Retaining it in a warm projection would invalidate every cached
	// credential whenever an unrelated account, key or credential is created.
	// Admission instead checks the discovered objects and the bulk policy fence.
	snapshot.Generations = generations[1:]
	snapshot.CachedUntil = now.Add(identitySnapshotTTL).UnixMilli()
	snapshot.DirectoryVersion = generations[0].Value
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		return "", err
	}
	args := make([]interface{}, 0, len(generations)+2)
	args = append(args, string(encoded))
	for _, generation := range generations {
		args = append(args, generation.Value)
	}
	remaining := time.Until(time.UnixMilli(snapshot.CachedUntil)).Milliseconds()
	if remaining <= 0 {
		return "", ErrIdentityStale
	}
	args = append(args, remaining)
	keys := append([]string{cacheKey}, identityGenerationKeys(generations)...)
	published, err := publishIdentitySnapshotScript.Run(ctx, common.RDB, keys, args...).Int()
	if err != nil {
		return "", err
	}
	if published != 1 {
		return "", ErrIdentityStale
	}
	identitySnapshotL1.set(digest, string(encoded), now)
	return string(encoded), nil
}

type identityJoinedRow struct {
	Token               `gorm:"embedded"`
	ProjectOwnerID      int
	ProjectUpgradedAt   *int64
	PaymentRiskHold     bool
	MemberStatus        string
	MemberAllowedModels StringSlice
	MemberEffortCap     string
	ActorID             int
	ActorEmail          string
	ActorQuota          int64
	ActorStatus         int
	ActorUsername       string
	ActorSetting        string
	ActorNeedsPassword  bool
	ActorHasToppedUp    bool
	ActorPricePlanID    int
	ActorRPMLimit       int
	ActorTPMLimit       int
	PayerID             int
	PayerEmail          string
	PayerQuota          int64
	PayerStatus         int
	PayerUsername       string
	PayerSetting        string
	PayerNeedsPassword  bool
	PayerHasToppedUp    bool
	PayerPricePlanID    int
	PayerRPMLimit       int
	PayerTPMLimit       int
	CredentialExpiresAt int64
}

func loadIdentitySnapshot(ctx context.Context, key string, now time.Time) (*IdentitySnapshot, error) {
	return loadIdentitySnapshotForCredential(ctx, key, 0, now)
}

func loadIdentitySnapshotForCredential(ctx context.Context, key string, tokenID int, now time.Time) (*IdentitySnapshot, error) {
	candidates := common.APIKeyLookupCandidates(key)
	if tokenID <= 0 && len(candidates) == 0 {
		return nil, ErrInvalidAPIKey
	}
	digests := tokenKeyLookupDigests(candidates)
	selects := []string{"tokens.*", "p.owner_user_id AS project_owner_id", "p.upgraded_at AS project_upgraded_at", "p.payment_risk_hold AS payment_risk_hold", "m.status AS member_status", "m.allowed_models AS member_allowed_models", "m.reasoning_effort_cap AS member_effort_cap"}
	if tokenID > 0 {
		selects = append(selects, "0 AS credential_expires_at")
	} else {
		selects = append(selects, "CASE WHEN tokens.key_digest IN ? OR tokens.key IN ? THEN 0 ELSE credential.expires_at END AS credential_expires_at")
	}
	for _, prefix := range []string{"actor", "payer"} {
		for _, field := range []string{"id", "email", "quota", "status", "username", "setting", "has_topped_up", "price_plan_id", "rpm_limit", "tpm_limit"} {
			selects = append(selects, prefix+"."+field+" AS "+prefix+"_"+field)
		}
		selects = append(selects, "CASE WHEN "+prefix+".password <> '' THEN true ELSE false END AS "+prefix+"_needs_password")
	}
	var row identityJoinedRow
	query := CoreDB().WithContext(ctx).Model(&Token{})
	if tokenID > 0 {
		query = query.Select(strings.Join(selects, ", ")).Where("tokens.id = ?", tokenID)
	} else {
		query = query.Select(strings.Join(selects, ", "), digests, candidates).
			Joins("LEFT JOIN token_credentials credential ON credential.token_id = tokens.id AND credential.digest IN ? AND credential.expires_at > ?", digests, now.Unix()).
			Where("tokens.key_digest IN ? OR tokens.key IN ? OR credential.id IS NOT NULL", digests, candidates)
	}
	query = query.
		Joins("LEFT JOIN projects p ON p.id = tokens.project_id").
		Joins("LEFT JOIN project_members m ON m.project_id = tokens.project_id AND m.user_id = tokens.user_id").
		Joins("LEFT JOIN users actor ON actor.id = tokens.user_id AND actor.deleted_at IS NULL").
		Joins("LEFT JOIN users payer ON payer.id = p.owner_user_id AND payer.deleted_at IS NULL")
	if err := query.Limit(1).Scan(&row).Error; err != nil {
		return nil, err
	}
	if row.Token.Id <= 0 {
		return nil, ErrInvalidAPIKey
	}
	row.Token.RemainQuota = 0
	row.Token.UsedQuota = 0
	row.Token.LastUsedAt = 0
	actor := &UserBase{Id: row.ActorID, Email: row.ActorEmail, Status: row.ActorStatus, Username: row.ActorUsername, Setting: row.ActorSetting, NeedsPassword: row.ActorNeedsPassword, HasToppedUp: row.ActorHasToppedUp, PricePlanID: row.ActorPricePlanID, RPMLimit: row.ActorRPMLimit, TPMLimit: row.ActorTPMLimit}
	payer := &UserBase{Id: row.PayerID, Email: row.PayerEmail, Status: row.PayerStatus, Username: row.PayerUsername, Setting: row.PayerSetting, NeedsPassword: row.PayerNeedsPassword, HasToppedUp: row.PayerHasToppedUp, PricePlanID: row.PayerPricePlanID, RPMLimit: row.PayerRPMLimit, TPMLimit: row.PayerTPMLimit}
	snapshot := &IdentitySnapshot{Token: &row.Token, Actor: actor, Payer: payer, CredentialExpiresAt: row.CredentialExpiresAt, ProjectUpgraded: row.ProjectUpgradedAt != nil, PaymentRiskHold: row.PaymentRiskHold, MemberStatus: row.MemberStatus}
	snapshot.Principal = ProjectPrincipal{ProjectID: row.Token.ProjectID, PayerUserID: row.ProjectOwnerID, ActorUserID: row.Token.UserId, TokenID: row.Token.Id, MemberAllowedModels: row.MemberAllowedModels, MemberEffortCap: row.MemberEffortCap}
	budgets, err := applicableProjectSpendBudgetsWithDB(CoreDB().WithContext(ctx), SpendBudgetChargeInput{ProjectID: row.Token.ProjectID, ActorUserID: row.Token.UserId, TokenID: row.Token.Id}, false)
	if err != nil {
		return nil, err
	}
	snapshot.Budgets = budgets
	return snapshot, nil
}

// ResolveVerifiedIdentitySnapshot is for credentials that middleware has
// already verified through a trusted server signature, session ownership or
// authoritative ephemeral credential lookup. It never fills the API-key
// cache, and reloads current identity and budget definitions behind fences.
func ResolveVerifiedIdentitySnapshot(ctx context.Context, tokenID int, ephemeralID uint) (*IdentitySnapshot, error) {
	beforeScopes := []string{"directory", "policy", "token:" + strconv.Itoa(tokenID)}
	if ephemeralID > 0 {
		beforeScopes = append(beforeScopes, "ephemeral:"+strconv.FormatUint(uint64(ephemeralID), 10))
	}
	before, err := readIdentityGenerations(ctx, beforeScopes, true)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	snapshot, err := loadIdentitySnapshotForCredential(ctx, "", tokenID, now)
	if err != nil {
		return nil, err
	}
	if ephemeralID > 0 {
		var credential EphemeralCredential
		if err := CoreDB().WithContext(ctx).First(&credential, ephemeralID).Error; err != nil {
			return nil, IdentityAuthorityQueryError(err)
		}
		if credential.TokenID != tokenID || credential.UserID != snapshot.Actor.Id || credential.RevokedAt > 0 || credential.ExpiresAt <= now.Unix() {
			return nil, ErrInvalidAPIKey
		}
		snapshot.CredentialExpiresAt = credential.ExpiresAt
	}
	scopes := append(append([]string{}, beforeScopes...), "user:"+strconv.Itoa(snapshot.Actor.Id))
	if snapshot.Payer.Id != snapshot.Actor.Id {
		scopes = append(scopes, "user:"+strconv.Itoa(snapshot.Payer.Id))
	}
	scopes = append(scopes, "project:"+strconv.Itoa(snapshot.Principal.ProjectID))
	generations, err := readIdentityGenerations(ctx, scopes, true)
	if err != nil {
		return nil, err
	}
	for i := range before {
		if before[i].Value != generations[i].Value {
			return nil, ErrIdentityStale
		}
	}
	snapshot.Generations = generations[1:]
	snapshot.trustedCredential = true
	snapshot.CachedUntil = now.Add(identitySnapshotTTL).UnixMilli()
	return snapshot, snapshot.Validate(now)
}
