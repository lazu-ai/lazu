package common

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"
)

func TestIsPrivateIPSpecialRanges(t *testing.T) {
	tests := []struct {
		name string
		ip   string
		want bool
	}{
		{name: "nil", ip: "", want: true},
		{name: "cgnat", ip: "100.64.0.1", want: true},
		{name: "testnet", ip: "198.51.100.7", want: true},
		{name: "documentation-v6", ip: "2001:db8::1", want: true},
		{name: "public-v4", ip: "8.8.8.8", want: false},
		{name: "public-v6", ip: "2606:4700:4700::1111", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var ip net.IP
			if tt.ip != "" {
				ip = net.ParseIP(tt.ip)
			}
			if got := isPrivateIP(ip); got != tt.want {
				t.Fatalf("isPrivateIP(%q) = %v, want %v", tt.ip, got, tt.want)
			}
		})
	}
}

func withLookupIP(t *testing.T, fn func(string) ([]net.IP, error)) {
	t.Helper()
	oldLookupIP := lookupIP
	lookupIP = fn
	t.Cleanup(func() {
		lookupIP = oldLookupIP
	})
}

func testSSRFProtection() *SSRFProtection {
	return &SSRFProtection{
		AllowPrivateIp:         false,
		DomainFilterMode:       false,
		DomainList:             []string{},
		IpFilterMode:           false,
		IpList:                 []string{},
		AllowedPorts:           []int{},
		ApplyIPFilterForDomain: false,
	}
}

func TestValidateURLBlocksDomainResolvingToPrivateIPWhenDomainIPFilterFlagDisabled(t *testing.T) {
	withLookupIP(t, func(host string) ([]net.IP, error) {
		if host != "metadata.internal" {
			return nil, fmt.Errorf("unexpected host %s", host)
		}
		return []net.IP{net.ParseIP("127.0.0.1")}, nil
	})

	err := testSSRFProtection().ValidateURL("https://metadata.internal/path")
	if err == nil {
		t.Fatal("expected private domain resolution to be rejected")
	}
}

func TestValidateURLBlocksDomainWhenAnyResolvedIPIsPrivate(t *testing.T) {
	withLookupIP(t, func(host string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("8.8.8.8"), net.ParseIP("10.0.0.5")}, nil
	})

	err := testSSRFProtection().ValidateURL("https://mixed.example.com")
	if err == nil {
		t.Fatal("expected mixed public/private domain resolution to be rejected")
	}
}

func TestValidateURLBlocksDomainResolvingToIPv6Loopback(t *testing.T) {
	withLookupIP(t, func(host string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("::1")}, nil
	})

	err := testSSRFProtection().ValidateURL("https://ipv6-loopback.example.com")
	if err == nil {
		t.Fatal("expected IPv6 loopback domain resolution to be rejected")
	}
}

func TestValidateURLAllowsDomainResolvingToPublicIPWithDomainIPFilterFlagDisabled(t *testing.T) {
	withLookupIP(t, func(host string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("8.8.8.8")}, nil
	})

	if err := testSSRFProtection().ValidateURL("https://public.example.com"); err != nil {
		t.Fatalf("expected public domain resolution to be allowed, got %v", err)
	}
}

func TestValidateURLDomainOptionalIPListOnlyAppliesWhenEnabled(t *testing.T) {
	withLookupIP(t, func(host string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("8.8.8.8")}, nil
	})

	protection := testSSRFProtection()
	protection.IpFilterMode = true
	protection.IpList = []string{"1.1.1.1"}
	protection.ApplyIPFilterForDomain = false
	if err := protection.ValidateURL("https://public.example.com"); err != nil {
		t.Fatalf("expected domain to ignore optional IP whitelist when disabled, got %v", err)
	}

	protection.ApplyIPFilterForDomain = true
	if err := protection.ValidateURL("https://public.example.com"); err == nil {
		t.Fatal("expected domain to honor optional IP whitelist when enabled")
	}
}

func TestFetchSettingProtectionBlocksPrivateResolutionWhenDomainIPFilterFlagDisabled(t *testing.T) {
	withLookupIP(t, func(host string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("169.254.169.254")}, nil
	})

	protection, err := NewSSRFProtectionFromFetchSetting(false, false, false, nil, nil, []string{"80", "443"}, false, nil)
	if err != nil {
		t.Fatalf("build protection: %v", err)
	}
	err = protection.ValidateURL("http://metadata.internal/latest/meta-data")
	if err == nil {
		t.Fatal("expected fetch setting validation to reject metadata IP resolution")
	}
}

func TestDialContextDialsValidatedResolvedIP(t *testing.T) {
	withLookupIP(t, func(host string) ([]net.IP, error) {
		if host != "public.example.com" {
			return nil, fmt.Errorf("unexpected host %s", host)
		}
		return []net.IP{net.ParseIP("8.8.8.8")}, nil
	})

	dialer := &recordingDialer{}
	_, err := testSSRFProtection().DialContext(context.Background(), "tcp", "public.example.com:443", dialer)
	if !errors.Is(err, errDialStop) {
		t.Fatalf("expected dialer error, got %v", err)
	}
	if len(dialer.addresses) != 1 || dialer.addresses[0] != "8.8.8.8:443" {
		t.Fatalf("expected dial to resolved IP, got %v", dialer.addresses)
	}
}

func TestDialContextBlocksMixedPrivateResolutionBeforeDialing(t *testing.T) {
	withLookupIP(t, func(host string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("8.8.8.8"), net.ParseIP("127.0.0.1")}, nil
	})

	dialer := &recordingDialer{}
	_, err := testSSRFProtection().DialContext(context.Background(), "tcp", "mixed.example.com:443", dialer)
	if err == nil {
		t.Fatal("expected mixed public/private resolution to be rejected")
	}
	if len(dialer.addresses) != 0 {
		t.Fatalf("expected no dial before all resolved IPs are validated, got %v", dialer.addresses)
	}
}

var errDialStop = errors.New("dial stopped")

type recordingDialer struct {
	addresses []string
}

func (d *recordingDialer) DialContext(_ context.Context, _ string, address string) (net.Conn, error) {
	d.addresses = append(d.addresses, address)
	return nil, errDialStop
}

func TestIsPrivateIPCoversEveryGuardedRange(t *testing.T) {
	private := map[string]string{
		"loopback-v4":          "127.0.0.1",
		"loopback-v4-high":     "127.255.255.254",
		"loopback-v6":          "::1",
		"rfc1918-10":           "10.1.2.3",
		"rfc1918-172":          "172.16.0.1",
		"rfc1918-172-end":      "172.31.255.255",
		"rfc1918-192":          "192.168.1.1",
		"link-local-v4":        "169.254.169.254",
		"link-local-v6":        "fe80::1",
		"link-local-v6-end":    "febf::1",
		"cgnat":                "100.64.0.1",
		"cgnat-end":            "100.127.255.255",
		"unique-local":         "fc00::1",
		"unique-local-fd":      "fd12:3456::1",
		"multicast-v4":         "224.0.0.1",
		"multicast-v6":         "ff02::1",
		"unspecified-v4":       "0.0.0.0",
		"unspecified-v6":       "::",
		"broadcast":            "255.255.255.255",
		"reserved-240":         "240.0.0.1",
		"mapped-loopback":      "::ffff:127.0.0.1",
		"mapped-rfc1918":       "::ffff:10.0.0.1",
		"mapped-metadata":      "::ffff:169.254.169.254",
		"mapped-cgnat":         "::ffff:100.64.0.1",
		"nat64-well-known":     "64:ff9b::7f00:1",
		"benchmark":            "198.18.0.1",
		"documentation-v4":     "203.0.113.1",
		"documentation-v6":     "2001:db8::1",
		"ietf-protocol-assign": "192.0.0.1",
	}
	for name, raw := range private {
		t.Run("private/"+name, func(t *testing.T) {
			ip := net.ParseIP(raw)
			if ip == nil {
				t.Fatalf("test address %q does not parse", raw)
			}
			if !isPrivateIP(ip) {
				t.Fatalf("isPrivateIP(%q) = false, want true", raw)
			}
		})
	}
	public := map[string]string{
		"google-dns":        "8.8.8.8",
		"cloudflare-dns":    "1.1.1.1",
		"just-above-cgnat":  "100.128.0.1",
		"just-above-172":    "172.32.0.1",
		"public-v6":         "2606:4700:4700::1111",
		"mapped-public":     "::ffff:8.8.8.8",
		"just-below-172-16": "172.15.255.255",
	}
	for name, raw := range public {
		t.Run("public/"+name, func(t *testing.T) {
			if isPrivateIP(net.ParseIP(raw)) {
				t.Fatalf("isPrivateIP(%q) = true, want false", raw)
			}
		})
	}
}

func TestValidateURLRefusesNonHTTPSchemes(t *testing.T) {
	for _, raw := range []string{"ftp://example.com/a.png", "file:///etc/passwd", "gopher://127.0.0.1:70", "//example.com/a.png"} {
		err := testSSRFProtection().ValidateURL(raw)
		rejected := AsURLRejected(err)
		if rejected == nil {
			t.Fatalf("%q: expected a policy rejection, got %v", raw, err)
		}
		if rejected.Reason != RejectUnsupportedScheme {
			t.Fatalf("%q: reason = %s, want %s", raw, rejected.Reason, RejectUnsupportedScheme)
		}
	}
}

func TestValidateURLLiteralPrivateAddressIsRefusedWithReason(t *testing.T) {
	for _, raw := range []string{
		"http://127.0.0.1/a.png",
		"http://169.254.169.254/latest/meta-data",
		"http://[::1]/a.png",
		"http://[::ffff:10.0.0.1]/a.png",
		"http://[fe80::1]/a.png",
	} {
		rejected := AsURLRejected(testSSRFProtection().ValidateURL(raw))
		if rejected == nil || rejected.Reason != RejectPrivateAddress {
			t.Fatalf("%q: expected private-address rejection, got %v", raw, rejected)
		}
	}
}

func TestResolvedPrivateRejectionDoesNotEchoResolvedAddress(t *testing.T) {
	withLookupIP(t, func(string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("10.42.7.9")}, nil
	})
	err := testSSRFProtection().ValidateURL("http://storage.corp.internal/a.png")
	rejected := AsURLRejected(err)
	if rejected == nil || rejected.Reason != RejectPrivateAddress {
		t.Fatalf("expected private-address rejection, got %v", err)
	}
	if strings.Contains(rejected.Error(), "10.42.7.9") {
		t.Fatalf("rejection leaks the resolved address: %s", rejected.Error())
	}
	if !strings.Contains(rejected.Error(), "storage.corp.internal") {
		t.Fatalf("rejection should name the host the caller supplied: %s", rejected.Error())
	}
	if strings.Contains(rejected.PublicReason(), "storage.corp.internal") || strings.Contains(rejected.PublicReason(), "10.42") {
		t.Fatalf("public reason must not carry host or address: %s", rejected.PublicReason())
	}
}

func TestPrivateAllowListByCIDRAndHost(t *testing.T) {
	withLookupIP(t, func(host string) ([]net.IP, error) {
		switch host {
		case "minio.internal":
			return []net.IP{net.ParseIP("10.0.5.20")}, nil
		case "img.lan.example":
			return []net.IP{net.ParseIP("192.168.9.9")}, nil
		case "other.internal":
			return []net.IP{net.ParseIP("10.9.9.9")}, nil
		}
		return nil, fmt.Errorf("unexpected host %s", host)
	})
	p := testSSRFProtection()
	p.PrivateAllowList = []string{"10.0.5.0/24", "*.lan.example", "127.0.0.1"}

	for _, raw := range []string{
		"http://minio.internal/bucket/a.png", // resolves inside the allowed CIDR
		"http://10.0.5.77/a.png",             // literal inside the allowed CIDR
		"http://img.lan.example/a.png",       // host matched by wildcard, resolves private
		"http://127.0.0.1/a.png",             // single allowed IP
	} {
		if err := p.ValidateURL(raw); err != nil {
			t.Fatalf("%q: expected allowlisted private source to pass, got %v", raw, err)
		}
	}
	for _, raw := range []string{
		"http://other.internal/a.png", // private, not listed
		"http://10.0.6.1/a.png",       // just outside the CIDR
		"http://127.0.0.2/a.png",      // loopback but not the listed address
		"http://169.254.169.254/",     // metadata stays refused
	} {
		rejected := AsURLRejected(p.ValidateURL(raw))
		if rejected == nil || rejected.Reason != RejectPrivateAddress {
			t.Fatalf("%q: expected private-address rejection despite allowlist, got %v", raw, rejected)
		}
	}
}

func TestPrivateAllowListDoesNotRelaxPorts(t *testing.T) {
	p := testSSRFProtection()
	p.AllowedPorts = []int{80, 443}
	p.PrivateAllowList = []string{"127.0.0.0/8"}
	rejected := AsURLRejected(p.ValidateURL("http://127.0.0.1:6379/"))
	if rejected == nil || rejected.Reason != RejectPortNotAllowed {
		t.Fatalf("expected port rejection, got %v", rejected)
	}
}

func TestDialControlRefusesAddressesDialContextDidNotApprove(t *testing.T) {
	if err := DialControl(context.Background(), "tcp", "8.8.8.8:443", nil); AsURLRejected(err) == nil {
		t.Fatalf("expected a dial without approval to be refused, got %v", err)
	}
	rejected := AsURLRejected(DialControl(context.Background(), "tcp", "169.254.169.254:80", nil))
	if rejected == nil || rejected.Reason != RejectPrivateAddress {
		t.Fatalf("expected unapproved private dial to be refused as private, got %v", rejected)
	}
	approved := withApprovedDialAddrs(context.Background(), []net.IP{net.ParseIP("8.8.8.8")})
	if err := DialControl(approved, "tcp", "8.8.8.8:443", nil); err != nil {
		t.Fatalf("expected approved address to pass, got %v", err)
	}
	if err := DialControl(approved, "tcp", "8.8.4.4:443", nil); AsURLRejected(err) == nil {
		t.Fatalf("expected a different address than the approved one to be refused, got %v", err)
	}
	if err := DialControl(approved, "tcp", "example.com:443", nil); AsURLRejected(err) == nil {
		t.Fatalf("expected a name at the control hook to be refused, got %v", err)
	}
}

func TestDialContextApprovesOnlyTheResolvedAddresses(t *testing.T) {
	withLookupIP(t, func(string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("8.8.8.8")}, nil
	})
	dialer := &approvalCheckingDialer{}
	_, err := testSSRFProtection().DialContext(context.Background(), "tcp", "public.example.com:443", dialer)
	if !errors.Is(err, errDialStop) {
		t.Fatalf("expected dialer error, got %v", err)
	}
	if dialer.controlErr != nil {
		t.Fatalf("control hook refused the address DialContext validated: %v", dialer.controlErr)
	}
}

// approvalCheckingDialer runs DialControl exactly as net.Dialer would, with
// the context DialContext handed it.
type approvalCheckingDialer struct {
	controlErr error
}

func (d *approvalCheckingDialer) DialContext(ctx context.Context, network string, address string) (net.Conn, error) {
	d.controlErr = DialControl(ctx, network, address, nil)
	return nil, errDialStop
}
