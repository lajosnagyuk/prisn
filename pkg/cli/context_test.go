package cli

import (
	"testing"
)

func TestValidateContextName(t *testing.T) {
	tests := []struct {
		name    string
		wantErr bool
	}{
		// Valid names
		{"local", false},
		{"prod", false},
		{"staging", false},
		{"my-context", false},
		{"my_context", false},
		{"context123", false},
		{"UPPER", false},
		{"MixedCase", false},

		// Invalid names
		{"", true},                  // empty
		{"has space", true},         // space
		{"has\ttab", true},          // tab
		{"has\nnewline", true},      // newline
		{"has/slash", true},         // forward slash
		{"has\\backslash", true},    // backslash
		{"has:colon", true},         // colon
		{"has.dot", true},           // dot
		{"-starts-with-dash", true}, // starts with dash
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateContextName(tt.name)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateContextName(%q) error = %v, wantErr %v", tt.name, err, tt.wantErr)
			}
		})
	}
}

func TestMaskToken(t *testing.T) {
	tests := []struct {
		token string
		want  string
	}{
		{"", ""},                                    // empty token
		{"short", "****"},                           // short token (<=12 chars)
		{"exacttwelve", "****"},                     // exactly 12 chars
		{"sk_live_abcdefghijklmnop", "sk_l...mnop"}, // long token
		{"1234567890123", "1234...0123"},            // 13 chars
		{"a_very_long_token_here_xxxx", "a_ve...xxxx"},
	}

	for _, tt := range tests {
		t.Run(tt.token, func(t *testing.T) {
			got := maskToken(tt.token)
			if got != tt.want {
				t.Errorf("maskToken(%q) = %q, want %q", tt.token, got, tt.want)
			}
		})
	}
}

func TestContextCmdStructure(t *testing.T) {
	// Test that the context command has all expected subcommands
	cmd := newContextCmd()

	if cmd.Use != "context" {
		t.Errorf("Use = %q, want %q", cmd.Use, "context")
	}

	// Check expected subcommands exist
	expectedSubcmds := []string{"add", "use", "list", "current", "delete", "rename"}
	subCmds := make(map[string]bool)
	for _, sub := range cmd.Commands() {
		subCmds[sub.Use] = true
	}

	for _, expected := range expectedSubcmds {
		// Use prefix match since cobra Use field might be "add <name>"
		found := false
		for use := range subCmds {
			if len(use) >= len(expected) && use[:len(expected)] == expected {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("missing subcommand %q", expected)
		}
	}
}

func TestContextAddCmdFlags(t *testing.T) {
	cmd := newContextAddCmd()

	// Check expected flags exist
	expectedFlags := []string{
		"mode",
		"server",
		"kube-context",
		"namespace",
		"token",
		"tls-ca",
		"tls-cert",
		"tls-key",
		"insecure",
		"set-current",
	}

	for _, flag := range expectedFlags {
		if cmd.Flags().Lookup(flag) == nil {
			t.Errorf("missing flag %q", flag)
		}
	}
}

func TestContextUseCmdArgs(t *testing.T) {
	cmd := newContextUseCmd()

	// Should require exactly 1 argument
	if err := cmd.Args(cmd, []string{}); err == nil {
		t.Error("expected error for 0 args")
	}
	if err := cmd.Args(cmd, []string{"ctx"}); err != nil {
		t.Errorf("unexpected error for 1 arg: %v", err)
	}
	if err := cmd.Args(cmd, []string{"ctx1", "ctx2"}); err == nil {
		t.Error("expected error for 2 args")
	}
}

func TestContextDeleteCmdFlags(t *testing.T) {
	cmd := newContextDeleteCmd()

	// Check --yes/-y flag exists
	yesFlag := cmd.Flags().Lookup("yes")
	if yesFlag == nil {
		t.Error("missing --yes flag")
	}
	if yesFlag.Shorthand != "y" {
		t.Errorf("--yes shorthand = %q, want %q", yesFlag.Shorthand, "y")
	}
}

func TestContextRenameCmdArgs(t *testing.T) {
	cmd := newContextRenameCmd()

	// Should require exactly 2 arguments
	if err := cmd.Args(cmd, []string{}); err == nil {
		t.Error("expected error for 0 args")
	}
	if err := cmd.Args(cmd, []string{"old"}); err == nil {
		t.Error("expected error for 1 arg")
	}
	if err := cmd.Args(cmd, []string{"old", "new"}); err != nil {
		t.Errorf("unexpected error for 2 args: %v", err)
	}
	if err := cmd.Args(cmd, []string{"a", "b", "c"}); err == nil {
		t.Error("expected error for 3 args")
	}
}

func TestContextListCmdAliases(t *testing.T) {
	cmd := newContextListCmd()

	// Should have "ls" as alias
	found := false
	for _, alias := range cmd.Aliases {
		if alias == "ls" {
			found = true
			break
		}
	}
	if !found {
		t.Error("context list should have 'ls' alias")
	}
}

func TestContextDeleteCmdAliases(t *testing.T) {
	cmd := newContextDeleteCmd()

	// Should have "rm" and "remove" as aliases
	aliases := make(map[string]bool)
	for _, alias := range cmd.Aliases {
		aliases[alias] = true
	}

	if !aliases["rm"] {
		t.Error("context delete should have 'rm' alias")
	}
	if !aliases["remove"] {
		t.Error("context delete should have 'remove' alias")
	}
}

func TestContextCurrentCmdFlags(t *testing.T) {
	cmd := newContextCurrentCmd()

	// Check --verbose/-v flag exists
	verboseFlag := cmd.Flags().Lookup("verbose")
	if verboseFlag == nil {
		t.Error("missing --verbose flag")
	}
	if verboseFlag.Shorthand != "v" {
		t.Errorf("--verbose shorthand = %q, want %q", verboseFlag.Shorthand, "v")
	}
}
