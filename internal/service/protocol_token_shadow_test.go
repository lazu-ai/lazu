package service

import (
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	gormsqlite "github.com/glebarez/sqlite"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/types"
	"gorm.io/gorm"
)

func TestProtocolTokenShadowOnlyAugmentsDiagnostics(t *testing.T) {
	db, err := gorm.Open(gormsqlite.Open(filepath.Join(t.TempDir(), "shadow.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	previousDB := model.ConfiguredTelemetryDB()
	previousEnabled, previousNonStream := constant.GetMediaToken, constant.GetMediaTokenNotStream
	previousRedis := common.RedisEnabled
	model.SetTelemetryDB(db)
	// Legacy billing fallback switches must not suppress background diagnostics.
	constant.GetMediaToken, constant.GetMediaTokenNotStream, common.RedisEnabled = false, false, false
	t.Cleanup(func() {
		model.SetTelemetryDB(previousDB)
		constant.GetMediaToken, constant.GetMediaTokenNotStream, common.RedisEnabled = previousEnabled, previousNonStream, previousRedis
		sqlDB, _ := db.DB()
		_ = sqlDB.Close()
	})
	if err := db.AutoMigrate(&model.Log{}); err != nil {
		t.Fatal(err)
	}
	row := &model.Log{Id: 1, Type: model.LogTypeConsume, RequestID: "shadow-fixture", RequestTS: 1234, AmountMicrousd: 4567, PromptTokens: 800, Other: `{"usage_source":"provider","protocol_admission_tokens":800}`}
	if err := db.Create(row).Error; err != nil {
		t.Fatal(err)
	}
	runProtocolTokenShadow(protocolTokenShadowTask{requestID: row.RequestID, model: "gpt-4o", format: types.RelayFormatOpenAI, admission: 800, meta: &types.TokenCountMeta{CombineText: "hello world", MessagesCount: 1}})
	var result model.Log
	if err := db.First(&result).Error; err != nil {
		t.Fatal(err)
	}
	var fields map[string]interface{}
	if err := json.Unmarshal([]byte(result.Other), &fields); err != nil {
		t.Fatal(err)
	}
	local, observed := fields["protocol_local_prompt_tokens"].(float64)
	if !observed || local <= 0 || fields["protocol_admission_token_delta"] != local-800 {
		t.Fatalf("invalid comparison facts: %+v", fields)
	}
	if result.PromptTokens != 800 || result.AmountMicrousd != 4567 || fields["usage_source"] != "provider" {
		t.Fatalf("shadow changed accounting: %+v", result)
	}
	memo := newProtocolTokenShadowMemo()
	task := protocolTokenShadowTask{requestID: row.RequestID, model: "gpt-4o", format: types.RelayFormatOpenAI, admission: 800, meta: &types.TokenCountMeta{CombineText: "hello world", MessagesCount: 1}}
	runProtocolTokenShadowWithMemo(task, memo)
	repeat := &model.Log{Id: 2, Type: model.LogTypeConsume, RequestID: "shadow-repeat-fixture", RequestTS: 1235, AmountMicrousd: 8901, PromptTokens: 900, Other: `{"usage_source":"provider"}`}
	if err := db.Create(repeat).Error; err != nil {
		t.Fatal(err)
	}
	task.requestID, task.admission = repeat.RequestID, 900
	runProtocolTokenShadowWithMemo(task, memo)
	var repeated model.Log
	if err := db.Where("request_id = ?", repeat.RequestID).First(&repeated).Error; err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(repeated.Other), &fields); err != nil {
		t.Fatal(err)
	}
	if fields["protocol_local_prompt_tokens"] != local || fields["protocol_admission_token_delta"] != local-900 || repeated.AmountMicrousd != 8901 || repeated.PromptTokens != 900 {
		t.Fatalf("cached count reused request facts/accounting: %+v %+v", repeated, fields)
	}
	if err := db.Model(&model.Log{}).Where("id = ?", row.Id).Updates(map[string]interface{}{"type": model.LogTypeUnknown, "other": "{}"}).Error; err != nil {
		t.Fatal(err)
	}
	runProtocolTokenShadow(protocolTokenShadowTask{requestID: row.RequestID, model: "gpt-4o", format: types.RelayFormatOpenAI, admission: 800, meta: &types.TokenCountMeta{CombineText: "late shadow must not resurrect expired data", MessagesCount: 1}})
	if err := db.First(&result).Error; err != nil || result.Other != "{}" {
		t.Fatalf("late shadow resurrected expired consume facts: %+v %v", result, err)
	}
}

func TestProtocolTokenShadowQueueBytesDropWithoutBlocking(t *testing.T) {
	previous := tokenShadowQueuedBytes.Load()
	tokenShadowQueuedBytes.Store(tokenShadowQueueBytes)
	t.Cleanup(func() { tokenShadowQueuedBytes.Store(previous) })
	before := common.SnapshotHotPathMetrics().Counters["protocol.token_shadow.dropped"]
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Set(common.RequestIdKey, "shadow-queue-fixture")
	c.Set("protocol_token_shadow_meta", &types.TokenCountMeta{CombineText: "hello"})
	EnqueueProtocolTokenShadow(c)
	if tokenShadowQueuedBytes.Load() != tokenShadowQueueBytes {
		t.Fatal("dropped task retained a byte reservation")
	}
	if common.SnapshotHotPathMetrics().Counters["protocol.token_shadow.dropped"] != before+1 {
		t.Fatal("queue drop was invisible with timing disabled")
	}
}
