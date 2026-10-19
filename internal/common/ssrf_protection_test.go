package common

import (
	"context"
	"errors"
	"fmt"
	"net"
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

func TestValidateURLWithFetchSettingBlocksPrivateResolutionWhenDomainIPFilterFlagDisabled(t *testing.T) {
	withLookupIP(t, func(host string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("169.254.169.254")}, nil
	})

	err := ValidateURLWithFetchSetting(
		"http://metadata.internal/latest/meta-data",
		true,
		false,
		false,
		false,
		nil,
		nil,
		[]string{"80", "443"},
		false,
	)
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
