package model

import (
	"testing"

	gormsqlite "github.com/glebarez/sqlite"
	"github.com/lazu-ai/lazu/internal/constant"
	"gorm.io/gorm"
)

func TestInferLegacyProtocolDialectFallsBackConservatively(t *testing.T) {
	if got := inferLegacyProtocolDialect(constant.ChannelTypeOpenAI, "https://gateway.example.test/v1"); got != protocolDialectOpenAIChatCompatible {
		t.Fatalf("expected conservative dialect, got %q", got)
	}
	for _, lookalike := range []string{
		"https://api.openai.com.proxy.example/v1",
		"https://openrouter.ai.proxy.example/v1",
		"https://tenant.openai.azure.com.proxy.example/v1",
		"https://example.test/api.openai.com/v1",
	} {
		if got := inferLegacyProtocolDialect(constant.ChannelTypeOpenAI, lookalike); got != protocolDialectOpenAIChatCompatible {
			t.Fatalf("lookalike URL %q inferred unsafe dialect %q", lookalike, got)
		}
	}
	if got := inferLegacyProtocolDialect(constant.ChannelTypeOpenAI, "https://tenant.services.ai.azure.com"); got != protocolDialectAzureOpenAI {
		t.Fatalf("expected Azure AI Services dialect, got %q", got)
	}
}

func TestBatchInsertChannelsInitializesMissingProtocolDialect(t *testing.T) {
	db := withChannelKeyTestDB(t, "channel_protocol_identity_batch_insert")
	explicitURL := "https://api.openai.com/v1"
	channels := []Channel{
		{Type: constant.ChannelTypeOpenAI, Name: "inferred", Key: "test-1", BaseURL: &explicitURL, Models: "gpt-test", Group: ChannelLaneDirect},
		{Type: constant.ChannelTypeOpenAI, Name: "explicit", Key: "test-2", BaseURL: &explicitURL, Models: "gpt-test", Group: ChannelLaneDirect, OtherSettings: `{"protocol_dialect":"openai_chat_compatible"}`},
	}
	if err := BatchInsertChannels(channels); err != nil {
		t.Fatalf("batch insert channels: %v", err)
	}
	var stored []Channel
	if err := db.Order("id ASC").Find(&stored).Error; err != nil {
		t.Fatalf("load channels: %v", err)
	}
	if len(stored) != 2 || stored[0].GetOtherSettings().ProtocolDialect != protocolDialectOpenAI {
		t.Fatalf("new channel did not persist inferred OpenAI dialect: %+v", stored)
	}
	if stored[1].GetOtherSettings().ProtocolDialect != protocolDialectOpenAIChatCompatible {
		t.Fatalf("explicit dialect was overwritten: %+v", stored[1].GetOtherSettings())
	}
}

func TestChannelUpdateInitializesMissingLegacyProtocolDialect(t *testing.T) {
	db := withChannelKeyTestDB(t, "channel_protocol_identity_update")
	baseURL := "https://api.cloudflare.com/client/v4/accounts/example/ai"
	channel := Channel{Type: constant.ChannelTypeOpenAI, Name: "legacy", Key: "test", BaseURL: &baseURL, Models: "gpt-test", Group: ChannelLaneDirect, OtherSettings: `{}`}
	if err := db.Create(&channel).Error; err != nil {
		t.Fatalf("seed legacy channel: %v", err)
	}
	channel.Name = "updated"
	if err := channel.Update(); err != nil {
		t.Fatalf("update legacy channel: %v", err)
	}
	if dialect := channel.GetOtherSettings().ProtocolDialect; dialect != protocolDialectCloudflare {
		t.Fatalf("update did not persist inferred Cloudflare dialect: %q (%s)", dialect, channel.OtherSettings)
	}
}

func TestBackfillDeepSeekDialectMovesGenericChannelsOnce(t *testing.T) {
	db, err := gorm.Open(gormsqlite.Open("file:deepseek_dialect_backfill?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&Channel{}, &SchemaVersion{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	deepseek := "https://api.deepseek.com"
	other := "https://api.openai.com"
	rows := []Channel{
		{Type: constant.ChannelTypeOpenAI, Name: "generic", Key: "k", BaseURL: &deepseek, OtherSettings: `{"protocol_dialect":"openai_chat_compatible","azure_responses_version":"preview"}`},
		{Type: constant.ChannelTypeOpenAI, Name: "unset", Key: "k", BaseURL: &deepseek, OtherSettings: ``},
		{Type: constant.ChannelTypeOpenAI, Name: "operator chose", Key: "k", BaseURL: &deepseek, OtherSettings: `{"protocol_dialect":"openai"}`},
		{Type: constant.ChannelTypeOpenAI, Name: "not deepseek", Key: "k", BaseURL: &other, OtherSettings: `{"protocol_dialect":"openai_chat_compatible"}`},
	}
	for i := range rows {
		if err := db.Create(&rows[i]).Error; err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	if err := backfillDeepSeekDialectOnce(db); err != nil {
		t.Fatalf("backfill: %v", err)
	}
	want := map[string]string{"generic": "deepseek", "unset": "deepseek", "operator chose": "openai", "not deepseek": "openai_chat_compatible"}
	for name, dialect := range want {
		var got Channel
		if err := db.Where("name = ?", name).First(&got).Error; err != nil {
			t.Fatalf("load %s: %v", name, err)
		}
		settings := got.GetOtherSettings()
		if settings.ProtocolDialect != dialect {
			t.Fatalf("%s: dialect %q want %q", name, settings.ProtocolDialect, dialect)
		}
		if name == "generic" && settings.AzureResponsesVersion != "preview" {
			t.Fatalf("other settings lost: %#v", settings)
		}
	}
	// marker recorded: a channel switched back by an operator stays switched
	if err := db.Model(&Channel{}).Where("name = ?", "generic").Update("settings", `{"protocol_dialect":"openai_chat_compatible"}`).Error; err != nil {
		t.Fatal(err)
	}
	if err := backfillDeepSeekDialectOnce(db); err != nil {
		t.Fatal(err)
	}
	var again Channel
	if err := db.Where("name = ?", "generic").First(&again).Error; err != nil {
		t.Fatal(err)
	}
	if again.GetOtherSettings().ProtocolDialect != "openai_chat_compatible" {
		t.Fatal("second run must be a no-op")
	}
}
