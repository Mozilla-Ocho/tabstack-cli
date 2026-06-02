package cmd

import (
	"testing"
)

func TestValidateKeyFormat(t *testing.T) {
	cases := []struct {
		key     string
		wantErr bool
	}{
		{"validkey12345678", false},
		{"sk-abc123-valid-key-here", false},
		{"short", true},
		{`key"bad`, true},
		{"key\nbad", true},
		{"key\rbad", true},
		{"key\tbad", true},
		{" leading", true},
		{"trailing ", true},
	}
	for _, tc := range cases {
		err := validateKeyFormat(tc.key)
		if tc.wantErr && err == nil {
			t.Errorf("validateKeyFormat(%q): expected error, got nil", tc.key)
		}
		if !tc.wantErr && err != nil {
			t.Errorf("validateKeyFormat(%q): unexpected error: %v", tc.key, err)
		}
	}
}
