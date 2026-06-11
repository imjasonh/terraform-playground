package lock

import "testing"

func TestEvalMarker(t *testing.T) {
	linux := NewMarkerEnv("linux", "amd64", 3, 12)

	cases := []struct {
		marker string
		want   bool
	}{
		{"", true},
		{"sys_platform == 'win32'", false},
		{"sys_platform == 'linux'", true},
		{"sys_platform != 'win32'", true},
		{"platform_system == 'Linux'", true},
		{"platform_machine == 'x86_64'", true},
		{"os_name == 'posix'", true},
		{"python_version >= '3.11'", true},
		{"python_version < '3.11'", false},
		{"python_full_version >= '3.12'", true},
		{"python_version >= '3.10' and python_version < '3.13'", true},
		{"python_version < '3.10' or sys_platform == 'linux'", true},
		{"python_version < '3.10' or sys_platform == 'win32'", false},
		{"extra == 'foo'", false}, // unknown var -> empty
		{"(sys_platform == 'win32') or (os_name == 'posix')", true},
		{"platform_machine in 'x86_64 aarch64'", true}, // substring membership
		{"'arm' not in platform_machine", true},
		{"this is not valid pep508 @#$", true}, // unparseable -> include
	}
	for _, c := range cases {
		if got := EvalMarker(c.marker, linux); got != c.want {
			t.Errorf("EvalMarker(%q) = %v, want %v", c.marker, got, c.want)
		}
	}

	// A python_version gate flips on a different target.
	py310 := NewMarkerEnv("linux", "amd64", 3, 10)
	if EvalMarker("python_version >= '3.11'", py310) {
		t.Error("python_version >= 3.11 should be false on 3.10")
	}
}
