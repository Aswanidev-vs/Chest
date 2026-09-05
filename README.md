# CHEST

<center>
<img src="assests/image.png" alt="chest" width="360">
</center>

> **Give CHEST a folder, tell it how you want the files organized, and CHEST puts them into the right compartments.**

CHEST is a high-speed, safe, cross-platform file organization, indexing, and automation CLI tool inspired by the Minecraft chest.

---

## 🚀 Installation

### Option 1: Go Install (Recommended for Go users)
```bash
go install github.com/Aswanidev-vs/chest/cmd/chest@latest
```
Ensure `$GOPATH/bin` (or `%USERPROFILE%\go\bin` on Windows) is in your system `PATH`.

### Option 2: Curl / Shell Script (Linux & macOS)
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

## 📦 Features & Capabilities

- ⚡ **Ultra-Fast Parallel Search**: Built on `fastwalk` (parallel multi-core directory traversal) & `fzf/src/algo` (zero-allocation fuzzy scoring). Includes regex, extension/type filtering, and content grep.
- 📦 **Simple by Default**: Just run `chest sort` to clean messy Downloads or Desktop folders with zero config.
- 🛡️ **Safe & Reversible**:
  - `chest sort --dry-run` (`-n`) previews every filesystem change without touching your files.
  - Interactive confirmation prompt before executing moves.
  - `chest undo [id]` restores files back to their exact original positions.
  - Collision management: `skip`, `rename`, `replace`, `abort`.
- 👁️ **Continuous Watch Mode (`chest watch`)**: Monitors incoming files in real-time using `fsnotify` and auto-sorts them on the fly.
- 🔌 **Process-Isolated Plugin Architecture**: Extend CHEST with custom classifiers and rules via `hashicorp/go-plugin` without rebuilding the core.
- 🧠 **Local File Intelligence & SQLite Index**:
  - `chest index` stores file metadata and optional SHA256 hashes incrementally into pure-Go SQLite (`ncruces/go-sqlite3`).
  - `chest stats` provides instant category and storage distribution analytics.
  - `chest duplicates` finds byte-for-byte identical duplicates via cryptographic hashing.
  - `chest analyze` reports storage consumers, old/stale files, and wasted space.
  - `chest clean` / `chest index --clear` clears cached database records safely anytime.

---

## 🛠️ Complete CLI Command Reference

| Command | Purpose | Example |
|---|---|---|
| `chest sort` | Organize files in current or target folder | `chest sort ~/Downloads -t -p downloads` |
| `chest search` | Fast concurrent fuzzy & content grep search | `chest search "report" -e pdf -c "invoice"` |
| `chest watch` | Real-time continuous folder automation | `chest watch ~/Downloads --preset media` |
| `chest history` | View previous file organization runs | `chest history` |
| `chest undo` | Revert latest or specific operation by ID | `chest undo` or `chest undo 4` |
| `chest index` | Index directory into SQLite index DB | `chest index ~/Documents --hash` |
| `chest index --clear` | Empty cached index records | `chest index --clear` |
| `chest stats` | Show category breakdowns & largest files | `chest stats` |
| `chest duplicates` | Scan and group duplicate files by hash | `chest duplicates ~/Downloads` |
| `chest analyze` | Read-only storage health & intelligence report | `chest analyze ~/Downloads` |
| `chest plugin` | Manage external plugins (`list`, `info`, `install`, `remove`) | `chest plugin list` |
| `chest clean` | Remove cached index data (`--all` deletes DB file) | `chest clean --all` |
| `chest preset` | List available built-in rule presets | `chest preset` |
| `chest version` | Print CLI version and architecture | `chest version` |

---

## 🎮 Plugin Development & Extension

CHEST plugins are standalone binaries communicating over standard RPC using `hashicorp/go-plugin`.

See the interactive documentation and step-by-step guide in [docs/](file:///e:/Chest/docs/index.html) or visit the [Plugin Guide](file:///e:/Chest/docs/documentation.html).

---

## 📄 License

