// Package ssrf owns Nanite's shared IP-range policy for outbound requests.
// It deliberately does not decide which URL schemes or hosts a caller may
// use; callers retain those endpoint-specific rules and share only the
// destination-address boundary that must not drift between egress paths.
package ssrf

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
)

// Resolver resolves a hostname to every address returned by DNS. Callers may
// replace it in tests, but production callers normally use DefaultResolver.
type Resolver func(ctx context.Context, host string) ([]net.IP, error)

// ErrBlocked classifies rejections by the shared destination-address policy.
var ErrBlocked = errors.New("ssrf: blocked destination")

var deniedCIDRs = mustParseCIDRs([]string{
	"169.254.0.0/16", // link-local incl. cloud IMDS
	"10.0.0.0/8",     // RFC1918
	"172.16.0.0/12",  // RFC1918
	"192.168.0.0/16", // RFC1918
	"0.0.0.0/8",      // unspecified
	"100.64.0.0/10",  // CGNAT
	"fc00::/7",       // IPv6 ULA
	"fe80::/10",      // IPv6 link-local
	"::/128",         // IPv6 unspecified
	// NAT64 local-use prefix (RFC 8215), denied whole: the operator picks
	// the network-specific prefix length inside it, so where the embedded
	// IPv4 sits (RFC 6052 section 2.2) cannot be known from the address,
	// and reading every candidate position always finds a zero-filled one.
	// The well-known prefix gets an exact reading instead; see below.
	"64:ff9b:1::/48",
})

// Loopback is separate because the sandbox proxy and web_fetch have an
// explicit localhost opt-in. Catalog downloads leave that opt-in disabled.
var loopbackCIDRs = mustParseCIDRs([]string{
	"127.0.0.0/8",
	"::1/128",
})

// IPv6 transition forms that carry an IPv4 address the sender picks
// (CW-20260930-0027, CW-20260930-0028). A DNS answer in one of these forms
// matches none of the IPv6 ranges above, so the deny check reads the
// embedded IPv4 out of it (embeddedIPv4s) and applies the IPv4 ranges to
// that. Denying the prefixes outright instead would refuse every IPv4
// destination on a DNS64/NAT64 network, which synthesizes a NAT64 answer for
// each IPv4-only host. The IPv4-mapped form (::ffff:0:0/96) needs no entry:
// net.IPNet.Contains already reads it as IPv4.
var (
	nat64WellKnown = mustParseCIDR("64:ff9b::/96") // RFC 6052: IPv4 in the last 32 bits
	sixToFour      = mustParseCIDR("2002::/16")    // RFC 3056: IPv4 in bits 16-47
	teredo         = mustParseCIDR("2001::/32")    // RFC 4380: server and obfuscated client IPv4
	ipv4Compatible = mustParseCIDR("::/96")        // RFC 4291 (deprecated): IPv4 in the last 32 bits
)

// DefaultResolver uses the system resolver.
func DefaultResolver(ctx context.Context, host string) ([]net.IP, error) {
	return net.DefaultResolver.LookupIP(ctx, "ip", host)
}

// ResolveAndPin resolves host once, rejects the entire answer if any returned
// address is denied, and returns the first validated IP. Callers must dial the
// returned literal rather than resolving host again; that pins the checked DNS
// answer and closes the DNS-rebinding window.
func ResolveAndPin(ctx context.Context, resolver Resolver, host string, allowLocalhost bool) (net.IP, error) {
	if !allowLocalhost && IsLocalhostName(host) {
		return nil, fmt.Errorf("%w: localhost name %q", ErrBlocked, host)
	}
	if resolver == nil {
		resolver = DefaultResolver
	}
	ips, err := resolver(ctx, host)
	if err != nil {
		return nil, err
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("%w: no IPs for %q", ErrBlocked, host)
	}
	for _, ip := range ips {
		if reason, denied := deniedReason(ip, allowLocalhost); denied {
			return nil, fmt.Errorf("%w: %s", ErrBlocked, reason)
		}
		for _, v4 := range embeddedIPv4s(ip) {
			if reason, denied := deniedReason(v4, allowLocalhost); denied {
				return nil, fmt.Errorf("%w: %s embeds %s", ErrBlocked, ip, reason)
			}
		}
	}
	return ips[0], nil
}

// deniedReason reports whether ip falls in a denied range, and which.
// Loopback is denied only without the localhost opt-in.
func deniedReason(ip net.IP, allowLocalhost bool) (string, bool) {
	if !allowLocalhost {
		for _, block := range loopbackCIDRs {
			if block.Contains(ip) {
				return fmt.Sprintf("loopback %s", ip), true
			}
		}
	}
	for _, block := range deniedCIDRs {
		if block.Contains(ip) {
			return fmt.Sprintf("%s in %s", ip, block), true
		}
	}
	return "", false
}

// embeddedIPv4s returns the IPv4 addresses an IPv6 transition-form address
// carries, for the deny check to judge; any denied one denies the address.
// For Teredo both the server address and the client address (stored XORed
// with 0xffffffff) are returned. The NAT64 local-use prefix is not read
// here: deniedCIDRs refuses all of it.
//
// The IPv4-compatible range ::/96 also holds :: and ::1, which are judged as
// IPv6 (unspecified, loopback) and are not read as 0.0.0.0 and 0.0.0.1.
func embeddedIPv4s(ip net.IP) []net.IP {
	if ip.To4() != nil {
		return nil
	}
	b := ip.To16()
	if b == nil {
		return nil
	}
	switch {
	case nat64WellKnown.Contains(ip):
		return []net.IP{net.IPv4(b[12], b[13], b[14], b[15])}
	case sixToFour.Contains(ip):
		return []net.IP{net.IPv4(b[2], b[3], b[4], b[5])}
	case teredo.Contains(ip):
		return []net.IP{
			net.IPv4(b[4], b[5], b[6], b[7]),
			net.IPv4(^b[12], ^b[13], ^b[14], ^b[15]),
		}
	case ipv4Compatible.Contains(ip) && !ip.Equal(net.IPv6unspecified) && !ip.Equal(net.IPv6loopback):
		return []net.IP{net.IPv4(b[12], b[13], b[14], b[15])}
	}
	return nil
}

// IsLocalhostName matches localhost and every subdomain reserved by RFC 6761.
func IsLocalhostName(host string) bool {
	h := strings.ToLower(strings.TrimSuffix(host, "."))
	return h == "localhost" || strings.HasSuffix(h, ".localhost")
}

func mustParseCIDRs(cidrs []string) []*net.IPNet {
	out := make([]*net.IPNet, 0, len(cidrs))
	for _, cidr := range cidrs {
		out = append(out, mustParseCIDR(cidr))
	}
	return out
}

func mustParseCIDR(cidr string) *net.IPNet {
	_, block, err := net.ParseCIDR(cidr)
	if err != nil {
		panic(fmt.Sprintf("ssrf: invalid CIDR %q: %v", cidr, err))
	}
	return block
}
