package ops

// storage.go — external volume setup (ports storage-volume.sh).
//
// Configures an APFS external drive as model storage, creates directory layout,
// excludes from Spotlight, wires /Library symlinks, adds fstab entry, and
// installs the com.llm-server.storage-mount LaunchDaemon.
//
// Must run as root. Idempotent.

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mediumroast/headless-macs/internal/config"
	ilog "github.com/mediumroast/headless-macs/internal/log"
)

// StorageResult mirrors BaselineResult — same action/status model, separate type
// so TUI can distinguish "storage done" from "baseline done".
type StorageResult struct {
	Actions  []BaselineAction
	Sets     int
	Skips    int
	Warnings int
	Failures int
	LogPath  string
	// Populated on success for downstream consumers
	VolumeMount string
	ModelRoot   string
}

func (r *StorageResult) add(section string, status ActionStatus, msg, detail string) {
	r.Actions = append(r.Actions, BaselineAction{
		Section: section,
		Status:  status,
		Message: msg,
		Detail:  detail,
	})
	switch status {
	case ActionSet:
		r.Sets++
		ilog.Set(msg)
	case ActionSkip:
		r.Skips++
		ilog.Skip(msg)
	case ActionWarn:
		r.Warnings++
		ilog.Warn(msg)
	case ActionFail:
		r.Failures++
		ilog.Fail(msg)
	default:
		ilog.Info(msg)
	}
	if detail != "" {
		ilog.Detail(detail)
	}
}

// ---------------------------------------------------------------------------
// Entry point
// ---------------------------------------------------------------------------

// RunStorage configures the external volume. Must run as root.
// Returns a non-nil result with an informational action when the feature is
// disabled in config (no error, no changes).
func RunStorage(cfg *config.Config) (*StorageResult, error) {
	if os.Getuid() != 0 {
		return nil, fmt.Errorf("storage setup requires root — run as: sudo headless-macs")
	}

	r := &StorageResult{}

	// Early exit before logging if the feature is disabled.
	if !cfg.Storage.UseExternalVolume {
		r.add("CONFIG", ActionSkip,
			"storage.use_external_volume is false — nothing to do",
			"Set storage.use_external_volume: true and storage.volume_label in config to enable")
		return r, nil
	}

	logPath, err := ilog.InitTUI("storage-volume")
	if err != nil {
		logPath = "/tmp/headless-macs-storage.log"
	}
	r.LogPath = logPath

	ilog.Info(fmt.Sprintf("headless-macs 2.0.0 — storage setup started at %s", time.Now().Format(time.RFC1123)))

	volumeLabel := cfg.Storage.VolumeLabel
	modelsSubdir := cfg.Storage.ModelsSubdir
	if modelsSubdir == "" {
		modelsSubdir = "LLMModels"
	}
	minFreeGB := cfg.Storage.MinFreeGB
	if minFreeGB == 0 {
		minFreeGB = 100
	}
	symlinkInternal := cfg.Storage.SymlinkInternalPaths

	r.add("CONFIG", ActionInfo, fmt.Sprintf("volume_label: %q  models_subdir: %q  min_free_gb: %d  symlink_internal: %v",
		volumeLabel, modelsSubdir, minFreeGB, symlinkInternal), "")

	// Run sections in order; abort on fatal errors.
	volMount, volDevice, volUUID, err := r.sectionLocate(volumeLabel)
	if err != nil {
		r.add("LOCATE", ActionFail, err.Error(), "")
		return r, nil
	}

	if err := r.sectionValidate(volMount, &volDevice, &volUUID, minFreeGB); err != nil {
		r.add("VALIDATE", ActionFail, err.Error(), "")
		return r, nil
	}

	modelRoot := filepath.Join(volMount, modelsSubdir)
	r.VolumeMount = volMount
	r.ModelRoot = modelRoot

	r.sectionDirectories(modelRoot)
	r.sectionSpotlight(volMount)

	ollamaVolDir := filepath.Join(modelRoot, "ollama")
	rapidMLXVolDir := filepath.Join(modelRoot, "rapid-mlx")
	mlxVolDir := filepath.Join(modelRoot, "mlx-lm")
	infinityVolDir := filepath.Join(modelRoot, "infinity")

	if symlinkInternal {
		r.sectionSymlinks(ollamaVolDir, rapidMLXVolDir, mlxVolDir, infinityVolDir)
	} else {
		r.add("SYMLINKS", ActionSkip,
			"Symlink wiring disabled (storage.symlink_internal_paths: false)",
			"install-tools.sh will use volume paths from config")
	}

	r.sectionFstab(volMount, volDevice, volUUID, volumeLabel)
	r.sectionStorageDaemon(volMount, volDevice)

	if !symlinkInternal {
		r.sectionUpdateConfig(cfg, ollamaVolDir, mlxVolDir, volMount)
	}

	r.sectionUpdatePrecheckJSON(volMount, modelRoot)
	r.sectionVerify(modelRoot, volMount, volUUID, volumeLabel, symlinkInternal,
		ollamaVolDir, rapidMLXVolDir, mlxVolDir)

	ilog.Info(fmt.Sprintf("Log written to: %s", logPath))
	return r, nil
}

// ---------------------------------------------------------------------------
// Section 1: Locate volume
// ---------------------------------------------------------------------------

func (r *StorageResult) sectionLocate(label string) (mount, device, uuid string, err error) {
	expectedMount := "/Volumes/" + label

	// Fast path: precheck JSON cache
	precheckJSON := "/tmp/mac-llm-precheck.json"
	if data, readErr := os.ReadFile(precheckJSON); readErr == nil {
		var pc map[string]interface{}
		if json.Unmarshal(data, &pc) == nil {
			if st, ok := pc["storage"].(map[string]interface{}); ok {
				if labelFound, _ := st["external_volume_label_found"].(bool); labelFound {
					if cachedMount, _ := st["external_volume_mount"].(string); cachedMount != "" {
						if info, statErr := os.Stat(cachedMount); statErr == nil && info.IsDir() {
							r.add("LOCATE", ActionInfo, "Volume found via precheck cache: "+cachedMount, "")
							mount = cachedMount
						}
					}
				}
			}
		}
	}

	if mount == "" {
		// Live lookup
		info := diskutilInfo(label)
		device = extractDiskutilField(info, "Device Identifier")
		uuid = extractDiskutilField(info, "Volume UUID")
		mount = extractDiskutilField(info, "Mount Point")

		unmounted := mount == "" || mount == "Not mounted" || mount == "(null)"
		if device != "" && unmounted {
			r.add("LOCATE", ActionInfo, fmt.Sprintf("Volume %q found as %s but not mounted — mounting…", label, device), "")
			if mkErr := os.MkdirAll(expectedMount, 0755); mkErr == nil {
				_ = sudoRun("diskutil", "mount", "-mountPoint", expectedMount, device)
				if info2, statErr := os.Stat(expectedMount); statErr == nil && info2.IsDir() {
					mount = expectedMount
					r.add("LOCATE", ActionSet, fmt.Sprintf("Mounted %q at %s", label, mount), "")
				}
			}
		}
	}

	if mount == "" || mount == "Not mounted" || mount == "(null)" {
		if info2, statErr := os.Stat(expectedMount); statErr != nil || !info2.IsDir() {
			err = fmt.Errorf("volume %q not found or not mounted — connect the drive and re-run", label)
			return
		}
		mount = expectedMount
	}

	// Ensure device/uuid are populated for later sections
	if device == "" || uuid == "" {
		info := diskutilInfo(mount)
		if device == "" {
			device = extractDiskutilField(info, "Device Identifier")
		}
		if uuid == "" {
			uuid = extractDiskutilField(info, "Volume UUID")
		}
	}

	r.add("LOCATE", ActionInfo, fmt.Sprintf("Volume %q mounted at %s", label, mount), "")
	return
}

// ---------------------------------------------------------------------------
// Section 2: Validate
// ---------------------------------------------------------------------------

func (r *StorageResult) sectionValidate(mount string, device, uuid *string, minFreeGB int) error {
	// Refresh device/uuid if missing
	if *device == "" || *uuid == "" {
		info := diskutilInfo(mount)
		if *device == "" {
			*device = extractDiskutilField(info, "Device Identifier")
		}
		if *uuid == "" {
			*uuid = extractDiskutilField(info, "Volume UUID")
		}
	}

	// Free space
	freeGB, totalGB := volumeDF(mount)
	r.add("VALIDATE", ActionInfo, fmt.Sprintf("Volume: %dGB total, %dGB free", totalGB, freeGB), "")
	if freeGB < minFreeGB {
		return fmt.Errorf("only %dGB free on volume (minimum %dGB — set storage.min_free_gb to change)", freeGB, minFreeGB)
	}
	r.add("VALIDATE", ActionInfo, fmt.Sprintf("Free space: %dGB ≥ %dGB minimum", freeGB, minFreeGB), "")

	// Filesystem type
	fsOut := diskutilInfo(mount)
	fsType := strings.ToLower(extractDiskutilField(fsOut, "Type (Bundle)"))
	if fsType == "" {
		fsType = strings.ToLower(extractDiskutilField(fsOut, "File System Personality"))
	}
	for _, bad := range []string{"exfat", "fat32", "msdos", "ntfs"} {
		if strings.Contains(fsType, bad) {
			return fmt.Errorf("filesystem %q does not support Unix permissions — reformat as APFS", fsType)
		}
	}
	r.add("VALIDATE", ActionInfo, fmt.Sprintf("Filesystem: %s (permissions supported)", fsType), "")

	// Ownership
	ownersOut := extractDiskutilField(fsOut, "Owners")
	if strings.EqualFold(ownersOut, "Enabled") {
		r.add("VALIDATE", ActionSkip, "Ownership already enabled on "+mount, "")
	} else {
		if err := sudoRun("diskutil", "enableOwnership", mount); err != nil {
			r.add("VALIDATE", ActionWarn, "Could not enable ownership on "+mount, err.Error())
		} else {
			r.add("VALIDATE", ActionSet, "Ownership enabled on "+mount, "")
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Section 3: Directory layout
// ---------------------------------------------------------------------------

func (r *StorageResult) sectionDirectories(modelRoot string) {
	dirs := []string{
		filepath.Join(modelRoot, "ollama"),
		filepath.Join(modelRoot, "rapid-mlx"),
		filepath.Join(modelRoot, "mlx-lm"),
		filepath.Join(modelRoot, "infinity"),
		filepath.Join(modelRoot, "exo"),
		filepath.Join(modelRoot, "gguf"),
	}

	for _, d := range dirs {
		if info, err := os.Stat(d); err == nil && info.IsDir() {
			r.add("DIRECTORIES", ActionSkip, d+" (exists)", "")
		} else {
			if err := sudoMkdirAll(d, 0755); err != nil {
				r.add("DIRECTORIES", ActionFail, "Could not create "+d, err.Error())
			} else {
				r.add("DIRECTORIES", ActionSet, d, "")
			}
		}
	}

	// Ownership
	if userExists("_llmserver") {
		_ = sudoRun("chown", "-R", "_llmserver:_llmserver", modelRoot)
		r.add("DIRECTORIES", ActionSet, "Ownership: _llmserver:_llmserver", "")
	} else {
		_ = sudoRun("chown", "-R", "root:wheel", modelRoot)
		r.add("DIRECTORIES", ActionWarn,
			"_llmserver account not found; using root:wheel until install-tools creates it", "")
	}
	_ = sudoRun("chmod", "-R", "755", modelRoot)
	r.add("DIRECTORIES", ActionInfo, "Permissions: 755", "")
}

// ---------------------------------------------------------------------------
// Section 4: Spotlight exclusion
// ---------------------------------------------------------------------------

func (r *StorageResult) sectionSpotlight(mount string) {
	if err := sudoRun("mdutil", "-i", "off", mount); err != nil {
		r.add("SPOTLIGHT", ActionWarn, "Could not disable Spotlight on "+mount, err.Error())
	} else {
		r.add("SPOTLIGHT", ActionSet, "Spotlight indexing disabled on "+mount, "")
	}
	// Erase any existing index (best-effort)
	_ = sudoRun("mdutil", "-E", mount)

	// Belt-and-suspenders marker file
	marker := filepath.Join(mount, ".metadata_never_index")
	if _, err := os.Stat(marker); os.IsNotExist(err) {
		if f, err := os.Create(marker); err == nil {
			f.Close()
			r.add("SPOTLIGHT", ActionSet, ".metadata_never_index marker placed", "")
		}
	}
}

// ---------------------------------------------------------------------------
// Section 5: Symlink wiring
// ---------------------------------------------------------------------------

func (r *StorageResult) sectionSymlinks(ollamaDir, rapidMLXDir, mlxDir, infinityDir string) {
	links := []struct {
		internal string
		target   string
		label    string
	}{
		{"/Library/Ollama/models", ollamaDir, "Ollama models"},
		{"/Library/RapidMLX/cache", rapidMLXDir, "Rapid-MLX cache"},
		{"/Library/MLX/models", mlxDir, "mlx-lm models"},
		{"/Library/Infinity", infinityDir, "Infinity models"},
	}

	for _, lk := range links {
		r.wireSymlink(lk.internal, lk.target, lk.label)
	}

	r.add("SYMLINKS", ActionInfo,
		"/Library/Ollama/models → "+ollamaDir+
			" | /Library/RapidMLX/cache → "+rapidMLXDir+
			" | /Library/MLX/models → "+mlxDir+
			" | /Library/Infinity → "+infinityDir, "")
}

func (r *StorageResult) wireSymlink(internal, target, label string) {
	fi, err := os.Lstat(internal)

	if err == nil && fi.Mode()&os.ModeSymlink == 0 {
		// Real directory — migrate and remove
		r.add("SYMLINKS", ActionInfo, fmt.Sprintf("Migrating existing %s from %s → %s", label, internal, target), "")
		_ = sudoRun("rsync", "-a", "--remove-source-files", internal+"/", target+"/")
		_ = sudoRun("rm", "-rf", internal)
	}

	// Ensure parent exists
	_ = sudoMkdirAll(filepath.Dir(internal), 0755)

	current, readErr := os.Readlink(internal)
	if readErr == nil {
		if current == target {
			r.add("SYMLINKS", ActionSkip, fmt.Sprintf("%s → %s (already correct)", internal, target), "")
		} else {
			_ = sudoRun("ln", "-sf", target, internal)
			r.add("SYMLINKS", ActionSet, fmt.Sprintf("%s → %s  (was: %s)", internal, target, current), "")
		}
	} else {
		_ = sudoRun("ln", "-s", target, internal)
		r.add("SYMLINKS", ActionSet, fmt.Sprintf("%s → %s", internal, target), "")
	}
}

// ---------------------------------------------------------------------------
// Section 6: fstab + storage-mount LaunchDaemon
// ---------------------------------------------------------------------------

const storageMountPlistPath = "/Library/LaunchDaemons/com.llm-server.storage-mount.plist"

func (r *StorageResult) sectionFstab(mount, device, uuid, label string) {
	if uuid == "" {
		r.add("FSTAB", ActionWarn, "Could not determine UUID for "+mount+" — auto-mount at boot not configured",
			"Try: diskutil info '"+label+"' | grep UUID")
		return
	}
	r.add("FSTAB", ActionInfo, "Volume UUID: "+uuid, "")

	fstabEntry := fmt.Sprintf("UUID=%s %s apfs rw,auto,nobrowse 0 0", uuid, mount)
	fstab, err := os.ReadFile("/etc/fstab")
	if err == nil && strings.Contains(string(fstab), uuid) {
		r.add("FSTAB", ActionSkip, "fstab entry for "+label+" already present", "")
		return
	}

	// Append entry
	f, err := os.OpenFile("/etc/fstab", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		r.add("FSTAB", ActionWarn, "Could not write /etc/fstab: "+err.Error(), "")
		return
	}
	defer f.Close()
	_, _ = fmt.Fprintln(f, fstabEntry)
	r.add("FSTAB", ActionSet, "fstab entry added: "+fstabEntry, "")
	r.add("FSTAB", ActionInfo, "Volume will auto-mount at boot before LaunchDaemons start", "")
}

func (r *StorageResult) sectionStorageDaemon(mount, device string) {
	if device == "" {
		r.add("DAEMON", ActionWarn, "Could not determine device identifier — skipping storage-mount daemon", "")
		return
	}

	plistContent := storageMountPlist(mount, device)

	if err := os.WriteFile(storageMountPlistPath, []byte(plistContent), 0644); err != nil {
		r.add("DAEMON", ActionFail, "Could not write storage-mount plist: "+err.Error(), "")
		return
	}
	_ = sudoRun("chown", "root:wheel", storageMountPlistPath)
	_ = sudoRun("chmod", "644", storageMountPlistPath)

	// Reload: bootout first (ignore error if not loaded), then bootstrap
	_ = sudoRun("launchctl", "bootout", "system", storageMountPlistPath)
	if err := sudoRun("launchctl", "bootstrap", "system", storageMountPlistPath); err != nil {
		r.add("DAEMON", ActionWarn, "Could not start storage-mount daemon: "+err.Error(), "")
	} else {
		r.add("DAEMON", ActionSet, "com.llm-server.storage-mount installed and started", "")
		r.add("DAEMON", ActionInfo, fmt.Sprintf("Re-mounts %s and re-enables ownership at boot", mount), "")
	}
}

func storageMountPlist(mount, device string) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN"
  "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>com.llm-server.storage-mount</string>
  <key>ProgramArguments</key>
  <array>
    <string>/bin/sh</string>
    <string>-c</string>
    <string>for i in 1 2 3 4 5 6 7 8 9 10 11 12; do /bin/mkdir -p '%s'; /usr/sbin/diskutil mount -mountPoint '%s' '%s' &gt;/dev/null 2&gt;&amp;1 || true; /usr/sbin/diskutil enableOwnership '%s' &gt;/dev/null 2&gt;&amp;1 || true; /sbin/mount | /usr/bin/grep -Fq " on %s " &amp;&amp; exit 0; /bin/sleep 10; done; exit 1</string>
  </array>
  <key>RunAtLoad</key><true/>
  <key>StartInterval</key><integer>300</integer>
  <key>StandardOutPath</key><string>/var/log/mac-llm-setup/storage-mount.log</string>
  <key>StandardErrorPath</key><string>/var/log/mac-llm-setup/storage-mount.log</string>
</dict>
</plist>
`, mount, mount, device, mount, mount)
}

// ---------------------------------------------------------------------------
// Section 7: Update user config (symlink mode OFF only)
// ---------------------------------------------------------------------------

func (r *StorageResult) sectionUpdateConfig(cfg *config.Config, ollamaDir, mlxDir, mount string) {
	cfg.Tools.Ollama.ModelsDir = ollamaDir
	cfg.Tools.MLXLM.ModelPath = mlxDir
	cfg.Storage.VolumeMountPoint = mount

	if err := config.Save(cfg); err != nil {
		r.add("CONFIG-UPDATE", ActionWarn, "Could not save updated config: "+err.Error(), "")
	} else {
		r.add("CONFIG-UPDATE", ActionSet, "Config updated with volume paths", "")
		r.add("CONFIG-UPDATE", ActionInfo, "tools.ollama.models_dir → "+ollamaDir, "")
		r.add("CONFIG-UPDATE", ActionInfo, "tools.mlx_lm.model_path → "+mlxDir, "")
	}
}

// ---------------------------------------------------------------------------
// Section 8: Update precheck JSON
// ---------------------------------------------------------------------------

func (r *StorageResult) sectionUpdatePrecheckJSON(mount, modelRoot string) {
	precheckJSON := "/tmp/mac-llm-precheck.json"
	data, err := os.ReadFile(precheckJSON)
	if err != nil {
		return
	}
	var pc map[string]interface{}
	if json.Unmarshal(data, &pc) != nil {
		return
	}

	freeGB, _ := volumeDF(mount)
	st, _ := pc["storage"].(map[string]interface{})
	if st == nil {
		st = make(map[string]interface{})
	}
	st["volume_configured"] = true
	st["volume_mount"] = mount
	st["model_root"] = modelRoot
	st["free_gb"] = freeGB
	pc["storage"] = st

	updated, err := json.MarshalIndent(pc, "", "  ")
	if err != nil {
		return
	}
	if err := os.WriteFile(precheckJSON, updated, 0644); err != nil {
		r.add("PRECHECK-JSON", ActionWarn, "Could not update precheck JSON: "+err.Error(), "")
	} else {
		r.add("PRECHECK-JSON", ActionSet, "/tmp/mac-llm-precheck.json updated with volume info", "")
	}
}

// ---------------------------------------------------------------------------
// Section 9: Verification
// ---------------------------------------------------------------------------

func (r *StorageResult) sectionVerify(modelRoot, mount, uuid, label string, symlinkInternal bool,
	ollamaDir, rapidMLXDir, mlxDir string) {

	dirs := []string{ollamaDir, rapidMLXDir, mlxDir}
	for _, d := range dirs {
		if info, err := os.Stat(d); err == nil && info.IsDir() {
			owner := statOwner(d)
			r.add("VERIFY", ActionSet, fmt.Sprintf("%s (owner: %s)", d, owner), "")
		} else {
			r.add("VERIFY", ActionFail, "Missing directory: "+d, "")
		}
	}

	if symlinkInternal {
		for _, link := range []string{"/Library/Ollama/models", "/Library/RapidMLX/cache", "/Library/MLX/models"} {
			target, err := os.Readlink(link)
			if err != nil {
				continue
			}
			if _, err := os.Stat(link); err == nil {
				r.add("VERIFY", ActionSet, fmt.Sprintf("%s → %s", link, target), "")
			} else {
				r.add("VERIFY", ActionFail, fmt.Sprintf("%s is a dangling symlink → %s", link, target), "")
			}
		}
	}

	// Spotlight
	out, _ := exec.Command("mdutil", "-s", mount).Output()
	if strings.Contains(strings.ToLower(string(out)), "disabled") ||
		strings.Contains(strings.ToLower(string(out)), "off") {
		r.add("VERIFY", ActionSet, "Spotlight disabled on "+mount, "")
	} else {
		r.add("VERIFY", ActionWarn, "Spotlight may still be active on "+mount, "")
	}

	// fstab
	if uuid != "" {
		if data, err := os.ReadFile("/etc/fstab"); err == nil && strings.Contains(string(data), uuid) {
			r.add("VERIFY", ActionSet, "fstab entry present for "+label, "")
		} else {
			r.add("VERIFY", ActionWarn, "fstab entry not found for "+label, "")
		}
	}
}

// ---------------------------------------------------------------------------
// Low-level helpers
// ---------------------------------------------------------------------------

// diskutilInfo runs `diskutil info <target>` and returns the raw output.
func diskutilInfo(target string) string {
	out, _ := exec.Command("diskutil", "info", target).Output()
	return string(out)
}

// extractDiskutilField extracts a field value from diskutil info output.
func extractDiskutilField(info, field string) string {
	for _, line := range strings.Split(info, "\n") {
		if idx := strings.Index(line, field+":"); idx >= 0 {
			return strings.TrimSpace(line[idx+len(field)+1:])
		}
	}
	return ""
}

// volumeDF returns (freeGB, totalGB) using df.
func volumeDF(mount string) (free, total int) {
	out, err := exec.Command("df", "-g", mount).Output()
	if err != nil {
		return
	}
	lines := strings.Split(string(out), "\n")
	if len(lines) < 2 {
		return
	}
	fields := strings.Fields(lines[1])
	if len(fields) >= 4 {
		total, _ = strconv.Atoi(fields[1])
		free, _ = strconv.Atoi(fields[3])
	}
	return
}

// sudoRun executes a command that requires root (we are already root).
func sudoRun(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	return cmd.Run()
}

// sudoMkdirAll creates directories as root.
func sudoMkdirAll(path string, perm os.FileMode) error {
	return os.MkdirAll(path, perm)
}

// userExists returns true if the Unix user exists.
func userExists(username string) bool {
	err := exec.Command("id", username).Run()
	return err == nil
}

// statOwner returns "user:group" for a path.
func statOwner(path string) string {
	out, err := exec.Command("stat", "-f", "%Su:%Sg", path).Output()
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(out))
}
