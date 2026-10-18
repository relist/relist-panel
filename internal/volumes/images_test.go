package volumes_test

import (
	"context"
	"reflect"
	"testing"

	"relist-panel/internal/volumes"
	"relist-panel/internal/volumes/faketest"
)

func TestResolverImagesSplitsNamedAndBuilt(t *testing.T) {
	cli := &faketest.CLI{ComposeConfigOutput: []byte(`{"services":{
		"db":{"image":"postgres:16"},
		"api":{"build":{"context":"/d/src/api"}},
		"worker":{"build":{"context":"/d/src/w"},"image":"shop/worker:1"}
	}}`)}
	ref := volumes.ComposeRef{ProjectName: "relist-c7", AllProfiles: true}
	images, built, err := volumes.NewResolver(cli, "/d").Images(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(images, []string{"postgres:16", "shop/worker:1"}) {
		t.Fatalf("images = %q", images)
	}
	if !reflect.DeepEqual(built, []string{"relist-c7-api"}) {
		t.Fatalf("built = %q", built)
	}
	if !cli.ComposeConfigRefs[0].AllProfiles {
		t.Fatal("Images did not resolve with every profile")
	}
}
