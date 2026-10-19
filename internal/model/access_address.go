package model

import (
	"encoding/json"
	"errors"
	"net"
	"net/url"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/gin-gonic/gin"
)

// AccessAddressesOptionKey stores the extra API addresses a deployment
// answers on besides its default one, and which accounts may use each. The
// hostnames live only here, typed in by an administrator; no address is
// written into the code.
const AccessAddressesOptionKey = "AccessAddresses"

// Access address kinds. The kind decides the label and the one-line
// description customers see; administrators do not write that copy.
const (
	AccessAddressKindGlobal   = "global"
	AccessAddressKindCNDirect = "cn_direct"
)

// AccessAddress is one registered API address. UserIDs are the paying
// accounts allowed to call it: a team Key is checked against the project
// owner, so opening an address to a company opens it to all its members.
type AccessAddress struct {
	URL     string `json:"url"`
	Kind    string `json:"kind"`
	UserIDs []int  `json:"user_ids"`
}

type accessAddressIndex struct {
	list   []AccessAddress
	byHost map[string]map[int]struct{}
}

var accessAddresses atomic.Pointer[accessAddressIndex]

// accessAddressWriteMu serializes read-modify-write edits from the console.
var accessAddressWriteMu sync.Mutex

var (
	ErrAccessAddressInvalidURL  = errors.New("access address must be an http(s) origin with no path")
	ErrAccessAddressInvalidKind = errors.New("access address kind is not known")
	ErrAccessAddressDuplicate   = errors.New("access address is listed twice")
	ErrAccessAddressNotFound    = errors.New("access address is not registered")
)

// NormalizeAccessAddressURL reduces an address to scheme://host[:port] and
// rejects anything else, since the value is shown to customers to paste
// into their tools.
func NormalizeAccessAddressURL(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	trimmed = strings.TrimRight(trimmed, "/")
	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", ErrAccessAddressInvalidURL
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "https" && scheme != "http" {
		return "", ErrAccessAddressInvalidURL
	}
	if parsed.Path != "" && parsed.Path != "/" {
		return "", ErrAccessAddressInvalidURL
	}
	return scheme + "://" + strings.ToLower(parsed.Host), nil
}

func accessAddressHost(origin string) string {
	parsed, err := url.Parse(origin)
	if err != nil {
		return ""
	}
	return strings.ToLower(parsed.Hostname())
}

func requestHostname(host string) string {
	host = strings.TrimSpace(host)
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	return strings.ToLower(strings.TrimSuffix(strings.Trim(host, "[]"), "."))
}

// RequestHostOf is the hostname a request was sent to, normalized the way
// the address gate compares it. Empty outside a request.
func RequestHostOf(c *gin.Context) string {
	if c == nil || c.Request == nil {
		return ""
	}
	host := requestHostname(c.Request.Host)
	if len(host) > 128 {
		host = host[:128]
	}
	return host
}

// ValidateAccessAddresses normalizes a list and refuses unknown kinds,
// malformed addresses and a hostname listed twice. User ids are deduplicated
// and sorted.
func ValidateAccessAddresses(list []AccessAddress) ([]AccessAddress, error) {
	out := make([]AccessAddress, 0, len(list))
	seen := map[string]bool{}
	for _, item := range list {
		origin, err := NormalizeAccessAddressURL(item.URL)
		if err != nil {
			return nil, err
		}
		if item.Kind != AccessAddressKindGlobal && item.Kind != AccessAddressKindCNDirect {
			return nil, ErrAccessAddressInvalidKind
		}
		host := accessAddressHost(origin)
		if seen[host] {
			return nil, ErrAccessAddressDuplicate
		}
		seen[host] = true
		ids := make([]int, 0, len(item.UserIDs))
		used := map[int]bool{}
		for _, id := range item.UserIDs {
			if id > 0 && !used[id] {
				used[id] = true
				ids = append(ids, id)
			}
		}
		sort.Ints(ids)
		out = append(out, AccessAddress{URL: origin, Kind: item.Kind, UserIDs: ids})
	}
	return out, nil
}

// ParseAccessAddresses reads the stored option. An empty value is no extra
// addresses.
func ParseAccessAddresses(value string) ([]AccessAddress, error) {
	if strings.TrimSpace(value) == "" {
		return []AccessAddress{}, nil
	}
	var list []AccessAddress
	if err := json.Unmarshal([]byte(value), &list); err != nil {
		return nil, err
	}
	return ValidateAccessAddresses(list)
}

// EncodeAccessAddresses is the stored form of a validated list.
func EncodeAccessAddresses(list []AccessAddress) string {
	if len(list) == 0 {
		return ""
	}
	raw, _ := json.Marshal(list)
	return string(raw)
}

// applyAccessAddressesOption swaps the in-memory index. A value that does
// not parse leaves the previous index in place rather than opening or
// closing every address at once.
func applyAccessAddressesOption(value string) {
	list, err := ParseAccessAddresses(value)
	if err != nil {
		return
	}
	setAccessAddresses(list)
}

func setAccessAddresses(list []AccessAddress) {
	index := &accessAddressIndex{list: list, byHost: map[string]map[int]struct{}{}}
	for _, item := range list {
		users := map[int]struct{}{}
		for _, id := range item.UserIDs {
			users[id] = struct{}{}
		}
		index.byHost[accessAddressHost(item.URL)] = users
	}
	accessAddresses.Store(index)
}

// AccessAddresses returns a copy of the registered list.
func AccessAddresses() []AccessAddress {
	index := accessAddresses.Load()
	if index == nil {
		return []AccessAddress{}
	}
	out := make([]AccessAddress, len(index.list))
	for i, item := range index.list {
		out[i] = AccessAddress{URL: item.URL, Kind: item.Kind, UserIDs: append([]int(nil), item.UserIDs...)}
	}
	return out
}

// AccessAddressAllowed answers, for the Host a request arrived on, whether
// that host is a registered address and whether the paying account may use
// it. A host that is not registered — the default address included — is
// none of this check's business.
func AccessAddressAllowed(host string, payerUserID int) (registered bool, allowed bool) {
	index := accessAddresses.Load()
	if index == nil || len(index.byHost) == 0 {
		return false, true
	}
	users, ok := index.byHost[requestHostname(host)]
	if !ok {
		return false, true
	}
	_, allowed = users[payerUserID]
	return true, allowed
}

// AccessAddressesForUser lists the addresses an account may call, without
// the other accounts on each.
func AccessAddressesForUser(userID int) []AccessAddress {
	out := []AccessAddress{}
	for _, item := range AccessAddresses() {
		for _, id := range item.UserIDs {
			if id == userID {
				out = append(out, AccessAddress{URL: item.URL, Kind: item.Kind})
				break
			}
		}
	}
	return out
}

// IsAPIAccessAddress reports whether an address is one customers call
// directly (its host starts with "api."). A web domain that also forwards
// /v1 is registered only so the gate covers it, and is never shown.
func IsAPIAccessAddress(origin string) bool {
	return strings.HasPrefix(accessAddressHost(origin), "api.")
}

// SaveAccessAddresses validates and stores the whole list.
func SaveAccessAddresses(list []AccessAddress) ([]AccessAddress, error) {
	clean, err := ValidateAccessAddresses(list)
	if err != nil {
		return nil, err
	}
	if err := UpdateOption(AccessAddressesOptionKey, EncodeAccessAddresses(clean)); err != nil {
		return nil, err
	}
	return clean, nil
}

// ReplaceAccessAddresses stores a new set of addresses from the settings
// page, which edits addresses and kinds only: an address that stays keeps
// the accounts it was opened to.
func ReplaceAccessAddresses(next []AccessAddress) ([]AccessAddress, error) {
	accessAddressWriteMu.Lock()
	defer accessAddressWriteMu.Unlock()
	current := map[string][]int{}
	for _, item := range AccessAddresses() {
		current[accessAddressHost(item.URL)] = item.UserIDs
	}
	merged := make([]AccessAddress, 0, len(next))
	for _, item := range next {
		origin, err := NormalizeAccessAddressURL(item.URL)
		if err != nil {
			return nil, err
		}
		merged = append(merged, AccessAddress{URL: origin, Kind: item.Kind, UserIDs: current[accessAddressHost(origin)]})
	}
	return SaveAccessAddresses(merged)
}

// SetAccessAddressGrant opens or closes one address for one account.
func SetAccessAddressGrant(address string, userID int, enabled bool) ([]AccessAddress, error) {
	origin, err := NormalizeAccessAddressURL(address)
	if err != nil {
		return nil, err
	}
	accessAddressWriteMu.Lock()
	defer accessAddressWriteMu.Unlock()
	host := accessAddressHost(origin)
	list := AccessAddresses()
	found := false
	for i := range list {
		if accessAddressHost(list[i].URL) != host {
			continue
		}
		found = true
		ids := make([]int, 0, len(list[i].UserIDs)+1)
		for _, id := range list[i].UserIDs {
			if id != userID {
				ids = append(ids, id)
			}
		}
		if enabled {
			ids = append(ids, userID)
		}
		list[i].UserIDs = ids
	}
	if !found {
		return nil, ErrAccessAddressNotFound
	}
	return SaveAccessAddresses(list)
}

// ApplyAccessAddressesForTest loads a stored value without touching the
// options table, for tests in other packages.
func ApplyAccessAddressesForTest(value string) {
	if value == "" {
		setAccessAddresses(nil)
		return
	}
	applyAccessAddressesOption(value)
}
