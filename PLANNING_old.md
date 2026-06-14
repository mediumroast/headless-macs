# Headless Mac Scripts Refactoring Plan

## Overview
Refactor the monolithic setup scripts into modular, function-based scripts with consistent interfaces for setup, enable, disable, and removal operations.

## Current State
- `pmset_to_ollama.sh` - Monolithic script handling both power management AND Ollama setup
- `setup_colima.sh` - Handles Homebrew, Colima, and Docker setup
- No modular functions
- No master orchestration script
- No consistent enable/disable/remove operations

## Proposed New Structure

```
headless-macs/
├── lib/
│   └── common.sh                    # Shared utility functions
├── scripts/
│   ├── power_management.sh          # Power management (pmset)
│   ├── ollama_setup.sh              # Ollama installation and service
│   ├── colima_setup.sh              # Colima and Docker setup
│   └── homebrew_setup.sh            # Homebrew installation (extracted)
├── setup.sh                         # Master orchestration script
├── pmset_to_ollama.sh              # [DEPRECATED - keep for backward compat]
├── setup_colima.sh                 # [DEPRECATED - keep for backward compat]
└── PLANNING.md                     # This file
```

---

## 1. Common Library (`lib/common.sh`)

### Purpose
Shared utility functions used across all scripts.

### Functions
- `print_status()` - Green checkmark output
- `print_error()` - Red X output  
- `print_warning()` - Yellow warning output
- `print_info()` - Info output
- `check_macos()` - Verify running on macOS
- `check_apple_silicon()` - Verify ARM64 architecture
- `check_binary_arm64()` - Verify binary supports ARM64
- `confirm_action()` - Prompt user for y/n confirmation
- `backup_file()` - Create timestamped backup of a file

### Usage
All scripts will source this library:
```bash
source "$(dirname "$0")/../lib/common.sh"
```

---

## 2. Power Management Script (`scripts/power_management.sh`)

### Purpose
Configure macOS power management for headless 24/7 operation.

### Extracted From
`pmset_to_ollama.sh` (Step 2: Power Management Configuration)

### Functions

#### `setup_power_management()`
- Configure all pmset settings for headless operation
- Disable sleep, standby, autopoweroff, powernap
- Enable wake-on-LAN (womp)
- Set display sleep to 10 minutes
- Enable TCP keepalive
- Show current settings and confirm with user

#### `enable_power_management()`
- Apply power management settings (calls setup)
- Useful for re-enabling after disable

#### `disable_power_management()`
- Restore default macOS power settings
- Set reasonable sleep timers
- Disable wake-on-LAN
- Useful for returning Mac to normal laptop/desktop use

#### `remove_power_management()`
- Alias to `disable_power_management()`
- Restore to factory defaults

#### `status_power_management()`
- Display current pmset settings
- Show what would change with setup

### Environment Variables
None required.

### Dependencies
- `sudo` access
- `pmset` command (built-in to macOS)

---

## 3. Homebrew Setup Script (`scripts/homebrew_setup.sh`)

### Purpose
Install and configure Homebrew for Apple Silicon or Intel Macs.

### Extracted From
`setup_colima.sh` (Step 1) and `pmset_to_ollama.sh` (implicit)

### Functions

#### `setup_homebrew()`
- Check if Homebrew is installed
- Install if not present
- Verify correct architecture (ARM64 vs x86_64)
- Add to PATH in ~/.zprofile
- Update Homebrew

#### `enable_homebrew()`
- Ensure Homebrew is in PATH
- Source shellenv

#### `disable_homebrew()`
- Remove from PATH (comment out in ~/.zprofile)
- Note: Does not uninstall

#### `remove_homebrew()`
- Prompt for confirmation
- Run Homebrew uninstall script
- Clean up PATH entries
- Remove /opt/homebrew or /usr/local

#### `status_homebrew()`
- Check installation status
- Show version
- Show path
- List architecture

### Environment Variables
None required.

### Dependencies
- Internet connection for installation

---

## 4. Ollama Setup Script (`scripts/ollama_setup.sh`)

### Purpose
Install Ollama, configure environment, create launchd service.

### Extracted From
`pmset_to_ollama.sh` (Steps 1, 3, 4, 5, 6)

### Functions

#### `setup_ollama()`
- Check for existing Ollama installation
- Install from ollama.com if not present
- Verify ARM64 support
- Configure environment variables (interactive prompts):
  - `OLLAMA_MAX_LOADED_MODELS`
  - `OLLAMA_KEEP_ALIVE`
  - `OLLAMA_NUM_PARALLEL`
  - `OLLAMA_MAX_CONTEXT`
  - `OLLAMA_HOST` (bind address)
- Create launchd plist at `/Library/LaunchDaemons/com.ollama.server.plist`
- Load and start service
- Verify service is running
- Test API endpoint

#### `enable_ollama()`
- Load launchd plist if not loaded
- Start service if not running
- Verify API endpoint

#### `disable_ollama()`
- Stop service
- Unload launchd plist
- Note: Does not uninstall binary

#### `remove_ollama()`
- Stop and unload service
- Remove launchd plist
- Remove binary from:
  - `/usr/local/bin/ollama`
  - `/opt/homebrew/bin/ollama`
  - `/Applications/Ollama.app`
- Clean up logs
- Optional: Remove model storage (`~/.ollama`)

#### `status_ollama()`
- Check if binary exists
- Check if service is loaded
- Check if process is running
- Test API endpoint
- Show version
- Show configuration

### Environment Variables
Set in launchd plist:
- `OLLAMA_MAX_LOADED_MODELS` (default: 3)
- `OLLAMA_KEEP_ALIVE` (default: 86400 = 24 hours)
- `OLLAMA_NUM_PARALLEL` (default: 4)
- `OLLAMA_MAX_CONTEXT` (default: 32768)
- `OLLAMA_FLASH_ATTENTION` (default: 1)
- `OLLAMA_NUM_GPU` (default: 1)
- `OLLAMA_HOST` (default: 0.0.0.0:11434)

### Dependencies
- `curl` for download
- `unzip` for extraction
- `sudo` for installation and launchd
- `file` for architecture verification

---

## 5. Colima Setup Script (`scripts/colima_setup.sh`)

### Purpose
Install and configure Colima with Docker CLI, optimized for running containers alongside Ollama.

### Extracted From
`setup_colima.sh` (Steps 2, 3, 4, 5, 6)

### Key Enhancements
- **Ollama Awareness**: Check if Ollama is running and recommend resource allocation
- **Dynamic Resource Calculation**: 
  - Detect total system RAM
  - If Ollama is running, suggest leaving resources for it
  - Recommend: Total RAM - 8GB for Ollama = Colima RAM
- **Ollama Integration**:
  - Configure `--network-address` to allow containers to reach host Ollama
  - Add environment variable hints for connecting to Ollama from containers

### Functions

#### `setup_colima()`
- Install Colima via Homebrew
- Install Docker CLI
- Install Docker Compose
- Install Docker Buildx
- **Check for Ollama**: 
  - Detect if Ollama is running
  - Get system resources (RAM, CPU)
  - Calculate recommended resources
- Configure Colima with appropriate settings:
  - CPU cores (interactive)
  - Memory (with Ollama-aware recommendations)
  - Disk size
  - VM type (vz for Apple Silicon)
  - Enable Rosetta for x86_64 containers
  - Use virtiofs for faster mounts
  - Enable network-address for host communication
- Start Colima
- Verify Docker CLI connectivity
- Test with hello-world container
- Create launchd plist for auto-start (optional)

#### `enable_colima()`
- Start Colima if stopped
- Verify Docker CLI works
- Load launchd plist if configured

#### `disable_colima()`
- Stop Colima
- Unload launchd plist if present
- Note: Does not remove VM

#### `remove_colima()`
- Stop Colima
- Delete Colima VM (`colima delete`)
- Uninstall via Homebrew (optional)
- Remove launchd plist
- Clean up socket files

#### `status_colima()`
- Check if Colima is running
- Show configuration (CPU, RAM, disk)
- Show Docker info
- List running containers
- Check if Ollama is accessible from containers

### Environment Variables
None for Colima itself, but provides guidance for container environments:
```bash
# For containers to reach host Ollama
OLLAMA_BASE_URL=http://host.docker.internal:11434
```

### Dependencies
- Homebrew (via `homebrew_setup.sh`)
- `sudo` for launchd (optional)
- Ollama (optional, for resource awareness)

### Resource Calculation Logic
```bash
# Detect total system RAM
TOTAL_RAM=$(sysctl hw.memsize | awk '{print int($2/1024/1024/1024)}')

# Check if Ollama is running
if pgrep -x ollama > /dev/null; then
  OLLAMA_RUNNING=true
  # Recommend leaving 8-16GB for Ollama depending on models
  RECOMMENDED_COLIMA_RAM=$((TOTAL_RAM - 8))
else
  OLLAMA_RUNNING=false
  RECOMMENDED_COLIMA_RAM=$((TOTAL_RAM - 4))
fi

# Display recommendation
print_info "System RAM: ${TOTAL_RAM}GB"
if [ "$OLLAMA_RUNNING" = true ]; then
  print_warning "Ollama is running - recommend ${RECOMMENDED_COLIMA_RAM}GB for Colima"
  print_info "This leaves ~8GB for Ollama"
fi
```

---

## 6. Master Setup Script (`setup.sh`)

### Purpose
Orchestrate all setup scripts with a unified interface.

### Features
- Interactive menu or command-line arguments
- Call individual scripts in correct order
- Manage dependencies (e.g., Homebrew before Ollama)
- Provide status overview of all components

### Functions

#### Main Menu
```
Headless Mac Setup
==================
1. Install Homebrew
2. Configure Power Management
3. Install Ollama
4. Install Colima + Docker
5. Full Setup (All of the above)
6. Status (Check all components)
7. Enable All Services
8. Disable All Services
9. Remove All Components
0. Exit
```

#### Command-Line Interface
```bash
./setup.sh install homebrew        # Install Homebrew
./setup.sh install power            # Configure power management
./setup.sh install ollama           # Install Ollama
./setup.sh install colima           # Install Colima
./setup.sh install all              # Full setup

./setup.sh enable ollama            # Enable Ollama service
./setup.sh enable colima            # Enable Colima
./setup.sh enable all               # Enable all services

./setup.sh disable ollama           # Disable Ollama service
./setup.sh disable colima           # Disable Colima
./setup.sh disable all              # Disable all services

./setup.sh remove ollama            # Remove Ollama
./setup.sh remove colima            # Remove Colima
./setup.sh remove all               # Remove all components

./setup.sh status                   # Show status of all components
./setup.sh status ollama            # Show Ollama status
./setup.sh status colima            # Show Colima status
```

### Dependencies
- All scripts in `scripts/` directory
- Common library in `lib/`

### Order of Operations (Full Setup)
1. Check macOS and architecture
2. Install Homebrew
3. Configure Power Management
4. Install Ollama
5. Install Colima (with Ollama awareness)
6. Verify all components
7. Display summary and next steps

---

## 7. Backward Compatibility

### Strategy
Keep existing scripts but add deprecation notices:

#### `pmset_to_ollama.sh`
- Add banner: "DEPRECATED: Use './setup.sh install all' instead"
- Still functional but encourage migration
- Add note about new modular scripts

#### `setup_colima.sh`
- Add banner: "DEPRECATED: Use './setup.sh install colima' instead"  
- Still functional but encourage migration

### Timeline
- Phase 1: Create new modular scripts
- Phase 2: Add deprecation notices to old scripts
- Phase 3 (future): Move old scripts to `legacy/` directory
- Phase 4 (future): Remove old scripts (with release notes)

---

## Implementation Plan

### Phase 1: Foundation (Files 1-2)
1. ✅ Create `lib/common.sh` with shared functions
2. ✅ Create `scripts/power_management.sh`
3. ✅ Test power management script independently

### Phase 2: Core Components (Files 3-4)
4. ✅ Create `scripts/homebrew_setup.sh`
5. ✅ Create `scripts/ollama_setup.sh`
6. ✅ Test Homebrew and Ollama scripts

### Phase 3: Colima Integration (File 5)
7. ✅ Create `scripts/colima_setup.sh` with Ollama awareness
8. ✅ Implement resource detection and recommendations
9. ✅ Test container-to-host Ollama connectivity

### Phase 4: Orchestration (File 6)
10. ✅ Create `setup.sh` master script
11. ✅ Implement command-line interface
12. ✅ Implement interactive menu
13. ✅ Test full workflow

### Phase 5: Polish
14. ✅ Add deprecation notices to old scripts
15. ✅ Update README.md with new usage instructions
16. ✅ Test all combinations of setup/enable/disable/remove
17. ✅ Test on clean macOS installation

---

## Testing Strategy

### Unit Testing
Each script should be testable independently:
```bash
# Test individual functions
cd scripts
./power_management.sh setup
./power_management.sh status
./power_management.sh disable
```

### Integration Testing
Test via master script:
```bash
./setup.sh status              # Should show "not installed" for all
./setup.sh install homebrew    # Install Homebrew
./setup.sh install ollama      # Install Ollama
./setup.sh status              # Should show installed/running
./setup.sh disable ollama      # Disable service
./setup.sh enable ollama       # Re-enable service
./setup.sh remove ollama       # Clean removal
```

### Scenarios to Test
1. **Fresh install**: Clean macOS system
2. **Partial install**: Some components already present
3. **Upgrade scenario**: Old scripts installed, run new scripts
4. **Resource constraints**: Test Colima with Ollama running
5. **Container connectivity**: Verify containers can reach host Ollama
6. **Service management**: Enable/disable/restart all services
7. **Complete removal**: Verify clean uninstall

---

## Benefits of This Refactoring

### Modularity
- Each script has a single responsibility
- Easy to maintain and debug
- Can run components independently

### Consistency
- All scripts follow same function naming convention
- Predictable interface: setup, enable, disable, remove, status
- Shared utilities reduce code duplication

### Flexibility
- Users can install only what they need
- Easy to add new components later
- Can mix and match configurations

### Ollama-Colima Integration
- Smart resource allocation
- Containers can easily connect to host Ollama
- No port conflicts or networking issues

### User Experience
- Single entry point via `setup.sh`
- Clear status reporting
- Safe removal procedures
- Interactive or scriptable (CLI args)

---

## Open Questions / Decisions Needed

1. **Resource Defaults**: What should default Colima resources be?
   - Proposal: 4 CPU, 16GB RAM (if >24GB system), 100GB disk

2. **Ollama Detection**: Should Colima setup fail if Ollama not found?
   - Proposal: No, just warn and adjust recommendations

3. **Launchd Paths**: User-level or system-level?
   - Ollama: System-level (`/Library/LaunchDaemons/`) - requires sudo
   - Colima: User-level (`~/Library/LaunchAgents/`) - no sudo

4. **Configuration Files**: Should we support config files?
   - Proposal: Phase 2 feature, use environment variables for now

5. **Logging**: Centralized logging strategy?
   - Proposal: Each service logs to `/tmp/[service].log` (current behavior)

6. **Docker Context**: Should we manage Docker contexts?
   - Proposal: Colima automatically manages this, no changes needed

---

## File Size Estimates

- `lib/common.sh`: ~150 lines
- `scripts/power_management.sh`: ~200 lines
- `scripts/homebrew_setup.sh`: ~250 lines
- `scripts/ollama_setup.sh`: ~400 lines
- `scripts/colima_setup.sh`: ~450 lines
- `setup.sh`: ~300 lines

**Total new code**: ~1,750 lines (vs current ~1,100 lines)

The increase is due to:
- Function-based structure
- Enable/disable/remove operations
- Better error handling
- Status reporting
- Ollama awareness in Colima

---

## Success Criteria

1. ✅ All existing functionality preserved
2. ✅ Each component can be installed independently
3. ✅ Consistent interface across all scripts
4. ✅ Colima aware of and optimized for Ollama
5. ✅ Containers can connect to host Ollama
6. ✅ Clean enable/disable/remove operations
7. ✅ Master script provides unified interface
8. ✅ Backward compatibility maintained
9. ✅ Documentation updated
10. ✅ Tested on fresh macOS installation

---

## Next Steps

**AWAITING APPROVAL** before proceeding with implementation.

Once approved, I will:
1. Create the `lib/` and `scripts/` directories
2. Implement files in the order specified in Implementation Plan
3. Test each component as it's created
4. Create the master `setup.sh` script
5. Update documentation
6. Add deprecation notices to old scripts

---

## Questions for Review

1. Does this structure align with your vision?
2. Any changes to the proposed file organization?
3. Should we add any additional functions (e.g., update, backup)?
4. Any concerns about the Ollama-Colima integration approach?
5. Preferences for command-line argument style?

