package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLookupDebugToggle(t *testing.T) {
	if _, err := lookupDebugToggle("ollama"); err != nil {
		t.Errorf("ollama: unexpected error: %v", err)
	}

	if _, err := lookupDebugToggle("bogus-tool"); err == nil {
		t.Error("expected an error for a completely unknown tool")
	} else if !strings.Contains(err.Error(), "unknown tool") {
		t.Errorf("expected an 'unknown tool' error, got: %v", err)
	}

	// exo is a real, known tool (present in toolLogDirs) but has no
	// debug-toggle implementation yet — must produce a distinct error
	// from the "totally unknown" case above.
	if _, err := lookupDebugToggle("exo"); err == nil {
		t.Error("expected an error for a known tool without a debug toggle")
	} else if !strings.Contains(err.Error(), "not yet supported") {
		t.Errorf("expected a 'not yet supported' error, got: %v", err)
	}
}

// withTestLogDir redirects toolLogDirs["ollama"] to a temp directory for
// one test, restoring it afterward — never touches the real /var/log.
func withTestLogDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	orig := toolLogDirs["ollama"]
	toolLogDirs["ollama"] = dir
	t.Cleanup(func() { toolLogDirs["ollama"] = orig })
	return dir
}

func TestRunMark_WritesToBothStreams(t *testing.T) {
	dir := withTestLogDir(t)
	for _, name := range []string{"stdout.log", "stderr.log"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("existing content\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	if err := runMark("ollama", true); err != nil {
		t.Fatalf("runMark: %v", err)
	}

	for _, name := range []string{"stdout.log", "stderr.log"} {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("reading %s: %v", name, err)
		}
		s := string(data)
		if !strings.HasPrefix(s, "existing content\n") {
			t.Errorf("%s: existing content was not preserved, got %q", name, s)
		}
		if !strings.Contains(s, "DEBUG SESSION START") {
			t.Errorf("%s: expected a START marker, got %q", name, s)
		}
	}
}

func TestRunMark_StopMarker(t *testing.T) {
	dir := withTestLogDir(t)
	for _, name := range []string{"stdout.log", "stderr.log"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0644); err != nil {
			t.Fatal(err)
		}
	}

	if err := runMark("ollama", false); err != nil {
		t.Fatalf("runMark: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "stdout.log"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "DEBUG SESSION STOP") {
		t.Errorf("expected a STOP marker, got %q", data)
	}
}

func TestRunMark_UnknownTool(t *testing.T) {
	if err := runMark("bogus-tool", true); err == nil {
		t.Error("expected an error for an unknown tool")
	}
}

func TestRunMark_MissingLogFile(t *testing.T) {
	// A tool directory that exists but has no log files yet (never
	// started, or not enabled) should report a clear error, not panic.
	withTestLogDir(t)
	if err := runMark("ollama", true); err == nil {
		t.Error("expected an error when the log files don't exist")
	}
}

func TestExtractIntFlag(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		want    int
		wantSet []string
		wantErr bool
	}{
		{
			name:    "tool then equals form (the exact case that was broken)",
			args:    []string{"ollama", "--keep=5"},
			want:    5,
			wantSet: []string{"ollama"},
		},
		{
			name:    "equals form then tool",
			args:    []string{"--keep=5", "ollama"},
			want:    5,
			wantSet: []string{"ollama"},
		},
		{
			name:    "space form",
			args:    []string{"ollama", "--keep", "7"},
			want:    7,
			wantSet: []string{"ollama"},
		},
		{
			name:    "no flag at all uses default",
			args:    []string{"ollama"},
			want:    2,
			wantSet: []string{"ollama"},
		},
		{
			name:    "no tool, just the flag",
			args:    []string{"--keep=3"},
			want:    3,
			wantSet: []string{},
		},
		{
			name:    "invalid value",
			args:    []string{"ollama", "--keep=nope"},
			wantErr: true,
		},
		{
			name:    "space form missing value",
			args:    []string{"ollama", "--keep"},
			wantErr: true,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, rest, err := extractIntFlag(c.args, "keep", 2)
			if c.wantErr {
				if err == nil {
					t.Fatalf("expected an error, got value=%d rest=%v", got, rest)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != c.want {
				t.Errorf("value = %d, want %d", got, c.want)
			}
			if len(rest) != len(c.wantSet) {
				t.Errorf("rest = %v, want %v", rest, c.wantSet)
			} else {
				for i := range rest {
					if rest[i] != c.wantSet[i] {
						t.Errorf("rest = %v, want %v", rest, c.wantSet)
						break
					}
				}
			}
		})
	}
}

func TestExtractBoolFlag(t *testing.T) {
	cases := []struct {
		name       string
		args       []string
		flag       string
		wantFound  bool
		wantRemain []string
	}{
		{
			name:       "tool then flag (the exact case that was broken for mark)",
			args:       []string{"ollama", "--start"},
			flag:       "start",
			wantFound:  true,
			wantRemain: []string{"ollama"},
		},
		{
			name:       "flag then tool",
			args:       []string{"--start", "ollama"},
			flag:       "start",
			wantFound:  true,
			wantRemain: []string{"ollama"},
		},
		{
			name:       "flag absent",
			args:       []string{"ollama"},
			flag:       "start",
			wantFound:  false,
			wantRemain: []string{"ollama"},
		},
		{
			name:       "different flag not matched",
			args:       []string{"ollama", "--stop"},
			flag:       "start",
			wantFound:  false,
			wantRemain: []string{"ollama", "--stop"},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			found, rest := extractBoolFlag(c.args, c.flag)
			if found != c.wantFound {
				t.Errorf("found = %v, want %v", found, c.wantFound)
			}
			if len(rest) != len(c.wantRemain) {
				t.Errorf("rest = %v, want %v", rest, c.wantRemain)
				return
			}
			for i := range rest {
				if rest[i] != c.wantRemain[i] {
					t.Errorf("rest = %v, want %v", rest, c.wantRemain)
					break
				}
			}
		})
	}
}

func TestMarkArgParsing_BothOrders(t *testing.T) {
	// End-to-end regression for the exact bug reported live: `mark ollama
	// --start` (tool-name-then-flag, the documented and natural order)
	// must actually recognize --start, not silently leave both start and
	// stop false the way flag.Parse() did.
	for _, args := range [][]string{
		{"ollama", "--start"},
		{"--start", "ollama"},
	} {
		start, rest := extractBoolFlag(args, "start")
		stop, rest := extractBoolFlag(rest, "stop")
		if !start {
			t.Errorf("args=%v: expected start=true", args)
		}
		if stop {
			t.Errorf("args=%v: expected stop=false", args)
		}
		if start == stop {
			t.Errorf("args=%v: start and stop should not be equal here", args)
		}
		if len(rest) != 1 || rest[0] != "ollama" {
			t.Errorf("args=%v: rest = %v, want [ollama]", args, rest)
		}
	}
}
