package system_setting

import (
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/setting/config"
)

type FetchSetting struct {
	EnableSSRFProtection   bool     `json:"enable_ssrf_protection"` // 是否启用SSRF防护
	AllowPrivateIp         bool     `json:"allow_private_ip"`
	DomainFilterMode       bool     `json:"domain_filter_mode"`         // 域名过滤模式，true: 白名单模式，false: 黑名单模式
	IpFilterMode           bool     `json:"ip_filter_mode"`             // IP过滤模式，true: 白名单模式，false: 黑名单模式
	DomainList             []string `json:"domain_list"`                // domain format, e.g. example.com, *.example.com
	IpList                 []string `json:"ip_list"`                    // CIDR format
	AllowedPorts           []string `json:"allowed_ports"`              // port range format, e.g. 80, 443, 8000-9000
	ApplyIPFilterForDomain bool     `json:"apply_ip_filter_for_domain"` // 是否将可选 IP allow/deny list 应用于域名解析结果
	// PrivateAllowList names the private sources the operator fetches on
	// purpose (an internal object store, a LAN image host): hostnames
	// (exact or *.suffix), single IPs or CIDRs. Everything else private is
	// still refused; ports and domain lists still apply. Empty by default.
	PrivateAllowList []string `json:"private_allow_list"`
}

var defaultFetchSetting = FetchSetting{
	EnableSSRFProtection:   true, // 默认开启SSRF防护
	AllowPrivateIp:         false,
	DomainFilterMode:       false,
	IpFilterMode:           false,
	DomainList:             []string{},
	IpList:                 []string{},
	AllowedPorts:           []string{"80", "443", "8080", "8443"},
	ApplyIPFilterForDomain: false,
	PrivateAllowList:       []string{},
}

func init() {
	// 注册到全局配置管理器
	config.GlobalConfig.Register("fetch_setting", &defaultFetchSetting)
}

func GetFetchSetting() *FetchSetting {
	return &defaultFetchSetting
}

// Protection builds the fetch policy from the setting. It returns nil when
// SSRF protection is switched off, so callers dial unguarded only on that
// explicit operator choice.
func (s *FetchSetting) Protection() (*common.SSRFProtection, error) {
	if !s.EnableSSRFProtection {
		return nil, nil
	}
	return common.NewSSRFProtectionFromFetchSetting(
		s.AllowPrivateIp,
		s.DomainFilterMode,
		s.IpFilterMode,
		s.DomainList,
		s.IpList,
		s.AllowedPorts,
		s.ApplyIPFilterForDomain,
		s.PrivateAllowList,
	)
}

// ValidateURL applies the fetch policy to a URL before any request is made.
// A refusal is a *common.URLRejectedError.
func (s *FetchSetting) ValidateURL(rawURL string) error {
	protection, err := s.Protection()
	if err != nil {
		return err
	}
	if protection == nil {
		return nil
	}
	return protection.ValidateURL(rawURL)
}
