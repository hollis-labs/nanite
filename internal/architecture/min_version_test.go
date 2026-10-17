package architecture_test

import "golang.org/x/mod/semver"

// atLeastVersion reports whether version is a released semantic version (no
// prerelease, no pseudo-version) at or above minimum. The dependency guards in
// this package assert a floor, not an exact pin: a host may adopt a newer
// release of a library without touching these tests. Integrity of whatever
// version is resolved is go.sum's job; the recorded checksums below are only
// checked when go.sum holds the floor version itself.
func atLeastVersion(version, minimum string) bool {
	return semver.IsValid(version) && semver.Prerelease(version) == "" && semver.Compare(version, minimum) >= 0
}
