package model

import (
	"testing"
	"time"
)

func TestRemoveUserPricePlanPreservesNewerAssignment(t *testing.T) {
	withPricePlanTestDB(t, t.Name(), nil)
	user := User{Username: "plan-member", PricePlanID: 2}
	if err := coreDB.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	if err := RemoveUserPricePlan(user.Id, 1); err == nil {
		t.Fatal("stale membership removal should fail")
	}
	var stored User
	if err := coreDB.First(&stored, user.Id).Error; err != nil {
		t.Fatal(err)
	}
	if stored.PricePlanID != 2 {
		t.Fatalf("new assignment was removed: %d", stored.PricePlanID)
	}
	if err := RemoveUserPricePlan(user.Id, 2); err != nil {
		t.Fatal(err)
	}
	if err := coreDB.First(&stored, user.Id).Error; err != nil {
		t.Fatal(err)
	}
	if stored.PricePlanID != 0 {
		t.Fatalf("current assignment was not removed: %d", stored.PricePlanID)
	}
}

func withPricePlanTestDB(t *testing.T, name string, vendors map[string]int) {
	t.Helper()
	db := withPricingTestDB(t, name)
	if err := db.AutoMigrate(&PricePlan{}, &PricePlanRule{}, &Vendor{}); err != nil {
		t.Fatalf("migrate price plans: %v", err)
	}
	previousVendorOf := pricePlanVendorOf
	pricePlanVendorOf = func(modelName string) (int, string) {
		if id, ok := vendors[modelName]; ok {
			return id, "vendor-fixture"
		}
		return 0, ""
	}
	InvalidatePricePlanCache()
	t.Cleanup(func() {
		pricePlanVendorOf = previousVendorOf
		InvalidatePricePlanCache()
	})
}

func savePricePlanForTest(t *testing.T, plan PricePlan, rules ...PricePlanRule) int {
	t.Helper()
	plan.Active = true
	detail, err := SavePricePlan(plan, rules)
	if err != nil {
		t.Fatalf("save price plan %q: %v", plan.Name, err)
	}
	return detail.ID
}

func TestResolvePlanDiscountPrefersModelThenVendorThenDefault(t *testing.T) {
	withPricePlanTestDB(t, t.Name(), map[string]int{"exception-model": 7, "vendor-model": 7})
	defaultPct := 10.0
	planID := savePricePlanForTest(t, PricePlan{Name: "contract", DefaultPct: &defaultPct},
		PricePlanRule{Kind: PricePlanRuleVendor, VendorID: 7, Pct: 20},
		PricePlanRule{Kind: PricePlanRuleModel, ModelName: "exception-model", Pct: 30},
	)

	cases := []struct {
		model   string
		pct     float64
		rule    string
		subject string
	}{
		{model: "exception-model", pct: 30, rule: PricePlanRuleModel, subject: "exception-model"},
		{model: "vendor-model", pct: 20, rule: PricePlanRuleVendor, subject: "vendor-fixture"},
		{model: "other-model", pct: 10, rule: PricePlanRuleSourceDefault},
	}
	for _, tc := range cases {
		got, ok := ResolvePlanDiscount(planID, ChannelLaneDirect, tc.model)
		if !ok {
			t.Fatalf("%s: expected a discount", tc.model)
		}
		if got.PlanID != planID || got.PlanName != "contract" || got.Pct.InexactFloat64() != tc.pct || got.Rule != tc.rule || got.Subject != tc.subject {
			t.Fatalf("%s: got %+v, want pct=%v rule=%s subject=%q", tc.model, got, tc.pct, tc.rule, tc.subject)
		}
	}
}

func TestResolvePlanDiscountWithoutDefaultOnlyCoversNamedModels(t *testing.T) {
	withPricePlanTestDB(t, t.Name(), map[string]int{"vendor-model": 7})
	planID := savePricePlanForTest(t, PricePlan{Name: "vendor only"},
		PricePlanRule{Kind: PricePlanRuleVendor, VendorID: 7, Pct: 25},
	)
	if _, ok := ResolvePlanDiscount(planID, ChannelLaneDirect, "vendor-model"); !ok {
		t.Fatal("vendor rule did not apply")
	}
	if got, ok := ResolvePlanDiscount(planID, ChannelLaneDirect, "unlisted-model"); ok {
		t.Fatalf("plan without a default discounted an unlisted model: %+v", got)
	}
}

func TestResolvePlanDiscountHonoursLaneScope(t *testing.T) {
	withPricePlanTestDB(t, t.Name(), nil)
	pct := 15.0
	direct := savePricePlanForTest(t, PricePlan{Name: "direct", DefaultPct: &pct, Lanes: PricePlanLanesDirect})
	cheap := savePricePlanForTest(t, PricePlan{Name: "cheap", DefaultPct: &pct, Lanes: PricePlanLanesAgent})
	all := savePricePlanForTest(t, PricePlan{Name: "all", DefaultPct: &pct, Lanes: PricePlanLanesAll})
	unset := savePricePlanForTest(t, PricePlan{Name: "unset", DefaultPct: &pct})

	cases := []struct {
		plan int
		lane string
		want bool
	}{
		{direct, ChannelLaneDirect, true},
		{direct, ChannelLaneAgent, false},
		{cheap, ChannelLaneDirect, false},
		{cheap, ChannelLaneAgent, true},
		{all, ChannelLaneDirect, true},
		{all, ChannelLaneAgent, true},
		// An unset scope saves as direct.
		{unset, ChannelLaneDirect, true},
		{unset, ChannelLaneAgent, false},
	}
	for _, tc := range cases {
		if _, ok := ResolvePlanDiscount(tc.plan, tc.lane, "any-model"); ok != tc.want {
			t.Fatalf("plan %d on lane %s: got %v, want %v", tc.plan, tc.lane, ok, tc.want)
		}
	}
}

func TestResolvePlanDiscountIgnoresInactiveExpiredAndUnknownPlans(t *testing.T) {
	withPricePlanTestDB(t, t.Name(), nil)
	pct := 20.0
	past := time.Now().Add(-time.Hour)
	longPast := time.Now().Add(-48 * time.Hour)
	future := time.Now().Add(time.Hour)
	farFuture := time.Now().Add(48 * time.Hour)

	inactive := savePricePlanForTest(t, PricePlan{Name: "inactive", DefaultPct: &pct})
	if err := coreDB.Model(&PricePlan{}).Where("id = ?", inactive).Update("active", false).Error; err != nil {
		t.Fatal(err)
	}
	InvalidatePricePlanCache()
	expired := savePricePlanForTest(t, PricePlan{Name: "expired", DefaultPct: &pct, StartsAt: &longPast, EndsAt: &past})
	notStarted := savePricePlanForTest(t, PricePlan{Name: "not started", DefaultPct: &pct, StartsAt: &future, EndsAt: &farFuture})
	live := savePricePlanForTest(t, PricePlan{Name: "live", DefaultPct: &pct, StartsAt: &past, EndsAt: &future})

	for name, planID := range map[string]int{"inactive": inactive, "expired": expired, "not started": notStarted, "none": 0, "unknown": 999_999} {
		if got, ok := ResolvePlanDiscount(planID, ChannelLaneDirect, "any-model"); ok {
			t.Fatalf("%s plan gave a discount: %+v", name, got)
		}
	}
	if _, ok := ResolvePlanDiscount(live, ChannelLaneDirect, "any-model"); !ok {
		t.Fatal("plan inside its window gave no discount")
	}
}

// The console sends vendor rules by the vendor's public id and reads them
// back the same way; saving what it read must round-trip.
func TestSavePricePlanResolvesVendorPublicIDsAndRoundTrips(t *testing.T) {
	db := setupProjectTestDB(t, false)
	if err := db.AutoMigrate(&PricePlan{}, &PricePlanRule{}, &Vendor{}); err != nil {
		t.Fatal(err)
	}
	vendor := Vendor{Name: "OpenAI"}
	if err := db.Create(&vendor).Error; err != nil {
		t.Fatal(err)
	}
	if vendor.PublicID == "" {
		t.Fatal("vendor has no public id")
	}
	pct := 10.0
	saved, err := SavePricePlan(PricePlan{Name: "A", DefaultPct: &pct, Active: true}, []PricePlanRule{
		{Kind: PricePlanRuleVendor, Vendor: vendor.PublicID, Pct: 40},
		{Kind: PricePlanRuleModel, ModelName: "gpt-6-sol", Pct: 30},
	})
	if err != nil {
		t.Fatal(err)
	}
	var vendorRule *PricePlanRule
	for i := range saved.Rules {
		if saved.Rules[i].Kind == PricePlanRuleVendor {
			vendorRule = &saved.Rules[i]
		}
	}
	if vendorRule == nil || vendorRule.VendorID != vendor.Id || vendorRule.Vendor != vendor.PublicID || vendorRule.VendorName != "OpenAI" {
		t.Fatalf("vendor rule = %+v", vendorRule)
	}
	// Save exactly what was read back, as the editor does.
	readBack := make([]PricePlanRule, len(saved.Rules))
	for i, rule := range saved.Rules {
		readBack[i] = PricePlanRule{Kind: rule.Kind, Vendor: rule.Vendor, ModelName: rule.ModelName, Pct: rule.Pct}
	}
	again, err := SavePricePlan(saved.PricePlan, readBack)
	if err != nil {
		t.Fatal(err)
	}
	if len(again.Rules) != 2 {
		t.Fatalf("rules after resave = %+v", again.Rules)
	}
	if _, err := SavePricePlan(saved.PricePlan, []PricePlanRule{{Kind: PricePlanRuleVendor, Vendor: "nope", Pct: 40}}); err == nil {
		t.Fatal("unknown vendor accepted")
	}
}

func TestPlanModelZeroExcludesVendorAndDefault(t *testing.T) {
	withPricePlanTestDB(t, t.Name(), map[string]int{"excluded-model": 7})
	pct := 30.0
	id := savePricePlanForTest(t, PricePlan{Name: "exceptions", DefaultPct: &pct}, PricePlanRule{Kind: PricePlanRuleVendor, VendorID: 7, Pct: 40}, PricePlanRule{Kind: PricePlanRuleModel, ModelName: "excluded-model", Pct: 0})
	discount, ok := ResolvePlanDiscount(id, "direct", "excluded-model")
	if !ok || !discount.Pct.IsZero() || discount.Rule != PricePlanRuleModel {
		t.Fatalf("exclusion fell through: %+v", discount)
	}
}
