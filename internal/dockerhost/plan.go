package dockerhost

import "strings"

// ProjectLabel is the label Docker Compose puts on everything a project creates.
const ProjectLabel = "com.docker.compose.project"

// Ownership says which Docker resources the panel's Composes own or name, each
// mapped to the Compose's name.
type Ownership struct {
	Projects map[string]string // deploy project name -> Compose name
	Volumes  map[string]string // volume name a Compose's YAML names -> Compose name
	Images   map[string]string // literal image reference -> Compose name
}

// Candidate is an unused volume or image a prune could remove.
type Candidate struct {
	ID, Name string
	Labels   map[string]string
	Tags     []string
	Digests  []string // repo@sha256:... references
	Size     *uint64
}

type PlanEntry struct {
	Name string  `json:"name"`
	Size *uint64 `json:"size"`
}
type SkipEntry struct {
	Name    string `json:"name"`
	Compose string `json:"compose,omitempty"`
	Reason  string `json:"reason"`
}

// PrunePlan is what a prune would delete and what it would leave alone.
type PrunePlan struct {
	Kind   Kind        `json:"kind"`
	Delete []PlanEntry `json:"delete"`
	Skip   []SkipEntry `json:"skip"`
}

// Plan splits candidates into what a prune deletes and what it skips because a
// panel Compose owns or names it. includePanel deletes those as well.
func Plan(kind Kind, candidates []Candidate, owners Ownership, includePanel bool) PrunePlan {
	p, _ := plan(kind, candidates, owners, includePanel)
	return p
}

// plan also returns the candidates behind p.Delete, in the same order.
func plan(kind Kind, candidates []Candidate, owners Ownership, includePanel bool) (PrunePlan, []Candidate) {
	p := PrunePlan{Kind: kind, Delete: []PlanEntry{}, Skip: []SkipEntry{}}
	owners.Images = normalizedKeys(owners.Images)
	doomed := []Candidate{}
	for _, c := range candidates {
		if compose := owner(kind, c, owners); compose != "" && !includePanel {
			p.Skip = append(p.Skip, SkipEntry{Name: c.Name, Compose: compose, Reason: "used by Compose " + compose})
			continue
		}
		p.Delete = append(p.Delete, PlanEntry{Name: c.Name, Size: c.Size})
		doomed = append(doomed, c)
	}
	return p, doomed
}

func normalizedKeys(images map[string]string) map[string]string {
	out := make(map[string]string, len(images))
	for ref, compose := range images {
		out[normalizeReference(ref)] = compose
	}
	return out
}

// owner returns the panel Compose that owns or names the candidate, "" if none.
func owner(kind Kind, c Candidate, owners Ownership) string {
	if compose, ok := owners.Projects[c.Labels[ProjectLabel]]; ok && c.Labels[ProjectLabel] != "" {
		return compose
	}
	switch kind {
	case Volumes, AllVolumes:
		return owners.Volumes[c.Name]
	case Images, UnusedImages:
		// An image whose tag moved on has none left, so the ID names it too.
		for _, ref := range append(append([]string{c.ID}, c.Tags...), c.Digests...) {
			if compose, ok := owners.Images[normalizeReference(ref)]; ok {
				return compose
			}
		}
	}
	return ""
}

// normalizeReference spells an image reference one way, so "nginx",
// "docker.io/library/nginx:latest" and "index.docker.io/library/nginx" compare
// equal. A registry host with a port ("localhost:5000/app") is kept intact.
func normalizeReference(ref string) string {
	ref = strings.TrimPrefix(ref, "index.docker.io/")
	ref = strings.TrimPrefix(ref, "docker.io/")
	if rest, ok := strings.CutPrefix(ref, "library/"); ok && !strings.Contains(rest, "/") {
		ref = rest
	}
	if !strings.Contains(ref, "@") && !strings.Contains(ref[strings.LastIndex(ref, "/")+1:], ":") {
		ref += ":latest"
	}
	return ref
}
