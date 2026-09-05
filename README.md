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
- **Rule-Based Organization**: Sort by extensions, categories, file size boundaries, and dates. Define custom rules on the CLI or use built-in presets (`downloads`, `media`, `documents`, `developer`, `photos`).
- **Safety First**:
  - Full dry-run preview (`chest sort --dry-run` or `-n`) before files move.
  - Interactive confirmations.
  - Built-in collision policies: `skip`, `rename`, `replace`, or `abort`.
  - Comprehensive operation logging and rollbacks via `chest undo`.
- **Folder Watch Automation**: Monitor directories continuously with `chest watch`. New files are picked up via filesystem events (`fsnotify`) and sorted automatically with debounce safeguards.
- **Local Indexing & Analysis**:
  - Incremental metadata and SHA256 hashing backed by pure-Go SQLite (`ncruces/go-sqlite3`).
  - Storage analytics with `chest stats`.
  - Exact duplicate file detection with `chest duplicates`.
  - Stale and zero-byte file reports with `chest analyze`.
  - Cache management with `chest index --clear` and `chest clean`.
- **Plugin Architecture**: Extend classification and rule handling through independent external processes using `hashicorp/go-plugin` over standard RPC.

---

## Command Reference

| Command | Description | Example |
|---|---|---|
| `chest sort` | Sort files using presets or custom rules | `chest sort ~/Downloads -t -p downloads` |
| `chest search` | Search files by name, metadata, or file content | `chest search "report" -e pdf -c "invoice"` |
| `chest watch` | Continuously watch and organize incoming files | `chest watch ~/Downloads --preset media` |
| `chest history` | View previous operations log | `chest history` |
| `chest undo` | Revert the latest operation or a specific run by ID | `chest undo` or `chest undo 3` |
| `chest index` | Index directory metadata into local SQLite | `chest index ~/Documents --hash` |
| `chest stats` | Display storage consumption and category breakdown | `chest stats` |
| `chest duplicates` | Locate duplicate files using cryptographic hashes | `chest duplicates ~/Downloads` |
| `chest analyze` | Read-only audit of storage distribution and old files | `chest analyze ~/Downloads` |
| `chest plugin` | Manage external plugins (`list`, `info`, `install`, `remove`) | `chest plugin list` |
| `chest clean` | Clear cached SQLite index (`--all` removes database file) | `chest clean --all` |
| `chest man` | Display detailed manual page with examples (like Linux man) | `chest man sort` |
| `chest preset` | List available built-in sorting presets | `chest preset` |
| `chest version` | Display version and build architecture | `chest version` |

---

## Documentation & Plugin Development

Comprehensive guides, design details, and instructions on creating custom external plugins are documented in https://aswanidev-vs.github.io/Chest/

---

## License

MIT License. See [LICENSE](LICENSE) for details.
