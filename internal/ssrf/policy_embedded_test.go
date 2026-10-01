package ssrf

import (
	"context"
	"errors"
	"net"
	"testing"
)

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
			_, err := ResolveAndPin(context.Background(), resolver, "target.example", false)
			if got := errors.Is(err, ErrBlocked); got != tc.blocked {
				t.Fatalf("ResolveAndPin(%s) error = %v, want blocked=%t", tc.ip, err, tc.blocked)
			}
		})
	}
}

// TestResolveAndPinLocalhostOptInStillAllowsIPv6Loopback guards the
// IPv4-compatible range: ::1 and :: sit inside ::/96, and reading ::1 as the
// embedded 0.0.0.1 would refuse the localhost opt-in the sandbox proxy and
// web_fetch rely on.
func TestResolveAndPinLocalhostOptInStillAllowsIPv6Loopback(t *testing.T) {
	resolver := func(context.Context, string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("::1")}, nil
	}
	if _, err := ResolveAndPin(context.Background(), resolver, "localhost", true); err != nil {
		t.Fatalf("localhost opt-in rejected ::1: %v", err)
	}
	// An IPv4 loopback embedded in a transition form is loopback too.
	embedded := func(context.Context, string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("64:ff9b::7f00:1")}, nil
	}
	if _, err := ResolveAndPin(context.Background(), embedded, "nat64.example", true); err != nil {
		t.Fatalf("localhost opt-in rejected an embedded 127.0.0.1: %v", err)
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
