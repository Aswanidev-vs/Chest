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
- **Rule-Based Organization**: Sort by extensions, categories, file size boundaries, and date. `--date` defaults to modification-year folders, while `--date-source auto` can use embedded photo/video dates when available; finer granularities name their month folders (`2025/Jan/`) unless `--month-format number` is passed. Define custom rules on the CLI or use built-in presets (`downloads`, `media`, `documents`, `developer`, `photos`).
- **Signature-Based Format Detection & Metadata Extraction**: Every file is inspected by content signature (magic bytes first, extension as fallback) to determine its true format, then enriched with embedded metadata — EXIF `DateTimeOriginal`/`CreateDate`, QuickTime `mvhd` creation time, PDF Info dictionary fields, and ID3 / Vorbis / RIFF-INFO audio tags, plus ZIP-based OOXML / ODF / EPUB core properties. The extractor registry is open, so custom binary formats can be registered programmatically (CHEST ships with a built-in custom-format example: `.gocut` GoCut project files, detected by JSON signature or extension, exposing `format=gocut`, MIME `application/x-gocut-project`, category `video`, and an embedded `createdAt` as the taken date) and then routed with `format=...` rules even when the extension is unknown or renamed.
- **Safety First**:
  - Full dry-run preview (`chest sort --dry-run` or `-n`) before files move.
  - Interactive confirmations.
  - Built-in collision policies: `skip`, `rename`, `replace`, or `abort`.
  - Comprehensive operation logging and rollbacks via `chest undo`.
- **Folder Watch Automation**: Monitor directories continuously with `chest watch`. New files are picked up via filesystem events (`fsnotify`) and sorted automatically with debounce safeguards. Use `--initial` to sort what's already in the folder before watching, and get instant terminal feedback when folders are created or removed.
- **Interactive Rules Builder**: Design a rule set with `chest repl`. The directory is scanned once, then `add` / `edit` / `rm` refine the rules while `preview` re-plans from the current set on every keystroke and renders the same dry-run report as `sort --dry-run` — `preview` is strictly read-only, and only `apply` moves files (recorded in history, so `chest undo` reverts the run). Reusable rule sets live in `~/.chest/templates` as TOML, which keeps fields the rule-string syntax cannot express.
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
- **Network Speed Testing**: Compare Ookla (nearest speedtest.net server) and Cloudflare (`speed.cloudflare.com`) results, with download and upload bars shown as `◆` filled and `◇` empty diamonds; select `--ookla` or `--cloudflare`, or use `--json` for machine-readable output. Add `--disk` to also benchmark local storage (sequential read/write MB/s + random 4K IOPS) on a private temp file — size it with `--disk-size` and target a directory with `--disk-path`.
- **Battery Monitoring**: `chest battery` reports charge, power draw, health, cycle count and drain rate using the platform's own reader on Windows, Linux and macOS — watch it live with `--realtime`, or record with `--record` into `~/.chest/battery.db`, a history database kept separate from the file index so `chest clean --all` cannot destroy it. A field the platform cannot supply is omitted rather than shown as zero, and anything derived rather than read — the drain rate, and per-application attribution via `--apps` — is labelled as an estimate.
- **Plugin Architecture**: Extend classification and rule handling through independent external processes using `hashicorp/go-plugin` over standard RPC.

---

## Command Reference

| Command | Description | Example |
|---|---|---|
| `chest sort` | Sort files using presets, flags, or custom rules; `--date` groups by modification year by default, or embedded media date with `--date-source auto`; `--date-granularity month|day` groups into named month folders (`2025/Jan/`), or `2025/01/` with `--month-format number`; `--format`/`format=` use content-detected format (`--allow-system` for OS dirs) | `chest sort ~/Downloads -d --date-granularity month` |
| `chest search` | Search files by name, metadata, or file content grep | `chest search "report" -e pdf -c "invoice"` |
| `chest repl` | Interactive rules builder with a live, read-only preview; `preview` re-plans from the current rules and never touches disk, only `apply` moves files (recorded in history, so `chest undo` reverts it). Save reusable rule sets to `~/.chest/templates` | `chest repl ~/Downloads -p media` |
| `chest watch` | Continuously watch and organize incoming files (`--initial` sorts existing files first; `--allow-system` guard) | `chest watch ~/Downloads --preset media --initial` |
| `chest history` | View previous operations log | `chest history` |
| `chest undo` | Revert latest operation or specific ID, auto-cleaning empty created directories | `chest undo` or `chest undo 3` |
| `chest undo cache` | Inspect undo cache or wipe all history (`--clear`) | `chest undo cache --clear` |
| `chest index` | Index directory metadata into local SQLite (incremental, parallel; `--except` to skip, prunes deleted/excluded rows on re-index) | `chest index ~/Documents --hash` |
| `chest stats` | Display storage consumption and category breakdown; scoped runs auto-refresh, bare runs repopulate an empty cache, `--refresh` forces re-scan+purge, largest files shown with absolute paths | `chest stats ~/Projects --refresh` |
| `chest speedtest` | Compare Ookla vs Cloudflare download/upload/ping/jitter; transfer bars use `◆` filled and `◇` empty diamonds | `chest speedtest` (both), `chest speedtest --ookla`, `chest speedtest --cloudflare`, `chest speedtest --json`, `chest speedtest --disk` (adds storage benchmark; `--disk-size`, `--disk-path`) |
| `chest battery` | Report battery charge, power draw, health, cycle count and drain rate (aliases `power`, `batt`); `--realtime` redraws the reading in place until Ctrl+C, `--daemon` records in the foreground. Drain rate is derived from change in charge, so `--seconds` must span a window long enough to move the reading — short ones report `n/a` instead of a bogus rate; `--apps` attributes activity per application as an ESTIMATE apportioned from measured CPU and I/O, since no platform reports true per-app battery energy unprivileged, and draws a bar chart of it over the period. `--record` appends to `~/.chest/battery.db`, a database kept separate from the `index.db` so `chest clean --all` cannot destroy it (`chest clean --battery` clears it, opt-in); `--day`/`--week`/`--month`/`--year` report those periods from history, cut in UTC. Cross-platform, with per-platform readers for Windows, Linux and macOS | `chest battery`, `chest battery --json`, `chest battery --realtime`, `chest battery --seconds 600`, `chest battery --record --daemon`, `chest battery --week --apps`, `chest battery --low 20` |
| `chest duplicates` | Locate duplicate files using content hashes (`--except` to skip dirs/globs; excluded rows are purged from the cache) | `chest duplicates ~/Downloads --except node_modules,venv` |
| `chest analyze` | Read-only audit of storage distribution and old files (`--except`/`--refresh` supported; scoped runs auto-refresh, bare runs repopulate an empty cache), with total run time in the report footer | `chest analyze ~/Downloads --refresh` |
| `chest plugin` | Manage external plugins (`list`, `info`, `install`, `remove`) | `chest plugin list` |
| `chest clean` | Clear cached SQLite index with confirmation (`-y` to skip, `--all` for DB, `--history` to also wipe undo history, `--battery` to also delete recorded battery history — not implied by `--all`) | `chest clean -y` |
| `chest man` | Display detailed manual page with examples (like Linux man) | `chest man sort` |
| `chest preset` | List available built-in sorting presets | `chest preset` |
| `chest completion` | Install shell tab-completion for `bash`, `zsh`, `fish` (`--install` auto-detects `$SHELL`) | `chest completion --install` |
| `chest version` | Display version and build architecture | `chest version` |

---

## Building a Rule Set Interactively (`chest repl`)

`chest repl` is the loop for designing a rule set: the directory is scanned once, then you
add, refine and preview rules until the plan looks right, and only `apply` moves anything.

```bash
chest repl ~/Downloads
```

### 1. Build — start broad, check as you go

```text
chest> add type=Video -> Videos
added type=Video -> Videos
chest> add type=Image -> Images
added type=Image -> Images
chest> list
2 rule(s):
   1. type=Video -> Videos
   2. type=Image -> Images
chest> preview
CHEST PLAN
────────────────────────

bigvideo.mp4
  FROM: bigvideo.mp4
  TO:   Videos/bigvideo.mp4
  WHY:  type = Video

movie.mp4
  FROM: movie.mp4
  TO:   Videos/movie.mp4
  WHY:  type = Video

photo.png
  FROM: photo.png
  TO:   Images/photo.png
  WHY:  type = Image
────────────────────────
3 files would be moved.
2 folders would be created.
No changes made.
```

`preview` is strictly read-only. It re-plans from the current rules on every call, so it always
shows what `apply` *would* do — and it never creates a folder or moves a file.

### 2. Update — mind the priority trap

Rules are evaluated **top to bottom and the first match wins**, so a narrow rule added *after* a
broad one is dead code. Adding a "big videos" exception below the general video rule looks right
in `list`, but it never fires — rule 1 already claimed the file:

```text
chest> add type=Video && size>2MB -> Videos/HiFi
added type=Video && size>2MB -> Videos/HiFi
chest> preview
...
bigvideo.mp4
  TO:   Videos/bigvideo.mp4     <-- still rule 1, the HiFi rule is dead
  WHY:  type = Video
```

Fix it by re-ordering: drop the broad rule, then add the narrow one **first**.

```text
chest> rm 1
removed rule 1 (1 remaining)
chest> add type=Video && size>2MB -> Videos/HiFi
chest> add type=Video -> Videos
chest> list
3 rule(s):
   1. type=Image -> Images
   2. type=Video && size>2MB -> Videos/HiFi
   3. type=Video -> Videos
chest> preview
...
bigvideo.mp4
  TO:   Videos/HiFi/bigvideo.mp4
  WHY:  type = Video, size > 2MB

movie.mp4
  TO:   Videos/movie.mp4
  WHY:  type = Video
```

### 3. Update one rule in place

`edit` replaces a rule while keeping its position and priority, so it never disturbs the order
around it. Reach for it when you only want to retune one line.

```text
chest> edit 1 type=Image -> Photos/Screenshots
replaced rule 1 type=Image -> Photos/Screenshots
```

### 4. Reuse the set

Save a rule set to `~/.chest/templates` and load it into any other folder later. Templates are
stored as TOML, so a saved rule keeps every field a built-in preset rule can carry — including
size bounds and extension lists that the `--rule` text syntax cannot express.

```text
chest> save downloads-v2
saved 2 rule(s) to ~/.chest/templates/downloads-v2.toml
chest> templates
- downloads-v2
chest> load downloads-v2
loaded 2 rule(s) from template 'downloads-v2'
```

`load` **replaces** the whole current rule set, and restores the priority order that was saved.

### 5. Commit

`apply` is the only command that moves files. It records history, so a run can be reverted with
`chest undo`; pass `--yes` to skip the confirmation prompt in scripts.

```text
chest> apply
3 file(s) will be moved.
Continue? [y/N]: y
[DONE] 3 file(s) organized into 3 folder(s).
```

Unlike `sort`, `chest repl` refuses protected system directories outright and has **no**
`--allow-system` escape hatch.

### Rule syntax cheat-sheet

| Field | Aliases | Matches |
|---|---|---|
| `type` | | `Image`, `Video`, `Audio`, `Document`, `Archive`, `Executable`, `Code`, `Other` |
| `extension` | `ext` | the filename extension (`extension=mp4`, `extension=.mp4`) |
| `format` | | the content-detected format, via magic bytes first |
| `size` | | a size such as `size>1GB`, `size<=100MB` |
| `name` | `filename` | the filename; supports `contains`, `starts_with`, `ends_with`, `glob`, `=~regex` |
| `date` | `modified`, `modtime` | a date or range: `>2025-01`, `=2024` |
| `taken_date` | `takendate` | the embedded media date |
| `mime` | `mimetype` | the detected MIME type |

Destinations support the placeholders `{ext}`, `{date}`, `{year}`, `{month}` and `{day}`, and
rules combine with `&&`. A bare pattern such as `*.mp4 -> Videos` is shorthand for matching the
filename.

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
 │   [Metadata]        Inspect(FileSample) -> FileMetadata    │
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

3. **Rich Metadata Inspection (`Inspect`)** (protocol v2):
   - Plugins declaring the `metadata` capability receive a bounded `models.FileSample` per scanned file (path, name, extension, size, plus the first 4 KiB and last 1 KiB of content) and return a `models.FileMetadata`.
   - Returned values fill or override format, MIME type, category, embedded capture dates (`TakenDate`/`DateSource`) and arbitrary metadata fields on the file record — e.g. sniffing camera RAW containers or reading capture dates from `.xmp` sidecars.
   - Enrichment runs in `chest sort` and `chest watch` (best-effort: broken plugins are skipped, empty results change nothing) and the fields become available to rule conditions.
   - See `examples/plugins/rich-meta` for a working plugin.

4. **Process Isolation & Language Independence**:
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
