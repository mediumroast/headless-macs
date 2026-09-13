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
