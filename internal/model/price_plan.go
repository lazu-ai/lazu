package model

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/lazu-ai/lazu/internal/types"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

// A price plan (专属折扣) is a negotiated discount a set of customers share.
// It replaces the old spend tiers, whose only real use was carrying these
// discounts. The discount is taken off the vendor's official list price and
// never stacks on a lane's own discount: a customer on "OpenAI 6折" pays 60%
// of OpenAI's list price whatever the lane sells at, and never more than the
// lane's standard price (see billing.ApplyPlanDiscount).
//
// One request is discounted by one rule, the most specific that applies:
// a model exception, then the model's vendor, then the plan default.
const (
	PricePlanLanesDirect = "direct"
	PricePlanLanesAgent  = "cheap" // the agent lane's slug; see setting.LaneSlugAgent
	PricePlanLanesAll    = "all"

	PricePlanRuleVendor = "vendor"
	PricePlanRuleModel  = "model"

	PricePlanRuleSourceDefault = "default"
)

type PricePlan struct {
	ID   int    `json:"id" gorm:"primaryKey"`
	Name string `json:"name" gorm:"type:varchar(64);not null"`
	Note string `json:"note" gorm:"type:varchar(255)"`
	// DefaultPct is the percentage off the official price for models no rule
	// names; nil leaves them at the standard price.
	DefaultPct *float64   `json:"default_pct" gorm:"type:decimal(10,4)"`
	Lanes      string     `json:"lanes" gorm:"type:varchar(16);not null;default:'direct'"`
	StartsAt   *time.Time `json:"starts_at"`
	EndsAt     *time.Time `json:"ends_at"`
	Active     bool       `json:"active" gorm:"not null;default:true"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
}

type PricePlanRule struct {
	ID       int    `json:"id" gorm:"primaryKey"`
	PlanID   int    `json:"plan_id" gorm:"index;not null"`
	Kind     string `json:"kind" gorm:"type:varchar(16);not null"`
	VendorID int    `json:"-" gorm:"not null;default:0"`
	// Vendor / VendorName are the vendor's public id and name for the API.
	Vendor     string  `json:"vendor_id,omitempty" gorm:"-"`
	VendorName string  `json:"vendor_name,omitempty" gorm:"-"`
	ModelName  string  `json:"model_name" gorm:"type:varchar(191);not null;default:''"`
	Pct        float64 `json:"pct" gorm:"type:decimal(10,4);not null"`
}

// PlanDiscount is the rule that priced a request, carried onto the receipt
// so a later edit to the plan does not rewrite what the customer was shown.
type PlanDiscount struct {
	PlanID   int
	PlanName string
	Pct      decimal.Decimal
	// Rule is "model", "vendor" or "default"; Subject names the model or
	// vendor it matched.
	Rule    string
	Subject string
}

func (plan PricePlan) coversLane(lane string) bool {
	switch plan.Lanes {
	case PricePlanLanesAll:
		return true
	case PricePlanLanesAgent:
		return NormalizeChannelLane(lane) == ChannelLaneAgent
	default:
		return NormalizeChannelLane(lane) == ChannelLaneDirect
	}
}

func (plan PricePlan) liveAt(now time.Time) bool {
	if !plan.Active {
		return false
	}
	if plan.StartsAt != nil && now.Before(*plan.StartsAt) {
		return false
	}
	if plan.EndsAt != nil && !now.Before(*plan.EndsAt) {
		return false
	}
	return true
}

type pricePlanEntry struct {
	plan    PricePlan
	vendors map[int]float64
	models  map[string]float64
}

type pricePlanCache struct {
	sync.RWMutex
	plans    map[int]*pricePlanEntry
	loadedAt time.Time
}

const pricePlanCacheTTL = 30 * time.Second

var pricePlans pricePlanCache

// pricePlanVendorOf names a model's vendor; tests swap it for a fixed map.
var pricePlanVendorOf = GetModelVendorMeta

// InvalidatePricePlanCache makes the next lookup reload plans from the CoreDB().
func InvalidatePricePlanCache() {
	pricePlans.Lock()
	pricePlans.loadedAt = time.Time{}
	pricePlans.Unlock()
}

func loadPricePlanEntries() (map[int]*pricePlanEntry, error) {
	if CoreDB() == nil || !CoreDB().Migrator().HasTable(&PricePlan{}) {
		return map[int]*pricePlanEntry{}, nil
	}
	var plans []PricePlan
	if err := CoreDB().Find(&plans).Error; err != nil {
		return nil, err
	}
	var rules []PricePlanRule
	if err := CoreDB().Find(&rules).Error; err != nil {
		return nil, err
	}
	entries := make(map[int]*pricePlanEntry, len(plans))
	for _, plan := range plans {
		entries[plan.ID] = &pricePlanEntry{plan: plan, vendors: map[int]float64{}, models: map[string]float64{}}
	}
	for _, rule := range rules {
		entry, ok := entries[rule.PlanID]
		if !ok {
			continue
		}
		switch rule.Kind {
		case PricePlanRuleVendor:
			entry.vendors[rule.VendorID] = rule.Pct
		case PricePlanRuleModel:
			entry.models[NormalizePricingModelName(rule.ModelName)] = rule.Pct
		}
	}
	return entries, nil
}

func getPricePlanEntry(planID int) (*pricePlanEntry, bool) {
	if planID <= 0 {
		return nil, false
	}
	pricePlans.RLock()
	fresh := !pricePlans.loadedAt.IsZero() && time.Since(pricePlans.loadedAt) < pricePlanCacheTTL
	entry, ok := pricePlans.plans[planID]
	pricePlans.RUnlock()
	if fresh {
		return entry, ok
	}
	entries, err := loadPricePlanEntries()
	if err != nil {
		// Keep billing on the last good copy rather than dropping discounts.
		pricePlans.RLock()
		entry, ok = pricePlans.plans[planID]
		pricePlans.RUnlock()
		return entry, ok
	}
	pricePlans.Lock()
	pricePlans.plans = entries
	pricePlans.loadedAt = time.Now()
	pricePlans.Unlock()
	entry, ok = entries[planID]
	return entry, ok
}

// ResolvePlanDiscount picks the one rule of the plan that prices modelName
// on lane. ok is false when the plan is missing, not live, does not cover
// the lane, or has nothing for this model.
func ResolvePlanDiscount(planID int, lane, modelName string) (PlanDiscount, bool) {
	entry, ok := getPricePlanEntry(planID)
	if !ok || !entry.plan.liveAt(time.Now()) || !entry.plan.coversLane(lane) {
		return PlanDiscount{}, false
	}
	result := PlanDiscount{PlanID: entry.plan.ID, PlanName: entry.plan.Name}
	normalized := NormalizePricingModelName(strings.TrimSpace(modelName))
	if pct, ok := entry.models[normalized]; ok {
		result.Pct, result.Rule, result.Subject = decimal.NewFromFloat(pct), PricePlanRuleModel, normalized
		return result, true
	}
	if vendorID, vendorName := pricePlanVendorOf(normalized); vendorID > 0 {
		if pct, ok := entry.vendors[vendorID]; ok {
			result.Pct, result.Rule, result.Subject = decimal.NewFromFloat(pct), PricePlanRuleVendor, vendorName
			return result, true
		}
	}
	if entry.plan.DefaultPct != nil {
		result.Pct, result.Rule = decimal.NewFromFloat(*entry.plan.DefaultPct), PricePlanRuleSourceDefault
		return result, true
	}
	return PlanDiscount{}, false
}

// GetPricePlan returns a plan by id, live or not.
func GetPricePlan(planID int) (PricePlan, bool) {
	entry, ok := getPricePlanEntry(planID)
	if !ok {
		return PricePlan{}, false
	}
	return entry.plan, true
}

type PricePlanDetail struct {
	PricePlan
	Rules       []PricePlanRule `json:"rules"`
	MemberCount int64           `json:"member_count"`
}

func ListPricePlans() ([]PricePlanDetail, error) {
	var plans []PricePlan
	if err := CoreDB().Order("id ASC").Find(&plans).Error; err != nil {
		return nil, err
	}
	var rules []PricePlanRule
	if err := CoreDB().Order("id ASC").Find(&rules).Error; err != nil {
		return nil, err
	}
	type memberCount struct {
		PricePlanID int
		N           int64
	}
	var counts []memberCount
	if err := CoreDB().Model(&User{}).Select("price_plan_id, count(*) AS n").
		Where("price_plan_id > 0").Group("price_plan_id").Scan(&counts).Error; err != nil {
		return nil, err
	}
	fillPricePlanRuleVendors(rules)
	byPlan := make(map[int][]PricePlanRule, len(plans))
	for _, rule := range rules {
		byPlan[rule.PlanID] = append(byPlan[rule.PlanID], rule)
	}
	members := make(map[int]int64, len(counts))
	for _, count := range counts {
		members[count.PricePlanID] = count.N
	}
	result := make([]PricePlanDetail, 0, len(plans))
	for _, plan := range plans {
		planRules := byPlan[plan.ID]
		if planRules == nil {
			planRules = []PricePlanRule{}
		}
		result = append(result, PricePlanDetail{PricePlan: plan, Rules: planRules, MemberCount: members[plan.ID]})
	}
	return result, nil
}

func GetPricePlanDetail(planID int) (PricePlanDetail, error) {
	var plan PricePlan
	if err := CoreDB().First(&plan, planID).Error; err != nil {
		return PricePlanDetail{}, err
	}
	var rules []PricePlanRule
	if err := CoreDB().Where("plan_id = ?", planID).Order("id ASC").Find(&rules).Error; err != nil {
		return PricePlanDetail{}, err
	}
	fillPricePlanRuleVendors(rules)
	var members int64
	if err := CoreDB().Model(&User{}).Where("price_plan_id = ?", planID).Count(&members).Error; err != nil {
		return PricePlanDetail{}, err
	}
	return PricePlanDetail{PricePlan: plan, Rules: rules, MemberCount: members}, nil
}

func validatePct(pct float64) error {
	if pct <= 0 || pct >= 100 {
		return fmt.Errorf("discount must be between 0 and 100 percent, got %v", pct)
	}
	return nil
}

func normalizePricePlan(plan *PricePlan, rules []PricePlanRule) ([]PricePlanRule, error) {
	plan.Name = strings.TrimSpace(plan.Name)
	plan.Note = strings.TrimSpace(plan.Note)
	if plan.Name == "" {
		return nil, errors.New("plan name is required")
	}
	switch plan.Lanes {
	case PricePlanLanesDirect, PricePlanLanesAgent, PricePlanLanesAll:
	case "":
		plan.Lanes = PricePlanLanesDirect
	default:
		return nil, fmt.Errorf("unknown lanes %q", plan.Lanes)
	}
	if plan.DefaultPct != nil {
		if *plan.DefaultPct == 0 {
			plan.DefaultPct = nil
		} else if err := validatePct(*plan.DefaultPct); err != nil {
			return nil, err
		}
	}
	if plan.StartsAt != nil && plan.EndsAt != nil && !plan.EndsAt.After(*plan.StartsAt) {
		return nil, errors.New("plan must end after it starts")
	}
	seenVendors := map[int]bool{}
	seenModels := map[string]bool{}
	out := make([]PricePlanRule, 0, len(rules))
	for _, rule := range rules {
		if err := validatePct(rule.Pct); err != nil && !(rule.Kind == PricePlanRuleModel && rule.Pct == 0) {
			return nil, err
		}
		rule.ID = 0
		switch rule.Kind {
		case PricePlanRuleVendor:
			if rule.VendorID <= 0 && strings.TrimSpace(rule.Vendor) != "" {
				vendorID, err := ResolvePublicIntID("vendors", rule.Vendor)
				if err != nil {
					return nil, fmt.Errorf("unknown vendor %q", rule.Vendor)
				}
				rule.VendorID = vendorID
			}
			if rule.VendorID <= 0 {
				return nil, errors.New("vendor rule needs a vendor")
			}
			if seenVendors[rule.VendorID] {
				return nil, fmt.Errorf("vendor %d has two rules", rule.VendorID)
			}
			seenVendors[rule.VendorID] = true
			rule.ModelName = ""
		case PricePlanRuleModel:
			rule.ModelName = NormalizePricingModelName(strings.TrimSpace(rule.ModelName))
			if rule.ModelName == "" {
				return nil, errors.New("model rule needs a model")
			}
			if seenModels[rule.ModelName] {
				return nil, fmt.Errorf("model %s has two rules", rule.ModelName)
			}
			seenModels[rule.ModelName] = true
			rule.VendorID = 0
		default:
			return nil, fmt.Errorf("unknown rule kind %q", rule.Kind)
		}
		out = append(out, rule)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Kind > out[j].Kind })
	return out, nil
}

// SavePricePlan creates (ID 0) or replaces a plan and its whole rule set.
func SavePricePlan(plan PricePlan, rules []PricePlanRule) (PricePlanDetail, error) {
	rules, err := normalizePricePlan(&plan, rules)
	if err != nil {
		return PricePlanDetail{}, err
	}
	err = CoreDB().Transaction(func(tx *gorm.DB) error {
		if plan.ID == 0 {
			active := plan.Active
			if err := tx.Create(&plan).Error; err != nil {
				return err
			}
			// Active has a CoreDB() default of true, which a zero-value false on
			// Create would silently take; write it explicitly.
			if !active {
				if err := tx.Model(&plan).Update("active", false).Error; err != nil {
					return err
				}
				plan.Active = false
			}
		} else {
			var existing PricePlan
			if err := tx.First(&existing, plan.ID).Error; err != nil {
				return err
			}
			plan.CreatedAt = existing.CreatedAt
			if err := tx.Select("*").Omit("created_at").Save(&plan).Error; err != nil {
				return err
			}
			if err := tx.Where("plan_id = ?", plan.ID).Delete(&PricePlanRule{}).Error; err != nil {
				return err
			}
		}
		for i := range rules {
			rules[i].PlanID = plan.ID
		}
		if len(rules) > 0 {
			return tx.Create(&rules).Error
		}
		return nil
	})
	if err != nil {
		return PricePlanDetail{}, err
	}
	InvalidatePricePlanCache()
	return GetPricePlanDetail(plan.ID)
}

// DeletePricePlan removes a plan; its members fall back to standard prices.
func DeletePricePlan(planID int) error {
	var memberIDs []int
	err := CoreDB().Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&User{}).Where("price_plan_id = ?", planID).Pluck("id", &memberIDs).Error; err != nil {
			return err
		}
		if err := tx.Model(&User{}).Where("price_plan_id = ?", planID).Update("price_plan_id", 0).Error; err != nil {
			return err
		}
		if err := tx.Where("plan_id = ?", planID).Delete(&PricePlanRule{}).Error; err != nil {
			return err
		}
		return tx.Delete(&PricePlan{}, planID).Error
	})
	if err != nil {
		return err
	}
	for _, id := range memberIDs {
		_ = invalidateUserCache(id)
	}
	InvalidatePricePlanCache()
	return nil
}

type PricePlanMember struct {
	ID          int    `json:"id"`
	PublicID    string `json:"public_id"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	Email       string `json:"email"`
}

func ListPricePlanMembers(planID int) ([]PricePlanMember, error) {
	var members []PricePlanMember
	err := CoreDB().Model(&User{}).Select("id, public_id, username, display_name, email").
		Where("price_plan_id = ?", planID).Order("id ASC").Scan(&members).Error
	return members, err
}

// SetUserPricePlan moves a user onto a plan (0 = standard prices). A user
// sits on one plan at most, so joining a plan leaves the previous one.
func SetUserPricePlan(userID, planID int) error {
	if planID > 0 {
		var plan PricePlan
		if err := CoreDB().Select("id").First(&plan, planID).Error; err != nil {
			return err
		}
	}
	if err := CoreDB().Model(&User{}).Where("id = ?", userID).Update("price_plan_id", planID).Error; err != nil {
		return err
	}
	return invalidateUserCache(userID)
}

// RemoveUserPricePlan only removes membership from the plan the operator
// opened. A stale editor must not clear a newer assignment to another plan.
func RemoveUserPricePlan(userID, planID int) error {
	if planID <= 0 {
		return types.Err(types.ErrorCodeVersionConflict)
	}
	result := CoreDB().Model(&User{}).Where("id = ? AND price_plan_id = ?", userID, planID).
		Update("price_plan_id", 0)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return types.Err(types.ErrorCodeVersionConflict)
	}
	return invalidateUserCache(userID)
}

func fillPricePlanRuleVendors(rules []PricePlanRule) {
	if len(rules) == 0 {
		return
	}
	var vendors []Vendor
	if err := CoreDB().Select("id", "public_id", "name").Find(&vendors).Error; err != nil {
		return
	}
	byID := make(map[int]Vendor, len(vendors))
	for _, vendor := range vendors {
		byID[vendor.Id] = vendor
	}
	for i := range rules {
		if vendor, ok := byID[rules[i].VendorID]; ok && rules[i].VendorID > 0 {
			rules[i].Vendor = vendor.PublicID
			rules[i].VendorName = vendor.Name
		}
	}
}
