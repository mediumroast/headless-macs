package tui

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/mediumroast/headless-macs/internal/config"
)

// Messages sent back to the parent app.
type SavedMsg struct{ Cfg *config.Config }
type DiscardMsg struct{}

// TeardownPromptMsg is sent instead of saving directly when Save detects
// one or more tools' `enabled` flag transitioned true -> false — routes
// to a confirmation screen before anything touches disk, rather than
// silently doing nothing (the previous behavior) or silently tearing
// down a running daemon without asking. See issue #13.
type TeardownPromptMsg struct{ Tools []string }

type fieldKind int

const (
	kindSectionHeader fieldKind = iota
	kindToolHeader
	kindBool
	kindString
	kindInt
)

type field struct {
	kind    fieldKind
	label   string
	getBool func() bool
	setBool func(bool)
	getText func() string
	setText func(string)
}

func (f field) editable() bool {
	return f.kind == kindBool || f.kind == kindString || f.kind == kindInt
}

// ConfigEditorModel is the Bubble Tea model for the config editor screen.
type ConfigEditorModel struct {
	cfg             *config.Config
	origJSON        []byte
	fields          []field
	editableIndices []int // indices into fields that are editable
	cursorIdx       int   // index into editableIndices

	editing   bool
	textInput textinput.Model

	scroll int
	width  int
	height int

	// saveErr is set when a save was attempted but couldn't be verified
	// (write failed, or a read-back didn't match what was written) —
	// rendered instead of silently claiming [saved]. See issue #13.
	saveErr string
}

// NewConfigEditor creates a config editor model for the given config.
// A deep copy of cfg is taken so the editor has its own working copy.
func NewConfigEditor(cfg *config.Config) ConfigEditorModel {
	// Deep-copy via JSON round-trip
	data, _ := json.Marshal(cfg)
	var working config.Config
	_ = json.Unmarshal(data, &working)

	origJSON, _ := json.Marshal(&working)

	ti := textinput.New()
	ti.CharLimit = 256
	ti.Width = 40

	m := ConfigEditorModel{
		cfg:       &working,
		origJSON:  origJSON,
		textInput: ti,
	}
	m.fields = buildFields(m.cfg)
	for i, f := range m.fields {
		if f.editable() {
			m.editableIndices = append(m.editableIndices, i)
		}
	}
	return m
}

func (m ConfigEditorModel) Init() tea.Cmd { return nil }

func (m ConfigEditorModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case tea.KeyMsg:
		if m.editing {
			return m.updateEditing(msg)
		}
		return m.updateNavigating(msg)
	}
	return m, nil
}

func (m ConfigEditorModel) updateNavigating(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "up", "k":
		if m.cursorIdx > 0 {
			m.cursorIdx--
			m.ensureVisible()
		}
	case "down", "j":
		if m.cursorIdx < len(m.editableIndices)-1 {
			m.cursorIdx++
			m.ensureVisible()
		}
	case " ", "enter":
		f := m.currentField()
		if f == nil {
			break
		}
		if f.kind == kindBool {
			f.setBool(!f.getBool())
		} else {
			// Enter text edit mode
			m.textInput.SetValue(f.getText())
			m.textInput.Focus()
			m.textInput.CursorEnd()
			m.editing = true
		}
	case "s", "S":
		var orig config.Config
		_ = json.Unmarshal(m.origJSON, &orig)
		if disabled := disabledTools(&orig, m.cfg); len(disabled) > 0 {
			return m, func() tea.Msg { return TeardownPromptMsg{Tools: disabled} }
		}
		return m.doSave()
	case "r", "R":
		// Reset working copy to last saved state
		var orig config.Config
		if err := json.Unmarshal(m.origJSON, &orig); err == nil {
			m.cfg = &orig
			m.fields = buildFields(m.cfg)
		}
	case "q", "Q", "esc":
		return m, func() tea.Msg { return DiscardMsg{} }
	}
	return m, nil
}

func (m ConfigEditorModel) updateEditing(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		f := m.currentField()
		if f != nil {
			f.setText(m.textInput.Value())
		}
		m.textInput.Blur()
		m.editing = false
		return m, nil
	case "esc":
		m.textInput.Blur()
		m.editing = false
		return m, nil
	}
	var cmd tea.Cmd
	m.textInput, cmd = m.textInput.Update(msg)
	return m, cmd
}

func (m *ConfigEditorModel) ensureVisible() {
	visible := m.visibleRows()
	if visible <= 0 {
		return
	}
	// Row number of the cursor field in the full field list
	fieldRow := m.editableIndices[m.cursorIdx]
	if fieldRow < m.scroll {
		m.scroll = fieldRow
	} else if fieldRow >= m.scroll+visible {
		m.scroll = fieldRow - visible + 1
	}
}

func (m ConfigEditorModel) visibleRows() int {
	return m.height - 2 // cfgPath/state footer line(1) + padding(1)
}

func (m ConfigEditorModel) currentField() *field {
	if len(m.editableIndices) == 0 {
		return nil
	}
	idx := m.editableIndices[m.cursorIdx]
	return &m.fields[idx]
}

// disabledTools returns the config.json tool keys whose `enabled` flag
// transitioned from true (orig) to false (cur) — the trigger for routing
// through the teardown confirmation screen instead of saving directly.
func disabledTools(orig, cur *config.Config) []string {
	var out []string
	check := func(key string, was, is bool) {
		if was && !is {
			out = append(out, key)
		}
	}
	check("ollama", orig.Tools.Ollama.Enabled, cur.Tools.Ollama.Enabled)
	check("rapid_mlx", orig.Tools.RapidMLX.Enabled, cur.Tools.RapidMLX.Enabled)
	check("mlx_lm", orig.Tools.MLXLM.Enabled, cur.Tools.MLXLM.Enabled)
	check("infinity", orig.Tools.Infinity.Enabled, cur.Tools.Infinity.Enabled)
	check("exo", orig.Tools.Exo.Enabled, cur.Tools.Exo.Enabled)
	check("macmon", orig.Tools.Macmon.Enabled, cur.Tools.Macmon.Enabled)
	return out
}

// reenableTools restores Enabled=true for exactly the tool keys named —
// used when the teardown confirmation screen is cancelled, so the
// working copy doesn't silently keep a disabled state the operator just
// said not to apply. See issue #13.
func reenableTools(cfg *config.Config, tools []string) {
	for _, t := range tools {
		switch t {
		case "ollama":
			cfg.Tools.Ollama.Enabled = true
		case "rapid_mlx":
			cfg.Tools.RapidMLX.Enabled = true
		case "mlx_lm":
			cfg.Tools.MLXLM.Enabled = true
		case "infinity":
			cfg.Tools.Infinity.Enabled = true
		case "exo":
			cfg.Tools.Exo.Enabled = true
		case "macmon":
			cfg.Tools.Macmon.Enabled = true
		}
	}
}

// doSave writes the working config to disk and reads it back to confirm
// the write actually took effect, instead of trusting Save()'s nil error
// alone — surfaces a clear error in the UI (m.saveErr) rather than
// silently claiming [saved] when something went wrong. See issue #13.
func (m ConfigEditorModel) doSave() (tea.Model, tea.Cmd) {
	m.saveErr = ""
	if err := config.Save(m.cfg); err != nil {
		m.saveErr = "save failed: " + err.Error()
		return m, nil
	}
	reloaded, err := config.Load()
	wantJSON, _ := json.Marshal(m.cfg)
	gotJSON, _ := json.Marshal(reloaded)
	if err != nil || string(wantJSON) != string(gotJSON) {
		m.saveErr = "save did not verify — re-read config did not match what was written"
		return m, nil
	}
	m.origJSON, _ = json.Marshal(m.cfg)
	return m, func() tea.Msg { return SavedMsg{Cfg: m.cfg} }
}

func (m ConfigEditorModel) isModified() bool {
	cur, _ := json.Marshal(m.cfg)
	return string(cur) != string(m.origJSON)
}

// StatusHints is the shell-level status bar's content while this screen
// is the active content pane. The cfgPath/[modified]/[saved] indicator is
// NOT here — it's the last line of Body() instead, since it's specific
// information about this screen's state, not a generic key hint like
// every other screen's status bar content.
func (m ConfigEditorModel) StatusHints() string {
	return strings.Join([]string{
		hint("s", "save"),
		hint("r", "reset"),
		hint("q", "cancel"),
		hint("↑↓", "navigate"),
		hint("enter", "edit"),
		hint("space", "toggle"),
	}, statusGap())
}

// View reassembles a full-screen render for tea.Model conformance — not
// how the shell actually renders this screen (it calls Body()/
// StatusHints() directly).
func (m ConfigEditorModel) View() string {
	if m.width == 0 {
		return "loading..."
	}
	var b strings.Builder
	b.WriteString(styleTitle.Render(fmt.Sprintf(" headless-macs v%s — Configuration Editor ", Version)))
	b.WriteByte('\n')
	b.WriteString(styleDivider.Render(strings.Repeat("─", m.width)))
	b.WriteByte('\n')
	b.WriteString(m.Body())
	b.WriteString(styleStatusBar.Render(m.StatusHints()))
	return b.String()
}

// Body renders the scrollable field list plus the cfgPath/state footer
// line, using its own stored width/height (set via the content-pane-sized
// WindowSizeMsg the shell sends it — see app.go).
func (m ConfigEditorModel) Body() string {
	if m.width == 0 {
		return "loading..."
	}
	var b strings.Builder

	rows := m.renderRows()
	visible := m.visibleRows()
	if visible < 0 {
		visible = 0
	}
	end := m.scroll + visible
	if end > len(rows) {
		end = len(rows)
	}
	start := m.scroll
	if start > end {
		start = end
	}
	for _, row := range rows[start:end] {
		b.WriteString(row)
		b.WriteByte('\n')
	}
	rendered := end - start
	for i := rendered; i < visible; i++ {
		b.WriteByte('\n')
	}

	cfgPath := config.UserConfigPath()
	var stateStr string
	switch {
	case m.saveErr != "":
		stateStr = styleError.Render("[save failed: " + m.saveErr + "]")
	case m.isModified():
		stateStr = styleStatusModified.Render("[modified]")
	default:
		stateStr = styleStatusSaved.Render("[saved]")
	}
	b.WriteString(styleFieldValue.Render("  "+cfgPath+"  ") + stateStr)

	return b.String()
}

func (m ConfigEditorModel) renderRows() []string {
	rows := make([]string, 0, len(m.fields))
	editIdx := 0 // tracks position within editableIndices

	// Build a reverse map: field index → cursor position (if editable)
	fieldToCursor := make(map[int]int)
	for ci, fi := range m.editableIndices {
		fieldToCursor[fi] = ci
	}

	for fi, f := range m.fields {
		cursorPos, isEditable := fieldToCursor[fi]
		selected := isEditable && cursorPos == m.cursorIdx
		_ = editIdx

		switch f.kind {
		case kindSectionHeader:
			rows = append(rows, "  "+styleSectionHeader.Render(f.label))
		case kindToolHeader:
			line := styleToolHeader.Render("  ── " + f.label + " " + strings.Repeat("─", max(0, m.width-8-len(f.label))))
			rows = append(rows, line)
		case kindBool:
			rows = append(rows, m.renderBoolRow(f, selected))
		case kindString, kindInt:
			rows = append(rows, m.renderTextRow(f, fi, selected))
		}
	}
	return rows
}

func (m ConfigEditorModel) renderBoolRow(f field, selected bool) string {
	check := "[ ]"
	if f.getBool() {
		check = "[✓]"
	}
	if selected {
		return styleCursor.Render("▶ ") + styleSelectedLabel.Render(f.label) + styleSelectedValue.Render(" "+check)
	}
	return "  " + styleFieldLabel.Render(f.label) + styleFieldValue.Render(" "+check)
}

func (m ConfigEditorModel) renderTextRow(f field, fi int, selected bool) string {
	// If this field is currently being edited, show the textinput inline
	if selected && m.editing {
		label := styleCursor.Render("▶ ") + styleSelectedLabel.Render(f.label) + styleSelectedValue.Render(" ")
		return label + m.textInput.View()
	}

	val := f.getText()
	origVal := m.origValue(fi)
	modified := val != origVal

	if selected {
		var valStyle lipgloss.Style
		if modified {
			valStyle = styleSelectedModified
		} else {
			valStyle = styleSelectedValue
		}
		return styleCursor.Render("▶ ") + styleSelectedLabel.Render(f.label) + valStyle.Render(" "+val)
	}
	var valStyle lipgloss.Style
	if modified {
		valStyle = styleFieldModified
	} else {
		valStyle = styleFieldValue
	}
	return "  " + styleFieldLabel.Render(f.label) + valStyle.Render(" "+val)
}

// origValue returns the value from the original (pre-edit) config for field at index fi.
// We compare by re-parsing origJSON and walking the same field index.
func (m ConfigEditorModel) origValue(fi int) string {
	var orig config.Config
	if err := json.Unmarshal(m.origJSON, &orig); err != nil {
		return ""
	}
	origFields := buildFields(&orig)
	if fi >= len(origFields) {
		return ""
	}
	return origFields[fi].getText()
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// buildFields constructs the ordered list of form rows from a Config pointer.
// Closures capture cfg directly — changes to the returned fields mutate cfg.
func buildFields(cfg *config.Config) []field {
	f := []field{}

	// ── TOOLS ────────────────────────────────────────────────
	f = append(f, field{kind: kindSectionHeader, label: "TOOLS"})

	f = append(f, field{kind: kindToolHeader, label: "Ollama"})
	f = append(f, boolField("Enabled", func() bool { return cfg.Tools.Ollama.Enabled }, func(v bool) { cfg.Tools.Ollama.Enabled = v }))
	f = append(f, strField("Host", func() string { return cfg.Tools.Ollama.Host }, func(v string) { cfg.Tools.Ollama.Host = v }))
	f = append(f, strField("Models Dir", func() string { return cfg.Tools.Ollama.ModelsDir }, func(v string) { cfg.Tools.Ollama.ModelsDir = v }))
	f = append(f, intField("Keep Alive (sec)", func() int { return cfg.Tools.Ollama.KeepAlive }, func(v int) { cfg.Tools.Ollama.KeepAlive = v }))
	f = append(f, boolField("Flash Attention", func() bool { return cfg.Tools.Ollama.FlashAttention }, func(v bool) { cfg.Tools.Ollama.FlashAttention = v }))
	f = append(f, intField("GPU Percent", func() int { return cfg.Tools.Ollama.GPUPercent }, func(v int) { cfg.Tools.Ollama.GPUPercent = v }))
	f = append(f, strField("Log Level", func() string { return cfg.Tools.Ollama.LogLevel }, func(v string) { cfg.Tools.Ollama.LogLevel = v }))

	f = append(f, field{kind: kindToolHeader, label: "Rapid-MLX"})
	f = append(f, boolField("Enabled", func() bool { return cfg.Tools.RapidMLX.Enabled }, func(v bool) { cfg.Tools.RapidMLX.Enabled = v }))
	f = append(f, strField("Host", func() string { return cfg.Tools.RapidMLX.Host }, func(v string) { cfg.Tools.RapidMLX.Host = v }))
	f = append(f, intField("Port", func() int { return cfg.Tools.RapidMLX.Port }, func(v int) { cfg.Tools.RapidMLX.Port = v }))
	f = append(f, strField("Model", func() string { return cfg.Tools.RapidMLX.Model }, func(v string) { cfg.Tools.RapidMLX.Model = v }))
	f = append(f, strField("Cache Dir", func() string { return cfg.Tools.RapidMLX.CacheDir }, func(v string) { cfg.Tools.RapidMLX.CacheDir = v }))
	f = append(f, intField("Prefill Step Size", func() int { return cfg.Tools.RapidMLX.PrefillStep }, func(v int) { cfg.Tools.RapidMLX.PrefillStep = v }))
	f = append(f, boolField("No Thinking", func() bool { return cfg.Tools.RapidMLX.NoThinking }, func(v bool) { cfg.Tools.RapidMLX.NoThinking = v }))

	f = append(f, field{kind: kindToolHeader, label: "mlx-lm"})
	f = append(f, boolField("Enabled", func() bool { return cfg.Tools.MLXLM.Enabled }, func(v bool) { cfg.Tools.MLXLM.Enabled = v }))
	f = append(f, strField("Host", func() string { return cfg.Tools.MLXLM.Host }, func(v string) { cfg.Tools.MLXLM.Host = v }))
	f = append(f, intField("Port", func() int { return cfg.Tools.MLXLM.Port }, func(v int) { cfg.Tools.MLXLM.Port = v }))
	f = append(f, strField("Model Path", func() string { return cfg.Tools.MLXLM.ModelPath }, func(v string) { cfg.Tools.MLXLM.ModelPath = v }))
	f = append(f, strField("Default Model", func() string { return cfg.Tools.MLXLM.DefaultModel }, func(v string) { cfg.Tools.MLXLM.DefaultModel = v }))

	f = append(f, field{kind: kindToolHeader, label: "Infinity"})
	f = append(f, boolField("Enabled", func() bool { return cfg.Tools.Infinity.Enabled }, func(v bool) { cfg.Tools.Infinity.Enabled = v }))
	f = append(f, strField("Host", func() string { return cfg.Tools.Infinity.Host }, func(v string) { cfg.Tools.Infinity.Host = v }))
	f = append(f, intField("Port", func() int { return cfg.Tools.Infinity.Port }, func(v int) { cfg.Tools.Infinity.Port = v }))
	f = append(f, strField("Model", func() string { return cfg.Tools.Infinity.Model }, func(v string) { cfg.Tools.Infinity.Model = v }))
	f = append(f, strField("Engine", func() string { return cfg.Tools.Infinity.Engine }, func(v string) { cfg.Tools.Infinity.Engine = v }))

	f = append(f, field{kind: kindToolHeader, label: "Exo"})
	f = append(f, boolField("Enabled", func() bool { return cfg.Tools.Exo.Enabled }, func(v bool) { cfg.Tools.Exo.Enabled = v }))
	f = append(f, intField("ChatGPT API Port", func() int { return cfg.Tools.Exo.ChatGPTAPIPort }, func(v int) { cfg.Tools.Exo.ChatGPTAPIPort = v }))
	f = append(f, strField("Bootstrap Peers (comma-separated)",
		func() string { return strings.Join(cfg.Tools.Exo.BootstrapPeers, ",") },
		func(v string) {
			if v == "" {
				cfg.Tools.Exo.BootstrapPeers = nil
				return
			}
			cfg.Tools.Exo.BootstrapPeers = strings.Split(v, ",")
		}))

	f = append(f, field{kind: kindToolHeader, label: "macmon"})
	f = append(f, boolField("Enabled", func() bool { return cfg.Tools.Macmon.Enabled }, func(v bool) { cfg.Tools.Macmon.Enabled = v }))
	f = append(f, intField("Port", func() int { return cfg.Tools.Macmon.Port }, func(v int) { cfg.Tools.Macmon.Port = v }))
	f = append(f, intField("Interval (ms)", func() int { return cfg.Tools.Macmon.IntervalMs }, func(v int) { cfg.Tools.Macmon.IntervalMs = v }))

	// ── STORAGE ───────────────────────────────────────────────
	f = append(f, field{kind: kindSectionHeader, label: "STORAGE"})
	f = append(f, boolField("Use External Volume", func() bool { return cfg.Storage.UseExternalVolume }, func(v bool) { cfg.Storage.UseExternalVolume = v }))
	f = append(f, strField("Volume Label", func() string { return cfg.Storage.VolumeLabel }, func(v string) { cfg.Storage.VolumeLabel = v }))
	f = append(f, strField("Volume Mount Point", func() string { return cfg.Storage.VolumeMountPoint }, func(v string) { cfg.Storage.VolumeMountPoint = v }))
	f = append(f, strField("Models Subdir", func() string { return cfg.Storage.ModelsSubdir }, func(v string) { cfg.Storage.ModelsSubdir = v }))
	f = append(f, boolField("Auto Detect Volume", func() bool { return cfg.Storage.AutoDetectVolume }, func(v bool) { cfg.Storage.AutoDetectVolume = v }))
	f = append(f, intField("Min Free GB", func() int { return cfg.Storage.MinFreeGB }, func(v int) { cfg.Storage.MinFreeGB = v }))
	f = append(f, boolField("Symlink Internal Paths", func() bool { return cfg.Storage.SymlinkInternalPaths }, func(v bool) { cfg.Storage.SymlinkInternalPaths = v }))

	// ── SYSTEM ────────────────────────────────────────────────
	f = append(f, field{kind: kindSectionHeader, label: "SYSTEM"})
	f = append(f, boolField("Disable Spotlight", func() bool { return cfg.System.DisableSpotlight }, func(v bool) { cfg.System.DisableSpotlight = v }))
	f = append(f, boolField("Disable Software Update", func() bool { return cfg.System.DisableSoftwareUpdate }, func(v bool) { cfg.System.DisableSoftwareUpdate = v }))
	f = append(f, boolField("Disable Time Machine", func() bool { return cfg.System.DisableTimeMachine }, func(v bool) { cfg.System.DisableTimeMachine = v }))
	f = append(f, boolField("Disable iCloud", func() bool { return cfg.System.DisableICloud }, func(v bool) { cfg.System.DisableICloud = v }))
	f = append(f, boolField("Disable AirDrop/Handoff", func() bool { return cfg.System.DisableAirdropHandoff }, func(v bool) { cfg.System.DisableAirdropHandoff = v }))
	f = append(f, boolField("Disable Notifications", func() bool { return cfg.System.DisableNotifications }, func(v bool) { cfg.System.DisableNotifications = v }))
	f = append(f, boolField("Disable Telemetry", func() bool { return cfg.System.DisableTelemetry }, func(v bool) { cfg.System.DisableTelemetry = v }))
	f = append(f, boolField("Disable Siri", func() bool { return cfg.System.DisableSiri }, func(v bool) { cfg.System.DisableSiri = v }))
	f = append(f, boolField("Disable Media Services", func() bool { return cfg.System.DisableMediaServices }, func(v bool) { cfg.System.DisableMediaServices = v }))
	f = append(f, boolField("Network Tuning", func() bool { return cfg.System.NetworkTuning }, func(v bool) { cfg.System.NetworkTuning = v }))

	// ── NETWORK ───────────────────────────────────────────────
	f = append(f, field{kind: kindSectionHeader, label: "NETWORK"})
	f = append(f, boolField("Localhost Only", func() bool { return cfg.Network.LocalhostOnly }, func(v bool) { cfg.Network.LocalhostOnly = v }))
	f = append(f, boolField("Disable Firewall", func() bool { return cfg.Network.DisableFirewall }, func(v bool) { cfg.Network.DisableFirewall = v }))

	// ── TUI ───────────────────────────────────────────────────
	f = append(f, field{kind: kindSectionHeader, label: "TUI"})
	f = append(f, intField("Dashboard Refresh (ms)", func() int { return cfg.TUI.DashboardRefreshMs }, func(v int) { cfg.TUI.DashboardRefreshMs = v }))

	return f
}

func boolField(label string, get func() bool, set func(bool)) field {
	return field{kind: kindBool, label: label, getBool: get, setBool: set}
}

func strField(label string, get func() string, set func(string)) field {
	return field{kind: kindString, label: label, getText: get, setText: set}
}

func intField(label string, get func() int, set func(int)) field {
	return field{
		kind:    kindInt,
		label:   label,
		getText: func() string { return strconv.Itoa(get()) },
		setText: func(v string) {
			if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
				set(n)
			}
		},
	}
}
