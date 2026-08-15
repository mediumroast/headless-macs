// Package log provides structured output helpers matching the headless-macs
// prefix convention: [SET], [SKIP], [WARN], [FAIL], [PASS], [OK], [INFO], etc.
// All output is written to both stdout and a timestamped log file.
package log

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

var out io.Writer = os.Stdout

// Init creates the log file. In TUI mode (tuiMode=true) output goes only to
// the file; in CLI mode it tees to stdout as well. Returns the log file path.
func Init(scriptName string) (string, error) {
	return InitMode(scriptName, false)
}

// InitTUI is like Init but suppresses stdout — for use inside the Bubble Tea TUI
// where writing to stdout trashes the alt-screen renderer.
func InitTUI(scriptName string) (string, error) {
	return InitMode(scriptName, true)
}

func InitMode(scriptName string, tuiMode bool) (string, error) {
	logDir := "/var/log/mac-llm-setup"
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		logDir = "/tmp"
	}
	logPath := filepath.Join(logDir, fmt.Sprintf("%s-%s.log", scriptName, time.Now().Format("20060102-150405")))
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return "", err
	}
	if tuiMode {
		out = f
	} else {
		out = io.MultiWriter(os.Stdout, f)
	}
	fmt.Fprintf(out, "=== %s started at %s ===\n", scriptName, time.Now().Format(time.RFC1123))
	return logPath, nil
}

func Set(msg string)    { fmt.Fprintf(out, "[SET]  %s\n", msg) }
func Skip(msg string)   { fmt.Fprintf(out, "[SKIP] %s\n", msg) }
func Warn(msg string)   { fmt.Fprintf(out, "[WARN] %s\n", msg) }
func Fail(msg string)   { fmt.Fprintf(out, "[FAIL] %s\n", msg) }
func Pass(msg string)   { fmt.Fprintf(out, "[PASS] %s\n", msg) }
func OK(msg string)     { fmt.Fprintf(out, "[OK]   %s\n", msg) }
func Info(msg string)   { fmt.Fprintf(out, "[INFO] %s\n", msg) }
func Notice(msg string) { fmt.Fprintf(out, "[NOTICE] %s\n", msg) }
func Backup(msg string) { fmt.Fprintf(out, "[BACKUP] %s\n", msg) }
func Detail(msg string) { fmt.Fprintf(out, "       %s\n", msg) }
