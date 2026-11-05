package policy

import (
	"reflect"
	"testing"
)

func TestParseApprover(t *testing.T) {
	for in, want := range map[string]Approver{"": ApproverAdmins, "admins": ApproverAdmins, "owner": ApproverOwner, " Owner ": ApproverOwner} {
		got, err := ParseApprover(in)
		if err != nil || got != want {
			t.Errorf("ParseApprover(%q) = %q, %v", in, got, err)
		}
	}
	if _, err := ParseApprover("everyone"); err == nil {
		t.Error("an unknown approver was accepted")
	}
}

func TestApproverAllows(t *testing.T) {
	if !ApproverAdmins.Allows(2, true) || ApproverAdmins.Allows(2, false) {
		t.Error("admins: any admin, no member")
	}
	if !ApproverOwner.Allows(1, true) || ApproverOwner.Allows(2, true) {
		t.Error("owner: only id 1")
	}
}

func TestParseRoots(t *testing.T) {
	got, err := ParseRoots(" /srv, /data/media/ ,,")
	if err != nil || !reflect.DeepEqual(got, []string{"/srv", "/data/media"}) {
		t.Fatalf("roots = %v, %v", got, err)
	}
	if got, err := ParseRoots(""); err != nil || len(got) != 0 {
		t.Fatalf("empty = %v, %v", got, err)
	}
	for _, bad := range []string{"srv", "/srv,data", "/"} {
		if _, err := ParseRoots(bad); err == nil {
			t.Errorf("ParseRoots(%q) accepted", bad)
		}
	}
}
