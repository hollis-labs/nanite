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
})

// Loopback is separate because the sandbox proxy and web_fetch have an
// explicit localhost opt-in. Catalog downloads leave that opt-in disabled.
var loopbackCIDRs = mustParseCIDRs([]string{
	"127.0.0.0/8",
	"::1/128",
})

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
		if !allowLocalhost {
			for _, block := range loopbackCIDRs {
				if block.Contains(ip) {
					return nil, fmt.Errorf("%w: loopback %s", ErrBlocked, ip)
				}
			}
		}
		for _, block := range deniedCIDRs {
			if block.Contains(ip) {
				return nil, fmt.Errorf("%w: %s in %s", ErrBlocked, ip, block)
			}
		}
		if ip.IsUnspecified() {
			return nil, fmt.Errorf("%w: unspecified %s", ErrBlocked, ip)
		}
	}
	return ips[0], nil
}

// IsLocalhostName matches localhost and every subdomain reserved by RFC 6761.
func IsLocalhostName(host string) bool {
	h := strings.ToLower(strings.TrimSuffix(host, "."))
	return h == "localhost" || strings.HasSuffix(h, ".localhost")
}

func mustParseCIDRs(cidrs []string) []*net.IPNet {
	out := make([]*net.IPNet, 0, len(cidrs))
	for _, cidr := range cidrs {
		_, block, err := net.ParseCIDR(cidr)
		if err != nil {
			panic(fmt.Sprintf("ssrf: invalid CIDR %q: %v", cidr, err))
		}
		out = append(out, block)
	}
	return out
}
