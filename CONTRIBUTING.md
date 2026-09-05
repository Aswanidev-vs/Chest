# Contributing to CHEST

Thank you for your interest in contributing to CHEST! We welcome issues, suggestions, architectural discussions, and pull requests from developers of all skill levels.

---

## Code of Conduct

All contributors and participants agree to abide by our [Code of Conduct](CODE_OF_CONDUCT.md). Please report unacceptable behavior to project maintainers.

---

## Development Setup

### Prerequisites

- **Go**: Version `1.22` or higher (compatible with Go `1.24+`)
- **Git**
- **Make** (optional, recommended on Linux/macOS)

### Getting the Code

```bash
git clone https://github.com/Aswanidev-vs/Chest.git
cd Chest
```

### Building the Project

```bash
# Using Makefile
make build

# Or standard Go toolchain
go build -o chest ./cmd/chest
```

### Running Tests

```bash
# Run all unit tests
go test -v ./...

# Run tests with race detection (supported platforms)
go test -race ./...
```

---

## Codebase Structure

```
├── cmd/chest/            # CLI binary entry point (main.go)
├── internal/
│   ├── classifier/       # File category rules & MIME classification
│   ├── cli/              # Cobra commands, flags, man pages & formatting
│   ├── filesystem/       # Atomic file operations, collisions, system guard
│   ├── history/          # Undo operation logging (~/.chest/history.json)
│   ├── indexer/          # Pure-Go SQLite metadata & SHA256 storage
│   ├── models/           # Shared domain models (File, Rule, Plan, etc.)
│   ├── organizer/        # Plan executor & progress reporter
│   ├── planner/          # Dry-run execution planner & path resolver
│   ├── plugin/           # HashiCorp RPC plugin server/client
│   ├── presets/          # Built-in presets (downloads, media, developer...)
│   ├── rules/            # Custom expression parsing & priority evaluation
│   ├── scanner/          # Cross-platform filesystem traversal & hidden file checks
│   ├── search/           # Multi-core fastwalk + fzf fuzzy search & grep
│   └── watcher/          # Real-time directory monitoring (fsnotify)
└── docs/                 # Web documentation, interactive demo & guide
```

---

## Contribution Workflow

### 1. Issues & Discussions

- **Bug Reports**: Open an issue detailing your OS, Go version, the exact command run, terminal output, and steps to reproduce.
- **Feature Requests**: Describe the problem you are solving, why existing commands/flags are insufficient, and proposed CLI syntax.

### 2. Branching & PR Guidelines

1. Fork the repository and create your branch from `main`:
   ```bash
   git checkout -b feature/your-feature-name
   ```
2. Make clean, atomic commits following [Conventional Commits](https://www.conventionalcommits.org/):
   - `feat: add tag filtering to search`
   - `fix: prevent crash on broken symlinks`
   - `docs: update plugin reference guide`
3. Ensure cross-platform compatibility:
   - Avoid OS-specific syscalls in shared code. Use `//go:build` tags if platform-specific logic is necessary (see `internal/scanner/hidden_windows.go` vs `hidden_other.go`).
   - Use `filepath.Join` and `filepath.Clean` instead of hardcoded `/` or `\` separators.
4. Add unit tests for your changes. Run `go test ./...` and verify all tests pass.
5. Update documentation if introducing new CLI flags, commands, or behaviors.
6. Submit a Pull Request targeting the `main` branch with a clear description of your changes.

---

## Guidelines for New Features

- **Safety First**: Destructive filesystem operations must support `--dry-run`, interactive confirmation (with `-y` bypass), and rollback via `chest undo`.
- **System Protection**: Never allow unrestricted modification of root volumes or OS system directories without the `--allow-system` guard.
- **Zero Heavy CGO**: Keep dependencies pure Go whenever possible to maintain effortless cross-compilation.

---

## Plugin Architecture & Extending Engine Tools

CHEST provides an external plugin system powered by **`hashicorp/go-plugin`** over standard IPC/RPC. Rather than creating isolated standalone tools, CHEST plugins are designed to directly plug into and extend the core engine tools (`sort`, `search`, `watch`, `classifier`).

### Architecture Diagram

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

### How Plugins Interact with Existing Engine Tools

When developing a plugin for CHEST, the objective is **not** to build standalone utilities, but to add domain capabilities to CHEST's built-in tools:

1. **Custom Classification (`Classify`)**:
   - The built-in classifier categorizes standard extensions (`jpg` -> Image, `go` -> Code).
   - A plugin overrides or expands classification for specialized domain files (e.g. classifying DICOM `.dcm` files as `MedicalImaging`, `.spec.ts` as `UnitTests`, or `.wasm` as `WebAssembly`).
   - The engine tools (`sort`, `watch`, `search -t`) automatically use these custom categories when evaluating rules and filter flags.

2. **Dynamic Rule Injection (`GetCustomRules`)**:
   - Plugins can return a slice of `models.Rule` structs with custom priorities, conditions (extension, size, regex, date), and compartment destinations.
   - These rules are injected directly into `rules.Engine`, allowing plugins to provide domain-specific sorting presets without modifying CHEST core.

3. **RPC Process Isolation**:
   - Plugins run as independent child processes communicating over RPC. A crash in a plugin cannot crash the main CHEST engine or corrupt the filesystem.
   - Plugins can be written in Go or any language that implements HashiCorp's plugin handshake protocol.

---

## Working Developer Example

A complete, working reference implementation is located in [`examples/plugins/code-artifacts/`](examples/plugins/code-artifacts/).

### Minimal Plugin Implementation

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

// Extends chest sort, watch, and search -t
func (c *DomainClassifier) Classify(name string, ext string) (string, error) {
    if strings.HasSuffix(name, ".coverage.out") || strings.HasSuffix(name, ".lcov") {
        return "CoverageReports", nil
    }
    if ext == "wasm" {
        return "WebAssembly", nil
    }
    return "", nil // Fallback to CHEST's built-in classifier
}

// Injects dynamic rules directly into rules.Engine
func (c *DomainClassifier) GetCustomRules() ([]models.Rule, error) {
    return []models.Rule{
        {
            Name:        "WebAssembly Binaries",
            Priority:    80,
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

#### 3. Build & Install into CHEST

```bash
# Build binary
go build -o code-artifacts .

# Install into ~/.chest/plugins/
chest plugin install .

# Verify
chest plugin list
chest plugin info code-artifacts
```

#### 4. Test with Existing Engine Tools

```bash
# Sort will now automatically route .wasm into Build/Wasm/
chest sort ~/Projects/my-app --dry-run

# Search will now match custom category
chest search -t WebAssembly ~/Projects/my-app
```
