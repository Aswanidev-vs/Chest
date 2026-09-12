package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

type manTopic struct {
	Name        string
	Synopsis    string
	Description string
	Options     []string
	Examples    []string
	Concepts    []string
	SeeAlso     []string
}

var manTopics = map[string]manTopic{
	"chest": {
		Name:     "CHEST(1) - General Commands Manual",
		Synopsis: "chest [command] [flags] [arguments...]",
		Description: `CHEST is a high-speed, deterministic, cross-platform file organization,
indexing, and storage intelligence tool inspired by the Minecraft chest.

It provides rule-based sorting, continuous background folder monitoring,
fast concurrent fuzzy & content searching, safe reversible rollbacks,
and local SQLite metadata indexing.

Files are detected by content signature and enriched with embedded metadata
(EXIF dates, QuickTime creation times, PDF / Office / audio tags). The extractor
registry is open: custom formats can be added programmatically.`,
		Options: []string{
			"-h, --help       Show brief CLI help",
			"-v, --version    Show version and build info",
		},
		Examples: []string{
			"chest sort ~/Downloads               Organize downloads folder",
			"chest search \"report\" -e pdf         Search for PDF reports",
			"chest watch ~/Downloads              Continuously watch and sort incoming files",
			"chest undo                           Revert last file move operation",
			"chest stats                          Display storage distribution analytics",
			"chest man sort                       Show full manual page for sort command",
		},
		SeeAlso: []string{"sort", "search", "watch", "history", "undo", "index", "stats", "duplicates", "analyze", "completion", "plugin", "clean", "speedtest"},
	},
	"sort": {
		Name:     "CHEST-SORT(1) - File Organization",
		Synopsis: "chest sort [directory] [flags]",
		Description: `Sorts files in target or current directory into organized compartments.
Evaluates built-in presets or user-defined custom rules with priority ranking.
Each file is signature-detected for its real format (magic bytes first, extension as
fallback) and enriched with embedded metadata (EXIF/photography dates, QuickTime
creation time, PDF / Office / audio tags) via built-in or custom extractors.
Supports dry-run preview and automatic creation of destination folders.`,
		Options: []string{
			"-t, --type           Sort files into category folders (Images, Videos, Documents...)",
			"-f, --format         Boolean flag (no value). Group files into folders by their detected format using content signatures (magic bytes first, extension as fallback), e.g. MP4/, MP3/, PNG/",
			"-s, --size           Sort files by size thresholds (Tiny, Small, Medium, Large, Huge)",
			"-d, --date           Sort files by date (default: modification year YYYY)",
			"--date-source        Date source for --date and date placeholders: modified (default), auto, taken",
			"--date-granularity   Date folder granularity: year (default), month, day",
			"-p, --preset <name>  Apply built-in preset (downloads, media, developer, documents, photos)",
			"--rule <spec>        Custom rule on the fly (e.g. 'type=video && size>1GB -> Videos/Large')",
			"-i, --into <dir>     Base destination directory to move organized files into",
			"--name <name>        Move ALL matching files flat into one folder in the target dir (e.g. --name \"Anime\" -> Anime/*.mp4); cannot be combined with --into",
			"-n, --dry-run        Simulate organization without moving any files",
			"-y, --yes            Skip interactive confirmation prompt",
			"-c, --collision      Collision policy: skip (default), rename, replace, abort",
			"-H, --hidden         Include hidden files and folders",
			"--allow-system       Allow modifications in protected OS or system directories",
		},
		Examples: []string{
			"chest sort                           Sort current directory with default preset",
			"chest sort ~/Downloads -t -p media   Sort downloads using media preset into categories",
			"chest sort --dry-run                 Preview what files would be moved without changing anything",
			"chest sort --rule \"*.mkv -> Movies\" Move all MKV files to Movies folder",
			"chest sort --format                          Group by detected format -> MP4/, MP3/, PNG/ folders",
			"chest sort --format --into \"out\"            Group by extension AND put all those folders inside ./out/",
			"chest sort -t --into \"Organized\"   Put all categorized folders inside ./Organized/",
			"chest sort -t --name \"Anime\"            Move all matched files flat into one folder named Anime",
			"chest sort --rule \"*.mp4 -> Anime\"    \"->\" = move into that folder; here only *.mp4 files go into Anime",
			"chest sort --date --date-granularity month        Group by modification year/month, e.g. 2025/01/",
			"chest sort --date --date-source auto --date-granularity day --rule \"type=image -> Photos/{date}\"  Use embedded media date when available",
			"chest sort --rule \"format=gocut -> GoCutProjects\"  Route a custom registered format (e.g. .gocut GoCut projects) by content-detected format",
		},
		Concepts: []string{
			"CATEGORY MODES  -t/--type, -f/--format, -s/--size and -d/--date split a batch into MULTIPLE category folders (Images/, MP4/, Large Files/, 2024/, 2025/, 2026/). --date defaults to modification year; use --date-source auto and --date-granularity month|day for embedded media dates and finer folders.",
			"FLAT MODES      --name <x> or --rule \"<matcher> -> <x>\" put matches into ONE folder. --name takes every matched file; --rule lets you choose WHICH files.",
			"WRAPPING        --into <dir> places any mode's folders inside <dir>, e.g. --format --into out -> out/MP4/f.mp4.",
			"RULE ARROW      In --rule, \"->\" means \"move into this folder\": the left side is what to match, the right side is the destination folder.",
			"FORMAT RULE     In --rule, extension= matches the filename extension while format= matches the content-detected format. Example: format=pdf -> MyPdfs routes a PDF even if its extension was renamed. Custom extractors can be registered so format= understands your own file types.",
		},
		SeeAlso: []string{"preset", "undo", "history", "watch"},
	},
	"search": {
		Name:     "CHEST-SEARCH(1) - Fast Multi-Core Search",
		Synopsis: "chest search [pattern] [path] [flags]",
		Description: `Performs high-speed concurrent filesystem search using parallel multi-core
directory traversal (fastwalk) and zero-allocation fuzzy matching (fzf/src/algo).
Supports both filename/path searching and streaming content grep with line numbers.`,
		Options: []string{
			"-c, --content <text> Search inside file contents (streaming grep)",
			"-t, --type <cat>     Filter by category (image, video, document, code, archive...)",
			"-e, --ext <exts>     Comma-separated extension filter (e.g. 'go,md,png')",
			"-s, --size <cond>    Size condition (e.g. '>1GB', '<50MB', '=10KB')",
			"-m, --modified <val> Modification time condition (e.g. '>30d', '<7d', '2026-01-01')",
			"-r, --regex          Treat pattern as a regular expression",
			"-x, --exact          Exact substring matching instead of fuzzy ranking",
			"-H, --hidden         Include hidden files in search",
			"-l, --limit <n>      Stop after returning n matches",
			"-j, --json           Output matches in machine-readable JSON",
		},
		Examples: []string{
			"chest search \"invoice\"              Fuzzy search for invoice across current directory",
			"chest search -t image --size \">5MB\" Find high-resolution images larger than 5MB",
			"chest search -c \"TODO\" internal/    Grep for TODO comments inside internal/ folder",
			"chest search \"*.mkv\" ~/Downloads    Find MKV video files in Downloads",
			"chest search -e go -j \"test\"        Output Go test files as JSON",
		},
		SeeAlso: []string{"sort", "index", "stats"},
	},
	"watch": {
		Name:     "CHEST-WATCH(1) - Continuous Directory Automation",
		Synopsis: "chest watch [directory] [flags]",
		Description: `Monitors target folder in real-time using filesystem notifications (fsnotify).
When incoming files finish downloading or are saved, CHEST immediately sorts them
into compartments according to chosen presets or rules.`,
		Options: []string{
			"-p, --preset <name>  Preset to apply (default: 'downloads')",
			"-r, --rule <spec>    Custom rule expression to apply",
			"-d, --debounce <dur> Settle duration before moving file (default: 500ms)",
			"-n, --dry-run        Print actions without actually moving files",
			"--name <name>        Move ALL incoming matching files flat into one folder in the watched dir (e.g. --name \"Anime\" -> Anime/*.mp4)",
			"--allow-system       Allow monitoring and moving in protected system directories",
		},
		Examples: []string{
			"chest watch ~/Downloads             Continuously organize downloads as files arrive",
			"chest watch ~/Downloads -p media    Watch and sort downloads using media preset",
			"chest watch ~/Desktop --rule \"*.png -> Screenshots\" Auto-move screenshot files",
			"chest watch ~/Downloads --name \"Anime\"   Watch and dump every incoming file flat into a folder named Anime",
		},
		SeeAlso: []string{"sort", "preset"},
	},
	"undo": {
		Name:     "CHEST-UNDO(1) - Operation Rollback",
		Synopsis: "chest undo [operation-id] [flags]",
		Description: `Reverses previous file organization operations, restoring moved files back
to their exact original paths. Checks for destination file existence and source path
availability before modifying filesystem.

Subcommand 'cache' manages the undo history store (~/.chest/history.json).
Use 'chest undo cache' to view cache info or '--clear' to wipe all history.`,
		Options: []string{
			"[operation-id]       Specific history ID to undo (default: latest active run)",
			"--allow-system       Allow undo restoring into protected system directories",
			"cache                Show undo cache info (stored operations count)",
			"cache --clear        Permanently delete all undo history",
		},
		Examples: []string{
			"chest undo                           Undo the most recent organization run",
			"chest undo 3                         Undo specific operation run with ID #3",
			"chest undo cache                     Show how many operations are stored",
			"chest undo cache --clear             Wipe all undo history permanently",
		},
		SeeAlso: []string{"history", "sort", "clean"},
	},
	"history": {
		Name:     "CHEST-HISTORY(1) - Organization Log",
		Synopsis: "chest history",
		Description: `Displays chronological log of previous organization operations, including
timestamp, target directory, count of moved files, and current status (Complete / Undone).`,
		Options: []string{},
		Examples: []string{
			"chest history                        View table of previous organization runs",
		},
		SeeAlso: []string{"undo", "sort"},
	},
	"index": {
		Name:     "CHEST-INDEX(1) - Metadata Indexing",
		Synopsis: "chest index [path] [flags]",
		Description: `Scans target directory and stores file metadata, sizes, categories, and
optional cryptographic SHA256 hashes inside local SQLite database (~/.chest/index.db).
Enables fast offline storage intelligence without cloud dependency.

Re-indexing also prunes rows for files that no longer exist on disk or are now
covered by --except, keeping the cache free of stale paths.`,
		Options: []string{
			"--hash               Compute SHA256 hashes during indexing",
			"--clear              Clear cached index records from SQLite files table",
			"--except <dir,glob>  Exclude directories/files from the scan (comma-separated, globs allowed)",
		},
		Examples: []string{
			"chest index ~/Documents              Index documents folder metadata",
			"chest index ~/Downloads --hash       Index downloads and calculate hashes",
			"chest index ~/Projects --except node_modules,venv   Skip noisy dirs",
			"chest index --clear                  Empty cached index records",
		},
		SeeAlso: []string{"stats", "duplicates", "analyze", "clean"},
	},
	"stats": {
		Name:     "CHEST-STATS(1) - Storage Analytics",
		Synopsis: "chest stats [path] [flags]",
		Description: `Displays aggregated storage analytics from local index: total file count,
total storage volume, breakdown per category (Images, Videos, Documents, Code...),
and top largest files consuming disk space (reported with absolute paths).

A scoped stats run always re-indexes that folder first (auto-refresh). A bare
run re-populates an empty cache from the current directory instead of reporting
all zeros, and --refresh forces a fresh re-scan with stale/excluded-row purge.`,
		Options: []string{
			"--except <dir,glob>  Exclude directories/files from the scan (comma-separated, globs allowed)",
			"--refresh            Force a fresh re-index (with stale/excluded-row purge) before reporting",
		},
		Examples: []string{
			"chest stats                          Display cached storage analytics",
			"chest stats ~/Downloads              Auto-refresh Downloads and show its breakdown",
			"chest stats --refresh                Force refresh of the current directory",
			"chest stats ~/Projects --except node_modules   Skip noisy directories",
		},
		SeeAlso: []string{"index", "analyze", "duplicates"},
	},
	"duplicates": {
		Name:     "CHEST-DUPLICATES(1) - Duplicate Detection",
		Synopsis: "chest duplicates [path] [--except dir,glob]",
		Description: `Locates byte-identical files using cryptographic SHA256 hashes.
Groups candidates first by exact file size, then calculates hashes to guarantee
100% accurate duplicate detection. Read-only operation; never deletes files automatically.
Use --except to skip directories/files (e.g. node_modules, vendored deps) so large,
noisy subtrees never get hashed during the scan. Excluded paths are also removed
from any previously-indexed rows on re-index. Reading/analytics commands
(stats, analyze, duplicates) resolve paths case-insensitively on macOS/Windows.`,
		Options: []string{
			"--except <dir,glob>  Exclude directories/files from the scan (comma-separated, globs allowed)",
		},
		Examples: []string{
			"chest duplicates                     Check for duplicates in indexed files",
			"chest duplicates ~/Downloads         Index and find duplicates in Downloads",
			"chest duplicates ~/Projects --except node_modules,venv,.git   Skip noise dirs",
		},
		SeeAlso: []string{"index", "analyze"},
	},
	"analyze": {
		Name:     "CHEST-ANALYZE(1) - Storage Health Audit",
		Synopsis: "chest analyze [path] [flags]",
		Description: `Generates a comprehensive read-only intelligence report: storage volume,
duplicate candidate sets, stale/old files (>180 days since last modification),
empty 0-byte files, and largest storage consumers (reported with absolute paths).

A scoped analyze run always re-indexes that folder first (auto-refresh). A bare
run re-populates an empty cache from the current directory instead of reporting
all zeros, and --refresh forces a fresh re-scan with stale/excluded-row purge.`,
		Options: []string{
			"--except <dir,glob>  Exclude directories/files from the scan (comma-separated, globs allowed)",
			"--refresh            Force a fresh re-index (with stale/excluded-row purge) before reporting",
		},
		Examples: []string{
			"chest analyze ~/Downloads              Auto-refresh and audit Downloads",
			"chest analyze ~/Downloads --refresh    Force a fresh audit with purge",
			"chest analyze --except node_modules    Skip noisy directories",
		},
		SeeAlso: []string{"stats", "duplicates", "index"},
	},
	"plugin": {
		Name:     "CHEST-PLUGIN(1) - External Plugin Management",
		Synopsis: "chest plugin [command] [args...]",
		Description: `Manages CHEST external-process plugins. Plugins run as isolated processes
via hashicorp/go-plugin over RPC, exposing custom file classifiers, rules,
or rich metadata inspection (format sniffing, embedded dates, extra fields)
without touching the core binary. Capabilities: classifier, metadata, rule.`,
		Options: []string{
			"list                 List all installed plugins in ~/.chest/plugins",
			"info <name>          View detailed capabilities, version, and author",
			"install <dir>        Install a plugin directory containing binary and manifest.json",
			"remove <name>        Uninstall a plugin",
		},
		Examples: []string{
			"chest plugin list                    Show installed plugins",
			"chest plugin install ./my-plugin     Install plugin from local directory",
			"chest plugin info anime              View info on 'anime' plugin",
			"chest plugin remove anime            Uninstall plugin",
		},
		SeeAlso: []string{"sort", "preset"},
	},
	"clean": {
		Name:     "CHEST-CLEAN(1) - Cache & Database Cleanup",
		Synopsis: "chest clean [flags]",
		Description: `Empties cached index records from SQLite files table or deletes the database
file completely from disk. Requires interactive y/N confirmation before executing.
Use -y/--yes flag to bypass the prompt in scripts or automation.
Use --history to also delete the undo history file (~/.chest/history.json).`,
		Options: []string{
			"--all                Completely delete ~/.chest/index.db file",
			"--history            Also delete ~/.chest/history.json undo history",
			"-y, --yes            Skip confirmation prompt",
		},
		Examples: []string{
			"chest clean                          Empty cached file records (with confirmation)",
			"chest clean -y                       Empty cached records without asking",
			"chest clean --history                Clear index records and undo history",
			"chest clean --all                    Remove ~/.chest/index.db database file",
			"chest clean --all -y                 Remove database file without asking",
			"chest clean --history --all -y       Remove index database and undo history without asking",
		},
		SeeAlso: []string{"index", "undo"},
	},
	"completion": {
		Name:     "CHEST-COMPLETION(1) - Shell Tab Completion",
		Synopsis: "chest completion [shell] [--install] [--output <file>]",
		Description: `Give your shell tab-completion for chest, so you can type "chest s" and press
Tab to complete to sort, or "chest sort --p" and let it complete --preset - the
same autocomplete you get with git or docker.

The recommended setup is a one-liner:
    chest completion --install
which detects your shell, writes the script to the standard location for that
shell, and adds the load line to your shell profile, so completion loads in every
new terminal session. If detection can't tell (for example on Windows), pass the
shell explicitly:
    chest completion zsh --install

Without --install the script for the chosen shell is printed to stdout so you can
inspect it or save it wherever you like. The script is what the shell runs to
complete chest; it is only needed once, and regenerating it is harmless.`,
		Options: []string{
			"-s, --shell <name>    Shell to generate for: bash, zsh, fish (auto-detected when using --install)",
			"-i, --install         Detect your shell (or use the shell argument) and install completion permanently",
			"-o, --output <file>   Write the completion script to this file instead of stdout",
		},
		Examples: []string{
			"chest completion --install             Detect your shell, install, and wire it up (recommended)",
			"chest completion zsh --install         Install zsh completion permanently",
			"chest completion fish --install        Install fish completion permanently",
			"chest completion bash > _chest         Save the bash completion script to a file",
			"chest completion zsh -o _chest         Write the zsh script to a custom path",
		},
		SeeAlso: []string{"watch", "man"},
	},
	"update": {
		Name:     "CHEST-UPDATE(1) - Self-Update",
		Synopsis: "chest update",
		Description: `Updates CHEST to the latest published version.

Runs the Go toolchain's "go install" on the CHEST source module, which rebuilds
the binary from source and replaces the copy in your Go bin directory. A live
progress spinner is shown while the update runs. Because the currently running
process has already loaded the old binary, restart CHEST after the update to
start using the new version.

Requires the Go toolchain installed and available on your PATH.`,
		Options: []string{
			"-h, --help          Show brief CLI help for update",
		},
		Examples: []string{
			"chest update                      Update CHEST to the latest published version",
		},
		SeeAlso: []string{"version", "man"},
	},
	"speedtest": {
		Name:     "CHEST-SPEEDTEST(1) - Network Speed Test",
		Synopsis: "chest speedtest [flags]",
		Description: `Measure your connection speed from the terminal - no API key required.
Runs TWO independent tests side-by-side and shows a comparison table:
  - Ookla      real speedtest.net protocol: nearest server, multi-stream DL/UL, latency/jitter (accurate)
  - Cloudflare  stdlib test against speed.cloudflare.com (reference)
Use --ookla or --cloudflare to run only one. Use --json for machine output.`,
		Options: []string{
			"--ookla              Run only the Ookla (speedtest.net) test",
			"--cloudflare         Run only the Cloudflare test",
			"-j, --json           Output results as machine-readable JSON",
			"-h, --help           Show brief CLI help",
		},
		Examples: []string{
			"chest speedtest                        Run both tests and compare",
			"chest speedtest --ookla               Only the accurate Ookla test",
			"chest speedtest --cloudflare          Only the quick Cloudflare test",
			"chest speedtest --json                Machine-readable JSON output",
		},
		SeeAlso: []string{"update", "man"},
	},
}

func newManCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "man [command]",
		Short: "Display manual page with detailed explanation and examples (like Linux man)",
		Long: `Display manual documentation for CHEST and its subcommands, formatted in
traditional Unix man page style with Synopsis, Description, Options, and Examples.`,
		Example: `  chest man
  chest man sort
  chest man search
  chest man watch
  chest man plugin`,
		Run: func(cmd *cobra.Command, args []string) {
			topicName := "chest"
			if len(args) > 0 {
				topicName = strings.ToLower(strings.TrimSpace(args[0]))
			}

			topic, ok := manTopics[topicName]
			if !ok {
				fmt.Printf("\x1b[38;5;196mNo manual entry for '%s'.\x1b[0m\n", topicName)
				fmt.Println("Available manual topics:")
				for k := range manTopics {
					fmt.Printf("  • %s\n", k)
				}
				return
			}

			printManPage(topic)
		},
	}
}

func printManPage(t manTopic) {
	bold := "\x1b[1m"
	reset := "\x1b[0m"
	cyan := "\x1b[38;5;75m"
	gold := "\x1b[38;5;220m"
	green := "\x1b[38;5;114m"
	dim := "\x1b[38;5;246m"

	fmt.Printf("\n%s%s%s\n\n", bold, t.Name, reset)

	fmt.Printf("%sNAME%s\n", bold, reset)
	fmt.Printf("    %s\n\n", t.Synopsis)

	fmt.Printf("%sSYNOPSIS%s\n", bold, reset)
	fmt.Printf("    %s%s%s\n\n", cyan, t.Synopsis, reset)

	fmt.Printf("%sDESCRIPTION%s\n", bold, reset)
	for _, line := range strings.Split(t.Description, "\n") {
		fmt.Printf("    %s\n", line)
	}
	fmt.Println()

	if len(t.Options) > 0 {
		fmt.Printf("%sOPTIONS%s\n", bold, reset)
		for _, opt := range t.Options {
			parts := strings.SplitN(opt, "  ", 2)
			if len(parts) == 2 {
				fmt.Printf("    %s%-24s%s %s\n", gold, parts[0], reset, parts[1])
			} else {
				fmt.Printf("    %s%s%s\n", gold, opt, reset)
			}
		}
		fmt.Println()
	}

	if len(t.Examples) > 0 {
		fmt.Printf("%sEXAMPLES%s\n", bold, reset)
		for _, ex := range t.Examples {
			parts := strings.SplitN(ex, "  ", 2)
			cmdPart := parts[0]
			descPart := ""
			if len(parts) == 2 {
				descPart = strings.TrimSpace(parts[1])
			}

			fmt.Printf("    %s$ %s%s\n", green, cmdPart, reset)
			if descPart != "" {
				fmt.Printf("        %s%s%s\n\n", dim, descPart, reset)
			} else {
				fmt.Println()
			}
		}
	}

	if len(t.Concepts) > 0 {
		fmt.Printf("%sCONCEPTS%s\n", bold, reset)
		for _, c := range t.Concepts {
			fmt.Printf("    %s\n", c)
		}
		fmt.Println()
	}

	if len(t.SeeAlso) > 0 {
		fmt.Printf("%sSEE ALSO%s\n", bold, reset)
		var linked []string
		for _, sa := range t.SeeAlso {
			linked = append(linked, fmt.Sprintf("%schest-%s%s(1)", cyan, sa, reset))
		}
		fmt.Printf("    %s\n\n", strings.Join(linked, ", "))
	}
}
