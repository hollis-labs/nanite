//go:build !linux

package sandbox

import "errors"

// ErrNetworkBridgeUnavailable is linux-only in substance; see
// netns_probe_linux.go. Other platforms have no netns bridge.
var ErrNetworkBridgeUnavailable = errors.New("sandbox: network-granted exec is unavailable on this host")

// ProbeNetworkBridge reports nil off linux: there is no netns bridge to
// probe, and network-granted exec goes through the platform's own sandbox.
func ProbeNetworkBridge() error { return nil }
