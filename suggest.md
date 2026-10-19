# CHEST — New Feature Suggestions

> **Scope of this document:** analysis-only. No code was modified. Suggestions are grounded in
> the codebase at **v0.7.0** so that nothing below duplicates what already
> ships. Items marked ✅ were implemented on top of that baseline and released in **v0.8.0**.
> Items previously proposed in `plan.md` are marked *(plan.md §3-N)* (its section 3,
> "Evaluation of Proposed Features", item N) or *(plan.md §4-N)* (section 4, "Additional Ideas",
> item N); items marked **new** were not in `plan.md`. Effort: **S** = days, **M** = ~1–2 weeks,
> **L** = multi-week. Risk = chance of regressing existing behavior.
>
> **Note:** cloud/network sync remains explicitly deferred per the project owner's earlier
> request and is not re-proposed here.

---

## 0. Delivery status

**13 items below are marked ✅ and are now implemented, tested and documented.** They shipped as
seven new commands plus new flags on existing ones:

| Area | What shipped |
|---|---|
| New commands | `plan` (+`apply`/`diff`), `rename`, `manifest`, `verify`, `doctor`, `tree`, `checkpoint` |
| Safety | `undo --to <checkpoint>`, `undo --file <name>`, `history --prune --keep`, `sort --hash-collisions` |
| Analysis | `duplicates --similar` (pHash), `duplicates --quarantine`, `stats --trend`, `rule eval`, `index --watch` |
| Output | `search --csv`, `search --out`, `search --save/--load` |

Notable safety properties, each covered by tests: `plan --out` is provably read-only;
`plan apply` is all-or-nothing; `undo --to` validates the whole batch before moving anything;
`--hash-collisions` hashes **only** the conflicting subset; `verify` exits **4** on drift;
`doctor` opens the index read-only; `rename` can never move a file between directories.

Still open: `search --index` (#1), rule composition `extends` (#11), archive auto-extract (#12),
folder aliases (#13), event hooks (#18), `analyze --profile` (#24), and the Phase 4 stretch items.

---

## 1. Where CHEST stands today (baseline)

Implemented and out of scope for re-proposal: `chest.toml` loader with precedence chain,
interactive `repl` (save/load templates, read-only preview), `rule check`, rule DSL with
metadata fields (`camera=`, `meta.<key>=`), report export (`--json`/`--csv`/`--out`),
`schedule` (cron via OS schedulers), filename date mining (`--date-source filename`),
intra-plan conflict detection with auto-rename + CONFLICTS block, session-batched watch history,
plugin classifier/metadata RPC, battery (`cellwatch`), speedtest, `man`, `completion`, `update`.

The suggestions below are grouped by theme, then by effort within each theme.

---

## 2. Search & Index Intelligence

| # | Feature | Effort | Risk | Notes |
|---|---|---|---|---|
| 1 | **`search --index`** *(plan.md §4-6)* | S | Low | Today `internal/search/search.go` always does a live `fastwalk` + fzf pass. Query the existing SQLite `~/.chest/index.db` instead: `chest search --index "report" -e pdf`. Instant on huge trees; keep the live walk as `--live`/fallback when the index is stale. |
| 2 | **FTS5 full-text content search** — **new** (owner's own issue #57) | M | Low | `ncruces/go-sqlite3` ships an FTS5 extension that is not yet used. `chest search --index --content "invoice"` would index text-file contents once and answer content greps in milliseconds instead of re-walking the tree every run. Natural pairing with `--content` output columns and `--json` export. |
| 3 | ✅ **Continuous/background indexing** — **new** | S | Low | **Not** about fixing stale reports — query-time refresh already exists (`refreshIndexBeforeReport`, `internal/cli/intel.go:33`, called by `stats` at `:331` and `analyze` at `:507`; `duplicates` re-indexes when given a path at `:413`). Real value: keep the index warm so those commands stop re-indexing at query time, make **bare** `chest duplicates` trustworthy (with no path argument it queries the cache as-is, `:403-416`), and let a future `search --index` (#1) be trustworthy without a fresh walk. |
| 4 | ✅ **`search --csv` + saved searches** — **new** | S | Low | `--json` already exists on `search`; add `--csv` for parity with `emitReport` in `internal/cli/intel.go`, plus `~/.chest/searches/` named queries (`chest search --save recent-pdfs`, `chest search --load recent-pdfs`). |

---

## 3. Safety & Reversibility (the core promise)

| # | Feature | Effort | Risk | Notes |
|---|---|---|---|---|
| 5 | ✅ **Checkpoints & selective undo** *(plan.md §3-12)* | M | **High** | `chest checkpoint --name before_cleanup`, `chest undo --to before_cleanup`, `chest undo <id> --file photo.jpg`, plus history retention/rotation to cap unbounded `history.json` growth. The `indexer` already has **zero-caller** `history_entries`/`history_operations` SQLite tables — activating them gives queryable history for free. This is the safety net; it needs the strongest test coverage of any item here. |
| 6 | ✅ **Content-aware conflict resolution** *(plan.md §7 Stage 3)* | S | Medium | `--hash-collisions`: hash **only the conflicting subset** (typically 0–20 files), never the whole tree. Same hash → genuine duplicate → auto-skip; different hash → distinct content → disambiguate. The plumbing exists (`indexer.hashFile`, `--hash` stores SHA-256). |
| 7 | ✅ **`chest plan save/apply/diff`** *(plan.md §4-3)* | M | Low | `sort --dry-run --json` already emits the full plan; make it a first-class artifact: `chest plan --out plan.json` → review → `chest plan apply plan.json`, and `chest plan diff a.json b.json`. Bridges CI automation and the future Web UI (#28). |
| 8 | ✅ **Opt-in duplicate quarantine** — **new** | S | Medium | `chest duplicates` stays read-only by design (PRD §30). Add a *separate, explicit* follow-up: `chest duplicates --quarantine <dir>` moves duplicates into a staging folder after confirmation, recorded in history so `chest undo` restores them. Policy-sensitive: never auto-delete, never implied by detection alone. |

---

## 4. Organization Power

| # | Feature | Effort | Risk | Notes |
|---|---|---|---|---|
| 9 | **Rule DSL v2: OR, negation, named rules** *(plan.md §4-2)* | M | Low | `ParseRule` splits on `&&` only. Add `\|\|`, `!(...)`, and `name:` labels so ad-hoc rules are expressive without TOML. |
| 10 | ✅ **`chest rule eval <file>` explain** — **new** *(plan.md §4-1 partially)* | S | Low | Print which rule matches a given file and why (the CLI version of the repl's preview loop). Pairs with `rule check` which already validates syntax — this validates *behavior*. |
| 11 | **Rule composition `extends`** *(plan.md §3-13)* | S–M | Low | TOML-native inheritance (`extends = "base-video"`) merged at load time in `internal/config`. Depends on #9 only for CLI-syntax parity. |
| 12 | **Archive auto-extract & organize** *(plan.md §3-6)* | M | Medium | `sort --auto-extract`: extract to temp → feed entries through the normal scan→plan→execute pipeline → keep the archive unless everything succeeded. `metadata` already detects ZIP/GZIP/TAR/7z/RAR/ISO. Must stay dry-run-friendly and never delete an archive on failure. |
| 13 | **Folder aliases** *(plan.md §3-7)* | S–M | Low | `{alias:Movies}` or `alias:...` destinations resolved from `~/.chest/aliases.toml` — portable rules across machines/NAS volumes. Implementation is contained to `Planner.expandDestination`. |
| 14 | **Sidecar co-move** — **new** | S–M | Low | A rule match on `photo.jpg` also moves `photo.xmp`, `photo.jpg.supplemental-metadata.json`; `movie.mkv` pulls `movie.mkv.srt`/`.nfo`/`.ass`. Small, high-value for media libraries; respects collision policy as a group. |
| 15 | ✅ **`chest rename` — rule-based batch rename** — **new** | M | Medium | Normalize messy filenames (`IMG_2023  copy(2).JPG` → `2023-01-15.jpg`) using the existing rule/date/naming machinery, with dry-run, collision policy, history and `undo`. Same safety model as `sort`, new verb — keeps rename out of the organization path. |
| 16 | ✅ **Near-duplicate (perceptual) image detection** — **new** | M | Low | `chest duplicates --similar` groups burst shots / re-saves via perceptual hash (pHash), reported separately from exact SHA-256 matches. Read-only, like all duplicate reporting. |
| 17 | ✅ **`chest manifest` / `chest verify`** — **new** | S–M | Low | Export a SHA-256 inventory (`manifest.json`/`.csv`) of a folder for backup integrity; `chest verify` re-checks it and reports changed/missing/added files. Reuses `indexer` hashing and the report-export helper. |

---

## 5. Automation & Integration

| # | Feature | Effort | Risk | Notes |
|---|---|---|---|---|
| 18 | **Event hooks + notifications** *(plan.md §3-8)* | M | Low | No pre/post-execution command hooks exist today — despite the name, `config.Hooks` (`internal/config/resolve.go:27`) is unrelated: it is an internal struct carrying `PluginServices`/`PluginRules` into the rule resolver, not an event system. `[hooks] pre/post = [...]` in `chest.toml` would run external commands with `CHEST_OP`, `CHEST_PLAN_FILE`, `CHEST_COUNT`, `CHEST_ROOT` env vars. Two clean seams exist: post-plan/pre-execute in `sort.go`, and post-`Execute` after history is recorded. Use cases: desktop notification after a watch session, Plex/Jellyfin reindex, webhook into a chat channel. |
| 19 | **Watch as a background service** — **new** | M | Low | Missing today: `--quiet`, `--daemon`, `--exclude`. Add `chest watch install/uninstall` to register a login-time background watcher (systemd unit / Windows scheduled task) — the `internal/scheduler` installers already pattern-match both platforms. |
| 20 | **Finer schedule granularity** — **new** | S | Low | On Windows, `schtasks` installation maps only a daily time; richer cron expressions are **rejected loudly**, not silently degraded (`internal/scheduler/installer.go:78-80`: "cron %q is too complex for a Windows scheduled task"). Support hourly/weekly/monthly expressions and `schedule add --on login`. |
| 21 | **Plugin lifecycle hardening** *(plan.md §4-8)* | M | Medium | `plugin enable/disable`, `plugin update`, checksum verification on `install`, RPC deadlines + batched `Inspect` (today it is one round-trip *per file*), and actually enforcing the declared `preset` capability (declared in docs, never checked). |
| 22 | **MCP server (read-only)** — **new** | S–M | Low | `chest serve --mcp` exposing **read-only** tools — `search`, `stats`, `analyze`, `duplicates`, `plan-preview` — over the Model Context Protocol. A thin stdio wrapper around the existing `--json` commands; fully local-first. **Do NOT expose `sort`/`undo`/`clean`** by default — the plan/apply split covers legitimate mutation safely, and an LLM freely reorganizing a codebase is a footgun. |
| 23 | **`chest config` command** — documented in PRD §8.13, not built | S | Low | `chest config init` scaffolds a `chest.toml`, `chest config show` prints the *effective* rule set with its source (flag / rule / project / user / preset / plugin), `chest config path` prints which file was discovered. Turns the precedence chain from invisible behavior into something users can debug. |

---

## 6. Analysis & Reporting

| # | Feature | Effort | Risk | Notes |
|---|---|---|---|---|
| 24 | **`analyze --profile` stage timing** *(plan.md §3-15)* | S–M | Low | Time each stage (scan / signature detect / metadata RPC / hash / rule match / moves) and print actionable hints: "hashing dominates → drop `--hash`", "huge tree → add `--except`". Progress callbacks already mark stage boundaries; `analyze` already prints a runtime footer. |
| 25 | ✅ **`chest doctor`** — **new** | S | Low | One read-only health check: index freshness vs. disk, orphaned history entries, unreadable directories, broken symlinks, stale schedules, plugin handshake status, config validity (delegates to `rule check`). Cheap to build from existing pieces; great first command for new users. |
| 26 | ✅ **`chest tree`** — **new** | S–M | Low | Box-drawing / treemap-style directory visualization (`--depth`, `--sort size`, `--json`) using the chart rendering already in `internal/cli/chart.go`. Pure read-only sugar over indexed stats. |
| 27 | ✅ **Storage trends over time** — **new** | M | Low | Snapshot aggregate size/category rows per `stats` run and report growth (`chest stats --trend --month`). The SQLite index makes this a schema addition, not a new subsystem. |

---

## 7. Interfaces (stretch)

| # | Feature | Effort | Risk | Notes |
|---|---|---|---|---|
| 28 | **Web UI** *(plan.md §3-10)* | L | Medium | `chest serve --ui :8080` embedding a static SPA over plan/history/stats JSON endpoints. Prereqs: plan JSON (#7) lands first; keep it opt-in so the CLI-only footprint and binary size stay intact. |
| 29 | **TUI dashboard** — **new** | L | Medium | A single-screen dashboard (index freshness, watch status, recent history, duplicate count). Note PRD §45's dependency policy: add a TUI framework only if a real product requirement justifies it — this is the candidate that would justify it, but it is optional. |
| 30 | **GUI** *(PRD Phase 5)* | L | Medium | The core engine is already UI-agnostic (`models.Plan` is pure, `ProgressFunc` drives spinners) — a GUI consumes the same plan/execute/history pipeline. |
| 31 | **`chest install <tool>`** *(PRD Phase 6)* | L | Low | Cross-platform development-tool installer using platform-native sources. Explicitly outside the file-organization engine; listed for completeness only. |

---

## 8. Quick wins worth folding into any release

Small, low-risk items that improve trust and polish while bigger features are in flight:

- **`sort --json` without `--dry-run` is silently ignored** (`internal/cli/sort.go:283`) — either
  imply dry-run when `--json` is present or reject the combination with an error. Today a
  user piping the plan to a script gets their files moved instead of a plan.
- **Missing man topics:** `man.go` defines 19 topics, one of which is the root `chest` topic. Of
  the 21 registered commands, `preset` and `version` have no entry — and neither does `man`
  itself, though that one may be intentional.
- **Watch parity:** `--exclude`/`--except`, `--quiet`, `--hidden` exist on other commands but
  not on `watch`.
- **README duplication:** the speedtest feature and Command Reference row each appear twice
  (README lines ~72/74 and ~93/95).
- **Stale docs:** `plan.md` §7 says the intra-plan conflict fix is "PROPOSAL ONLY" though it is
  implemented; `battery.md` says `cellwatch/` "does not exist yet" though the package ships.

---

## 9. Suggested sequencing

### Phase 1 — Quick wins (each ≤ ~1 week, low risk)
| # | Feature | Why first |
|---|---|---|
| — | §8 quick wins (sort --json, man topics, watch parity) | Trust & polish, trivial |
| 1 | `search --index` | Instant results; index already exists |
| 23 | `chest config` | Documented in PRD; unblocks debugging of precedence |
| 10 | `chest rule eval <file>` | Pairs with existing `rule check` |
| 17 | `chest manifest` / `chest verify` | Reuses hashing + export helpers |

### Phase 2 — Core experience
| # | Feature | Depends on |
|---|---|---|
| 9 | Rule DSL v2 (OR/negation/names) | — |
| 11 | `extends` composition | 9 (CLI parity), `chest.toml` |
| 5 | Checkpoints & selective undo | — (strongest test coverage required) |
| 6 | Content-aware conflicts (`--hash-collisions`) | index hashes |
| 12 | Archive auto-extract | — |
| 2 | FTS5 content search | 1 |

### Phase 3 — Automation & integration
| # | Feature | Depends on |
|---|---|---|
| 18 | Hooks + notifications | `chest.toml`, plan JSON |
| 19 | Watch as a service | scheduler installers |
| 20 | Schedule granularity | — |
| 21 | Plugin lifecycle hardening | — |
| 3 | Continuous indexing | watcher |
| 14 | Sidecar co-move | — |

### Phase 4 — Stretch
| # | Feature | Notes |
|---|---|---|
| 7 | `chest plan` save/apply/diff | Bridge to Web UI & CI |
| 28 | Web UI | Requires 7 |
| 22 | MCP server (read-only) | Better fit: a shell-less **desktop/personal assistant** ("organize my downloads") than a coding harness. Wraps existing `--json`; keep mutating tools off by default |
| 15/16/26/27 | rename, near-dupes, tree, trends | Independent, pick by demand |

---

---

## 10. Appendix — FTS5 implementation design (design only; no code written)

Resolves the eight drawbacks raised against indexing file contents with FTS5. Seven are fully
neutralized by design; two are mitigated by scoping. Everything here reuses machinery that
already ships.

### 10.1 Goal / non-goals

**Goal:** make `chest search -c` (content grep) instant on large trees by answering it from a
persistent index instead of re-reading every file on every run.

**Non-goals:** do not touch name search (fzf stays), do not index PDF/Office **body** text (new
dependency — separate feature), do not change default indexing cost, do not add a network call.

### 10.2 CLI surface

```bash
chest index --content                 # opt-in, mirrors existing --hash gating
chest search --index --content "term" # trigram, case-insensitive, freshness-checked
chest clean --content                 # drop the content DB (NOT implied by --all)
```

Default behavior of both commands is unchanged. `--content` off ⇒ zero added cost.

### 10.3 Schema — separate DB, not a new table in `index.db`

Keeping contents in `index.db` would contradict `prd.md` §39/§404 ("metadata … not file
contents") and make `chest clean --index` destructive by surprise. Use a second file:

```sql
-- ~/.chest/content.db
CREATE VIRTUAL TABLE docs USING fts5(
    body,
    path UNINDEXED,     -- join key back to index.db files.path
    tokenizer='trigram',
    content=''          -- store postings only, no second copy of the text
);
```

`content=''` roughly halves DB size; the cost is losing `snippet()`/`highlight()`. If snippets
matter more than size, drop `content=''` and take the space hit.

### 10.4 Wiring — files and symbols

| File | Change |
|---|---|
| `internal/indexer/indexer.go` | Add `OpenContentDB()` next to `OpenOrCreate()` (line 63). Schema above. |
| `internal/indexer/indexer.go` | Extend `IndexDirectory()` (line 344) with a `content bool` param; tokenize only when set. |
| `internal/indexer/indexer.go` | Extend `loadIndexMap()` (line 525) `fileMeta` with a content-fingerprint so unchanged files skip tokenizing. |
| `internal/cli/index.go` | Wire `--content`, `--content-max-size`. |
| `internal/cli/search.go` | Wire `search --index --content`. |
| `internal/cli/intel.go` | `--json`/`--csv` parity via the existing `emitReport` helper. |
| `internal/search/search.go` | **Leave `searchFileContent()` (line 290) untouched** — it stays the `--live` fallback. |

**Connection registration — the one non-obvious constraint.** `ext/fts5` is a **subpackage of the
already-direct `github.com/ncruces/go-sqlite3@v0.35.4` module** — adopting it changes nothing in
`go.mod` or `go.sum`, and the driver is cgo-free, so the `CGO_ENABLED=0` cross-compile matrix is
unaffected. There is no CGo build-tag alternative: the base WASM blob
(`go-sqlite3-wasm/v5/sqlite3.go`, 4,690,964 bytes) contains **zero** `fts5` references, so
`ext/fts5` is the only way to obtain FTS5 at all. What it does require: `ext/fts5.Register(conn)`
takes a `*sqlite3.Conn`, but `indexer.go:81` sets `db.SetMaxOpenConns(1)`, and `database/sql` may
replace the underlying connection over time. Registering once on a pooled connection is not
guaranteed to stick. Two options, in preference order:

1. **Own one connection** — `sqlite3.Open(dsn)` → `fts5.Register(conn)` → `sql.OpenDB(conn.NewConnector(dsn))`. Deterministic, and matches the existing single-connection posture.
2. **Driver wrapper** that registers on every new connection — more code, survives `MaxOpenConns` changes.

*(Exact `ncruces` API names to be confirmed against v0.35.4 at implementation time.)*

### 10.5 Freshness contract

The dangerous failure mode is a stale index silently answering as if current. Therefore:

- Reuse the existing `size + mtime` skip so unchanged files are never re-read.
- `search --index` must **surface staleness**: auto-refresh (matching what `stats`/`analyze`
  already do) or print "index stale — N files changed, run `chest index --refresh --content`".
- Never let a stale answer look like a fresh one. Prefer the loud option.

### 10.6 Tokenizer

`tokenize='trigram'` (SQLite ≥ 3.34; the ncruces WASM build tracks 3.53). Rationale:

- Preserves today's literal-substring semantics from `searchFileContent()`, so `-c` results don't
  silently change meaning.
- Handles CJK without a segmentation dictionary (`unicode61` does not).
- Case-insensitive by default, which also **fixes** the naive `bytes.ToLower` Unicode bug in `search.go:302`.
- Accepted cost: larger index, slower indexing, and no stemming — but `bytes.Contains` never
  stemmed either, so that is consistent, not a regression.

### 10.7 Performance budget

| Guardrail | Value |
|---|---|
| Default index cost | unchanged (`--content` off) |
| Size cap | skip files > `--content-max-size` (default 5 MB), mirroring the existing <500 MB hash gate |
| Eligibility | plain text only — reuse `internal/classifier` (`category` Code/Document, `text/*` mime) |
| Tokenization | inside the existing `errgroup` pool, parallel with hashing |
| Writes | one transaction shared with the `files` row — not a second commit |
| Concurrency | moot: `SetMaxOpenConns(1)` already serializes writers; WAL/busy_timeout unchanged |

### 10.8 Acceptance criteria

- [ ] `chest index` **without** `--content` produces byte-identical DB state to today (regression guard).
- [ ] First `--content` run over N text files ≈ cost of one `--hash` run; second run ≈ cost of a plain index (only the delta).
- [ ] `search --index --content "invoice"` returns the same file set as `search --content "invoice"` on a small fixture (semantic parity test).
- [ ] Unicode case-folding test: `"İ"` / `"STRASSE"` match correctly (today's `bytes.ToLower` fails these).
- [ ] Editing a file then searching shows the **new** content, never the old (staleness test).
- [ ] A file deleted since indexing is absent from results and purged from `content.db`.
- [ ] `chest clean --index` leaves `content.db` intact; `chest clean --content` removes it.
- [ ] `content.db` is never created unless `--content` was passed.

### 10.9 Residual risks — what this design does *not* solve

| Risk | Status |
|---|---|
| Contents at rest in `~/.chest/content.db` | **Mitigated, not eliminated.** Still a conscious deviation from the PRD's metadata-only stance. It stays local (PRD §35 holds), but the author should amend §39/§404 to state the opt-in exception rather than leave the docs contradicting the code. |
| PDF/DOCX body-text search | **Out of scope.** Requires a new PDF text-extraction dependency; ship as a separate feature. |
| Binary files | Never indexed (existing NUL-byte heuristic, `search.go:336`). |
| Index growth on huge trees | Monitor; `--content-max-size` + `chest clean --content` are the escape hatches. |
| Binary payload | **+851 kB, and it is compile-time — not gated by `--content`.** Measured: `go-sqlite3-wasm/v5/fts5/fts5.go` = 836,239 B + `fts5.dat` = 15,056 B. Importing `ext/fts5` costs this in **every** shipped binary even if `--content` is never used; only a Go build tag (e.g. `-tags content_search`) can split it, at the cost of doubling the 6-target release matrix. This is the single real cost of the feature, and the only decision that matters — integration is otherwise free (no new modules, no CGo, no CI change). |

---

*Generated from a read-only review of the CHEST codebase at v0.7.0, with the ✅ items above
implemented and shipped in v0.8.0: CLI surface in
`internal/cli/root.go`, rule/config/planner packages, watcher/scheduler/plugin/indexer layers,
plus README/PRD/plan history and open GitHub issues.*
