package ssrf

import (
	"context"
	"errors"
	"net"
	"testing"
)

func TestResolveAndPinRejectsAnyDeniedDNSAnswer(t *testing.T) {
	resolver := func(context.Context, string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("203.0.113.10"), net.ParseIP("169.254.169.254")}, nil
	}
	if _, err := ResolveAndPin(context.Background(), resolver, "catalog.example", false); !errors.Is(err, ErrBlocked) {
		t.Fatalf("ResolveAndPin error = %v, want ErrBlocked", err)
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
			if _, err := ResolveAndPin(context.Background(), resolver, "catalog.example", false); !errors.Is(err, ErrBlocked) {
				t.Fatalf("ResolveAndPin(%s) error = %v, want ErrBlocked", rawIP, err)
			}
		})
	}
}

func TestResolveAndPinReturnsValidatedLiteral(t *testing.T) {
	want := net.ParseIP("203.0.113.10")
	resolver := func(context.Context, string) ([]net.IP, error) {
		return []net.IP{want}, nil
	}
	got, err := ResolveAndPin(context.Background(), resolver, "catalog.example", false)
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
	if _, err := ResolveAndPin(context.Background(), resolver, "localhost", true); err != nil {
		t.Fatalf("localhost opt-in rejected loopback: %v", err)
	}
	if _, err := ResolveAndPin(context.Background(), resolver, "private.example", true); !errors.Is(err, ErrBlocked) {
		t.Fatalf("private range error = %v, want ErrBlocked", err)
	}
}
