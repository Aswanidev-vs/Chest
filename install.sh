#!/usr/bin/env bash
# install.sh — CHEST CLI installer for macOS and Linux
#
# Usage:
#   curl -sSL https://raw.githubusercontent.com/Aswanidev-vs/chest/main/install.sh | bash
#
# Flags:
#   --update        Reinstall the latest version over an existing one
#   --uninstall     Remove an existing install
#   --to <dir>      Install into <dir> instead of the Go toolchain default
#                   (e.g. --to ~/.local/bin). The directory is created if missing.
#   --no-verify     Skip the `chest version` sanity check after install
#   --source        Build from a local clone of the repo instead of the module proxy
#   -h, --help      Show this help text
#   -V, --version   Show the script version
#
# Exit codes:
#   0   success
#   1   generic failure
#   2   Go is not installed
#   3   Go is too old
#   4   install path is not writable
#   5   sanity check failed after install
#   6   user-supplied --to path is invalid

set -u
# NOTE: no `set -e` — we want to handle failures ourselves and produce
# useful error messages instead of silent aborts.

SCRIPT_VERSION="0.1.0"
REPO="github.com/Aswanidev-vs/chest"
MODULE="${REPO}/cmd/chest@latest"
MIN_GO_MAJOR=1
MIN_GO_MINOR=22   # go 1.22 is the floor for the `for-range over int` and toolchain features used

# ---------- helpers ----------
log()   { printf '%s\n' "$*" >&2; }
info()  { log "  $*"; }
ok()    { log "  ✓ $*"; }
warn()  { log "  ! $*"; }
err()   { log "  ✗ $*"; }
hr()    { log "  ─────────────────────────────────────────────"; }

# Print a labelled value, right-aligned to 22 chars on the label side.
field() {
    local label="$1" value="$2"
    printf '  %-22s %s\n' "$label" "$value" >&2
}

usage() {
    sed -n '2,20p' "$0" | sed 's/^# \{0,1\}//'
}

# ---------- arg parsing ----------
UPDATE=0
UNINSTALL=0
TO_DIR=""
VERIFY=1
USE_SOURCE=0

while [ $# -gt 0 ]; do
    case "$1" in
        --update)        UPDATE=1 ;;
        --uninstall)     UNINSTALL=1 ;;
        --no-verify)     VERIFY=0 ;;
        --source)        USE_SOURCE=1 ;;
        --to)            shift; TO_DIR="${1:-}" ;;
        --to=*)          TO_DIR="${1#--to=}" ;;
        -h|--help)       usage; exit 0 ;;
        -V|--version)    printf 'install.sh %s\n' "$SCRIPT_VERSION"; exit 0 ;;
        *)               err "unknown flag: $1"; usage; exit 1 ;;
    esac
    shift
done

# ---------- header ----------
hr
log "  CHEST installer  v${SCRIPT_VERSION}"
log "  repo: ${REPO}"
hr

# ---------- uninstall path ----------
if [ "$UNINSTALL" = "1" ]; then
    BIN_DIR="$(go env GOPATH 2>/dev/null)/bin"
    if [ -n "$TO_DIR" ]; then BIN_DIR="$TO_DIR"; fi
    TARGET="$BIN_DIR/chest"
    if [ -f "$TARGET" ]; then
        if rm "$TARGET"; then
            ok "removed $TARGET"
        else
            err "could not remove $TARGET (check permissions)"
            exit 4
        fi
    else
        warn "no chest binary found at $TARGET"
    fi
    log "  done."
    exit 0
fi

# ---------- preflight: Go ----------
if ! command -v go >/dev/null 2>&1; then
    err "go is not on PATH."
    err "Install Go ${MIN_GO_MAJOR}.${MIN_GO_MINOR}+ from https://go.dev/dl/ and re-run."
    exit 2
fi

GO_VERSION_RAW="$(go version 2>/dev/null | awk '{print $3}')"   # e.g. go1.22.3
GO_VERSION="${GO_VERSION_RAW#go}"
GO_MAJOR="$(printf '%s' "$GO_VERSION" | cut -d. -f1)"
GO_MINOR="$(printf '%s' "$GO_VERSION" | cut -d. -f2)"
if [ "${GO_MAJOR:-0}" -lt "$MIN_GO_MAJOR" ] || \
   { [ "${GO_MAJOR:-0}" = "$MIN_GO_MAJOR" ] && [ "${GO_MINOR:-0}" -lt "$MIN_GO_MINOR" ]; }; then
    err "Go ${GO_VERSION} is too old. Need ${MIN_GO_MAJOR}.${MIN_GO_MINOR}+."
    err "Update at https://go.dev/dl/ and re-run."
    exit 3
fi

field "go"               "$GO_VERSION_RAW"
field "platform"         "$(go env GOOS)/$(go env GOARCH)"

# ---------- resolve target directory ----------
if [ -n "$TO_DIR" ]; then
    # Validate the user-supplied directory
    if [ -e "$TO_DIR" ] && [ ! -d "$TO_DIR" ]; then
        err "--to path exists and is not a directory: $TO_DIR"
        exit 6
    fi
    if ! mkdir -p "$TO_DIR" 2>/dev/null; then
        err "cannot create --to directory: $TO_DIR"
        exit 6
    fi
    if [ ! -w "$TO_DIR" ]; then
        err "--to directory is not writable: $TO_DIR"
        exit 4
    fi
    TARGET_DIR="$TO_DIR"
    info "install target: $TARGET_DIR (user-supplied)"
elif [ "$UPDATE" = "1" ] || [ -n "$(go env GOBIN)" ]; then
    # Respect GOBIN if set; otherwise use GOPATH/bin
    if [ -n "$(go env GOBIN 2>/dev/null)" ]; then
        TARGET_DIR="$(go env GOBIN)"
    else
        TARGET_DIR="$(go env GOPATH)/bin"
    fi
    info "install target: $TARGET_DIR (go toolchain default)"
else
    GOBIN_RAW="$(go env GOBIN 2>/dev/null || true)"
    GOPATH_RAW="$(go env GOPATH 2>/dev/null || true)"
    if [ -n "$GOBIN_RAW" ]; then
        TARGET_DIR="$GOBIN_RAW"
    elif [ -n "$GOPATH_RAW" ]; then
        TARGET_DIR="$GOPATH_RAW/bin"
    else
        TARGET_DIR="$HOME/go/bin"
    fi
    info "install target: $TARGET_DIR (default)"
fi

# Make sure it exists
if [ ! -d "$TARGET_DIR" ]; then
    if ! mkdir -p "$TARGET_DIR" 2>/dev/null; then
        err "cannot create install directory: $TARGET_DIR"
        exit 4
    fi
fi
if [ ! -w "$TARGET_DIR" ]; then
    err "install directory is not writable: $TARGET_DIR"
    err "try: --to ~/.local/bin"
    exit 4
fi

TARGET_BIN="$TARGET_DIR/chest"
field "binary"           "$TARGET_BIN"

# ---------- install ----------
hr
log "  installing…"

if [ "$USE_SOURCE" = "1" ]; then
    # Build from a local clone (rarely needed; provided for completeness)
    if ! command -v git >/dev/null 2>&1; then
        err "--source requires git on PATH"
        exit 1
    fi
    WORK="$(mktemp -d -t chest-build.XXXXXX)"
    trap 'rm -rf "$WORK"' EXIT
    info "cloning $REPO into $WORK"
    if ! git clone --depth 1 "https://${REPO}.git" "$WORK/src" >/dev/null 2>&1; then
        err "git clone failed"
        exit 1
    fi
    info "building (this may take a minute on first run)"
    ( cd "$WORK/src" && go build -o "$TARGET_BIN" ./cmd/chest ) || {
        err "go build failed"
        exit 1
    }
else
    info "running: go install $MODULE"
    # go install needs the bin dir on PATH so it can locate the resulting
    # binary, but it places the binary itself into GOBIN/GOPATH-bin.
    GOFLAGS="${GOFLAGS:-}" GOBIN="$TARGET_DIR" go install "$MODULE" 2>&1 | sed 's/^/    /' >&2
    if [ ! -f "$TARGET_BIN" ]; then
        err "go install completed but no binary was placed at $TARGET_BIN"
        err "if the module proxy is unreachable, try --source and ensure git is installed"
        exit 1
    fi
fi

ok "installed $TARGET_BIN"

# ---------- verify ----------
if [ "$VERIFY" = "1" ]; then
    hr
    log "  verifying…"
    if "$TARGET_BIN" version >/dev/null 2>&1; then
        VERSION_OUT="$("$TARGET_BIN" version 2>&1 | head -1)"
        ok "chest responds: $VERSION_OUT"
    else
        err "chest at $TARGET_BIN did not run cleanly"
        err "try: $TARGET_BIN version"
        exit 5
    fi
fi

# ---------- PATH advice ----------
hr
# Is TARGET_DIR on PATH?
ON_PATH=0
IFS=':' read -r -a PATH_DIRS <<< "$PATH"
for d in "${PATH_DIRS[@]}"; do
    if [ "$d" = "$TARGET_DIR" ]; then ON_PATH=1; break; fi
done

if [ "$ON_PATH" = "1" ]; then
    log "  ✓ $TARGET_DIR is on PATH — `chest` is ready to use."
else
    warn "$TARGET_DIR is NOT on PATH."
    warn "Add this line to your shell profile (~/.zshrc, ~/.bashrc, ~/.profile):"
    log ""
    log "      export PATH=\"$TARGET_DIR:\$PATH\""
    log ""
    warn "Then restart the shell, or run:"
    log "      export PATH=\"$TARGET_DIR:\$PATH\""
fi

log ""
log "  quick start:"
log "      chest sort ~/Downloads -p downloads --dry-run"
log "      chest man chest"
log ""
hr
log "  done."
