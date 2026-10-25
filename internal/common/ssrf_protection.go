package common

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"syscall"
)

// SSRFProtection SSRF防护配置
type SSRFProtection struct {
	AllowPrivateIp         bool
	DomainFilterMode       bool     // true: 白名单, false: 黑名单
	DomainList             []string // domain format, e.g. example.com, *.example.com
	IpFilterMode           bool     // true: 白名单, false: 黑名单
	IpList                 []string // CIDR or single IP
	AllowedPorts           []int    // 允许的端口范围
	ApplyIPFilterForDomain bool     // Apply optional IP allow/deny lists to resolved domain IPs.
	// PrivateAllowList names the private sources an operator fetches on
	// purpose: hostnames (exact or *.suffix), single IPs or CIDRs. A listed
	// host may resolve to a private address; a listed address may be dialled.
	// Nothing else about the policy (ports, domain lists) is relaxed.
	PrivateAllowList []string
}

var lookupIP = net.LookupIP

// ContextDialer is the subset of net.Dialer used by the SSRF-protected dialer.
type ContextDialer interface {
	DialContext(ctx context.Context, network, address string) (net.Conn, error)
}

// RejectReason classifies why the fetch policy refused a URL. It is stable
// so callers can choose a client-facing sentence without parsing text.
type RejectReason string

const (
	RejectInvalidURL        RejectReason = "invalid_url"
	RejectUnsupportedScheme RejectReason = "unsupported_scheme"
	RejectPortNotAllowed    RejectReason = "port_not_allowed"
	RejectDomainFiltered    RejectReason = "domain_filtered"
	RejectPrivateAddress    RejectReason = "private_address"
	RejectIPFiltered        RejectReason = "ip_filtered"
	RejectDNSFailed         RejectReason = "dns_failed"
)

// URLRejectedError is returned by ValidateURL, DialContext and DialControl
// when the fetch policy refuses a target. Detail is for logs: it names the
// host the caller supplied but never an address an internal name resolved
// to. PublicReason is the sentence a customer may see.
type URLRejectedError struct {
	Reason RejectReason
	Detail string
}

func (e *URLRejectedError) Error() string {
	return e.Detail
}

// PublicReason is safe to return to an API client: it names the policy that
// refused the URL and nothing about the operator's network.
func (e *URLRejectedError) PublicReason() string {
	switch e.Reason {
	case RejectUnsupportedScheme:
		return "the media URL must use http or https"
	case RejectPortNotAllowed:
		return "the media URL uses a port the gateway does not fetch from"
	case RejectDomainFiltered:
		return "the media URL host is not permitted by the gateway's fetch policy"
	case RejectPrivateAddress:
		return "the media URL points to a private or internal network address, which the gateway does not fetch"
	case RejectIPFiltered:
		return "the media URL address is not permitted by the gateway's fetch policy"
	case RejectDNSFailed:
		return "the media URL host could not be resolved"
	default:
		return "the media URL is not valid"
	}
}

func rejectf(reason RejectReason, format string, args ...any) *URLRejectedError {
	return &URLRejectedError{Reason: reason, Detail: fmt.Sprintf(format, args...)}
}

// AsURLRejected unwraps err (through *url.Error and fmt wrappers) and returns
// the policy rejection inside it, or nil when the failure was something else.
func AsURLRejected(err error) *URLRejectedError {
	var rejected *URLRejectedError
	if errors.As(err, &rejected) {
		return rejected
	}
	return nil
}

// privateIPv4Nets IPv4 私有/保留/特殊用途网段
var privateIPv4Nets = []net.IPNet{
	{IP: net.IPv4(0, 0, 0, 0), Mask: net.CIDRMask(8, 32)},
	{IP: net.IPv4(10, 0, 0, 0), Mask: net.CIDRMask(8, 32)},
	{IP: net.IPv4(100, 64, 0, 0), Mask: net.CIDRMask(10, 32)},
	{IP: net.IPv4(127, 0, 0, 0), Mask: net.CIDRMask(8, 32)},
	{IP: net.IPv4(169, 254, 0, 0), Mask: net.CIDRMask(16, 32)},
	{IP: net.IPv4(172, 16, 0, 0), Mask: net.CIDRMask(12, 32)},
	{IP: net.IPv4(192, 0, 0, 0), Mask: net.CIDRMask(24, 32)},
	{IP: net.IPv4(192, 0, 2, 0), Mask: net.CIDRMask(24, 32)},
	{IP: net.IPv4(192, 168, 0, 0), Mask: net.CIDRMask(16, 32)},
	{IP: net.IPv4(198, 18, 0, 0), Mask: net.CIDRMask(15, 32)},
	{IP: net.IPv4(198, 51, 100, 0), Mask: net.CIDRMask(24, 32)},
	{IP: net.IPv4(203, 0, 113, 0), Mask: net.CIDRMask(24, 32)},
	{IP: net.IPv4(224, 0, 0, 0), Mask: net.CIDRMask(4, 32)},
	{IP: net.IPv4(240, 0, 0, 0), Mask: net.CIDRMask(4, 32)},
	{IP: net.IPv4(255, 255, 255, 255), Mask: net.CIDRMask(32, 32)},
}

// privateIPv6Nets IPv6 私有/保留/特殊用途网段
var privateIPv6Nets = func() []net.IPNet {
	cidrs := []string{
		"::/128",
		"::1/128",
		"::ffff:0:0/96",
		"64:ff9b::/96",
		"100::/64",
		"2001::/23",
		"2001:db8::/32",
		"fc00::/7",
		"fe80::/10",
		"ff00::/8",
	}
	nets := make([]net.IPNet, 0, len(cidrs))
	for _, cidr := range cidrs {
		if _, network, err := net.ParseCIDR(cidr); err == nil && network != nil {
			nets = append(nets, *network)
		}
	}
	return nets
}()

// isPrivateIP 检查IP是否为私有/保留/特殊用途地址
func isPrivateIP(ip net.IP) bool {
	if ip == nil || ip.IsUnspecified() {
		return true
	}
	if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return true
	}
	if ip.IsInterfaceLocalMulticast() {
		return true
	}

	// An IPv4-mapped IPv6 address (::ffff:a.b.c.d) is the IPv4 address it
	// wraps: To4 unwraps it so the IPv4 table applies.
	if v4 := ip.To4(); v4 != nil {
		for _, privateNet := range privateIPv4Nets {
			if privateNet.Contains(v4) {
				return true
			}
		}
		return false
	}

	for _, privateNet := range privateIPv6Nets {
		if privateNet.Contains(ip) {
			return true
		}
	}

	return ip.IsPrivate()
}

// parsePortRanges 解析端口范围配置
// 支持格式: "80", "443", "8000-9000"
func parsePortRanges(portConfigs []string) ([]int, error) {
	var ports []int

	for _, config := range portConfigs {
		config = strings.TrimSpace(config)
		if config == "" {
			continue
		}

		if strings.Contains(config, "-") {
			// 处理端口范围 "8000-9000"
			parts := strings.Split(config, "-")
			if len(parts) != 2 {
				return nil, fmt.Errorf("invalid port range format: %s", config)
			}

			startPort, err := strconv.Atoi(strings.TrimSpace(parts[0]))
			if err != nil {
				return nil, fmt.Errorf("invalid start port in range %s: %v", config, err)
			}

			endPort, err := strconv.Atoi(strings.TrimSpace(parts[1]))
			if err != nil {
				return nil, fmt.Errorf("invalid end port in range %s: %v", config, err)
			}

			if startPort > endPort {
				return nil, fmt.Errorf("invalid port range %s: start port cannot be greater than end port", config)
			}

			if startPort < 1 || startPort > 65535 || endPort < 1 || endPort > 65535 {
				return nil, fmt.Errorf("port range %s contains invalid port numbers (must be 1-65535)", config)
			}

			// 添加范围内的所有端口
			for port := startPort; port <= endPort; port++ {
				ports = append(ports, port)
			}
		} else {
			// 处理单个端口 "80"
			port, err := strconv.Atoi(config)
			if err != nil {
				return nil, fmt.Errorf("invalid port number: %s", config)
			}

			if port < 1 || port > 65535 {
				return nil, fmt.Errorf("invalid port number %d (must be 1-65535)", port)
			}

			ports = append(ports, port)
		}
	}

	return ports, nil
}

// isAllowedPort 检查端口是否被允许
func (p *SSRFProtection) isAllowedPort(port int) bool {
	if len(p.AllowedPorts) == 0 {
		return true // 如果没有配置端口限制，则允许所有端口
	}

	for _, allowedPort := range p.AllowedPorts {
		if port == allowedPort {
			return true
		}
	}
	return false
}

// isDomainListed 检查域名是否在列表中 (exact or *.suffix)
func isDomainListed(domain string, list []string) bool {
	if len(list) == 0 {
		return false
	}

	domain = strings.ToLower(domain)
	for _, item := range list {
		item = strings.ToLower(strings.TrimSpace(item))
		if item == "" {
			continue
		}
		// 精确匹配
		if domain == item {
			return true
		}
		// 通配符匹配 (*.example.com)
		if strings.HasPrefix(item, "*.") {
			suffix := strings.TrimPrefix(item, "*.")
			if strings.HasSuffix(domain, "."+suffix) || domain == suffix {
				return true
			}
		}
	}
	return false
}

func (p *SSRFProtection) isDomainAllowed(domain string) bool {
	listed := isDomainListed(domain, p.DomainList)
	if p.DomainFilterMode { // 白名单
		return listed
	}
	// 黑名单
	return !listed
}

func isIPListed(ip net.IP, list []string) bool {
	if len(list) == 0 {
		return false
	}

	for _, entry := range list {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		_, network, err := net.ParseCIDR(entry)
		if err != nil {
			// 尝试作为单个IP处理
			if listedIP := net.ParseIP(entry); listedIP != nil {
				if ip.Equal(listedIP) {
					return true
				}
			}
			continue
		}

		if network.Contains(ip) {
			return true
		}
	}
	return false
}

// privateAllowed reports whether the operator deliberately allowed this
// private target: by the host name the caller used, or by the address.
// host is the URL host (a name or an IP literal); ip is the address being
// judged (a literal or a resolved one). Either match is enough.
func (p *SSRFProtection) privateAllowed(host string, ip net.IP) bool {
	if len(p.PrivateAllowList) == 0 {
		return false
	}
	if host != "" && net.ParseIP(host) == nil && isDomainListed(host, p.PrivateAllowList) {
		return true
	}
	return ip != nil && isIPListed(ip, p.PrivateAllowList)
}

// privateBlocked is the single private-address decision: the address is
// private, the operator did not allow private addresses globally, and did
// not allow this host or address specifically.
func (p *SSRFProtection) privateBlocked(host string, ip net.IP) bool {
	return isPrivateIP(ip) && !p.AllowPrivateIp && !p.privateAllowed(host, ip)
}

// IsIPAccessAllowed 检查IP是否允许访问 (literal IP target)
func (p *SSRFProtection) IsIPAccessAllowed(ip net.IP) bool {
	if p.privateBlocked("", ip) {
		return false
	}

	return p.isOptionalIPListAllowed(ip)
}

func (p *SSRFProtection) isDomainResolvedIPAllowed(host string, ip net.IP) bool {
	if p.privateBlocked(host, ip) {
		return false
	}
	if !p.ApplyIPFilterForDomain {
		return true
	}
	return p.isOptionalIPListAllowed(ip)
}

func (p *SSRFProtection) isOptionalIPListAllowed(ip net.IP) bool {
	listed := isIPListed(ip, p.IpList)
	if p.IpFilterMode { // 白名单
		return listed
	}
	// 黑名单
	return !listed
}

// ipAccessError names the host the caller supplied. When the host is a name
// it never includes the address it resolved to: that address is a fact
// about the operator's network, not about the request.
func (p *SSRFProtection) ipAccessError(host string, ip net.IP, domainResolved bool) *URLRejectedError {
	if p.privateBlocked(host, ip) {
		if domainResolved {
			return rejectf(RejectPrivateAddress, "private address not allowed: %s resolves to a private or internal address", host)
		}
		return rejectf(RejectPrivateAddress, "private address not allowed: %s", ip.String())
	}
	if p.IpFilterMode {
		if domainResolved {
			return rejectf(RejectIPFiltered, "ip not in whitelist: %s resolves to an address outside the whitelist", host)
		}
		return rejectf(RejectIPFiltered, "ip not in whitelist: %s", ip.String())
	}
	if domainResolved {
		return rejectf(RejectIPFiltered, "ip in blacklist: %s resolves to a blacklisted address", host)
	}
	return rejectf(RejectIPFiltered, "ip in blacklist: %s", ip.String())
}

func (p *SSRFProtection) domainError(host string) *URLRejectedError {
	if p.DomainFilterMode {
		return rejectf(RejectDomainFiltered, "domain not in whitelist: %s", host)
	}
	return rejectf(RejectDomainFiltered, "domain in blacklist: %s", host)
}

// ValidateURL 验证URL是否安全. A refusal is a *URLRejectedError.
func (p *SSRFProtection) ValidateURL(urlStr string) error {
	// 解析URL
	u, err := url.Parse(urlStr)
	if err != nil {
		return rejectf(RejectInvalidURL, "invalid URL format: %v", err)
	}

	// 只允许HTTP/HTTPS协议
	if u.Scheme != "http" && u.Scheme != "https" {
		return rejectf(RejectUnsupportedScheme, "unsupported protocol: %s (only http/https allowed)", u.Scheme)
	}

	// 解析主机和端口
	host, portStr, err := net.SplitHostPort(u.Host)
	if err != nil {
		// 没有端口，使用默认端口
		host = u.Hostname()
		if u.Scheme == "https" {
			portStr = "443"
		} else {
			portStr = "80"
		}
	}
	if host == "" {
		return rejectf(RejectInvalidURL, "invalid URL: missing host")
	}

	// 验证端口
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return rejectf(RejectInvalidURL, "invalid port: %s", portStr)
	}

	if !p.isAllowedPort(port) {
		return rejectf(RejectPortNotAllowed, "port %d is not allowed", port)
	}

	// 如果 host 是 IP，则跳过域名检查
	if ip := net.ParseIP(host); ip != nil {
		if !p.IsIPAccessAllowed(ip) {
			return p.ipAccessError(host, ip, false)
		}
		return nil
	}

	// 先进行域名过滤
	if !p.isDomainAllowed(host) {
		return p.domainError(host)
	}

	// 解析域名对应IP并检查
	ips, err := lookupIP(host)
	if err != nil {
		return rejectf(RejectDNSFailed, "DNS resolution failed for %s: %v", host, err)
	}
	if len(ips) == 0 {
		return rejectf(RejectDNSFailed, "DNS resolution failed for %s: no addresses", host)
	}
	for _, ip := range ips {
		if !p.isDomainResolvedIPAllowed(host, ip) {
			return p.ipAccessError(host, ip, true)
		}
	}
	return nil
}

// approvedDialKey carries the addresses DialContext validated to the
// dialer's control hook, which refuses any other address.
type approvedDialKey struct{}

type approvedDialAddrs []net.IP

func withApprovedDialAddrs(ctx context.Context, ips []net.IP) context.Context {
	return context.WithValue(ctx, approvedDialKey{}, approvedDialAddrs(ips))
}

// DialControl is a net.Dialer.ControlContext hook. It runs after the
// operating system has chosen the address to connect to and before the
// connection is made, and refuses any address DialContext did not validate
// in this same dial. A dialer carrying it cannot reach an unchecked target
// even if a caller bypasses DialContext.
func DialControl(ctx context.Context, network, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return rejectf(RejectInvalidURL, "invalid dial address: %s", address)
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return rejectf(RejectInvalidURL, "dial to unresolved address refused: %s", host)
	}
	approved, _ := ctx.Value(approvedDialKey{}).(approvedDialAddrs)
	for _, candidate := range approved {
		if candidate.Equal(ip) {
			return nil
		}
	}
	if isPrivateIP(ip) {
		return rejectf(RejectPrivateAddress, "dial to unvalidated private address refused")
	}
	return rejectf(RejectIPFiltered, "dial to unvalidated address refused")
}

// DialContext resolves domains once, validates the selected IPs, and dials the
// validated IP address directly so DNS rebinding cannot bypass ValidateURL.
// The validated addresses travel in ctx for DialControl.
func (p *SSRFProtection) DialContext(ctx context.Context, network, address string, dialer ContextDialer) (net.Conn, error) {
	if dialer == nil {
		dialer = &net.Dialer{ControlContext: DialControl}
	}
	host, portStr, err := net.SplitHostPort(address)
	if err != nil {
		return nil, rejectf(RejectInvalidURL, "invalid dial address: %s", address)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return nil, rejectf(RejectInvalidURL, "invalid port: %s", portStr)
	}
	if !p.isAllowedPort(port) {
		return nil, rejectf(RejectPortNotAllowed, "port %d is not allowed", port)
	}

	if ip := net.ParseIP(host); ip != nil {
		if !p.IsIPAccessAllowed(ip) {
			return nil, p.ipAccessError(host, ip, false)
		}
		return dialer.DialContext(withApprovedDialAddrs(ctx, []net.IP{ip}), network, net.JoinHostPort(ip.String(), portStr))
	}

	if !p.isDomainAllowed(host) {
		return nil, p.domainError(host)
	}

	ips, err := lookupIP(host)
	if err != nil {
		return nil, rejectf(RejectDNSFailed, "DNS resolution failed for %s: %v", host, err)
	}
	for _, ip := range ips {
		if !p.isDomainResolvedIPAllowed(host, ip) {
			return nil, p.ipAccessError(host, ip, true)
		}
	}
	ctx = withApprovedDialAddrs(ctx, ips)
	var lastErr error
	for _, ip := range ips {
		conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), portStr))
		if err == nil {
			return conn, nil
		}
		lastErr = err
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, rejectf(RejectDNSFailed, "DNS resolution failed for %s: no addresses", host)
}

// NewSSRFProtectionFromFetchSetting builds the policy from the operator's
// fetch setting fields. privateAllowList holds hosts, IPs or CIDRs that may
// be fetched although they are private.
func NewSSRFProtectionFromFetchSetting(allowPrivateIp bool, domainFilterMode bool, ipFilterMode bool, domainList, ipList, allowedPorts []string, applyIPFilterForDomain bool, privateAllowList []string) (*SSRFProtection, error) {
	allowedPortInts, err := parsePortRanges(allowedPorts)
	if err != nil {
		return nil, fmt.Errorf("request reject - invalid port configuration: %v", err)
	}
	return &SSRFProtection{
		AllowPrivateIp:         allowPrivateIp,
		DomainFilterMode:       domainFilterMode,
		DomainList:             domainList,
		IpFilterMode:           ipFilterMode,
		IpList:                 ipList,
		AllowedPorts:           allowedPortInts,
		ApplyIPFilterForDomain: applyIPFilterForDomain,
		PrivateAllowList:       privateAllowList,
	}, nil
}
