package validate

import "testing"

func TestPort(t *testing.T) {
	tests := []struct {
		port    int
		wantErr bool
	}{
		{0, false},     // disabled
		{8080, false},  // normal
		{65535, false}, // max valid
		{-1, true},     // negative
		{65536, true},  // too high
		{80, true},     // privileged
		{443, true},    // privileged
		{1024, false},  // just above privileged
	}

	for _, tt := range tests {
		err := Port(tt.port)
		if (err != nil) != tt.wantErr {
			t.Errorf("Port(%d) error = %v, wantErr %v", tt.port, err, tt.wantErr)
		}
	}
}

func TestReplicas(t *testing.T) {
	tests := []struct {
		n       int
		wantErr bool
	}{
		{0, false},    // paused
		{1, false},    // normal
		{100, false},  // reasonable
		{1000, false}, // max allowed
		{-1, true},    // negative
		{1001, true},  // too many
	}

	for _, tt := range tests {
		err := Replicas(tt.n)
		if (err != nil) != tt.wantErr {
			t.Errorf("Replicas(%d) error = %v, wantErr %v", tt.n, err, tt.wantErr)
		}
	}
}

func TestMemory(t *testing.T) {
	tests := []struct {
		mb      int
		wantErr bool
	}{
		{0, false},     // unset
		{16, false},    // minimum
		{512, false},   // normal
		{65536, false}, // max (64GB)
		{-1, true},     // negative
		{8, true},      // too low
		{100000, true}, // too high
	}

	for _, tt := range tests {
		err := Memory(tt.mb)
		if (err != nil) != tt.wantErr {
			t.Errorf("Memory(%d) error = %v, wantErr %v", tt.mb, err, tt.wantErr)
		}
	}
}

func TestName(t *testing.T) {
	tests := []struct {
		name    string
		wantErr bool
	}{
		{"api", false},
		{"my-api", false},
		{"api_v2", false},
		{"a1b2c3", false},
		{"MyService", false},
		{"", true},               // empty
		{".", true},              // dot
		{"..", true},             // dotdot
		{"-api", true},           // starts with dash
		{"_api", true},           // starts with underscore
		{"api!", true},           // invalid char
		{"api service", true},    // space
		{"a" + string(make([]byte, 64)), true}, // too long
	}

	for _, tt := range tests {
		err := Name(tt.name)
		if (err != nil) != tt.wantErr {
			t.Errorf("Name(%q) error = %v, wantErr %v", tt.name, err, tt.wantErr)
		}
	}
}

func TestEnvVar(t *testing.T) {
	tests := []struct {
		input     string
		wantKey   string
		wantValue string
		wantErr   bool
	}{
		{"KEY=value", "KEY", "value", false},
		{"DEBUG=true", "DEBUG", "true", false},
		{"PATH=/usr/bin", "PATH", "/usr/bin", false},
		{"EMPTY=", "EMPTY", "", false},
		{"COMPLEX=a=b=c", "COMPLEX", "a=b=c", false},
		{"_PRIVATE=x", "_PRIVATE", "x", false},
		{"", "", "", true},              // empty
		{"NOVALUE", "", "", true},       // no equals
		{"=value", "", "", true},        // empty key
		{"123=value", "", "", true},     // starts with number
		{"KEY-DASH=x", "", "", true},    // dash in key
		{"KEY.DOT=x", "", "", true},     // dot in key
	}

	for _, tt := range tests {
		k, v, err := EnvVar(tt.input)
		if (err != nil) != tt.wantErr {
			t.Errorf("EnvVar(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			continue
		}
		if !tt.wantErr {
			if k != tt.wantKey || v != tt.wantValue {
				t.Errorf("EnvVar(%q) = %q, %q; want %q, %q", tt.input, k, v, tt.wantKey, tt.wantValue)
			}
		}
	}
}

func TestCronSchedule(t *testing.T) {
	tests := []struct {
		expr    string
		wantErr bool
	}{
		{"* * * * *", false},       // every minute
		{"0 * * * *", false},       // every hour
		{"0 0 * * *", false},       // daily
		{"0 0 * * 0", false},       // weekly
		{"@hourly", false},         // shortcut
		{"@daily", false},          // shortcut
		{"@weekly", false},         // shortcut
		{"@monthly", false},        // shortcut
		{"@yearly", false},         // shortcut
		{"@annually", false},       // shortcut
		{"", true},                 // empty
		{"* * * *", true},          // 4 fields
		{"* * * * * *", true},      // 6 fields
		{"invalid", true},          // garbage
	}

	for _, tt := range tests {
		err := CronSchedule(tt.expr)
		if (err != nil) != tt.wantErr {
			t.Errorf("CronSchedule(%q) error = %v, wantErr %v", tt.expr, err, tt.wantErr)
		}
	}
}

func TestTimeout(t *testing.T) {
	tests := []struct {
		s       string
		wantErr bool
	}{
		{"", false},    // empty is OK (default used)
		{"30s", false}, // seconds
		{"5m", false},  // minutes
		{"1h", false},  // hours
		{"0s", false},  // explicit zero
		{"0", true},    // missing unit
		{"100", true},  // missing unit
	}

	for _, tt := range tests {
		err := Timeout(tt.s)
		if (err != nil) != tt.wantErr {
			t.Errorf("Timeout(%q) error = %v, wantErr %v", tt.s, err, tt.wantErr)
		}
	}
}

func TestPath(t *testing.T) {
	tests := []struct {
		path    string
		wantErr bool
	}{
		{"/tmp/file.txt", false},
		{"./script.py", false},
		{"script.py", false},
		{"", true},               // empty
		{"../../../etc/passwd", true}, // traversal
		{"/tmp/../etc/passwd", true},  // traversal
	}

	for _, tt := range tests {
		err := Path(tt.path)
		if (err != nil) != tt.wantErr {
			t.Errorf("Path(%q) error = %v, wantErr %v", tt.path, err, tt.wantErr)
		}
	}
}
