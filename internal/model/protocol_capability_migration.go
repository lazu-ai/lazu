package model

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"gorm.io/gorm"
)

const (
	protocolDialectOpenAI               = "openai"
	protocolDialectAzureOpenAI          = "azure_openai"
	protocolDialectOpenRouter           = "openrouter"
	protocolDialectCloudflare           = "cloudflare_ai_v1"
	protocolDialectOpenAIChatCompatible = "openai_chat_compatible"
	protocolDialectDeepSeek             = "deepseek"
	protocolDialectAnthropic            = "anthropic"
	protocolDialectGemini               = "gemini"
)

func initializeMissingProtocolDialect(channel *Channel, existing *Channel) error {
	if channel == nil {
		return nil
	}
	raw := strings.TrimSpace(channel.OtherSettings)
	if raw == "" && existing != nil {
		raw = strings.TrimSpace(existing.OtherSettings)
	}
	settings := make(map[string]json.RawMessage)
	if raw != "" {
		if err := json.Unmarshal([]byte(raw), &settings); err != nil {
			return fmt.Errorf("invalid channel settings: %w", err)
		}
	}
	if settings == nil {
		settings = make(map[string]json.RawMessage)
	}
	var configured string
	if value, ok := settings["protocol_dialect"]; ok {
		_ = json.Unmarshal(value, &configured)
	}
	if strings.TrimSpace(configured) != "" {
		return nil
	}

	channelType := channel.Type
	baseURL := channel.BaseURL
	if existing != nil {
		if channelType == constant.ChannelTypeUnknown {
			channelType = existing.Type
		}
		if baseURL == nil {
			baseURL = existing.BaseURL
		}
	}
	effectiveURL := ""
	if baseURL != nil {
		effectiveURL = strings.TrimSpace(*baseURL)
	}
	if effectiveURL == "" {
		effectiveURL = constant.ChannelBaseURLs[channelType]
	}
	dialect, err := json.Marshal(inferLegacyProtocolDialect(channelType, effectiveURL))
	if err != nil {
		return err
	}
	settings["protocol_dialect"] = dialect
	payload, err := json.Marshal(settings)
	if err != nil {
		return err
	}
	channel.OtherSettings = string(payload)
	return nil
}

// BackfillProtocolCapabilityIdentity persists the one-time name/URL inference
// required for legacy rows. Request routing never performs these heuristics.
func BackfillProtocolCapabilityIdentity(db *gorm.DB) error {
	if db == nil {
		return nil
	}
	if db.Migrator().HasTable(&Model{}) && db.Migrator().HasColumn(&Model{}, "family") {
		var models []Model
		if err := db.Select("id, model_name, family").Where("family = '' OR family IS NULL").Find(&models).Error; err != nil {
			return err
		}
		for _, entry := range models {
			family := InferModelFamily(entry.ModelName)
			if family != "" {
				if err := db.Model(&Model{}).Where("id = ?", entry.Id).Update("family", family).Error; err != nil {
					return err
				}
			}
		}
	}
	if !db.Migrator().HasTable(&Channel{}) {
		return nil
	}
	var channels []Channel
	if err := db.Select("id, type, base_url, settings").Find(&channels).Error; err != nil {
		return err
	}
	for _, channel := range channels {
		settings := make(map[string]json.RawMessage)
		if raw := strings.TrimSpace(channel.OtherSettings); raw != "" {
			if err := json.Unmarshal([]byte(raw), &settings); err != nil {
				common.SysLog(fmt.Sprintf("skipping protocol dialect backfill for malformed channel settings: channel_id=%d error=%v", channel.Id, err))
				continue
			}
		}
		if settings == nil {
			settings = make(map[string]json.RawMessage)
		}
		var configured string
		if raw, ok := settings["protocol_dialect"]; ok {
			_ = json.Unmarshal(raw, &configured)
		}
		if strings.TrimSpace(configured) != "" {
			continue
		}
		// Legacy rows commonly store an explicit empty URL and rely on the
		// channel type's built-in endpoint. Infer from that effective URL so an
		// official OpenAI channel retains native Responses support.
		baseURL := channel.GetBaseURL()
		if strings.TrimSpace(baseURL) == "" {
			baseURL = constant.ChannelBaseURLs[channel.Type]
		}
		dialect, err := json.Marshal(inferLegacyProtocolDialect(channel.Type, baseURL))
		if err != nil {
			return err
		}
		settings["protocol_dialect"] = dialect
		payload, err := json.Marshal(settings)
		if err != nil {
			return err
		}
		if err := db.Model(&Channel{}).Where("id = ?", channel.Id).Update("settings", string(payload)).Error; err != nil {
			return err
		}
	}
	return nil
}

func inferLegacyProtocolDialect(channelType int, baseURL string) string {
	host := protocolURLHostname(baseURL)
	switch {
	case host == "api.cloudflare.com" || host == "gateway.ai.cloudflare.com":
		return protocolDialectCloudflare
	case host == "openrouter.ai" || strings.HasSuffix(host, ".openrouter.ai"):
		return protocolDialectOpenRouter
	case host == "openai.azure.com" || strings.HasSuffix(host, ".openai.azure.com") ||
		host == "services.ai.azure.com" || strings.HasSuffix(host, ".services.ai.azure.com"):
		return protocolDialectAzureOpenAI
	case host == "api.openai.com":
		return protocolDialectOpenAI
	case host == "api.deepseek.com":
		return protocolDialectDeepSeek
	}
	apiType, ok := common.ChannelType2APIType(channelType)
	if !ok {
		return protocolDialectOpenAIChatCompatible
	}
	switch apiType {
	case constant.APITypeAnthropic:
		return protocolDialectAnthropic
	case constant.APITypeGemini:
		return protocolDialectGemini
	default:
		return protocolDialectOpenAIChatCompatible
	}
}

func protocolURLHostname(value string) string {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return ""
	}
	return strings.TrimSuffix(strings.ToLower(parsed.Hostname()), ".")
}

// backfillDeepSeekDialect moves channels that reach api.deepseek.com from the
// generic openai_chat_compatible dialect (or none) to the deepseek dialect, so
// the wire dialect patch layer can address DeepSeek's quirks on channels
// created before the dialect existed. A channel whose operator chose another
// dialect is left alone. Runs once (deepseek_dialect_v1).
func backfillDeepSeekDialect(db *gorm.DB) error {
	if db == nil || !db.Migrator().HasTable(&Channel{}) {
		return nil
	}
	var channels []Channel
	if err := db.Select("id, type, base_url, settings").Find(&channels).Error; err != nil {
		return err
	}
	for _, channel := range channels {
		if protocolURLHostname(channel.GetBaseURL()) != "api.deepseek.com" {
			continue
		}
		settings := make(map[string]json.RawMessage)
		if raw := strings.TrimSpace(channel.OtherSettings); raw != "" {
			if err := json.Unmarshal([]byte(raw), &settings); err != nil {
				common.SysLog(fmt.Sprintf("skipping deepseek dialect backfill for malformed channel settings: channel_id=%d error=%v", channel.Id, err))
				continue
			}
		}
		var configured string
		if raw, ok := settings["protocol_dialect"]; ok {
			_ = json.Unmarshal(raw, &configured)
		}
		switch strings.TrimSpace(configured) {
		case "", protocolDialectOpenAIChatCompatible:
		default:
			continue
		}
		dialect, _ := json.Marshal(protocolDialectDeepSeek)
		settings["protocol_dialect"] = dialect
		payload, err := json.Marshal(settings)
		if err != nil {
			return err
		}
		if err := db.Model(&Channel{}).Where("id = ?", channel.Id).Update("settings", string(payload)).Error; err != nil {
			return err
		}
	}
	return nil
}

// backfillDeepSeekDialectOnce runs backfillDeepSeekDialect under its one-time
// marker where the marker table exists, and unrecorded where it does not.
func backfillDeepSeekDialectOnce(db *gorm.DB) error {
	if db == nil {
		return nil
	}
	if !db.Migrator().HasTable(&SchemaVersion{}) {
		return backfillDeepSeekDialect(db)
	}
	return runOneTimeMigration(db, "deepseek_dialect_v1", func() error { return backfillDeepSeekDialect(db) })
}
