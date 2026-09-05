# Example CHEST Plugin: Code Artifacts Classifier

This is a working, production-ready example of an external CHEST plugin.

Rather than running standalone, this plugin interfaces directly with CHEST core via HashiCorp RPC (`go-plugin`). It enhances CHEST's existing engine tools (`sort`, `search`, `watch`) with:

1. **Custom Classification (`Classify`)**: Identifies test coverage files (`.coverage.out`, `.lcov`) as `CoverageReports`, WebAssembly binaries (`.wasm`) as `WebAssembly`, and TypeScript declaration maps (`.d.ts.map`) as `TypeMaps`.
2. **Injected Engine Rules (`GetCustomRules`)**: Directly injects sorting rules into `chest sort` and `chest watch` to automatically compartment WASM artifacts and test coverage reports.

---

## Directory Structure

```
examples/plugins/code-artifacts/
├── go.mod           # Independent Go module
├── main.go          # Plugin RPC service implementation
├── manifest.json    # Plugin metadata & capabilities
└── README.md        # This guide
```

---

## Building and Installing into CHEST

### 1. Build the Plugin Binary

From this directory:

```bash
# Linux / macOS
go build -o code-artifacts .

# Windows PowerShell
go build -o code-artifacts.exe .
```

### 2. Install into CHEST

Use the CHEST CLI to install this plugin:

```bash
chest plugin install .
```

This copies the binary and `manifest.json` into `~/.chest/plugins/code-artifacts/`.

### 3. Verify Installation

```bash
# List installed plugins
chest plugin list

# Inspect detailed metadata
chest plugin info code-artifacts
```

---

## How It Interacts with CHEST Engine

Once installed:

- **`chest sort` & `chest watch`**: Automatically pick up rules from `GetCustomRules()` and compartment `.wasm` files into `Build/Wasm/` and `.coverage.out` files into `Reports/Coverage/`.
- **`chest search -t CoverageReports`**: The search engine uses the plugin's `Classify()` method to match custom category filters.

---

## Uninstalling

```bash
chest plugin remove code-artifacts
```
