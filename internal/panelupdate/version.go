package panelupdate

import (
	"regexp"
	"strconv"
)

var semverPattern = regexp.MustCompile(`^v?(\d+)\.(\d+)\.(\d+)$`)

type semver [3]int

// parseVersion reads MAJOR.MINOR.PATCH with an optional leading "v". Anything
// else, "dev" included, is not a release version.
func parseVersion(s string) (semver, bool) {
	m := semverPattern.FindStringSubmatch(s)
	if m == nil {
		return semver{}, false
	}
	var v semver
	for i := range v {
		n, err := strconv.Atoi(m[i+1])
		if err != nil {
			return semver{}, false
		}
		v[i] = n
	}
	return v, true
}

func (a semver) greater(b semver) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i] > b[i]
		}
	}
	return false
}
