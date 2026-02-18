package cli

import (
	"testing"
	"time"
)

func TestParseExpiration(t *testing.T) {
	tests := []struct {
		input    string
		want     time.Duration
		wantErr  bool
	}{
		// Standard duration formats
		{"1h", 1 * time.Hour, false},
		{"24h", 24 * time.Hour, false},
		{"30m", 30 * time.Minute, false},
		{"1h30m", 90 * time.Minute, false},

		// Days format
		{"1d", 24 * time.Hour, false},
		{"7d", 7 * 24 * time.Hour, false},
		{"30d", 30 * 24 * time.Hour, false},
		{"365d", 365 * 24 * time.Hour, false},

		// Invalid formats
		{"", 0, true},
		{"invalid", 0, true},
		{"d", 0, true},
		{"7x", 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := parseExpiration(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseExpiration(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
				return
			}
			if !tt.wantErr && got != tt.want {
				t.Errorf("parseExpiration(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestTokenCmdStructure(t *testing.T) {
	cmd := newTokenCmd()

	// Check command is properly structured
	if cmd.Use != "token" {
		t.Errorf("Use = %q, want %q", cmd.Use, "token")
	}

	// Check subcommands exist
	subcommands := []string{"create", "list", "get", "revoke", "delete"}
	for _, name := range subcommands {
		found := false
		for _, sub := range cmd.Commands() {
			if sub.Use == name || (len(sub.Use) > len(name) && sub.Use[:len(name)] == name) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("missing subcommand: %s", name)
		}
	}
}

func TestTokenCreateCmdFlags(t *testing.T) {
	cmd := newTokenCreateCmd()

	// Check required flags exist
	// Note: --name has no shorthand because -n conflicts with global --namespace
	flags := []struct {
		name      string
		shorthand string
	}{
		{"name", ""},
		{"role", "r"},
		{"namespace", ""},
		{"expires-in", ""},
	}

	for _, f := range flags {
		flag := cmd.Flags().Lookup(f.name)
		if flag == nil {
			t.Errorf("missing flag: --%s", f.name)
			continue
		}
		if f.shorthand != "" && flag.Shorthand != f.shorthand {
			t.Errorf("flag --%s shorthand = %q, want %q", f.name, flag.Shorthand, f.shorthand)
		}
	}
}

func TestTokenListCmdFlags(t *testing.T) {
	cmd := newTokenListCmd()

	// Check aliases
	if len(cmd.Aliases) == 0 || cmd.Aliases[0] != "ls" {
		t.Error("list command should have 'ls' alias")
	}

	// Check role filter flag
	flag := cmd.Flags().Lookup("role")
	if flag == nil {
		t.Error("missing --role flag")
	}
}

func TestTokenDeleteCmdFlags(t *testing.T) {
	cmd := newTokenDeleteCmd()

	// Check yes flag (skip confirmation)
	flag := cmd.Flags().Lookup("yes")
	if flag == nil {
		t.Error("missing --yes flag")
	}
	if flag.Shorthand != "y" {
		t.Errorf("yes shorthand = %q, want %q", flag.Shorthand, "y")
	}
}
