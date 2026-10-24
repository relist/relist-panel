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

// owner returns the panel Compose that owns or names the candidate, "" if none.
func owner(kind Kind, c Candidate, owners Ownership) string {
	if compose, ok := owners.Projects[c.Labels[ProjectLabel]]; ok && c.Labels[ProjectLabel] != "" {
		return compose
	}
	switch kind {
	case Volumes, AllVolumes:
		return owners.Volumes[c.Name]
	case Images, UnusedImages:
		for _, tag := range c.Tags {
			for _, ref := range referenceForms(tag) {
				if compose, ok := owners.Images[ref]; ok {
					return compose
				}
			}
		}
	}
	return ""
}

// referenceForms is a stored repo:tag with the spellings a Compose file may
// use for it: "nginx" and "docker.io/library/nginx:latest" are both nginx:latest.
func referenceForms(tag string) []string {
	forms := []string{tag}
	short := strings.TrimPrefix(tag, "docker.io/")
	short = strings.TrimPrefix(short, "library/")
	if short != tag {
		forms = append(forms, short)
	}
	if name, ok := strings.CutSuffix(short, ":latest"); ok {
		forms = append(forms, name)
	}
	return forms
}
