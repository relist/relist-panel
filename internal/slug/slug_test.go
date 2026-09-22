package slug

import "testing"

func TestConflicts(t *testing.T) {
	siblings := []struct{ name string }{{"My App"}, {"Staging"}}

	if !Conflicts("my-app", siblings, func(s struct{ name string }) string { return s.name }) {
		t.Error("a slug variant of a sibling name must conflict")
	}
	if Conflicts("other", siblings, func(s struct{ name string }) string { return s.name }) {
		t.Error("a name with a distinct slug must not conflict")
	}

	punctuation := []struct{ name string }{{"???"}}
	if !Conflicts("!!!", punctuation, func(s struct{ name string }) string { return s.name }) {
		t.Error("punctuation-only names collapse to the same fallback slug and must conflict")
	}
}
