package deployments

import "relist-panel/internal/sources"

// stepKind is what a step does; the worker switches on it.
type stepKind int

const (
	kindFetch stepKind = iota
	kindBuild
	kindDeploy
)

// step is one step of a Deployment, as planned before it starts.
type step struct {
	kind         stepKind
	name         string // as stored and shown: "fetch", "build:web", "deploy"
	service      string // kindBuild
	noCache      bool   // kindBuild
	build        bool   // kindDeploy: pass --build to up
	freshVolumes bool   // kindDeploy
	note         string // logged right after the step begins
}

// plan is every step of a Deployment. prepare runs outside the Project gate,
// where Cancel and shutdown stop it; apply runs inside the gate, shielded from
// both. A plan with fail settles with it, after storing its steps if it has any.
type plan struct {
	fail         *outcome
	usesSource   bool // prepare holds the Source lock and records the Source it built
	prepare      []step
	afterPrepare string // logged after the last prepare step
	apply        []step
}

type planInput struct {
	mode          Mode
	noCache       bool
	freshVolumes  bool
	source        *sources.Source // nil when the Compose has none
	buildServices []string        // Services with build:, from the snapshot
}

// planDeployment decides every step of a Deployment from its inputs alone. It
// touches nothing: no Docker, no disk, no database.
func planDeployment(in planInput) plan {
	deploy := step{kind: kindDeploy, name: stepDeploy, freshVolumes: in.freshVolumes}
	switch in.mode {
	case ModeDeploy:
		if in.source != nil {
			deploy.note = "compose has a source; deploying without building"
		} else {
			deploy.build = true
		}
		return plan{apply: []step{deploy}}
	case ModeSkipBuild:
		if in.source == nil {
			return plan{fail: &outcome{status: StatusFailed, reason: reasonNoSource}}
		}
		return plan{apply: []step{deploy}}
	}

	if in.source == nil {
		return plan{fail: &outcome{status: StatusFailed, reason: reasonNoSource}}
	}
	p := plan{usesSource: true, apply: []step{deploy}}
	if in.source.Kind == sources.KindGit {
		p.prepare = append(p.prepare, step{kind: kindFetch, name: stepFetch})
		if reason := gitSourceProblem(*in.source); reason != "" {
			o := failed(stepFetch, reason)
			return plan{fail: &o, prepare: p.prepare, apply: p.apply}
		}
	}
	for _, svc := range in.buildServices {
		p.prepare = append(p.prepare, step{kind: kindBuild, name: "build:" + svc, service: svc, noCache: in.noCache})
	}
	if len(in.buildServices) == 0 {
		p.afterPrepare = "no Service has build:"
	}
	return p
}

// gitSourceProblem is why a Git Source cannot be fetched, or "".
func gitSourceProblem(src sources.Source) string {
	switch {
	case src.Branch == "":
		return reasonNoBranch
	case src.PrivateKey == "":
		return reasonNoKey
	}
	return ""
}

// stepNames is every step's name, prepare first.
func (p plan) stepNames() []string {
	names := make([]string, 0, len(p.prepare)+len(p.apply))
	for _, s := range p.prepare {
		names = append(names, s.name)
	}
	for _, s := range p.apply {
		names = append(names, s.name)
	}
	return names
}
