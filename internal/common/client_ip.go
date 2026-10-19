package common

import (
	"net"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
)

var trustedProxyCache struct {
	sync.RWMutex
	raw  string
	nets []*net.IPNet
}

// GetRealClientIP returns the originating client address when Lazu sits behind a
// configured trusted reverse proxy. Direct clients cannot choose their own IP
// through forwarding headers, so non-proxy requests fall back to the TCP peer.
func GetRealClientIP(c *gin.Context) string {
	if c == nil || c.Request == nil {
		return ""
	}
	if identity, ok := InternalRelayIdentityFromContext(c.Request.Context()); ok {
		return identity.ClientIP
	}

	remoteIP := requestRemoteIP(c)
	if isTrustedReverseProxy(remoteIP) {
		if ip := validHeaderIP(c.GetHeader("CF-Connecting-IP")); ip != "" {
			return ip
		}
		// A proxy in the chain (nginx in front of the API) writes its own peer
		// into X-Real-IP, which is the previous hop, not the client. Taking
		// that private address put every visitor behind it in one bucket.
		if ip := validHeaderIP(c.GetHeader("X-Real-IP")); ip != "" && !isProxyHop(net.ParseIP(ip)) {
			return ip
		}
		if ip := forwardedForClientIP(c.GetHeader("X-Forwarded-For")); ip != "" {
			return ip
		}
	}

	if remoteIP != nil {
		return remoteIP.String()
	}
	return ""
}

func requestRemoteIP(c *gin.Context) net.IP {
	raw := strings.TrimSpace(c.Request.RemoteAddr)
	if raw == "" {
		return nil
	}
	if host, _, err := net.SplitHostPort(raw); err == nil {
		raw = host
	} else {
		raw = strings.Trim(raw, "[]")
	}
	return net.ParseIP(raw)
}

func RequestFromTrustedProxy(c *gin.Context) bool {
	if c == nil || c.Request == nil {
		return false
	}
	return isTrustedReverseProxy(requestRemoteIP(c))
}

func validHeaderIP(raw string) string {
	raw = strings.TrimSpace(raw)
	if net.ParseIP(raw) == nil {
		return ""
	}
	return raw
}

func forwardedForClientIP(raw string) string {
	for _, candidate := range strings.Split(raw, ",") {
		candidate = strings.TrimSpace(candidate)
		ip := net.ParseIP(candidate)
		if ip == nil {
			continue
		}
		if isProxyHop(ip) {
			continue
		}
		return candidate
	}
	return ""
}

func isTrustedReverseProxy(ip net.IP) bool {
	if ip == nil {
		return false
	}
	for _, network := range trustedProxyNetworks() {
		if network.Contains(ip) {
			return true
		}
	}
	return false
}

func isProxyHop(ip net.IP) bool {
	return ip != nil && (ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast())
}

func trustedProxyNetworks() []*net.IPNet {
	raw := strings.TrimSpace(TrustedProxyCIDRs)

	trustedProxyCache.RLock()
	if trustedProxyCache.raw == raw {
		nets := trustedProxyCache.nets
		trustedProxyCache.RUnlock()
		return nets
	}
	trustedProxyCache.RUnlock()

	nets := parseTrustedProxyCIDRs(raw)
	trustedProxyCache.Lock()
	trustedProxyCache.raw = raw
	trustedProxyCache.nets = nets
	trustedProxyCache.Unlock()
	return nets
}

func parseTrustedProxyCIDRs(raw string) []*net.IPNet {
	if raw == "" || strings.EqualFold(raw, "none") {
		return nil
	}
	var networks []*net.IPNet
	for _, part := range strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ';' || r == ' ' || r == '\n' || r == '\t'
	}) {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		network, ok := trustedProxyNetworkFromToken(part)
		if !ok {
			continue
		}
		networks = append(networks, network)
	}
	return networks
}

func trustedProxyNetworkFromToken(token string) (*net.IPNet, bool) {
	if strings.Contains(token, "/") {
		_, network, err := net.ParseCIDR(token)
		return network, err == nil
	}
	ip := net.ParseIP(token)
	if ip == nil {
		return nil, false
	}
	bits := 128
	if ip.To4() != nil {
		bits = 32
	}
	return &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)}, true
}
