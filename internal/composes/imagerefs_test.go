package composes

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"testing"
)

func refKeys(m map[string][]ComposeRef) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func TestDeployRecordsImageRefs(t *testing.T) {
	svc, runner, target, _ := newDeployService(t, "services: {web: {image: nginx}}\n")
	runner.config = []byte(imageConfig)
	if _, err := svc.RunDeploy(context.Background(), target, DeployOptions{}); err != nil {
		t.Fatal(err)
	}
	refs, err := svc.ImageRefs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"app:1", "local:1", "redis:7", "relist-c1-built"}; !reflect.DeepEqual(refKeys(refs), want) {
		t.Fatalf("refs = %v, want %v", refKeys(refs), want)
	}
	want := []ComposeRef{{ComposeID: 1, Name: "c", ProjectID: 7, ProjectName: "p", EnvironmentID: 3, EnvironmentName: "e"}}
	if !reflect.DeepEqual(refs["app:1"], want) {
		t.Fatalf("app:1 = %+v, want %+v", refs["app:1"], want)
	}
}

func TestFailedDeployKeepsPreviousRefs(t *testing.T) {
	svc, runner, target, _ := newDeployService(t, "services: {web: {image: nginx}}\n")
	runner.config = []byte(`{"name":"relist-c1","services":{"web":{"image":"ghcr.io/me/app:1"}}}`)
	if _, err := svc.RunDeploy(context.Background(), target, DeployOptions{}); err != nil {
		t.Fatal(err)
	}
	runner.config = []byte(`{"name":"relist-c1","services":{"web":{"image":"ghcr.io/me/app:2"}}}`)
	runner.upErr = errors.New("up failed")
	if _, err := svc.RunDeploy(context.Background(), target, DeployOptions{}); err == nil {
		t.Fatal("deploy succeeded")
	}
	refs, _ := svc.ImageRefs(context.Background())
	if want := []string{"ghcr.io/me/app:1"}; !reflect.DeepEqual(refKeys(refs), want) {
		t.Fatalf("refs = %v, want %v", refKeys(refs), want)
	}
	runner.upErr = nil
	if _, err := svc.RunDeploy(context.Background(), target, DeployOptions{}); err != nil {
		t.Fatal(err)
	}
	refs, _ = svc.ImageRefs(context.Background())
	if want := []string{"ghcr.io/me/app:2"}; !reflect.DeepEqual(refKeys(refs), want) {
		t.Fatalf("after success refs = %v, want %v", refKeys(refs), want)
	}
}

func TestDeletedComposeDropsRefs(t *testing.T) {
	svc, runner, target, db := newDeployService(t, "services: {web: {image: nginx}}\n")
	runner.config = []byte(imageConfig)
	if _, err := svc.RunDeploy(context.Background(), target, DeployOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DELETE FROM compose_variables WHERE compose_id = 1`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DELETE FROM composes WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	if refs, _ := svc.ImageRefs(context.Background()); len(refs) != 0 {
		t.Fatalf("refs = %v after delete", refKeys(refs))
	}
}
