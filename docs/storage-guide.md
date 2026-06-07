# Storage Guide — External Volume Setup

## Why External Storage

A 70B Q4 model is ~40 GB. A modest library — one large generation model, one coder model, one embedding model — hits 60–80 GB easily. On a Mac Mini M4 with the base 256 GB SSD, that's 25–30% of the entire boot volume.

**External APFS over Thunderbolt 4 delivers 2–3 GB/s sequential read** — fast enough for model loading without meaningful inference latency penalty.

| Interface | Throughput | Suitable for |
|---|---|---|
| Thunderbolt 4 (40 Gbps) | 2–3 GB/s | Production inference |
| USB 3.2 Gen 2 (10 Gbps) | ~1 GB/s | Development / occasional use |
| USB 3.0 (5 Gbps) | ~500 MB/s | Cold storage only |

---

## Volume Requirements

| Requirement | Why |
|---|---|
| **APFS or HFS+ Journaled** | ExFAT/FAT32/NTFS lack Unix permissions — `root:wheel` ownership of model dirs won't work |
| **Auto-mount at boot** | LaunchDaemons start before Finder; without fstab, model dirs don't exist at first boot |
| **`disksleep 0`** | `setup.sh` handles this — without it, drives spin down mid-inference |
| **Spotlight excluded** | Without exclusion, `mds` indexes every `.gguf` and `.safetensors` — killing I/O during inference |
| **Volume label without spaces** | `fstab` and symlink paths break with spaces — use `LLMStorage` not `LLM Storage` |

---

## Quick Setup

### 1. Format the drive

```bash
# List connected disks to find diskN
diskutil list

# Format as APFS with your chosen label
diskutil eraseDisk APFS LLMStorage /dev/diskN
```

### 2. Enable external storage in config.json

```json
{
  "storage": {
    "use_external_volume": true,
    "volume_label": "LLMStorage",
    "models_subdir": "models",
    "min_free_gb": 100,
    "symlink_internal_paths": true
  }
}
```

### 3. Run storage-volume.sh

```bash
sudo ./storage-volume.sh
```

This handles everything: directory layout, Spotlight exclusion, symlinks, and fstab.

---

## Directory Layout on the Volume

`storage-volume.sh` creates this structure:

```
/Volumes/LLMStorage/
└── models/
    ├── ollama/        ← symlinked from /Library/Ollama/models
    ├── rapid-mlx/     ← symlinked from /Library/RapidMLX/cache
    ├── mlx-lm/        ← symlinked from /Library/MLX/models
    ├── infinity/      ← symlinked from /Library/Infinity
    ├── exo/
    └── gguf/          ← raw .gguf files usable by Ollama and llama.cpp
```

---

## Symlink Strategy

When `symlink_internal_paths: true` (the default), `storage-volume.sh` creates symlinks from the canonical `/Library` paths to the volume. This means `install-tools.sh` always writes plists pointing to `/Library/Ollama/models` — unchanged regardless of whether storage is internal or external.

```
/Library/Ollama/models   →  /Volumes/LLMStorage/models/ollama
/Library/RapidMLX/cache  →  /Volumes/LLMStorage/models/rapid-mlx
/Library/MLX/models      →  /Volumes/LLMStorage/models/mlx-lm
/Library/Infinity         →  /Volumes/LLMStorage/models/infinity
```

If the internal directory already has models when you run `storage-volume.sh`, they are migrated to the volume automatically before the symlink is created.

---

## fstab — Auto-Mount at Boot

LaunchDaemons start during early boot, before Finder has a chance to mount external volumes. Without an fstab entry, the Ollama daemon would fail on first start after a reboot because `/Volumes/LLMStorage/models/ollama` doesn't exist yet.

`storage-volume.sh` adds this entry automatically:

```
UUID=<volume-uuid> /Volumes/LLMStorage apfs rw,auto,nobrowse 0 0
```

- `auto` — mount at boot
- `nobrowse` — volume won't appear in Finder sidebar (appropriate for a server)
- `rw` — read-write

To verify the entry was added:

```bash
cat /etc/fstab
```

To verify the volume mounts at the expected path after reboot:

```bash
ls /Volumes/LLMStorage/models/ollama
```

---

## APFS vs HFS+ Journaled

| Feature | APFS | HFS+ Journaled |
|---|---|---|
| Recommended | ✅ Yes | ✅ Acceptable |
| Space sharing | Yes (multiple volumes share pool) | No |
| Snapshots | Yes | No |
| Encryption | Native | Via FileVault |
| Time Machine | Supported | Supported |
| Required for inference | No — both work | No — both work |

Use APFS unless you have a specific reason for HFS+. It's the macOS default since 2017 and handles large files better.

**Do not use ExFAT, FAT32, or NTFS** — these lack Unix permissions and will cause silent failures when `root:wheel` ownership is applied to model directories.

---

## Migrating Existing Models

If Ollama already has models on the internal drive when you enable external storage, `storage-volume.sh` migrates them automatically:

```
[MIGRATE] Moving existing Ollama models from /Library/Ollama/models → /Volumes/LLMStorage/models/ollama
```

This uses `rsync -a --remove-source-files` — it copies then removes the source, so you won't lose models if the transfer is interrupted.

After migration, verify:

```bash
ollama list          # should still show all models
ls /Volumes/LLMStorage/models/ollama   # should contain the model blobs
readlink /Library/Ollama/models        # should point to the volume
```

---

## Disabling External Storage

To move back to internal storage:

1. Set `storage.use_external_volume: false` in `config.json`
2. Copy models back: `sudo rsync -a /Volumes/LLMStorage/models/ollama/ /Library/Ollama/models/`
3. Remove symlinks: `sudo rm /Library/Ollama/models && sudo mkdir /Library/Ollama/models`
4. Remove the fstab entry: `sudo vi /etc/fstab`
5. Reboot
