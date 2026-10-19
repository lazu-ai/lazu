package model

import (
	"fmt"
	"strings"
	"testing"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"

	gormsqlite "github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func withChannelKeyTestDB(t *testing.T, name string) *gorm.DB {
	t.Helper()

	previousDB := coreDB
	previousCryptoSecret := common.CryptoSecret
	previousMemoryCacheEnabled := common.MemoryCacheEnabled
	previousUsingPostgreSQL := common.UsingPostgreSQL
	previousUsingSQLite := common.UsingSQLite
	previousCommonGroupCol := commonGroupCol
	previousCommonKeyCol := commonKeyCol
	previousCommonTrueVal := commonTrueVal
	previousCommonFalseVal := commonFalseVal
	previousLogGroupCol := logGroupCol
	previousLogKeyCol := logKeyCol

	db, err := gorm.Open(gormsqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", name)), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}
	coreDB = db
	common.CryptoSecret = "channel-key-test-secret" // secret-scan: allow -- test fixture
	common.MemoryCacheEnabled = false
	common.UsingPostgreSQL = false
	common.UsingSQLite = true
	initCol()
	if err := db.AutoMigrate(&Channel{}, &Ability{}); err != nil {
		t.Fatalf("failed to migrate channel tables: %v", err)
	}
	t.Cleanup(func() {
		sqlDB, _ := db.DB()
		_ = sqlDB.Close()
		coreDB = previousDB
		common.CryptoSecret = previousCryptoSecret
		common.MemoryCacheEnabled = previousMemoryCacheEnabled
		common.UsingPostgreSQL = previousUsingPostgreSQL
		common.UsingSQLite = previousUsingSQLite
		commonGroupCol = previousCommonGroupCol
		commonKeyCol = previousCommonKeyCol
		commonTrueVal = previousCommonTrueVal
		commonFalseVal = previousCommonFalseVal
		logGroupCol = previousLogGroupCol
		logKeyCol = previousLogKeyCol
	})
	return db
}

func storedChannelKey(t *testing.T, db *gorm.DB, id int) string {
	t.Helper()
	var stored string
	if err := db.Table("channels").Select(commonKeyCol).Where("id = ?", id).Scan(&stored).Error; err != nil {
		t.Fatalf("failed to read stored channel key: %v", err)
	}
	return stored
}

func TestChannelKeyInsertStoresEncryptedKeyAndReadsPlaintext(t *testing.T) {
	db := withChannelKeyTestDB(t, "channel_key_insert_encrypted")
	clearKey := "sk-channel-secret"
	channel := &Channel{
		Type:     constant.ChannelTypeOpenAI,
		Key:      clearKey,
		Status:   common.ChannelStatusEnabled,
		Name:     "secure-channel",
		Models:   "gpt-test",
		Group:    ChannelLaneDirect,
		Priority: common.GetPointer[int64](10),
	}
	if err := channel.Insert(); err != nil {
		t.Fatalf("insert channel: %v", err)
	}
	if channel.Key != clearKey {
		t.Fatalf("insert should preserve plaintext key in memory, got %q", channel.Key)
	}

	stored := storedChannelKey(t, db, channel.Id)
	if stored == clearKey || strings.Contains(stored, clearKey) {
		t.Fatalf("stored channel key should not contain plaintext, got %q", stored)
	}
	if !IsEncryptedChannelKey(stored) {
		t.Fatalf("stored channel key should use encrypted envelope, got %q", stored)
	}

	found, err := GetChannelById(channel.Id, true)
	if err != nil {
		t.Fatalf("load channel with key: %v", err)
	}
	if found.Key != clearKey {
		t.Fatalf("expected decrypted key %q, got %q", clearKey, found.Key)
	}

	common.MemoryCacheEnabled = true
	InitChannelCache()
	cached, err := CacheGetChannel(channel.Id)
	if err != nil {
		t.Fatalf("load channel from cache: %v", err)
	}
	if cached.Key != clearKey {
		t.Fatalf("expected cached plaintext key %q, got %q", clearKey, cached.Key)
	}
}

func TestChannelCacheUsesAbilityPriorityAndWeight(t *testing.T) {
	db := withChannelKeyTestDB(t, "channel_cache_ability_routing")
	priority := int64(10)
	channelWeight := uint(80)
	channel := &Channel{
		Type:     constant.ChannelTypeOpenAI,
		Key:      "sk-cache-routing",
		Status:   common.ChannelStatusEnabled,
		Name:     "cache-routing-channel",
		Models:   "gpt-cache-routing",
		Group:    ChannelLaneDirect,
		Priority: &priority,
		Weight:   &channelWeight,
	}
	if err := channel.Insert(); err != nil {
		t.Fatalf("insert channel: %v", err)
	}

	abilityPriority := int64(42)
	if err := db.Model(&Ability{}).
		Where(abilityGroupCol()+" = ? AND model = ? AND channel_id = ?", ChannelLaneDirect, "gpt-cache-routing", channel.Id).
		Updates(map[string]any{"priority": abilityPriority, "weight": 7}).Error; err != nil {
		t.Fatalf("update ability override: %v", err)
	}

	common.MemoryCacheEnabled = true
	InitChannelCache()
	abilities := group2model2channels[ChannelLaneDirect]["gpt-cache-routing"]
	if len(abilities) != 1 {
		t.Fatalf("expected one cached ability, got %#v", abilities)
	}
	if abilities[0].ChannelID != channel.Id || abilities[0].Priority != abilityPriority || abilities[0].Weight != 7 {
		t.Fatalf("expected ability-local routing values, got %#v", abilities[0])
	}
}

func TestChannelUpdatePreservesExistingAbilityRoutingOverrides(t *testing.T) {
	db := withChannelKeyTestDB(t, "channel_update_preserves_ability_overrides")
	priority := int64(10)
	channelWeight := uint(80)
	channel := &Channel{
		Type:     constant.ChannelTypeOpenAI,
		Key:      "sk-preserve-routing",
		Status:   common.ChannelStatusEnabled,
		Name:     "preserve-routing-channel",
		Models:   "gpt-existing",
		Group:    ChannelLaneDirect,
		Priority: &priority,
		Weight:   &channelWeight,
	}
	if err := channel.Insert(); err != nil {
		t.Fatalf("insert channel: %v", err)
	}

	overridePriority := int64(42)
	if err := db.Model(&Ability{}).
		Where(abilityGroupCol()+" = ? AND model = ? AND channel_id = ?", ChannelLaneDirect, "gpt-existing", channel.Id).
		Updates(map[string]any{"priority": overridePriority, "weight": 7}).Error; err != nil {
		t.Fatalf("set ability override: %v", err)
	}

	newDefaultWeight := uint(25)
	newDefaultPriority := int64(12)
	channel.Weight = &newDefaultWeight
	channel.Priority = &newDefaultPriority
	channel.Models = "gpt-existing,gpt-new"
	if err := channel.Update(); err != nil {
		t.Fatalf("update channel: %v", err)
	}

	var abilities []Ability
	if err := db.Where("channel_id = ?", channel.Id).Order("model ASC").Find(&abilities).Error; err != nil {
		t.Fatalf("load rebuilt abilities: %v", err)
	}
	if len(abilities) != 2 {
		t.Fatalf("expected two abilities, got %#v", abilities)
	}
	if abilities[0].Model != "gpt-existing" || abilities[0].Priority == nil || *abilities[0].Priority != overridePriority || abilities[0].Weight != 7 {
		t.Fatalf("expected existing override to survive, got %#v", abilities[0])
	}
	if abilities[1].Model != "gpt-new" || abilities[1].Priority == nil || *abilities[1].Priority != newDefaultPriority || abilities[1].Weight != newDefaultWeight {
		t.Fatalf("expected new membership to use channel defaults, got %#v", abilities[1])
	}
}

func TestChannelKeyUpdateStoresEncryptedKeyAndSearchesByDigest(t *testing.T) {
	db := withChannelKeyTestDB(t, "channel_key_update_encrypted")
	channel := &Channel{
		Type:     constant.ChannelTypeOpenAI,
		Key:      "sk-original",
		Status:   common.ChannelStatusEnabled,
		Name:     "update-channel",
		Models:   "gpt-test",
		Group:    ChannelLaneDirect,
		Priority: common.GetPointer[int64](10),
	}
	if err := channel.Insert(); err != nil {
		t.Fatalf("insert channel: %v", err)
	}

	clearKey := "sk-updated"
	channel.Key = clearKey
	if err := channel.Update(); err != nil {
		t.Fatalf("update channel: %v", err)
	}
	stored := storedChannelKey(t, db, channel.Id)
	if stored == clearKey || strings.Contains(stored, clearKey) {
		t.Fatalf("updated stored key should not contain plaintext, got %q", stored)
	}
	if !IsEncryptedChannelKey(stored) {
		t.Fatalf("updated stored key should use encrypted envelope, got %q", stored)
	}

	found, err := GetChannelById(channel.Id, true)
	if err != nil {
		t.Fatalf("load updated channel: %v", err)
	}
	if found.Key != clearKey {
		t.Fatalf("expected decrypted updated key %q, got %q", clearKey, found.Key)
	}

	results, err := SearchChannels(clearKey, "", "", false)
	if err != nil {
		t.Fatalf("search channel by key digest: %v", err)
	}
	if len(results) != 1 || results[0].Id != channel.Id {
		t.Fatalf("expected digest search to find channel %d, got %#v", channel.Id, results)
	}
}

func TestChannelKeySearchFindsEncryptedMultiKeyMember(t *testing.T) {
	db := withChannelKeyTestDB(t, "channel_key_multikey_digest_search")
	channel := &Channel{
		Type:     constant.ChannelTypeOpenAI,
		Key:      "sk-primary\nsk-secondary",
		Status:   common.ChannelStatusEnabled,
		Name:     "multi-key-channel",
		Models:   "gpt-test",
		Group:    ChannelLaneDirect,
		Priority: common.GetPointer[int64](10),
		ChannelInfo: ChannelInfo{
			IsMultiKey:   true,
			MultiKeySize: 2,
		},
	}
	if err := channel.Insert(); err != nil {
		t.Fatalf("insert multi-key channel: %v", err)
	}
	var stored struct {
		KeyDigest  string
		KeyDigests string
	}
	if err := db.Model(&Channel{}).Select("key_digest", "key_digests").Where("id = ?", channel.Id).Scan(&stored).Error; err != nil {
		t.Fatalf("load stored digests: %v", err)
	}
	secondaryDigest, err := channelKeyDigest("sk-secondary")
	if err != nil {
		t.Fatalf("digest secondary key: %v", err)
	}
	if stored.KeyDigest == secondaryDigest {
		t.Fatalf("single key_digest should remain the whole stored key digest, got member digest %q", stored.KeyDigest)
	}
	if !strings.Contains(stored.KeyDigests, "\n"+secondaryDigest+"\n") {
		t.Fatalf("expected per-key digest envelope to include secondary key, got %q", stored.KeyDigests)
	}
	results, err := SearchChannels("sk-secondary", "", "", false)
	if err != nil {
		t.Fatalf("search channel by per-key digest: %v", err)
	}
	if len(results) != 1 || results[0].Id != channel.Id {
		t.Fatalf("expected per-key digest search to find channel %d, got %#v", channel.Id, results)
	}
}

func TestMigrateChannelKeysEncryptsLegacyPlaintext(t *testing.T) {
	db := withChannelKeyTestDB(t, "channel_key_migrate_legacy")
	clearKey := "sk-legacy"
	insertSQL := fmt.Sprintf(
		"INSERT INTO channels (id, public_id, type, %s, status, name, models, %s) VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
		commonKeyCol,
		commonGroupCol,
	)
	if err := db.Exec(insertSQL, 77, "chan_legacy", constant.ChannelTypeOpenAI, clearKey, common.ChannelStatusEnabled, "legacy-channel", "gpt-test", ChannelLaneDirect).Error; err != nil {
		t.Fatalf("insert legacy channel: %v", err)
	}

	if err := migrateChannelKeysToEncrypted(db); err != nil {
		t.Fatalf("migrate channel keys: %v", err)
	}
	stored := storedChannelKey(t, db, 77)
	if stored == clearKey || strings.Contains(stored, clearKey) {
		t.Fatalf("migrated key should not contain plaintext, got %q", stored)
	}
	if !IsEncryptedChannelKey(stored) {
		t.Fatalf("migrated key should use encrypted envelope, got %q", stored)
	}

	found, err := GetChannelById(77, true)
	if err != nil {
		t.Fatalf("load migrated channel: %v", err)
	}
	if found.Key != clearKey {
		t.Fatalf("expected migrated channel key %q, got %q", clearKey, found.Key)
	}
}

func TestMigrateChannelKeysSkipsWithoutStableSecret(t *testing.T) {
	db := withChannelKeyTestDB(t, "channel_key_migrate_skip_without_stable_secret")
	common.CryptoSecret = common.SessionSecret
	clearKey := "sk-legacy"
	insertSQL := fmt.Sprintf(
		"INSERT INTO channels (id, public_id, type, %s, status, name, models, %s) VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
		commonKeyCol,
		commonGroupCol,
	)
	if err := db.Exec(insertSQL, 78, "chan_legacy_skip", constant.ChannelTypeOpenAI, clearKey, common.ChannelStatusEnabled, "legacy-channel-skip", "gpt-test", ChannelLaneDirect).Error; err != nil {
		t.Fatalf("insert legacy channel: %v", err)
	}

	if err := migrateChannelKeysToEncrypted(db); err != nil {
		t.Fatalf("expected migration to skip without stable secret, got %v", err)
	}
	stored := storedChannelKey(t, db, 78)
	if stored != clearKey {
		t.Fatalf("expected migration to leave legacy key unchanged, got %q", stored)
	}
}

func TestChannelSearchWithoutStableSecretStillFindsNonKeyFields(t *testing.T) {
	db := withChannelKeyTestDB(t, "channel_key_search_without_stable_secret")
	t.Setenv("CRYPTO_SECRET", "")
	t.Setenv("SESSION_SECRET", "")
	common.CryptoSecret = common.SessionSecret
	baseURL := "https://upstream.example.com/v1"
	tag := "local-tag"
	insertSQL := fmt.Sprintf(
		"INSERT INTO channels (id, public_id, type, %s, status, name, models, %s, base_url, tag) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		commonKeyCol,
		commonGroupCol,
	)
	if err := db.Exec(insertSQL, 88, "chan_plain_search", constant.ChannelTypeOpenAI, "legacy-plaintext", common.ChannelStatusEnabled, "plain-search-channel", "gpt-test", ChannelLaneDirect, baseURL, tag).Error; err != nil {
		t.Fatalf("insert legacy channel: %v", err)
	}

	results, err := SearchChannels("plain-search", "", "", false)
	if err != nil {
		t.Fatalf("search channel by name without stable secret: %v", err)
	}
	if len(results) != 1 || results[0].Id != 88 {
		t.Fatalf("expected name search to find channel 88, got %#v", results)
	}
	results, err = SearchChannels("upstream.example.com", "", "", false)
	if err != nil {
		t.Fatalf("search channel by base URL without stable secret: %v", err)
	}
	if len(results) != 1 || results[0].Id != 88 {
		t.Fatalf("expected base URL search to find channel 88, got %#v", results)
	}
	tags, err := SearchTags("plain-search", "", "", false)
	if err != nil {
		t.Fatalf("search tags without stable secret: %v", err)
	}
	if len(tags) != 1 || tags[0] == nil || *tags[0] != tag {
		t.Fatalf("expected tag %q, got %#v", tag, tags)
	}
}

func TestChannelKeyStorageFallsBackToPlaintextWithoutStableSecret(t *testing.T) {
	db := withChannelKeyTestDB(t, "channel_key_missing_stable_secret")
	t.Setenv("CRYPTO_SECRET", "")
	t.Setenv("SESSION_SECRET", "")
	common.CryptoSecret = common.SessionSecret
	clearKey := "sk-missing-stable-secret"

	channel := &Channel{
		Type:   constant.ChannelTypeOpenAI,
		Key:    clearKey,
		Status: common.ChannelStatusEnabled,
		Name:   "missing-stable-secret-channel",
		Models: "gpt-test",
		Group:  ChannelLaneDirect,
	}
	if err := channel.Insert(); err != nil {
		t.Fatalf("expected channel insert to fall back to plaintext without stable secret, got %v", err)
	}
	stored := storedChannelKey(t, db, channel.Id)
	if stored != clearKey {
		t.Fatalf("expected plaintext fallback key %q, got %q", clearKey, stored)
	}
	if IsEncryptedChannelKey(stored) {
		t.Fatalf("expected plaintext fallback, got encrypted key %q", stored)
	}

	found, err := GetChannelById(channel.Id, true)
	if err != nil {
		t.Fatalf("load plaintext fallback channel: %v", err)
	}
	if found.Key != clearKey {
		t.Fatalf("expected plaintext fallback key %q, got %q", clearKey, found.Key)
	}
}
