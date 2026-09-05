# CHEST

```text
                   [#]
                       [#]
                 [#]     [#]

           +-------------------+
          /                   /|
         /                   / |
        +-------------------+  |
        |   \           /   |  |
        |    \  [===]  /    |  |
        |     \_______/     |  |
        +-------------------+  |
        |                   |  /
        |                   | /
        +-------------------+/

        ____  _   _  _____  ____  _____ 
       / ___|| | | || ____|/ ___||_   _|
      | |    | |_| ||  _|  \___ \  | |  
      | |___ |  _  || |___  ___) | | |  
       \____||_| |_||_____||____/  |_|  

             ORGANIZE YOUR WORLD
```

> **Give CHEST a folder, tell it how you want the files organized, and CHEST puts them into the right compartments.**

CHEST is a fast, safe, cross-platform file organization tool inspired by the Minecraft chest.

---

## Features

- ⚡ **Lightweight & Fast**: Built with Go, works completely offline, zero heavy runtime dependencies.
- 📦 **Simple by Default**: Run `chest sort` to quickly organize messy downloads or desktop folders.
- 🛡️ **Safe & Predictable**:
  - `chest sort --dry-run` to preview operations before moving a single file.
  - Interactive confirmation prompt before executing moves.
  - `chest history` and `chest undo` to restore files to their exact previous locations.
  - Collision management (`skip`, `rename`, `replace`, `abort`).
- 🎯 **Powerful Rule Engine**:
  - Filter by file format (`-f`), file type (`-t`), file size (`-s`), and modification date (`-d`).
  - Custom rules directly from CLI (`--rule "type=video && size>1GB -> Videos/Large"`).
  - Priority-based deterministic rule evaluation.
- 🗂️ **Built-in Presets**:
  - `downloads`, `media`, `documents`, `developer`, `photos`.
- 📁 **Smart Folder Management**:
  - Automatically creates missing parent and destination directories (`--into "Folder/Sub"`).
  - Support for recursive sorting (`-r`), exclusions (`-x`), and hidden files (`--hidden`).

---

## Installation

### Prerequisites
- [Go 1.22+](https://golang.org/dl/)

### Build from source
```bash
git clone https://github.com/Aswanidev-vs/chest.git
cd chest
go build -o chest.exe ./cmd/chest
```

---

## Quick Start

### 1. Basic Sorting
Organize the current directory using the default preset:
```bash
chest sort
```

Organize a specific folder:
```bash
chest sort ~/Downloads
```

### 2. Preview First (Dry Run)
Inspect what CHEST plans to move without making any changes:
```bash
chest sort -n
# or
chest sort --dry-run
```

### 3. Undo Any Operation
List previous organization runs:
```bash
chest history
```

Revert the most recent operation:
```bash
chest undo
```

Revert a specific operation by ID:
```bash
chest undo 42
```

---

## Common Usage Examples

### Sort by File Type
Groups files into `Images`, `Videos`, `Audio`, `Documents`, `Archives`, `Code`, etc.:
```bash
chest sort -t
```

### Sort into a Custom Folder
```bash
chest sort -t --into "Organized"
```

### Sort by File Size
Sorts into `Small Files (<100MB)`, `Medium Files (100MB-1GB)`, and `Large Files (>1GB)`:
```bash
chest sort -s
```

### Using Built-in Presets
List available presets:
```bash
chest preset
```
Apply a preset:
```bash
chest sort -p media
chest sort -p developer
chest sort -p documents
```

### Custom Rules
```bash
chest sort --rule "type=video && size>1GB -> Videos/Large"
chest sort --rule "*.mp4 -> Videos" --rule "*.pdf -> Documents"
```

### Recursive Scan with Exclusions
```bash
chest sort -r -x ".git,node_modules,build"
```

### Automation (Skip Confirmation)
```bash
chest sort -p downloads -y
```

---

## CLI Flags Reference

| Flag | Long Form | Description |
|---|---|---|
| `-t` | `--type` | Sort by file type category |
| `-f` | `--format` | Sort by file extension / format |
| `-s` | `--size` | Sort by file size |
| `-d` | `--date` | Sort by modification date |
| `-i` | `--into <dir>` | Destination base directory |
| `-p` | `--preset <name>` | Use preset (`downloads`, `media`, `documents`, `developer`, `photos`) |
| `-n` | `--dry-run` | Preview changes without modifying filesystem |
| `-y` | `--yes` | Skip confirmation prompt (ideal for scripts) |
| `-r` | `--recursive` | Traverse subdirectories |
| `-v` | `--verbose` | Detailed step-by-step output |
| `-q` | `--quiet` | Minimal output |
| `-x` | `--exclude <pat>` | Exclude files/directories (comma-separated or pattern) |
| | `--hidden` | Include hidden and system files |
| | `--rule <rule>` | Define custom sorting rule |
| | `--collision <mode>` | Collision policy: `skip` (default), `rename`, `replace`, `abort` |
| | `--follow-symlinks` | Follow symbolic links |

---

## Testing

Run the test suite:
```bash
go test -v ./...
```

---

## License

MIT License.
