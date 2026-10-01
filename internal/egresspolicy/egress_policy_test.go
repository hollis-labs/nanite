// Package egresspolicy_test is Nanite's acceptance suite for go-egress-proxy's
// SSRF policy (CW-20260930-0219). It is Nanite's former internal/ssrf test
// suite, ported unchanged in substance onto egress.ResolveAndPin, so every
// address class Nanite fixed before adopting the library stays pinned
// against the library Nanite now uses: the original deny ranges, the
// embedded-IPv4 forms of CW-20260930-0027/0028 (#357), and the embedded
// loopback and 6to4 relay anycast rules of CW-20261001-0085 (#367).
package egresspolicy_test

import (
	"context"
	"errors"
	"net"
	"testing"

	"github.com/hollis-labs/go-egress-proxy/egress"
)

func TestResolveAndPinRejectsAnyDeniedDNSAnswer(t *testing.T) {
	resolver := func(context.Context, string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("203.0.113.10"), net.ParseIP("169.254.169.254")}, nil
	}
	if _, err := egress.ResolveAndPin(context.Background(), resolver, "catalog.example", false); !errors.Is(err, egress.ErrSSRFBlocked) {
		t.Fatalf("ResolveAndPin error = %v, want egress.ErrSSRFBlocked", err)
	}
}

func TestResolveAndPinRejectsDeniedRanges(t *testing.T) {
	tests := map[string]string{
		"rfc1918-10":       "10.1.2.3",
		"rfc1918-172":      "172.16.1.2",
		"rfc1918-192":      "192.168.1.2",
		"ipv4-loopback":    "127.0.0.2",
		"ipv6-loopback":    "::1",
		"imds-link-local":  "169.254.169.254",
		"ipv6-link-local":  "fe80::1",
		"cgnat":            "100.64.0.1",
		"ipv6-ula":         "fd00::1",
		"ipv4-unspecified": "0.0.0.0",
		"ipv6-unspecified": "::",
	}
	for name, rawIP := range tests {
		t.Run(name, func(t *testing.T) {
			resolver := func(context.Context, string) ([]net.IP, error) {
				return []net.IP{net.ParseIP(rawIP)}, nil
			}
			if _, err := egress.ResolveAndPin(context.Background(), resolver, "catalog.example", false); !errors.Is(err, egress.ErrSSRFBlocked) {
				t.Fatalf("egress.ResolveAndPin(%s) error = %v, want egress.ErrSSRFBlocked", rawIP, err)
			}
		})
	}
}

func TestResolveAndPinReturnsValidatedLiteral(t *testing.T) {
	want := net.ParseIP("203.0.113.10")
	resolver := func(context.Context, string) ([]net.IP, error) {
		return []net.IP{want}, nil
	}
	got, err := egress.ResolveAndPin(context.Background(), resolver, "catalog.example", false)
	if err != nil {
		t.Fatalf("ResolveAndPin: %v", err)
	}
	if !got.Equal(want) {
		t.Fatalf("ResolveAndPin = %s, want %s", got, want)
	}
}

func TestResolveAndPinLocalhostOptInDoesNotAllowOtherPrivateRanges(t *testing.T) {
	resolver := func(_ context.Context, host string) ([]net.IP, error) {
		if host == "localhost" {
			return []net.IP{net.ParseIP("127.0.0.1")}, nil
		}
		return []net.IP{net.ParseIP("10.0.0.1")}, nil
	}
	if _, err := egress.ResolveAndPin(context.Background(), resolver, "localhost", true); err != nil {
		t.Fatalf("localhost opt-in rejected loopback: %v", err)
	}
	if _, err := egress.ResolveAndPin(context.Background(), resolver, "private.example", true); !errors.Is(err, egress.ErrSSRFBlocked) {
		t.Fatalf("private range error = %v, want egress.ErrSSRFBlocked", err)
	}
}

// CW-20260930-0027 / CW-20260930-0028: IPv6 transition forms carry an IPv4
// address the attacker picks. A DNS answer in one of them used to match no
// IPv6 range in the deny set, so 169.254.169.254 (cloud metadata) or an
// RFC1918 address passed as "not denied". The IPv4 deny set must apply to
// the embedded address; a public embedded address must still pass, or a
// DNS64 network (which synthesizes NAT64 answers for every IPv4-only host)
// would lose all IPv4 egress.

func TestResolveAndPinAppliesIPv4DenySetToEmbeddedAddresses(t *testing.T) {
	tests := []struct {
		name    string
		ip      net.IP
		blocked bool
	}{
		{"nat64 well-known, metadata", net.ParseIP("64:ff9b::a9fe:a9fe"), true},
		{"nat64 well-known, rfc1918", net.ParseIP("64:ff9b::a00:1"), true},
		{"nat64 well-known, loopback", net.ParseIP("64:ff9b::7f00:1"), true},
		{"nat64 well-known, public", net.ParseIP("64:ff9b::808:808"), false},
		{"nat64 local-use /48, metadata", nat64LocalUseAddr(t, 48, "169.254.169.254"), true},
		{"nat64 local-use /56, metadata", nat64LocalUseAddr(t, 56, "169.254.169.254"), true},
		{"nat64 local-use /64, metadata", nat64LocalUseAddr(t, 64, "169.254.169.254"), true},
		{"nat64 local-use /96, metadata", nat64LocalUseAddr(t, 96, "169.254.169.254"), true},
		// The local-use prefix is denied whole (its prefix length, and so
		// where the IPv4 sits, is the operator's choice), public or not.
		{"nat64 local-use /96, public", nat64LocalUseAddr(t, 96, "8.8.8.8"), true},
		{"6to4, metadata", net.ParseIP("2002:a9fe:a9fe::1"), true},
		{"6to4, rfc1918", net.ParseIP("2002:c0a8:101::1"), true},
		{"6to4, public", net.ParseIP("2002:808:808::1"), false},
		// Teredo: server IPv4 in bits 32-63, client IPv4 XOR 0xffffffff in
		// the last 32 bits (RFC 4380).
		{"teredo, metadata client", net.ParseIP("2001:0:4136:e378:8000:63bf:5601:5601"), true},
		{"teredo, rfc1918 server", net.ParseIP("2001:0:a00:1:8000:63bf:f7f7:f7f7"), true},
		{"teredo, public server and client", net.ParseIP("2001:0:4136:e378:8000:63bf:f7f7:f7f7"), false},
		{"ipv4-compatible, metadata", net.ParseIP("::a9fe:a9fe"), true},
		{"ipv4-compatible, rfc1918", net.ParseIP("::a00:1"), true},
		{"ipv4-compatible, public", net.ParseIP("::808:808"), false},
		{"ipv4-mapped, metadata", net.ParseIP("::ffff:a9fe:a9fe"), true},
		{"plain global ipv6", net.ParseIP("2606:4700:4700::1111"), false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.ip == nil {
				t.Fatal("test address did not parse")
			}
			resolver := func(context.Context, string) ([]net.IP, error) {
				return []net.IP{tc.ip}, nil
			}
			_, err := egress.ResolveAndPin(context.Background(), resolver, "target.example", false)
			if got := errors.Is(err, egress.ErrSSRFBlocked); got != tc.blocked {
				t.Fatalf("egress.ResolveAndPin(%s) error = %v, want blocked=%t", tc.ip, err, tc.blocked)
			}
		})
	}
}

// TestResolveAndPinLocalhostOptInStillAllowsIPv6Loopback guards the
// IPv4-compatible range: ::1 and :: sit inside ::/96, and reading ::1 as the
// embedded 0.0.0.1 would refuse the localhost opt-in the sandbox proxy and
// web_fetch rely on.
func TestResolveAndPinLocalhostOptInStillAllowsIPv6Loopback(t *testing.T) {
	for _, raw := range []string{"::1", "127.0.0.1"} {
		resolver := func(context.Context, string) ([]net.IP, error) {
			return []net.IP{net.ParseIP(raw)}, nil
		}
		if _, err := egress.ResolveAndPin(context.Background(), resolver, "localhost", true); err != nil {
			t.Fatalf("localhost opt-in rejected %s: %v", raw, err)
		}
	}
}

// TestResolveAndPinDeniesEmbeddedLoopbackEvenWithLocalhostOptIn aligns with
// go-egress-proxy v0.2.2 (CW-20261001-0085): the localhost opt-in admits this
// host's own loopback, and a 127.0.0.1 reached through a NAT64 translator,
// a 6to4 or Teredo tunnel, or the IPv4-compatible form is not this host.
func TestResolveAndPinDeniesEmbeddedLoopbackEvenWithLocalhostOptIn(t *testing.T) {
	for name, raw := range map[string]string{
		"nat64 well-known":        "64:ff9b::7f00:1",
		"6to4":                    "2002:7f00:1::",
		"teredo client 127.0.0.1": "2001:0:4136:e378:8000:63bf:80ff:fffe",
		"teredo server 127.0.0.1": "2001:0:7f00:1:8000:63bf:f7f7:f7f7",
		"ipv4-compatible":         "::7f00:1",
		"nat64 well-known 127/8":  "64:ff9b::7f01:203",
	} {
		t.Run(name, func(t *testing.T) {
			resolver := func(context.Context, string) ([]net.IP, error) {
				return []net.IP{net.ParseIP(raw)}, nil
			}
			if _, err := egress.ResolveAndPin(context.Background(), resolver, "localhost", true); !errors.Is(err, egress.ErrSSRFBlocked) {
				t.Fatalf("egress.ResolveAndPin(%s) with the localhost opt-in = %v, want egress.ErrSSRFBlocked", raw, err)
			}
		})
	}
}

// TestResolveAndPinDenies6to4RelayAnycast pins 192.88.99.0/24, the
// deprecated 6to4 relay anycast range (RFC 7526), aligned with
// go-egress-proxy v0.2.2: the whole /24, its neighbors untouched, and a
// 6to4 address that embeds it.
func TestResolveAndPinDenies6to4RelayAnycast(t *testing.T) {
	for raw, blocked := range map[string]bool{
		"192.88.99.0":      true,
		"192.88.99.1":      true,
		"192.88.99.255":    true,
		"192.88.98.255":    false,
		"192.88.100.0":     false,
		"2002:c058:6301::": true,
	} {
		t.Run(raw, func(t *testing.T) {
			resolver := func(context.Context, string) ([]net.IP, error) {
				return []net.IP{net.ParseIP(raw)}, nil
			}
			_, err := egress.ResolveAndPin(context.Background(), resolver, "relay.example", false)
			if got := errors.Is(err, egress.ErrSSRFBlocked); got != blocked {
				t.Fatalf("egress.ResolveAndPin(%s) = %v, want blocked=%t", raw, err, blocked)
			}
		})
	}
}

// nat64LocalUseAddr builds an RFC 6052 address under the 64:ff9b:1::/48
// local-use prefix (RFC 8215) for a network-specific prefix of prefixLen
// bits, embedding v4 where RFC 6052 section 2.2 puts it for that length.
// Byte 8 (bits 64-71) is the reserved "u" octet and stays zero.
func nat64LocalUseAddr(t *testing.T, prefixLen int, v4 string) net.IP {
	t.Helper()
	b := net.ParseIP(v4).To4()
	if b == nil {
		t.Fatalf("bad IPv4 %q", v4)
	}
	ip := make(net.IP, net.IPv6len)
	copy(ip, net.ParseIP("64:ff9b:1::"))
	switch prefixLen {
	case 48:
		ip[6], ip[7], ip[9], ip[10] = b[0], b[1], b[2], b[3]
	case 56:
		ip[7], ip[9], ip[10], ip[11] = b[0], b[1], b[2], b[3]
	case 64:
		copy(ip[9:13], b)
	case 96:
		copy(ip[12:16], b)
	default:
		t.Fatalf("unsupported prefix length %d", prefixLen)
	}
	return ip
}
