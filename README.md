# CHEST

<center>
<img src="assests/image.png" alt="chest" width="360">
</center>

> **Give CHEST a folder, tell it how you want the files organized, and CHEST puts them into the right compartments.**

CHEST is a file organization, indexing, and automation CLI tool built in Go, inspired by the Minecraft chest. It focuses on deterministic sorting, reversible operations, fast searching, and an extensible architecture.

---

## Installation

### Option 1: Go Install
```bash
go install github.com/Aswanidev-vs/chest/cmd/chest@latest
```
Ensure your Go bin path (`$GOPATH/bin` or `%USERPROFILE%\go\bin`) is included in your system `PATH`.

### Option 2: Shell Script (Linux & macOS)
```bash
curl -sSL https://raw.githubusercontent.com/Aswanidev-vs/chest/main/install.sh | bash
```

### Option 3: PowerShell (Windows)
```powershell
irm https://raw.githubusercontent.com/Aswanidev-vs/chest/main/install.ps1 | iex
```

### Option 4: Build from Source
```bash
git clone https://github.com/Aswanidev-vs/chest.git
cd chest
make build
```

---

## Core Features

- **Concurrent Search Engine**: Built using `fastwalk` for parallel directory traversal and `fzf/src/algo` for exact and fuzzy scoring. Supports filtering by file category, extension, size, modification date, and in-file content grep.
- **Rule-Based Organization**: Sort by extensions, categories, file size boundaries, and date. `--date` defaults to modification-year folders, while `--date-source auto` can use embedded photo/video dates when available. Define custom rules on the CLI or use built-in presets (`downloads`, `media`, `documents`, `developer`, `photos`).
- **Signature-Based Format Detection & Metadata Extraction**: Every file is inspected by content signature (magic bytes first, extension as fallback) to determine its true format, then enriched with embedded metadata — EXIF `DateTimeOriginal`/`CreateDate`, QuickTime `mvhd` creation time, PDF Info dictionary fields, and ID3 / Vorbis / RIFF-INFO audio tags, plus ZIP-based OOXML / ODF / EPUB core properties. The extractor registry is open, so custom binary formats can be registered programmatically (CHEST ships with a built-in custom-format example: `.gocut` GoCut project files, detected by JSON signature or extension, exposing `format=gocut`, MIME `application/x-gocut-project`, category `video`, and an embedded `createdAt` as the taken date) and then routed with `format=...` rules even when the extension is unknown or renamed.
- **Safety First**:
  - Full dry-run preview (`chest sort --dry-run` or `-n`) before files move.
  - Interactive confirmations.
  - Built-in collision policies: `skip`, `rename`, `replace`, or `abort`.
  - Comprehensive operation logging and rollbacks via `chest undo`.
- **Folder Watch Automation**: Monitor directories continuously with `chest watch`. New files are picked up via filesystem events (`fsnotify`) and sorted automatically with debounce safeguards. Use `--initial` to sort what's already in the folder before watching, and get instant terminal feedback when folders are created or removed.
- **Local Indexing & Analysis**:
  - Incremental metadata and SHA256 hashing backed by pure-Go SQLite (`ncruces/go-sqlite3`).
  - Storage analytics with `chest stats`.
  - Exact duplicate file detection with `chest duplicates`.
  - Stale and zero-byte file reports with `chest analyze`, with a total run-time footer showing how long the command took to generate the report.
  - **Self-refreshing reports**: a scoped `stats`/`analyze` run re-indexes that folder each time, and a bare run re-populates an empty cache from the current directory (so `chest clean` never leaves you staring at all-zero reports). Re-indexing also **prunes** rows for files that were deleted/moved, or are now covered by `--except`.
  - Cache management with `chest index --clear` and `chest clean`.
- **Shell Tab Completion**: One-liner setup with `chest completion --install` (auto-detects your shell from `$SHELL`). Tab-complete subcommands and flags in `bash`, `zsh`, and `fish`, or print/save the raw script with `chest completion <shell>`.
- **Parallel Performance & Live Feedback**:
  - Incremental indexing skips unchanged files (using `size + mtime`) so repeated runs are near-instant.
  - Hashing and directory traversal run in parallel across CPU cores (`errgroup` + `fastwalk`), and SQLite writes are batched in transactions.
  - Live in-place progress bars on the long-wait commands: `index`, `duplicates`, `analyze`, and `sort`.
- **Exclusion Flags (`--except`)**:
  - Available on `index`, `stats`, `analyze`, and `duplicates` to prune noisy directories or globs (e.g. `node_modules`, `venv`, `.git`) before traversal — excluded paths are also removed from any previously-indexed rows: `chest duplicates ~/Projects --except node_modules,venv,.git`.
  - Report freshness: a scoped `stats`/`analyze` auto-refreshes its folder each run, an empty cache is re-populated from the current directory automatically, and `--refresh` forces a re-scan with stale/excluded-row purge. Largest-file listings use absolute paths.
- **System Path Protection Guard**:
  - Automatic safeguards preventing modification to OS root volumes (`/`, `C:\`) and system paths (`C:\Windows`, `C:\Program Files`, `/etc`, `/usr`, `/var`, etc.).
  - Protected paths remain completely accessible for read-only commands (`search`, `stats`, `duplicates`, `analyze`, `index`).
  - Dangerous operations (`sort`, `watch`, `undo`) require explicit `--allow-system` override with confirmation.
- **Network Speed Testing**: Compare Ookla (nearest speedtest.net server) and Cloudflare (`speed.cloudflare.com`) results, with download and upload bars shown as `◆` filled and `◇` empty diamonds; select `--ookla` or `--cloudflare`, or use `--json` for machine-readable output.
- **Plugin Architecture**: Extend classification and rule handling through independent external processes using `hashicorp/go-plugin` over standard RPC.

---

## Command Reference

| Command | Description | Example |
|---|---|---|
| `chest sort` | Sort files using presets, flags, or custom rules; `--date` groups by modification year by default, or embedded media date with `--date-source auto`; `--format`/`format=` use content-detected format (`--allow-system` for OS dirs) | `chest sort ~/Downloads -d --date-granularity month` |
| `chest search` | Search files by name, metadata, or file content grep | `chest search "report" -e pdf -c "invoice"` |
| `chest watch` | Continuously watch and organize incoming files (`--initial` sorts existing files first; `--allow-system` guard) | `chest watch ~/Downloads --preset media --initial` |
| `chest history` | View previous operations log | `chest history` |
| `chest undo` | Revert latest operation or specific ID, auto-cleaning empty created directories | `chest undo` or `chest undo 3` |
| `chest undo cache` | Inspect undo cache or wipe all history (`--clear`) | `chest undo cache --clear` |
| `chest index` | Index directory metadata into local SQLite (incremental, parallel; `--except` to skip, prunes deleted/excluded rows on re-index) | `chest index ~/Documents --hash` |
| `chest stats` | Display storage consumption and category breakdown; scoped runs auto-refresh, bare runs repopulate an empty cache, `--refresh` forces re-scan+purge, largest files shown with absolute paths | `chest stats ~/Projects --refresh` |
| `chest speedtest` | Compare Ookla vs Cloudflare download/upload/ping/jitter; transfer bars use `◆` filled and `◇` empty diamonds | `chest speedtest` (both), `chest speedtest --ookla`, `chest speedtest --cloudflare`, `chest speedtest --json` |
| `chest duplicates` | Locate duplicate files using content hashes (`--except` to skip dirs/globs; excluded rows are purged from the cache) | `chest duplicates ~/Downloads --except node_modules,venv` |
| `chest analyze` | Read-only audit of storage distribution and old files (`--except`/`--refresh` supported; scoped runs auto-refresh, bare runs repopulate an empty cache), with total run time in the report footer | `chest analyze ~/Downloads --refresh` |
| `chest plugin` | Manage external plugins (`list`, `info`, `install`, `remove`) | `chest plugin list` |
| `chest clean` | Clear cached SQLite index with confirmation (`-y` to skip, `--all` for DB, `--history` to also wipe undo history) | `chest clean -y` |
| `chest man` | Display detailed manual page with examples (like Linux man) | `chest man sort` |
| `chest preset` | List available built-in sorting presets | `chest preset` |
| `chest completion` | Install shell tab-completion for `bash`, `zsh`, `fish` (`--install` auto-detects `$SHELL`) | `chest completion --install` |
| `chest version` | Display version and build architecture | `chest version` |

---

## Plugin API & Extending Engine Tools

CHEST provides an external plugin architecture powered by **`hashicorp/go-plugin`** over standard IPC/RPC. Rather than creating isolated standalone tools, CHEST plugins are designed to directly plug into and extend the core engine tools (`sort`, `search`, `watch`, `classifier`).

### How the Plugin System Interacts with the Engine

```
 ┌─────────────────────────────────────────────────────────────┐
 │                         CHEST Core                          │
 │                                                             │
 │   CLI Tools (sort / watch / search)                         │
 │         │                                                   │
 │         ▼                                                   │
 │   Rule Engine & Classifier                                  │
 │         │                                                   │
 │         ▼                                                   │
 │   Plugin Manager (~/.chest/plugins)                         │
 └─────────┬───────────────────────────────────────────────────┘
           │  HashiCorp RPC Protocol (Unix socket / Local pipe)
           ▼
 ┌─────────────────────────────────────────────────────────────┐
 │                      External Plugin                        │
 │                                                             │
 │   [Manifest]        Name, Version, Capabilities             │
 │   [Classifier]      Classify(filename, ext) -> Category    │
 │   [Rule Provider]   GetCustomRules() -> []models.Rule       │
 └─────────────────────────────────────────────────────────────┘
```

When someone builds a plugin for CHEST, the goal is **not** to reinvent file operations, but to feed custom domain intelligence directly into CHEST's existing execution engine:

1. **Extending File Classification (`Classify`)**:
   - The built-in classifier categorizes common extensions (`jpg` -> Image, `go` -> Code).
   - A plugin overrides or expands classification for specialized domain files (e.g., classifying DICOM `.dcm` files as `MedicalImaging`, or `.spec.ts` as `UnitTests`, or audio stems `.stem.mp4` as `MusicProduction`).
   - The engine tools (`sort`, `watch`, `search -t`) automatically adopt these custom categories when evaluating conditions.

2. **Injecting Dynamic Engine Rules (`GetCustomRules`)**:
   - Plugins can return a slice of `models.Rule` structs with custom priority, conditions (extension, size, regex, date), and compartment destinations.
   - These rules are injected directly into the `rules.Engine`, allowing plugins to provide domain-specific sorting presets without modifying CHEST source code.

3. **Process Isolation & Language Independence**:
   - Plugins execute as separate child processes communicating over RPC. A crash in a third-party plugin cannot crash the main CHEST engine or corrupt the filesystem.
   - Plugins can be developed in Go or any language supporting standard gRPC / HashiCorp plugin handshakes.

### Creating a Plugin: Minimal Implementation

#### 1. Define the Plugin Service (`main.go`)

```go
package main

import (
    "strings"
    hplugin "github.com/hashicorp/go-plugin"
    "github.com/Aswanidev-vs/chest/internal/models"
    "github.com/Aswanidev-vs/chest/internal/plugin"
)

type DomainClassifier struct{}

// Return plugin metadata and declared capabilities
func (c *DomainClassifier) GetInfo() (plugin.Manifest, error) {
    return plugin.Manifest{
        Name:         "code-artifacts",
        Version:      "1.0.0",
        Description:  "Classifies developer build artifacts and test reports",
        Author:       "Community",
        Capabilities: []string{"classifier", "rule"},
        Binary:       "code-artifacts",
    }, nil
}

// Extend existing engine classification
func (c *DomainClassifier) Classify(name string, ext string) (string, error) {
    if strings.HasSuffix(name, ".coverage.out") || strings.HasSuffix(name, ".lcov") {
        return "CoverageReports", nil
    }
    if ext == "wasm" {
        return "WebAssembly", nil
    }
    return "", nil // Return empty string to fallback to default engine classifier
}

// Inject custom sorting rules directly into chest sort & watch
func (c *DomainClassifier) GetCustomRules() ([]models.Rule, error) {
    return []models.Rule{
        {
            Name:        "WASM Artifacts",
            Priority:    50,
            Destination: "Build/Wasm",
            Conditions: []models.Condition{
                {Field: models.FieldExtension, Operator: models.OpEqual, Value: "wasm"},
            },
        },
    }, nil
}

func main() {
    hplugin.Serve(&hplugin.ServeConfig{
        HandshakeConfig: plugin.HandshakeConfig,
        Plugins: map[string]hplugin.Plugin{
            "classifier": &plugin.ClassifierPluginRPC{Impl: &DomainClassifier{}},
        },
    })
}
```

#### 2. Create `manifest.json`

```json
{
  "name": "code-artifacts",
  "version": "1.0.0",
  "description": "Classifies developer build artifacts and test reports",
  "author": "Community",
  "capabilities": ["classifier", "rule"],
  "binary": "code-artifacts"
}
```

#### 3. Install into CHEST

```bash
go build -o code-artifacts .
chest plugin install .
chest plugin list
```

Once installed, CHEST engine tools automatically query `~/.chest/plugins` and incorporate the plugin's classification and rules into `sort`, `watch`, and `search`.

---

## Documentation & Web Guide

Comprehensive guides, interactive terminal simulations, and live CLI documentation:
https://aswanidev-vs.github.io/Chest/

---

## License

GPLv3 License. See [LICENSE](LICENSE) for details.
