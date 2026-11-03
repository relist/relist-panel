package panelupdate

import (
	"regexp"
	"strconv"
)

var semverPattern = regexp.MustCompile(`^v?(\d+)\.(\d+)\.(\d+)(?:-rc\.(\d+))?$`)

type semver struct {
	core [3]int
	// pre is true for a release candidate; rc is then its number.
	pre bool
	rc  int
}

// parseVersion reads MAJOR.MINOR.PATCH or MAJOR.MINOR.PATCH-rc.N with an
// optional leading "v" (ADR-0026). Anything else, "dev" included, is not a
// release version.
func parseVersion(s string) (semver, bool) {
	m := semverPattern.FindStringSubmatch(s)
	if m == nil {
		return semver{}, false
	}
	var v semver
	for i := range v.core {
		n, err := strconv.Atoi(m[i+1])
		if err != nil {
			return semver{}, false
		}
		v.core[i] = n
	}
	if m[4] != "" {
		n, err := strconv.Atoi(m[4])
		if err != nil {
			return semver{}, false
		}
		v.pre, v.rc = true, n
	}
	return v, true
}

// greater follows semver precedence: a release candidate comes before the
// release of the same MAJOR.MINOR.PATCH.
func (a semver) greater(b semver) bool {
	for i := range a.core {
		if a.core[i] != b.core[i] {
			return a.core[i] > b.core[i]
		}
	}
	if a.pre != b.pre {
		return !a.pre
	}
	return a.rc > b.rc
}
