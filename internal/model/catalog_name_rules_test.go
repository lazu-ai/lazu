package model

import "testing"

func TestCatalogNamesExcludePatternRules(t *testing.T) {
	db := withPricingTestDB(t, "catalog_concrete_names")
	rows := []Model{
		{ModelName: "concrete-model", NameRule: NameRuleExact},
		{ModelName: "prefix-", NameRule: NameRulePrefix},
		{ModelName: "-suffix", NameRule: NameRuleSuffix},
		{ModelName: "contains", NameRule: NameRuleContains},
		{ModelName: "retired-model", NameRule: NameRuleExact, LifecycleStatus: ModelLifecycleRetired},
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	names := ListCatalogModelNames()
	if len(names) != 1 || names[0] != "concrete-model" {
		t.Fatalf("not concrete choices: %v", names)
	}
}

func TestCatalogOwnersResolveRulesWithPricingPrecedence(t *testing.T) {
	db := withPricingTestDB(t, "catalog_rule_owners")
	if err := db.AutoMigrate(&Vendor{}); err != nil {
		t.Fatal(err)
	}
	vendors := []Vendor{{Name: "Exact"}, {Name: "Prefix"}, {Name: "Suffix"}, {Name: "Contains"}, {Name: "Inactive"}}
	if err := db.Create(&vendors).Error; err != nil {
		t.Fatal(err)
	}
	// Insert broad rules first; precedence must not depend on insertion order.
	rows := []Model{
		{ModelName: "middle", NameRule: NameRuleContains, VendorID: vendors[3].Id},
		{ModelName: "-end", NameRule: NameRuleSuffix, VendorID: vendors[2].Id},
		{ModelName: "start-", NameRule: NameRulePrefix, VendorID: vendors[1].Id},
		{ModelName: "start-middle-end", NameRule: NameRuleExact, VendorID: vendors[0].Id},
		{ModelName: "start-no-vendor", NameRule: NameRuleExact},
		{ModelName: "inactive-", NameRule: NameRulePrefix, VendorID: vendors[4].Id, LifecycleStatus: ModelLifecycleRetired},
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	names := []string{"start-middle-end", "start-another-end", "other-middle-end", "other-middle-other", "start-no-vendor", "inactive-model"}
	owners := CatalogModelOwners(names)
	want := []string{"Exact", "Prefix", "Suffix", "Contains", "", ""}
	for i, name := range names {
		if owners[name] != want[i] {
			t.Errorf("%s owner=%q want=%q", name, owners[name], want[i])
		}
	}
}
