package environmentvariables

import "testing"

func TestValidateInputKeyPattern(t *testing.T) {
	value := "v"
	str := func(key string) environmentVariableInput {
		return environmentVariableInput{Key: key, Value: &value}
	}

	valid := []string{"KEY", "KEY_2", "_private", "a", "A_b9"}
	for _, key := range valid {
		if _, _, err := validateInput(str(key)); err != nil {
			t.Errorf("validateInput(%q) = %v, want nil", key, err)
		}
	}

	invalid := map[string]string{
		"2KEY":   "environment variable key must match [A-Za-z_][A-Za-z0-9_]*",
		"MY-KEY": "environment variable key must match [A-Za-z_][A-Za-z0-9_]*",
		"MY KEY": "environment variable key must match [A-Za-z_][A-Za-z0-9_]*",
		"":       "environment variable key is required",
	}
	for key, want := range invalid {
		if _, _, err := validateInput(str(key)); err == nil || err.Error() != want {
			t.Errorf("validateInput(%q) = %v, want %q", key, err, want)
		}
	}
}

func TestValidateInputRequiresValue(t *testing.T) {
	if _, _, err := validateInput(environmentVariableInput{Key: "KEY"}); err == nil {
		t.Error("validateInput without a value = nil, want an error")
	}
}
