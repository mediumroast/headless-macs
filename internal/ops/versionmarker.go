package ops

// versionmarker.go — records which headless-macs version last successfully
// ran Baseline/Install Tools, so the Dashboard and CLI can tell an operator
// "you're running different code than what configured this box" instead of
// leaving that silently undiscoverable. See PHASE_10_PLAN.md, Phase 10G.

import (
	"encoding/json"
	"os"
	"time"
)

const versionMarkerPath = "/var/log/mac-llm-setup/.last-configured-version"

// Version is set once by main.go (mirroring tui.Version) so ops functions
// that need to know "what version is this binary" don't need it threaded
// through every call signature.
var Version = "dev"

// VersionMarker is what gets written after a successful Baseline/Install
// Tools run.
type VersionMarker struct {
	Version   string    `json:"version"`
	Timestamp time.Time `json:"timestamp"`
}

// WriteVersionMarker records the current binary's version as having just
// configured this box. Best-effort: a write failure here should never fail
// the caller's overall run, so this returns nothing for callers to check —
// matches how other bookkeeping (snapshots, logs) in this project behaves.
func WriteVersionMarker() {
	_ = os.MkdirAll("/var/log/mac-llm-setup", 0755)
	m := VersionMarker{Version: Version, Timestamp: time.Now().UTC()}
	data, err := json.Marshal(&m)
	if err != nil {
		return
	}
	_ = os.WriteFile(versionMarkerPath, data, 0644)
}

// ReadVersionMarker reads the marker, if any. Returns nil, nil when it's
// absent — an existing install that predates Phase 10, or one that's never
// had Baseline/Install Tools run under this code yet.
func ReadVersionMarker() (*VersionMarker, error) {
	data, err := os.ReadFile(versionMarkerPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var m VersionMarker
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

// VersionMismatch reports whether the running binary's version differs
// from what last configured this box — the condition the Dashboard/CLI
// nudge fires on. Marker absent counts as a mismatch (an existing
// pre-Phase-10 install has never recorded anything).
func VersionMismatch() (mismatched bool, marker *VersionMarker) {
	m, err := ReadVersionMarker()
	if err != nil || m == nil {
		return true, m
	}
	return m.Version != Version, m
}
