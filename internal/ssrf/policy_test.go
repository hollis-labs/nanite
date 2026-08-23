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
