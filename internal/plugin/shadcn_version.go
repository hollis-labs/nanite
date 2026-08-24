package plugin

import (
	"fmt"
	"strconv"
	"strings"
)

// HostShadcnVersion is the version of the host's shared shadcn/Radix primitive
// API surface (J.5 OQ9). Bumped when the exported primitive API (props,
// variants, or behavior) changes in a way that could break plugins importing
// `@nanite/ui/<primitive>` via the importmap.
//
// Versioning rules:
//   - MAJOR bumps for breaking primitive API changes.
//   - MINOR bumps for additive, backward-compatible changes (new primitive,
//     new variant).
//   - PATCH bumps for bugfixes that don't change the API.
const HostShadcnVersion = "1.0.0"

// CheckShadcnCompat returns an error when a plugin's declared ui.shadcn_version
// range is not satisfied by HostShadcnVersion. Empty pluginRange is treated as
// "plugin does not use shared primitives" and always passes.
//
// Supported range grammar (a deliberate npm-compatible subset):
//
//	""              → skip (no declaration).
//	"X.Y.Z"         → exact match.
//	"^X.Y.Z"        → same-major, >= X.Y.Z (the common case).
//	"~X.Y.Z"        → same-major+minor, >= X.Y.Z.
//
// Anything else is reported as an unsupported range so the plugin author can
// either narrow the constraint or file a BLG for richer syntax.
func CheckShadcnCompat(pluginRange string) error {
	return checkShadcnCompatAgainst(pluginRange, HostShadcnVersion)
}

func checkShadcnCompatAgainst(pluginRange, hostVersion string) error {
	pr := strings.TrimSpace(pluginRange)
	if pr == "" {
		return nil
	}
	host, err := parseSemver(hostVersion)
	if err != nil {
		return fmt.Errorf("host shadcn version %q is malformed: %w", hostVersion, err)
	}

	op := opExact
	body := pr
	switch {
	case strings.HasPrefix(pr, "^"):
		op = opCaret
		body = pr[1:]
	case strings.HasPrefix(pr, "~"):
		op = opTilde
		body = pr[1:]
	}
	want, err := parseSemver(strings.TrimSpace(body))
	if err != nil {
		return fmt.Errorf("ui.shadcn_version %q: %w (supported forms: X.Y.Z, ^X.Y.Z, ~X.Y.Z)", pluginRange, err)
	}

	if !satisfies(op, want, host) {
		return fmt.Errorf("ui.shadcn_version %q is incompatible with host shadcn %s — bump the plugin or release a compatible host", pluginRange, hostVersion)
	}
	return nil
}

type semverOp int

const (
	opExact semverOp = iota
	opCaret
	opTilde
)

type semver struct {
	major, minor, patch int
}

func parseSemver(s string) (semver, error) {
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return semver{}, fmt.Errorf("expected MAJOR.MINOR.PATCH, got %q", s)
	}
	out := semver{}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return semver{}, fmt.Errorf("non-numeric component %q in %q", p, s)
		}
		switch i {
		case 0:
			out.major = n
		case 1:
			out.minor = n
		case 2:
			out.patch = n
		}
	}
	return out, nil
}

// satisfies reports whether host satisfies the constraint (op, want).
func satisfies(op semverOp, want, host semver) bool {
	switch op {
	case opExact:
		return host == want
	case opCaret:
		// Same major, >= want. Matches npm `^1.2.3` semantics.
		return host.major == want.major && gte(host, want)
	case opTilde:
		// Same major+minor, >= want. Matches npm `~1.2.3` semantics.
		return host.major == want.major && host.minor == want.minor && gte(host, want)
	}
	return false
}

func gte(a, b semver) bool {
	if a.major != b.major {
		return a.major > b.major
	}
	if a.minor != b.minor {
		return a.minor > b.minor
	}
	return a.patch >= b.patch
}
