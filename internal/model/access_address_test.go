package model

import "testing"

func TestNormalizeAccessAddressURL(t *testing.T) {
	cases := map[string]string{
		"https://API.Example.com/":    "https://api.example.com",
		" https://api.example.com ":   "https://api.example.com",
		"http://api.example.com:8443": "http://api.example.com:8443",
	}
	for in, want := range cases {
		got, err := NormalizeAccessAddressURL(in)
		if err != nil || got != want {
			t.Fatalf("%q: got %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "api.example.com", "ftp://api.example.com", "https://api.example.com/v1", "https://u:p@api.example.com", "https://api.example.com?x=1"} {
		if _, err := NormalizeAccessAddressURL(bad); err == nil {
			t.Fatalf("%q should be refused", bad)
		}
	}
}

func TestValidateAccessAddressesRefusesDuplicatesAndUnknownKinds(t *testing.T) {
	if _, err := ValidateAccessAddresses([]AccessAddress{{URL: "https://a.example.com", Kind: "vip"}}); err != ErrAccessAddressInvalidKind {
		t.Fatalf("unknown kind: %v", err)
	}
	_, err := ValidateAccessAddresses([]AccessAddress{
		{URL: "https://a.example.com", Kind: AccessAddressKindCNDirect},
		{URL: "https://A.example.com/", Kind: AccessAddressKindGlobal},
	})
	if err != ErrAccessAddressDuplicate {
		t.Fatalf("duplicate host: %v", err)
	}
	list, err := ValidateAccessAddresses([]AccessAddress{{URL: "https://a.example.com", Kind: AccessAddressKindCNDirect, UserIDs: []int{3, 1, 3, 0, -2}}})
	if err != nil || len(list[0].UserIDs) != 2 || list[0].UserIDs[0] != 1 || list[0].UserIDs[1] != 3 {
		t.Fatalf("user ids not cleaned: %+v %v", list, err)
	}
}

func TestAccessAddressAllowed(t *testing.T) {
	t.Cleanup(func() { setAccessAddresses(nil) })
	setAccessAddresses(nil)
	if registered, allowed := AccessAddressAllowed("anything.example.com", 1); registered || !allowed {
		t.Fatal("no registry must allow everything")
	}
	setAccessAddresses([]AccessAddress{{URL: "https://direct.example.com", Kind: AccessAddressKindCNDirect, UserIDs: []int{7}}})
	cases := []struct {
		host       string
		user       int
		registered bool
		allowed    bool
	}{
		{"direct.example.com", 7, true, true},
		{"DIRECT.example.com:443", 7, true, true},
		{"direct.example.com.", 7, true, true},
		{"direct.example.com", 8, true, false},
		{"api.example.com", 8, false, true},
	}
	for _, tc := range cases {
		registered, allowed := AccessAddressAllowed(tc.host, tc.user)
		if registered != tc.registered || allowed != tc.allowed {
			t.Fatalf("%s/%d: got %v,%v", tc.host, tc.user, registered, allowed)
		}
	}
	if got := AccessAddressesForUser(7); len(got) != 1 || got[0].UserIDs != nil {
		t.Fatalf("user view: %+v", got)
	}
	if got := AccessAddressesForUser(8); len(got) != 0 {
		t.Fatalf("ungranted user sees %+v", got)
	}
}

func TestApplyAccessAddressesOptionKeepsIndexOnBadValue(t *testing.T) {
	t.Cleanup(func() { setAccessAddresses(nil) })
	applyAccessAddressesOption(`[{"url":"https://direct.example.com","kind":"cn_direct","user_ids":[2]}]`)
	applyAccessAddressesOption(`not json`)
	if registered, allowed := AccessAddressAllowed("direct.example.com", 3); !registered || allowed {
		t.Fatal("a bad stored value must not open the address")
	}
	applyAccessAddressesOption("")
	if registered, _ := AccessAddressAllowed("direct.example.com", 3); registered {
		t.Fatal("an empty value clears the registry")
	}
}
