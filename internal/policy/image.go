package policy

import (
	"sort"
	"strings"
)

// ImageLabels answers the Traefik labels an image puts on a service's
// container that the Compose's own labels do not override. Docker merges an
// image's labels into its containers' and Traefik reads them, so the policy
// on the Compose's labels does not cover them (ADR-0032). They matter only
// when Traefik reads the container at all: some spelling of traefik.enable
// that is not false, since Traefik exposes nothing by default. image names
// the image, for the messages.
func ImageLabels(service, image string, imageLabels, container map[string]string) []Violation {
	effective := map[string]string{}
	for key, value := range imageLabels {
		effective[key] = value
	}
	for key, value := range container {
		effective[key] = value
	}
	enabled := false
	for key, value := range effective {
		if strings.ToLower(key) == "traefik.enable" && !isFalse(value) {
			enabled = true
		}
	}
	if !enabled {
		return nil
	}
	keys := make([]string, 0, len(imageLabels))
	for key := range imageLabels {
		if _, overridden := container[key]; !overridden && strings.HasPrefix(strings.ToLower(key), "traefik.") {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	violations := make([]Violation, 0, len(keys))
	for _, key := range keys {
		violations = append(violations, Violation{Service: service, Message: "image " + image + " carries label " + key +
			"; set it in the Compose's labels instead, or turn Traefik off with traefik.enable: \"false\""})
	}
	return violations
}
