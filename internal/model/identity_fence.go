package model

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/lazu-ai/lazu/internal/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ErrIdentityUpdating = errors.New("identity is being updated")
var ErrIdentityStale = errors.New("identity snapshot is stale")

// IdentityGeneration is also consumed by the monetary admission script. A
// missing generation always invalidates a snapshot; it never means version 0.
type IdentityGeneration struct {
	Key        string `json:"key"`
	PendingKey string `json:"pending_key"`
	Value      string `json:"value"`
}

const identityPendingTTL = 30 * time.Second
const identityTransactionTimeout = 10 * time.Second

var identityVersionsScript = redis.NewScript(`
local result = {}
local now = redis.call('TIME')
local ms = now[1]*1000 + math.floor(now[2]/1000)
for i=1,#KEYS,2 do
  redis.call('ZREMRANGEBYSCORE', KEYS[i+1], '-inf', ms)
  if redis.call('ZCARD', KEYS[i+1]) > 0 then return {'pending'} end
  if ARGV[1] == 'initialize' then redis.call('SETNX', KEYS[i], ARGV[2]) end
  local generation = redis.call('GET', KEYS[i])
  if not generation then return {'missing'} end
  table.insert(result, generation)
end
return result
`)

var identityBeginScript = redis.NewScript(`
local now = redis.call('TIME')
local ms = now[1]*1000 + math.floor(now[2]/1000)
for i=1,#KEYS,2 do
  redis.call('SETNX', KEYS[i], ARGV[2])
  redis.call('INCR', KEYS[i])
  if KEYS[i] ~= 'gen:directory' then
    redis.call('ZADD', KEYS[i+1], ms+tonumber(ARGV[3]), ARGV[1])
  end
end
return 1
`)

var identityEndScript = redis.NewScript(`
for i=1,#KEYS,2 do
  redis.call('ZREM', KEYS[i+1], ARGV[1])
  redis.call('SETNX', KEYS[i], ARGV[2])
  redis.call('INCR', KEYS[i])
end
return 1
`)

func identityGeneration(scope string) IdentityGeneration {
	return IdentityGeneration{Key: "gen:" + scope, PendingKey: "pending:" + scope}
}

func identityGenerationKeys(generations []IdentityGeneration) []string {
	keys := make([]string, 0, len(generations)*2)
	for _, generation := range generations {
		keys = append(keys, generation.Key, generation.PendingKey)
	}
	return keys
}

func readIdentityGenerations(ctx context.Context, scopes []string, initialize bool) ([]IdentityGeneration, error) {
	if common.RDB == nil {
		return nil, errors.New("identity Redis unavailable")
	}
	generations := make([]IdentityGeneration, 0, len(scopes))
	for _, scope := range scopes {
		generations = append(generations, identityGeneration(scope))
	}
	values, err := readIdentityGenerationValues(ctx, identityGenerationKeys(generations), initialize)
	if err != nil {
		return nil, err
	}
	for i := range generations {
		generations[i].Value = values[i]
	}
	return generations, nil
}

func readIdentityGenerationValues(ctx context.Context, keys []string, initialize bool) ([]string, error) {
	if common.RDB == nil {
		return nil, errors.New("identity Redis unavailable")
	}
	mode, seed := "check", ""
	if initialize {
		mode = "initialize"
		// Random initial generations prevent an old L1 entry being admitted
		// after Redis data loss and recreation of all version keys.
		seed = strconv.FormatInt(time.Now().UnixNano()/1000, 10)
	}
	values, err := identityVersionsScript.Run(ctx, common.RDB, keys, mode, seed).StringSlice()
	if err != nil {
		return nil, err
	}
	if len(values) == 1 && values[0] == "pending" {
		return nil, ErrIdentityUpdating
	}
	if len(values) == 1 && values[0] == "missing" {
		return nil, ErrIdentityStale
	}
	if len(values)*2 != len(keys) {
		return nil, errors.New("invalid identity generation response")
	}
	return values, nil
}

func CheckIdentityGenerations(ctx context.Context, expected []IdentityGeneration) error {
	if len(expected) == 0 {
		return ErrIdentityStale
	}
	keys := make([]string, 0, len(expected)*2)
	for _, generation := range expected {
		scope := strings.TrimPrefix(generation.Key, "gen:")
		key := generation.Key
		if key == scope {
			key = "gen:" + scope
		}
		// Derive the pending key from the canonical scope, as on a cold
		// read; cached wire data cannot select a different pending fence.
		keys = append(keys, key, "pending:"+scope)
	}
	actual, err := readIdentityGenerationValues(ctx, keys, false)
	if err != nil {
		return err
	}
	for i := range actual {
		if expected[i].Value == "" || actual[i] != expected[i].Value {
			return ErrIdentityStale
		}
	}
	return nil
}

type identityMutation struct {
	operation   string
	generations []IdentityGeneration
}

// IdentityCounterMutation names a persisted counter whose definition or
// administrative baseline changed. The outbox hook writes a durable refresh
// task in the same SQL transaction; it must never discard active reservations.
type IdentityCounterMutation struct {
	Table     string
	ObjectID  int
	ProjectID int
	TokenID   int
	UserID    int
}

var identityCounterOutbox struct {
	sync.RWMutex
	write func(*gorm.DB, IdentityCounterMutation) error
}

func SetIdentityCounterRefreshOutbox(write func(*gorm.DB, IdentityCounterMutation) error) {
	identityCounterOutbox.Lock()
	defer identityCounterOutbox.Unlock()
	identityCounterOutbox.write = write
}

// Monetary writers that already maintain their own idempotent credit/settle
// protocol opt out of administrative refresh tasks on their SQL transaction.
func SkipIdentityCounterRefresh(tx *gorm.DB) *gorm.DB {
	// A context marker survives NewDB's fresh statement without turning the
	// reusable transaction base into a mutable clause/model chain (Set does).
	ctx := context.WithValue(tx.Statement.Context, identitySkipCounterRefreshContextKey{}, true)
	return tx.WithContext(ctx).Session(&gorm.Session{NewDB: true})
}

type identitySkipCounterRefreshContextKey struct{}

func identityCounterRefreshSkipped(tx *gorm.DB) bool {
	if skip, ok := tx.Statement.Context.Value(identitySkipCounterRefreshContextKey{}).(bool); ok && skip {
		return true
	}
	skip, ok := tx.Get("identity:skip_counter_refresh")
	return ok && skip == true
}

func persistIdentityCounterRefresh(tx *gorm.DB) error {
	if identityCounterRefreshSkipped(tx) {
		return nil
	}
	statement := tx.Statement
	needsRefresh := statement.Table == "spend_budgets"
	if statement.Table == "users" || statement.Table == "tokens" {
		field := "quota"
		if statement.Table == "tokens" {
			field = "remain_quota"
		}
		if values, ok := statement.Dest.(map[string]interface{}); ok {
			_, needsRefresh = values[field]
		} else {
			for _, selected := range statement.Selects {
				if strings.EqualFold(selected, field) || selected == "*" {
					needsRefresh = true
				}
			}
			// Full saves can change the administrator's baseline as well.
			if len(statement.Selects) == 0 && identityStatementID(statement, "id") > 0 {
				needsRefresh = true
			}
		}
	}
	if !needsRefresh {
		return nil
	}
	identityCounterOutbox.RLock()
	write := identityCounterOutbox.write
	identityCounterOutbox.RUnlock()
	if write == nil {
		return errors.New("identity counter refresh outbox is not configured")
	}
	mutation := IdentityCounterMutation{Table: statement.Table, ObjectID: identityStatementID(statement, "id"), ProjectID: identityStatementID(statement, "project_id"), TokenID: identityStatementID(statement, "token_id"), UserID: identityStatementID(statement, "user_id")}
	if statement.Table == "tokens" {
		mutation.TokenID = mutation.ObjectID
	}
	if statement.Table == "users" {
		mutation.UserID = mutation.ObjectID
	}
	return write(tx.Session(&gorm.Session{NewDB: true}), mutation)
}

func beginIdentityMutation(ctx context.Context, scopes []string) (*identityMutation, error) {
	scopes = append(scopes, "directory")
	sort.Strings(scopes)
	unique := scopes[:0]
	for _, scope := range scopes {
		if len(unique) == 0 || unique[len(unique)-1] != scope {
			unique = append(unique, scope)
		}
	}
	mutation := &identityMutation{operation: common.GeneratePublicID("imut")}
	for _, scope := range unique {
		mutation.generations = append(mutation.generations, identityGeneration(scope))
	}
	seed := strconv.FormatInt(time.Now().UnixNano()/1000, 10)
	_, err := identityBeginScript.Run(ctx, common.RDB, identityGenerationKeys(mutation.generations), mutation.operation, seed, identityPendingTTL.Milliseconds()).Result()
	return mutation, err
}

func (mutation *identityMutation) finish() error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err := identityEndScript.Run(ctx, common.RDB, identityGenerationKeys(mutation.generations), mutation.operation, strconv.FormatInt(time.Now().UnixNano()/1000, 10)).Result()
	return err
}

// The pool wrapper keeps pending operations alive until the outer SQL
// transaction finishes, including writes from workers and nested model calls.
// Statement callbacks alone cannot safely clear a fence inside an outer tx.
type identityConnPool struct{ gorm.ConnPool }

func (pool *identityConnPool) GetDBConn() (*sql.DB, error) {
	if db, ok := pool.ConnPool.(*sql.DB); ok {
		return db, nil
	}
	if getter, ok := pool.ConnPool.(gorm.GetDBConnector); ok {
		return getter.GetDBConn()
	}
	return nil, errors.New("identity pool has no SQL CoreDB()")
}

func (pool *identityConnPool) BeginTx(ctx context.Context, options *sql.TxOptions) (gorm.ConnPool, error) {
	transactionCtx, cancel := context.WithTimeout(ctx, identityTransactionTimeout)
	var conn gorm.ConnPool
	var err error
	if beginner, ok := pool.ConnPool.(gorm.TxBeginner); ok {
		conn, err = beginner.BeginTx(transactionCtx, options)
	} else if beginner, ok := pool.ConnPool.(gorm.ConnPoolBeginner); ok {
		conn, err = beginner.BeginTx(transactionCtx, options)
	} else {
		cancel()
		return nil, gorm.ErrInvalidTransaction
	}
	if err != nil {
		cancel()
		return nil, err
	}
	sqlDB, _ := pool.GetDBConn()
	return &identityTransaction{ConnPool: conn, cancel: cancel, sqlDB: sqlDB}, nil
}

type identityTransaction struct {
	gorm.ConnPool
	cancel         context.CancelFunc
	sqlDB          *sql.DB
	mu             sync.Mutex
	mutations      []*identityMutation
	completion     []func(bool)
	externalDebits map[int]int64
}

func (tx *identityTransaction) GetDBConn() (*sql.DB, error) {
	if tx.sqlDB == nil {
		return nil, gorm.ErrInvalidDB
	}
	return tx.sqlDB, nil
}

func (tx *identityTransaction) Commit() error {
	committer, ok := tx.ConnPool.(gorm.TxCommitter)
	if !ok {
		return gorm.ErrInvalidTransaction
	}
	err := committer.Commit()
	tx.finish(err == nil)
	return err
}
func (tx *identityTransaction) Rollback() error {
	committer, ok := tx.ConnPool.(gorm.TxCommitter)
	if !ok {
		return gorm.ErrInvalidTransaction
	}
	err := committer.Rollback()
	tx.finish(false)
	return err
}
func (tx *identityTransaction) finish(committed bool) {
	tx.cancel()
	tx.mu.Lock()
	mutations := tx.mutations
	tx.mutations = nil
	completion := tx.completion
	tx.completion = nil
	tx.mu.Unlock()
	for _, mutation := range mutations {
		if err := mutation.finish(); err != nil {
			common.SysError("finish identity mutation: " + err.Error())
		}
	}
	for _, callback := range completion {
		callback(committed)
	}
}

// InstallIdentityMutationFence must be called once when opening the core CoreDB(),
// before it is shared with request handlers. It covers every GORM write to
// identity tables; unrestricted bulk writes fall back to the policy scope.
func InstallIdentityMutationFence(db *gorm.DB) error {
	if db == nil {
		return errors.New("identity database unavailable")
	}
	if _, ok := db.ConnPool.(*identityConnPool); ok {
		return nil
	}
	pool := &identityConnPool{ConnPool: db.ConnPool}
	db.ConnPool = pool
	db.Statement.ConnPool = pool
	register := func(tx *gorm.DB, scopes []string) {
		ctx, cancel := context.WithTimeout(tx.Statement.Context, 2*time.Second)
		defer cancel()
		mutation, err := beginIdentityMutation(ctx, scopes)
		if err != nil {
			tx.AddError(fmt.Errorf("start identity mutation: %w", err))
			return
		}
		if transaction, ok := tx.Statement.ConnPool.(*identityTransaction); ok {
			transaction.mu.Lock()
			transaction.mutations = append(transaction.mutations, mutation)
			transaction.mu.Unlock()
		} else {
			mutations, _ := tx.InstanceGet("identity:mutations")
			pending, _ := mutations.([]*identityMutation)
			tx.InstanceSet("identity:mutations", append(pending, mutation))
		}
	}
	before := func(tx *gorm.DB, allowObservation, create bool) {
		if tx.Error != nil || !IdentitySnapshotsEnabled() {
			return
		}
		if allowObservation && identityObservationalWrite(tx.Statement) {
			return
		}
		scopes := identityMutationScopes(tx)
		if create && identityNewObjectCreate(tx.Statement) {
			// A generated ID cannot refer to an existing projection. Fence
			// cold discovery now, then fence the assigned object before its
			// transaction can commit; do not suspend the global policy scope.
			scopes = []string{"directory"}
			if token, ok := tx.Statement.Model.(*Token); ok && token.CacheDigest() != "" {
				scopes = append(scopes, "key:"+token.CacheDigest())
			}
			tx.InstanceSet("identity:new_object_create", true)
		}
		if len(scopes) > 0 {
			if _, inTransaction := tx.Statement.ConnPool.(*identityTransaction); !inTransaction {
				// Raw Exec and explicitly skipped GORM transactions have no
				// BeginTx wrapper. Their SQL must also finish before the lease.
				sqlContext, cancelSQL := context.WithTimeout(tx.Statement.Context, identityTransactionTimeout)
				tx.Statement.Context = sqlContext
				tx.InstanceSet("identity:cancel_sql", cancelSQL)
			}
		}
		if err := persistIdentityCounterRefresh(tx); err != nil {
			tx.AddError(err)
			return
		}
		if len(scopes) == 0 {
			return
		}
		register(tx, scopes)
	}
	after := func(tx *gorm.DB) {
		if value, ok := tx.InstanceGet("identity:cancel_sql"); ok {
			if cancel, ok := value.(context.CancelFunc); ok {
				cancel()
			}
		}
		if value, ok := tx.InstanceGet("identity:mutations"); ok {
			if mutations, ok := value.([]*identityMutation); ok {
				for _, mutation := range mutations {
					if err := mutation.finish(); err != nil {
						tx.AddError(err)
					}
				}
			}
		}
	}
	if err := db.Callback().Create().Before("gorm:create").Register("identity:begin", func(tx *gorm.DB) { before(tx, false, true) }); err != nil {
		return err
	}
	if err := db.Callback().Create().After("gorm:create").Before("gorm:after_create").Register("identity:create_scope", func(tx *gorm.DB) {
		if tx.Error != nil || tx.RowsAffected == 0 || !IdentitySnapshotsEnabled() {
			return
		}
		if value, ok := tx.InstanceGet("identity:new_object_create"); ok && value == true && identityStatementID(tx.Statement, "id") > 0 {
			register(tx, identityMutationScopes(tx))
		}
	}); err != nil {
		return err
	}
	if err := db.Callback().Update().Before("gorm:update").Register("identity:begin", func(tx *gorm.DB) { before(tx, true, false) }); err != nil {
		return err
	}
	if err := db.Callback().Delete().Before("gorm:delete").Register("identity:begin", func(tx *gorm.DB) { before(tx, false, false) }); err != nil {
		return err
	}
	if err := db.Callback().Create().After("gorm:commit_or_rollback_transaction").Register("identity:end", after); err != nil {
		return err
	}
	if err := db.Callback().Update().After("gorm:commit_or_rollback_transaction").Register("identity:end", after); err != nil {
		return err
	}
	if err := db.Callback().Delete().After("gorm:commit_or_rollback_transaction").Register("identity:end", after); err != nil {
		return err
	}
	if err := db.Callback().Raw().Before("gorm:raw").Register("identity:begin", func(tx *gorm.DB) { before(tx, false, false) }); err != nil {
		return err
	}
	return db.Callback().Raw().After("gorm:raw").Register("identity:end", after)
}

func identityNewObjectCreate(statement *gorm.Statement) bool {
	switch statement.Table {
	case "users", "tokens", "projects", "ephemeral_credentials":
	default:
		return false
	}
	// An unresolved upsert can alter an existing object selected by a unique
	// field. It must retain the fail-closed policy fence; ordinary INSERT and
	// ON CONFLICT DO NOTHING cannot change an existing object's permissions.
	if conflict, ok := statement.Clauses["ON CONFLICT"].Expression.(clause.OnConflict); ok && !conflict.DoNothing {
		return false
	}
	value := reflect.ValueOf(statement.Model)
	for value.IsValid() && value.Kind() == reflect.Pointer {
		value = value.Elem()
	}
	return value.IsValid() && value.Kind() == reflect.Struct && identityStatementID(statement, "id") == 0
}

func identityMutationScopes(tx *gorm.DB) []string {
	statement := tx.Statement
	if identityCounterRefreshSkipped(tx) {
		if statement.Table == "users" || statement.Table == "tokens" {
			return nil
		}
	}
	if statement.Table == "" && statement.SQL.Len() > 0 {
		words := strings.Fields(strings.ToLower(statement.SQL.String()))
		if len(words) >= 2 {
			var table string
			switch words[0] {
			case "update":
				table = words[1]
			case "delete":
				if len(words) >= 3 && words[1] == "from" {
					table = words[2]
				}
			case "insert":
				if len(words) >= 3 && words[1] == "into" {
					table = words[2]
				}
			}
			table = strings.Trim(table, "\"`")
			switch table {
			case "users", "tokens", "projects", "project_members", "token_credentials", "spend_budgets", "ephemeral_credentials":
				return []string{"policy"}
			}
		}
	}
	switch statement.Table {
	case "users", "tokens":
		// Administrator baselines also advance a fence. Only the marked ledger
		// writer and idempotent credit protocol can skip this invalidation.
		id := identityStatementID(statement, "id")
		if id > 0 {
			scope := "user:"
			if statement.Table == "tokens" {
				scope = "token:"
			}
			return []string{scope + strconv.Itoa(id)}
		}
		return []string{"policy"}
	case "projects":
		if id := identityStatementID(statement, "id"); id > 0 {
			return []string{"project:" + strconv.Itoa(id)}
		}
		return []string{"policy"}
	case "project_members", "spend_budgets":
		if id := identityStatementID(statement, "project_id"); id > 0 {
			return []string{"project:" + strconv.Itoa(id)}
		}
		return []string{"policy"}
	case "token_credentials":
		if id := identityStatementID(statement, "token_id"); id > 0 {
			return []string{"token:" + strconv.Itoa(id)}
		}
		return []string{"policy"}
	case "ephemeral_credentials":
		if id := identityStatementID(statement, "id"); id > 0 {
			return []string{"ephemeral:" + strconv.Itoa(id)}
		}
		return []string{"policy"}
	default:
		return nil
	}
}

// Usage timestamps and cumulative observations do not authorize a request or
// change an administrator's financial baseline. The allowlist is deliberately
// exact: a mixed update containing quota, status or another policy field is
// fenced normally, and full saves cannot opt out through omitted metadata.
func identityObservationalWrite(statement *gorm.Statement) bool {
	if statement.Table != "users" && statement.Table != "tokens" && statement.Table != "ephemeral_credentials" {
		return false
	}
	observed := func(column string) bool {
		if statement.Schema != nil {
			if field := statement.Schema.LookUpField(column); field != nil {
				column = field.DBName
			}
		}
		switch column {
		case "last_used_at", "first_request_at", "last_request_at", "used_quota", "used_tokens", "request_count", "use_count", "updated_at":
			return true
		}
		return false
	}
	if values, ok := statement.Dest.(map[string]interface{}); ok && len(values) > 0 {
		for column := range values {
			if !observed(column) {
				return false
			}
		}
		return true
	}
	if len(statement.Selects) > 0 {
		for _, column := range statement.Selects {
			if !observed(column) {
				return false
			}
		}
		return true
	}
	return false
}

func identityStatementID(statement *gorm.Statement, column string) int {
	if statement.Schema != nil {
		if field := statement.Schema.LookUpField(column); field != nil {
			value := reflect.ValueOf(statement.Model)
			for value.IsValid() && value.Kind() == reflect.Pointer {
				value = value.Elem()
			}
			if value.IsValid() && value.Kind() == reflect.Struct {
				v, _ := field.ValueOf(statement.Context, value)
				if id := identityNumericID(v); id > 0 {
					return id
				}
			}
		}
	}
	if where, ok := statement.Clauses["WHERE"].Expression.(clause.Where); ok {
		for _, expression := range where.Exprs {
			if expr, ok := expression.(clause.Expr); ok && len(expr.Vars) == 1 {
				normalized := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(expr.SQL, "\"", ""), "`", ""))
				if strings.TrimSpace(normalized) == column+" = ?" {
					return identityNumericID(expr.Vars[0])
				}
			}
		}
	}
	return 0
}

func identityNumericID(value interface{}) int {
	// GORM uses uint IDs for credentials and budgets. The generic legacy
	// converter accepts only signed integers; treating uint as zero would
	// lose the new object's scoped fence after INSERT assigns its ID.
	v := reflect.ValueOf(value)
	if v.IsValid() {
		switch v.Kind() {
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			id := v.Uint()
			if id <= uint64(^uint(0)>>1) {
				return int(id)
			}
			return 0
		}
	}
	return common.ParseIntFromAny(value)
}
