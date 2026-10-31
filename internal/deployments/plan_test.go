package deployments

import (
	"reflect"
	"testing"

	"relist-panel/internal/sources"
)

func TestPlanDeployment(t *testing.T) {
	git := &sources.Source{Kind: sources.KindGit, Branch: "main", PrivateKey: "key"}
	zip := &sources.Source{Kind: sources.KindZip, ZipName: "app.zip"}
	deploy := step{kind: kindDeploy, name: "deploy"}
	fetch := step{kind: kindFetch, name: "fetch"}
	build := func(svc string, noCache bool) step {
		return step{kind: kindBuild, name: "build:" + svc, service: svc, noCache: noCache}
	}
	noSource := &outcome{status: StatusFailed, reason: reasonNoSource}
	failedFetch := func(reason string) *outcome { o := failed(stepFetch, reason); return &o }

	tests := []struct {
		name string
		in   planInput
		want plan
	}{
		{
			name: "deploy without a source builds with up",
			in:   planInput{mode: ModeDeploy},
			want: plan{apply: []step{{kind: kindDeploy, name: "deploy", build: true}}},
		},
		{
			name: "deploy with a source never builds",
			in:   planInput{mode: ModeDeploy, source: git},
			want: plan{apply: []step{{kind: kindDeploy, name: "deploy", note: "compose has a source; deploying without building"}}},
		},
		{
			name: "skip-build with a source",
			in:   planInput{mode: ModeSkipBuild, source: git},
			want: plan{apply: []step{deploy}},
		},
		{
			name: "skip-build without a source fails before any step",
			in:   planInput{mode: ModeSkipBuild},
			want: plan{fail: noSource},
		},
		{
			name: "build without a source fails before any step",
			in:   planInput{mode: ModeBuild, buildServices: []string{"web"}},
			want: plan{fail: noSource},
		},
		{
			name: "build from git without a branch fails at fetch",
			in:   planInput{mode: ModeBuild, source: &sources.Source{Kind: sources.KindGit, PrivateKey: "key"}, buildServices: []string{"web"}},
			want: plan{fail: failedFetch(reasonNoBranch), prepare: []step{fetch}, apply: []step{deploy}},
		},
		{
			name: "build from git without a key fails at fetch",
			in:   planInput{mode: ModeBuild, source: &sources.Source{Kind: sources.KindGit, Branch: "main"}, buildServices: []string{"web"}},
			want: plan{fail: failedFetch(reasonNoKey), prepare: []step{fetch}, apply: []step{deploy}},
		},
		{
			name: "build from git fetches then builds each service",
			in:   planInput{mode: ModeBuild, noCache: true, source: git, buildServices: []string{"web", "api"}},
			want: plan{usesSource: true, prepare: []step{fetch, build("web", true), build("api", true)}, apply: []step{deploy}},
		},
		{
			name: "build from a zip has no fetch",
			in:   planInput{mode: ModeBuild, source: zip, buildServices: []string{"web"}},
			want: plan{usesSource: true, prepare: []step{build("web", false)}, apply: []step{deploy}},
		},
		{
			name: "build from git with nothing to build",
			in:   planInput{mode: ModeBuild, source: git},
			want: plan{usesSource: true, prepare: []step{fetch}, afterPrepare: "no Service has build:", apply: []step{deploy}},
		},
		{
			name: "build from a zip with nothing to build",
			in:   planInput{mode: ModeBuild, source: zip},
			want: plan{usesSource: true, afterPrepare: "no Service has build:", apply: []step{deploy}},
		},
		{
			name: "fresh volumes reach the deploy step",
			in:   planInput{mode: ModeDeploy, freshVolumes: true},
			want: plan{apply: []step{{kind: kindDeploy, name: "deploy", build: true, freshVolumes: true}}},
		},
		{
			name: "fresh volumes on a build",
			in:   planInput{mode: ModeBuild, freshVolumes: true, source: zip, buildServices: []string{"web"}},
			want: plan{usesSource: true, prepare: []step{build("web", false)}, apply: []step{{kind: kindDeploy, name: "deploy", freshVolumes: true}}},
		},
	}
	rollout := func(svc string, build bool) step {
		return step{kind: kindRollout, name: "rollout:" + svc, service: svc, build: build}
	}
	tests = append(tests, []struct {
		name string
		in   planInput
		want plan
	}{
		{
			name: "a zero-downtime service rolls out after the rest deploy",
			in:   planInput{mode: ModeDeploy, zeroDowntime: []string{"web"}},
			want: plan{apply: []step{{kind: kindDeploy, name: "deploy", build: true, exclude: []string{"web"}}, rollout("web", true)}},
		},
		{
			name: "rollouts follow the order given",
			in:   planInput{mode: ModeSkipBuild, source: git, zeroDowntime: []string{"web", "api"}},
			want: plan{apply: []step{{kind: kindDeploy, name: "deploy", exclude: []string{"web", "api"}}, rollout("web", false), rollout("api", false)}},
		},
		{
			name: "fresh volumes deploys everything without rollout",
			in:   planInput{mode: ModeDeploy, freshVolumes: true, zeroDowntime: []string{"web"}},
			want: plan{apply: []step{{kind: kindDeploy, name: "deploy", build: true, freshVolumes: true}}},
		},
	}...)
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := planDeployment(tc.in)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("planDeployment(%+v)\n got %+v\nwant %+v", tc.in, got, tc.want)
			}
		})
	}
}

func TestPlanStepNames(t *testing.T) {
	p := planDeployment(planInput{mode: ModeBuild, source: &sources.Source{Kind: sources.KindGit, Branch: "main", PrivateKey: "key"}, buildServices: []string{"web", "api"}})
	if want := []string{"fetch", "build:web", "build:api", "deploy"}; !reflect.DeepEqual(p.stepNames(), want) {
		t.Fatalf("stepNames = %v, want %v", p.stepNames(), want)
	}
}
