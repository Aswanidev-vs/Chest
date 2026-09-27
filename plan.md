# CHEST — Codebase Analysis & New Feature Proposals

> **Scope of this document:** analysis-only. No code was modified. Each proposed feature is evaluated against what already exists in the codebase, with effort/risk estimates and a phased roadmap. **Feature #3 (Sync to Cloud / Network Drives) is explicitly deferred** per project owner's request (see §6).

---

## 1. Codebase Analysis Summary

### 1.1 Architecture Pipeline

```
scan (internal/scanner, fastwalk-parallel)
  → classify (internal/classifier, magic-byte + extension)
  → enrich  (internal/metadata, EXIF/QuickTime/PDF/audio/OOXML + plugin RPC)
  → match   (internal/rules, priority-ordered first-match engine)
  → plan    (internal/planner, pure model — no I/O mutation)
  → execute (internal/organizer + internal/filesystem, collision policies)
  → journal (internal/history, ~/.chest/history.json → chest undo)
```

Side systems: `internal/indexer` (pure-Go SQLite `~/.chest/index.db`, SHA-256 hashing), `internal/search` (fastwalk + fzf `FuzzyMatchV2`), `internal/watcher` (fsnotify + 500ms debounce), `internal/plugin` (hashicorp/go-plugin, protocol v2, `classifier` + `metadata` RPC services).

### 1.2 CLI Surface (registered in `internal/cli/root.go`)

| Group | Commands |
|---|---|
| Core | `sort`, `watch`, `search` |
| Intel | `index`, `stats`, `duplicates`, `analyze`, `clean` |
| Safety | `history`, `undo` (+ `undo cache`) |
| Extensibility | `preset`, `plugin` (`list/info/install/remove`) |
| Meta | `version`, `completion`, `man`, `update`, `speedtest` |

### 1.3 How Rules Are Defined Today

Three sources, assembled ad-hoc in `internal/cli/sort.go` (~line 138) **and re-implemented separately in `internal/watcher/watcher.go`**:

1. **Presets** — 5 embedded TOML files (`internal/presets/toml/*.toml`) via `//go:embed` + `BurntSushi/toml` (`downloads`, `media`, `documents`, `developer`, `photos`).
2. **`--rule` DSL strings** — `rules.ParseRule` (`internal/rules/engine.go:284`), e.g. `type=video && size>1GB -> Videos/Large`, `*.mp4 -> Videos`, `name=~'^IMG_' -> Camera`. **AND-only** (`&&`), no `||`, no negation. Priorities auto-assigned `100-i`.
3. **Flag-generated rules** — `-t/--type`, `-f/--format`, `-s/--size`, `-d/--date`.

**Critical finding: there is no `chest.toml` loader.** A repo-wide search for `chest.toml` returns zero hits, even though `models.Rule`/`models.Condition` already carry `toml:` struct tags (`internal/models/rule.go`) and `prd.md` promises TOML configuration. The schema is TOML-ready; only the loader is missing.

### 1.4 Rule Matching Capabilities

- **Fields:** `extension`, `format`, `type` (category), `size`, `name`, `date`/`taken_date`, `mime`.
- **Operators:** `=`, `!=`, `>`, `<`, `>=`, `<=`, `contains`, `starts_with`, `ends_with`, `glob`, `regex`.
- **Evaluation:** `Engine.Evaluate` — first match wins, rules sorted by descending `Priority`.
- **Destination placeholders:** `{ext}`, `{date}`, `{year}`, `{month}`, `{day}` with `--date-source modified|auto|taken`, `--date-granularity`, `--month-format`.
- **Reasons:** every matched condition produces a human-readable reason shown in dry-run previews.

### 1.5 Safety & Preview

- Full dry-run (`-n/--dry-run`) with FROM/TO/**WHY** per file; interactive confirmation unless `-y`.
- Collision policies: `skip` (default), `rename`, `replace`, `abort` — enforced at **plan time** in `Planner.Plan` (`internal/planner/planner.go:195-212`).
- Undo: `history.Manager` pre-flights **all** ops (all-or-nothing), moves back, prunes emptied dirs.
- Protected-path guard: `filesystem.IsSystemPath` (`internal/filesystem/guard.go`) with `--allow-system` override.

### 1.6 Extension Points (where new features plug in)

| Extension point | Location | Used by |
|---|---|---|
| `metadata.Extractor` registry (`Register`/`RegisterCustom`) | `internal/metadata/metadata.go` | built-in formats, `.gocut` example |
| Plugin `ClassifierService` (`Classify`, `GetCustomRules`) | `internal/plugin/plugin.go` | rule injection into `sort` |
| Plugin `MetadataService` (`Inspect`) | `internal/plugin/metadata.go` | file enrichment |
| `planner.Option` functional options | `internal/planner/planner.go:31-41` | new config knobs |
| `ScanOptions.ClassifyFunc` / `EnrichFunc` | `internal/scanner` | per-invocation swapping |
| `rules.NewEngine([]models.Rule)` | any feature can inject rules pre-evaluation | |

---

## 2. Gap Matrix

| Area | Current state | Missing piece |
|---|---|---|
| Config file | Presets embedded only; rules rebuilt per-command | `chest.toml` loader, user presets, project profiles |
| Interactivity | Only y/N confirms + spinners | REPL / guided rule builder |
| Rule DSL | AND-only, implicit priority magic numbers, silent parse failures | OR, negation, validation command, `extends` |
| Collisions | Global `skip/rename/replace/abort`, disk-existence only at plan time | Intra-plan collision detection, content-aware (hash) decisions |
| Undo/journal | One entry per `Execute`; **watch mode fragments into 1-op entries**; no redo/retention | Checkpoints, session grouping, selective undo |
| History storage | JSON file rewritten whole (O(n)); **SQLite history tables exist but have zero callers** | Queryable history, rotation |
| Report output | `analyze/stats/duplicates/history/dry-run` print human text only | `--json`/`--csv`/file export (structs already JSON-tagged) |
| Dates | mtime or embedded content metadata only | Filename fuzzy-date mining (`IMG_20230115_...` etc.) |
| Scheduling | Continuous `watch` only | cron/interval one-shot runs |
| Plugin lifecycle | Local-dir install, no verify/enable/disable/timeouts | Hardening + `plugin update` |
| Watch + plugins | Docs promise `GetCustomRules()` applies to watch | **Bug: `watch.go` never wires classifier plugins** |
| Metadata plugin perf | One RPC `Inspect` per file (5 KiB sample) | Batching + RPC deadlines |
| Search | Always a live walk | `--index` mode against SQLite cache |
| Profiling | `analyze` runtime footer only | Stage timing (I/O vs hash vs match) |
| Web UI / REPL / aliases / hooks / auto-extract / rule composition / sync | None | See §3 |

---

## 3. Evaluation of Proposed Features

Effort: **S** = days, **M** = ~1–2 weeks, **L** = multi-week. Risk = chance of regressing existing behavior.

### Tier: High-Impact, Low-Risk

#### #1. Rules-as-Code Builder (REPL / Interactive Mode) — **Build** · Effort **M** · Risk Low
- **Fit:** Excellent. `Planner.Plan` is already a pure function producing a previewable `models.Plan` — an interactive loop can re-plan after each condition tweak with zero filesystem mutation, reusing `printDryRun` (`internal/cli/sort.go:482`).
- **Touched:** new `internal/cli/repl.go`, `rules` parse/serialize helpers, template storage under `~/.chest/templates/`.
- **Notes:** doubles as the UI for validating `chest.toml` rules; templates become the seed for user presets (see #2/#13).

#### #2. Configuration Profiles / Projects — **Build (highest leverage)** · Effort **M** · Risk Low
- **Fit:** Essentially pre-wired — `models.Rule` already has `toml:` tags; the decode path is proven by `presets.LoadPreset`. A loader + precedence chain (flags > `--rule` > project `chest.toml` > user `~/.chest/chest.toml` > preset) is straightforward.
- **Touched:** new `internal/config` package; **refactor** the duplicated rule-assembly logic in `sort.go` and `watcher.go` into one shared resolver (this also fixes the watch/plugin-rules gap).
- **Commands:** `chest project init`, `chest project run`, plus discovery of `chest.toml` by walking up from the target dir (like `git`).
- **Notes:** foundation for #13 (rule inheritance) and team-shared configs.

#### #4. Advanced Metadata Extraction as Built-in Plugin — **Adapt (≈70% already exists)** · Effort **S** · Risk Low
- **Current state:** EXIF `DateTimeOriginal/CreateDate`, QuickTime `mvhd`, PDF Info dict, ID3/Vorbis/RIFF audio, ZIP-based OOXML/ODF/EPUB — all built in (`internal/metadata/formats.go`), magic-byte detection first, plus an open `Extractor` registry and a metadata **plugin RPC** already shipped.
- **Remaining work:** expose it for *sorting decisions* — e.g. `--sort-by metadata` and rule fields like `artist=`, `camera=`, `codec=`; document the extractor registry as the "bundled optional plugin" API; optionally add video-codec/duration parsing (only `mvhd` time exists today).

#### #5. Smart Conflict Resolution Engine — **Adapt** · Effort **M** · Risk Medium
- **Fit:** Strong. `filesystem.ResolveCollision` exists; SHA-256 hashes already live in `index.db` (`indexer.hashFile`, files <500 MB); `DuplicateGroup` logic shows the pattern.
- **Work:** add a plan-time intra-plan collision check (two ops targeting the same name — currently undetected), a `hash-compare` policy (identical content → auto-skip/merge; different → rename with date suffix `photo.2025-01-15.jpg`), sidecar (`.xmp`, `.nfo`, `.srt`) co-move support, and per-rule collision overrides.
- **Risk:** `replace` semantics must stay byte-identical for backward compatibility — gate new heuristics behind explicit policies.

### Tier: Medium-Impact, Medium-Complexity

#### #6. Archive Auto-Extract & Organize — **Build** · Effort **M** · Risk Medium
- **Fit:** Good. `metadata` already detects ZIP/GZIP/TAR/7z/RAR/ISO formats. Extraction runs as a pre-scan stage in `sort` (`--auto-extract`): extract to temp → feed entries through the normal scanner→planner pipeline → remove archive only on success.
- **Risk:** destructive ops on archives — must default to dry-run-friendly behavior, keep archives on any failure, and respect exclusion patterns.

#### #7. Folder Aliases & Symlink Management — **Build** · Effort **S–M** · Risk Low
- **Fit:** Destinations are plain strings expanded by `Planner.expandDestination` — adding an alias resolver (e.g. `alias:Movies/...` or `{alias:Movies}`) is contained to one function plus a small `~/.chest/aliases.toml` store.
- **Benefit:** portable rules across machines/NAS volumes; pairs naturally with `chest.toml` profiles (#2).

#### #8. Event Hooks (Pre/Post Callbacks) — **Build** · Effort **M** · Risk Low
- **Fit:** Two clean seams already exist: pre-execution (post-`Plan`, pre-`Execute` in `sort.go`) and post-execution (after `organizer.Execute` records history).
- **Design:** hooks declared in `chest.toml` (`[hooks] pre = [...], post = [...]`) executed as external commands with env vars (`CHEST_OP`, `CHEST_PLAN_FILE`, `CHEST_FILES_COUNT`, `CHEST_ROOT`); optionally a plugin protocol v3 hook service later.
- **Use cases:** media-library reindex, desktop notifications, archival pipeline triggers.

#### #9. Scheduled Organizing via Cron/Systemd Timer — **Build** · Effort **M** · Risk Low
- **Fit:** `watch` already covers continuous mode; scheduling is "run a plan periodically". `prd.md:1889` already lists scheduled sorting as future work.
- **Design:** `chest schedule add --cron "0 2 * * *" --preset downloads <dir>` writing to `~/.chest/schedules.toml` + `chest schedule list|remove|run-now`. OS integration: systemd timer / cron file generation on Linux, `schtasks` registration on Windows (helpers already pattern-matched by `install.sh`/`install.ps1`).

#### #10. Web UI / Dashboard — **Stretch** · Effort **L** · Risk Medium
- **Fit:** Possible — the core engine is UI-agnostic (pure `Plan` + `ProgressFunc` callbacks already drive spinners/progress bars). `chest serve --ui :8080` would embed a static SPA and expose JSON endpoints over plan/history/stats.
- **Prereqs:** report export (#14) and plan JSON serialization land first; keep it an opt-in subcommand so the CLI-only footprint and binary size stay intact.

### Tier: Refinements & Polish

#### #11. Smarter Date Handling (fuzzy filename dates) — **Build (confirmed gap)** · Effort **S–M** · Risk Low
- **Current state:** **no filename date mining exists anywhere** — dates come only from mtime or embedded content metadata. `report_20250126.pdf`, `IMG_20230115_...`, `WhatsApp Image 2024-05-01` are invisible to CHEST today.
- **Work:** a `filename` date source (patterns for `20060102`, `2006-01-02`, `01-02-2006`, `2006Jan02`, `Jan 02 2006`) feeding `file.TakenDate` with a new `--date-source filename|auto` option plumbed through `Planner.WithDate` and rules' `date=` conditions.

#### #12. Batch Undo with Checkpoints — **Adapt** · Effort **M** · Risk Medium-High (safety net)
- **Current state:** multi-op undo already works **per plan run**, but (a) watch mode creates 1-op entries per file, (b) there are no named checkpoints, (c) undo is all-or-nothing (any missing destination aborts the whole revert), (d) `indexer` ships unused `history_entries`/`history_operations` SQLite tables.
- **Work:** group watch operations into session entries; `chest checkpoint --name before_cleanup` (a labeled marker over the existing journal); `chest undo --to <name>` reverting a range atomically; optional selective per-file undo reusing the existing pre-flight logic; history rotation/retention to cap unbounded growth.
- **Risk:** this is the safety net — it needs the strongest test coverage of any item here.

#### #13. Rule Composition & Inheritance — **Build (paired with #2)** · Effort **S–M** · Risk Low
- **Fit:** Trivially natural in TOML once `chest.toml` exists:

  ```toml
  [rules.base-video]
  type = "video"

  [rules.hd-video]
  extends = "base-video"
  min_size = "1GB"
  ```

  Merged at load time (child overrides parent field-by-field) before `rules.NewEngine`. Also the right moment to add OR/negation to the `--rule` DSL and document priority semantics (replacing the current magic numbers `100-i` / `20-25` / `50-60`).

#### #14. Report Generation & Export — **Build (cheapest quick win)** · Effort **S** · Risk Very Low
- **Current state:** `AnalyzeReport`, `StatsSummary`, `DuplicateGroup` **already carry JSON tags**; only flag plumbing is missing. `search --json` and `speedtest --json` set the precedent.
- **Work:** `--json` / `--csv` / `--out <file>` on `analyze`, `stats`, `duplicates`, `history`, and dry-run plans; optionally `chest plan --out plan.json` + `chest plan apply plan.json` for reviewable/replayable operations.

#### #15. Performance Profiling & Optimization Hints — **Build** · Effort **S–M** · Risk Low
- **Fit:** `analyze` already prints a total-runtime footer; progress callbacks mark stage boundaries.
- **Work:** `--profile` stage timers (scan / signature detect / metadata RPC / hashing / rule matching / moves) plus simple heuristics: "hashing dominates → drop `--hash`", "huge tree → add `--except`", "metadata RPC dominant → enable batching" (see §4 idea 7).

## 4. Additional Ideas (from this analysis)

Beyond the proposed list, the codebase review surfaced these grounded opportunities:

1. **`chest rule check` / `chest rule eval <file>`** — validate and explain rules before running. Today invalid `min_size` values are **silently ignored** (the rule matches everything) and unsupported field/operator combos silently return `false` (`internal/rules/engine.go`). An explain command would print which rule matched a file and why — the CLI version of the REPL's preview loop.
2. **Rule DSL v2: OR + negation + named rules** — `ParseRule` splits on `&&` only. Adding `||`, `!()`, and `name:` labels makes ad-hoc rules expressive without TOML, and is a prerequisite for clean `extends` (#13).
3. **`chest plan --out plan.json` / `chest plan apply`** — serialize the already-pure `models.Plan`, review it (or diff two plans), and execute later. Natural bridge to the Web UI (#10) and CI automation.
4. **Session-grouped watch history + selective undo** — `watcher.processFile` records one history entry per file, cluttering `history.json`; batch them per debounce window/session, and allow `chest undo 12 --file photo.jpg`.
5. **Fix: make `chest watch` honor plugin custom rules** — `README.md` and `examples/plugins/code-artifacts` promise `GetCustomRules()` applies to watch, but `watch.go` only wires the metadata enricher; the `Watcher` has no `ClassifyFunc`. Small, high-trust fix.
6. **`search --index`** — query the SQLite index instead of always doing a live `fastwalk` + fzf pass; instant results on huge trees, with `--live` as the fallback for freshness.
7. **Batched metadata plugin Inspect + RPC deadlines** — currently one RPC round-trip *per file*; batching (or streaming) plus a per-call timeout prevents a hung plugin from stalling a whole sort.
8. **Plugin lifecycle hardening** — `plugin enable/disable`, `plugin update`, checksum verification on `install`, honoring the documented `preset`/`rule` manifest capabilities (currently declared but never checked).
9. **`chest preset show <name>` + user preset directory** — presets are compile-time only; `preset` just lists names. Let users drop TOML into `~/.chest/presets/` and inspect built-ins.
10. **Unify rule resolution** — extract the duplicated preset→rule assembly from `sort.go` and `watcher.go` into one shared resolver (internal refactor; prerequisite for #2).

---

## 5. Prioritized Roadmap

### Phase 1 — Quick wins (each ≤ ~1 week, low risk)
| # | Feature | Why first |
|---|---|---|
| 14 | Report export (`--json`/`--csv`) | Structs already tagged; pure flag plumbing |
| — | Watch-honors-plugin-rules fix + unified rule resolver | Trust bug; unblocks #2 |
| 11 | Fuzzy filename dates | Confirmed gap; self-contained in `metadata`/`planner` |
| 2 | `chest.toml` + `chest project init/run` | Highest leverage foundation |
| 1/9 | `chest rule check` + `preset show` | Cheap validation tooling |

### Phase 2 — Core experience
| # | Feature | Depends on |
|---|---|---|
| 1 | REPL / interactive rule builder (with live preview + templates) | #2, rule-check |
| 12 | Checkpoints & batch undo (session grouping, `undo --to`) | — |
| 5 | Content-aware conflict engine (hash compare, intra-plan collisions, sidecars) | index hashes |
| 13 | Rule composition (`extends`) + DSL OR/negation | #2 |
| 6 | Archive auto-extract & organize | — |

### Phase 3 — Automation & integration
| # | Feature | Depends on |
|---|---|---|
| 7 | Folder aliases | #2 |
| 8 | Pre/post event hooks | #2 (config), plan JSON |
| 9 | Scheduling (cron/systemd/schtasks) | unified resolver |
| 15 | `analyze --profile` stage timing + hints | — |
| 4 | Metadata-as-sort-criteria (expose existing extractors) | — |

### Phase 4 — Stretch
| # | Feature | Notes |
|---|---|---|
| 10 | Web UI / dashboard (`chest serve --ui`) | Requires #14 + plan JSON; opt-in subcommand |
| — | `search --index`, batched plugin RPC, plugin lifecycle | Perf/hardening backlog |

---

## 6. Explicitly Deferred

- **#3 Sync to Cloud / Network Drives (S3, Google Drive, Dropbox, NFS)** — **deferred by request; not evaluated in depth.** One-line feasibility note: the SQLite index already stores per-file SHA-256 hashes, so a future `chest sync` could reuse `indexer` for rsync-style delta detection with modest effort — revisit after Phase 3.

---

## 7. Data-Loss Defect in `sort` — Analysis & Fix Plan

> **STATUS: PROPOSAL ONLY — NOT IMPLEMENTED.**
> This section records a data-loss defect reproduced against the current code, plus a plan to
> fix it. **No source code has been changed for this item.** Everything below is design work to
> be carried out later. The decisions recorded in §7.3 have been *made*, but nothing has been
> built. Section 7.4 is still open and unanswered.

### 7.0 The defect (reproduced)

Two files with the same name in different subfolders, both routed to the same destination,
using **default settings**:

```text
before:  sub1/photo.jpg = "PHOTO-ONE-original"
         sub2/photo.jpg = "PHOTO-TWO-original"

$ chest sort . -r --rule "ext=jpg -> Images" --dry-run
   → plans BOTH files to Images/photo.jpg, no collision warning

$ chest sort . -r --rule "ext=jpg -> Images" -y     # default policy: skip
   → "[DONE] 2 files organized into 1 folders."

after:   Images/photo.jpg = "PHOTO-TWO-original"
         PHOTO-ONE-original  << LOST
```

`Planner.Plan` checks `filesystem.Exists(targetFile)` against what is on disk *at plan time*.
Two different sources can resolve to the same path, and nothing de-duplicates them, so the second
move clobbers the first. `skip` did not skip, because at plan time neither file existed.

Verified as **format-agnostic** — the same loss occurs for non-image rules:

```text
$ chest sort . -r --rule "ext=md -> Docs" -y
   → Docs/notes.md => "beta text"   ("alpha text" destroyed)
```

Note the tool also reported "3 files organized" while only 2 files existed at the destination.

**Undo makes it worse.** Today both operations record the same destination, so `undo` restores
the first (consuming `Images/photo.jpg` entirely) and then fails on the second because the
source no longer exists:

```text
$ chest undo
Error: undo failed: ... open ...\Images\photo.jpg: The system cannot find the file specified.

final state:  sub1/photo.jpg => "PHOTO-TWO-original"   (wrong content, wrong folder)
               PHOTO-ONE-original                      << unrecoverable
```

The history entry is still marked `Complete`, which is what makes the corruption invisible.
This breaks the tool's headline "Completely Reversible" claim.

### 7.1 Why SHA-256 is not the fix

> *Original suggestion: when two files share a name but differ in content, use the SHA-256 to
> tell them apart. The instinct is sound; the placement is not.*

**The conflict is structural, not content-based.** Two sources resolving to one destination is
detectable with zero I/O, inside the loop `Plan` already runs — a `map[string]Operation` keyed
on the resolved target path. Roughly five lines, no disk reads, no performance cost.

Hashing would tell you the two files *differ*. It cannot tell you **which one should win** —
that still requires a naming or placement policy. So hashing answers a question we did not ask
while leaving the actual question open.

There is also a cost problem. Making `sort` SHA-256 every file means reading every byte of the
whole tree. The indexer gates hashing behind `--hash` for exactly this reason. `sort`'s entire
value proposition is speed — it has progress bars and a "Parallel Microsecond Engine" pitch.
Hash-everything would wreck that to fix a bug that needs no hashing.

**Where the idea should go — the key refinement.** Hash only the files that are *already in
conflict*, not the whole tree. In a typical run conflicts number between 0 and roughly 20
files; hashing 20 files is imperceptible. The indexer already ships a `hashFile` helper and
stores hashes under `--hash`, so the plumbing exists.

That yields the real distinction:

| Situation | Meaning | Right action |
|---|---|---|
| Same name, **same hash** | Genuine duplicate | Second copy is redundant — safe to auto-skip |
| Same name, **different hash** | Distinct content | **Both must be preserved** — needs disambiguation |

This is a meaningful improvement over today's behaviour, where both cases are handled
identically: silently destroyed.

### 7.2 The plan, staged

**Stage 0 — stop the data loss (the actual bug fix).**
In `Plan`, track claimed destinations. When a second file resolves to an already-claimed path it
is not planned as a clean operation; it becomes a recorded conflict on `models.Plan`. Under the
default `skip` policy it is not moved at all, and the default *is* `skip`, so this alone ends the
silent overwrite. `abort` errors out; `rename` uses the existing `ResolveCollision`; `replace`
keeps current behaviour but gets an explicit warning.

**Stage 1 — surface it where you can act on it.**
`--dry-run` and the REPL's `preview` show conflicts as a first-class block, not a footnote:

```text
!! CONFLICT — 2 files resolve to Images/photo.jpg
    sub1/photo.jpg
    sub2/photo.jpg
  policy 'skip' → sub2/photo.jpg will be LEFT IN PLACE
```

Today the dry run is where this would be caught, and it shows nothing. That is the part that
makes the tool untrustworthy.

**Stage 2 — make `undo` atomic (the second bug).**
Two-phase: validate that every file in the batch can be restored (sources present, destinations
writable, nothing blocking) and only then execute. If validation fails, refuse the whole undo
with a precise reason instead of half-restoring. Mark the history entry `Failed`/`Partial`
rather than leaving it `Complete`, which is what makes the corruption invisible today.
Validate-then-execute is preferred over rollback-on-failure, because once three files are
restored the destinations are occupied and rolling forward gets messy.

**Stage 3 — optional content-aware disambiguation.**
Consult the index for hashes of the *conflicting subset only*. Same hash → true duplicate →
auto-skip. Different hash → distinct → apply the chosen naming policy. Never hash the full tree.
Gate behind an explicit flag such as `--hash-collisions` so the fast path stays the default.

**One trap in Stage 0.** A naive `map[string]` keyed on the raw path is incomplete on Windows and
macOS. `sub1/Photo.jpg` and `sub2/photo.jpg` produce different map keys but the same file on a
case-insensitive filesystem, and `Exists` returns false for both at plan time, so nothing catches
it. The map key must be normalised (lowercased on case-insensitive platforms) or the same
data-loss bug survives in a subtler form.

**What we will not do.**
- No whole-tree hashing (severe performance regression)
- No silent deletion of anything
- No changing `replace` semantics without an explicit opt-in — arguably `replace` should require
  a deliberate flag such as `--allow-overwrite`, since today it destroys without asking
- No auto-renaming the user did not opt into

**How we would prove it.** Regression test on the exact fixture above: two same-named files with
different content → assert both survive and the dry run reports the conflict. Then the markdown
variant (format-agnostic), the case-insensitive variant (`Photo.jpg` vs `photo.jpg`), a
true-duplicate case (same hash → auto-skip), and a test that `undo` either fully restores or fully
refuses — never partially. Plus the existing suite and `-race`, since `Plan` is on the hot path
for every command.

### 7.3 Decisions taken, and what they simplify

**Decided:** auto-rename the conflicting file (rather than skip / keep-source-folder / abort), and
**no SHA-256 involvement** in conflict policy.

Dropping hashing removes Stage 3 from the critical path. Good — the fix becomes cheap. But
**auto-rename cannot be done by just calling `ResolveCollision`**, and this is the trap:

```text
op1 claims  Images/photo.jpg      <- in the plan, not yet on disk
op2 wants   Images/photo.jpg
  → ResolveCollision checks Exists(Images/photo.jpg) → FALSE (nothing written yet)
  → returns Images/photo.jpg unchanged
  → both ops still target the same path
```

`ResolveCollision` (`internal/filesystem/fs.go:92`) produces `photo (1).jpg`, but it is
**disk-aware only** — it short-circuits on `!Exists(dst)` before ever considering a counter. The
planner must carry the set of paths already claimed in this plan and feed that into resolution,
so op2 becomes `Images/photo (1).jpg`. Same helper, but it needs in-plan awareness as an input.

**A useful consequence.** Auto-rename also fixes the undo corruption in this scenario, as a side
effect. With distinct destinations — `Images/photo.jpg` and `Images/photo (1).jpg` — each restore
is independent and both succeed. The data loss and the broken undo both disappear for this class
of bug.

So the undo hardening drops from "critical" to "robustness": still worth doing, because a
genuinely missing file, a permission error, or a full disk can half-restore a batch by the same
mechanism. It just stops being a live data-loss path for this scenario.

**The finalized plan.**

1. **Detect intra-plan conflicts** — track claimed destinations in `Plan`. Normalise the key by
   case on case-insensitive platforms, or `Photo.jpg` vs `photo.jpg` slips through and the same
   bug survives in subtler form. On conflict, resolve against the union of on-disk and in-plan
   claims, producing `photo (1).jpg`, and record it on `models.Plan` so it can be reported.
2. **Report it in `--dry-run` and the REPL's `preview`** — auto-rename is non-obvious; the user
   should see `photo (1).jpg` appear with a marker, not have it surprise them. Same in
   `printDryRun`.
3. **Harden `undo`** — validate the whole batch is restorable before restoring any of it; refuse
   cleanly otherwise. Mark the history entry `Failed`/`Partial` rather than leaving it
   `Complete`.

### 7.4 Open question (unanswered — decide before implementing)

Auto-rename was chosen as the **default**. What should happen when someone *explicitly* passes
`--collision skip`?

Recommended: standard precedence — **an explicit flag beats the default**.

| Invocation | Behaviour |
|---|---|
| No flag given | intra-plan conflict auto-renames (the chosen default) |
| `--collision skip` | the second file is left in place, reported as a conflict |
| `--collision abort` | the run stops before moving anything |

This keeps the default safe and convenient while still honouring a deliberate instruction. The
cost is that `skip` no longer means "nothing moves" in every case, which is the right trade.

---

*Document generated from a read-only analysis of the CHEST codebase (rules/planner/organizer, CLI/plugin surface, history/indexer/metadata/watcher layers). Key files referenced: `internal/rules/engine.go`, `internal/planner/planner.go`, `internal/cli/sort.go`, `internal/cli/watch.go`, `internal/watcher/watcher.go`, `internal/history/history.go`, `internal/indexer/indexer.go`, `internal/metadata/*`, `internal/plugin/*`, `internal/presets/*`.*
