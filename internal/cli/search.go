package cli

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Aswanidev-vs/chest/internal/classifier"
	"github.com/Aswanidev-vs/chest/internal/plugin"
	"github.com/Aswanidev-vs/chest/internal/search"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// csvRowsForSearch builds the search CSV table: a header plus one row per
// match, carrying the same fields the human report shows (category, path,
// size, modification time, relevance score) and the content-grep snippet so a
// row is self-contained without reopening the file.
func csvRowsForSearch(matches []search.SearchMatch) [][]string {
	rows := [][]string{{"category", "path", "rel_path", "size_bytes", "modified", "score", "content_line", "content_match"}}
	for _, m := range matches {
		rows = append(rows, []string{
			m.Category,
			m.Path,
			m.RelPath,
			strconv.FormatInt(m.Size, 10),
			m.ModTime.Format(time.RFC3339),
			strconv.Itoa(m.Score),
			strconv.Itoa(m.ContentLineNo),
			m.ContentMatch,
		})
	}
	return rows
}

// mergeSavedQuery overlays a loaded query underneath the flags the user typed
// on this invocation. Changed() tells an explicit value apart from a flag's
// default, so `--load movies --limit 5` tweaks the saved query instead of
// silently discarding the limit, and a saved fuzzy=false is not clobbered by
// the --fuzzy default of true.
func mergeSavedQuery(f *pflag.FlagSet, current, loaded search.SavedQuery) search.SavedQuery {
	merged := current
	if !f.Changed("type") {
		merged.Type = loaded.Type
	}
	if !f.Changed("ext") {
		merged.Extensions = loaded.Extensions
	}
	if !f.Changed("size") {
		merged.SizeCondition = loaded.SizeCondition
	}
	if !f.Changed("modified") {
		merged.DateCondition = loaded.DateCondition
	}
	if !f.Changed("content") {
		merged.ContentQuery = loaded.ContentQuery
	}
	if !f.Changed("ignore-case") {
		merged.IgnoreCase = loaded.IgnoreCase
	}
	if !f.Changed("regex") {
		merged.Regex = loaded.Regex
	}
	if !f.Changed("exact") {
		merged.Exact = loaded.Exact
	}
	if !f.Changed("fuzzy") {
		merged.Fuzzy = loaded.Fuzzy
	}
	if !f.Changed("hidden") {
		merged.IncludeHidden = loaded.IncludeHidden
	}
	if !f.Changed("limit") {
		merged.Limit = loaded.Limit
	}
	return merged
}

func newSearchCmd() *cobra.Command {
	var (
		typeName      string
		exts          []string
		sizeCond      string
		dateCond      string
		contentQuery  string
		ignoreCase    bool
		regex         bool
		exact         bool
		fuzzy         bool
		includeHidden bool
		limit         int
		jsonOutput    bool
		csvOutput     bool
		outPath       string
		saveName      string
		loadName      string
	)

	cmd := &cobra.Command{
		Use:   "search [pattern] [path]",
		Short: "High-speed concurrent search using fastwalk & fzf algorithm",
		Long: `Search the filesystem with ultra-fast parallel directory traversal and fzf scoring.

Supports filtering by filename/path, file type, extension, size, modification date,
and content grep (-c).

Results can be emitted as JSON (--json), as CSV (--csv), or written to a file
(--out, JSON unless a format is given). A whole query can be stored under a name
with --save and replayed with --load; saved searches live in
~/.chest/searches/<name>.toml and names may not contain path separators.`,
		Example: `  chest search "movie"
  chest search --type video --size ">1GB"
  chest search "*.mkv" ~/Downloads
  chest search -c "TODO" internal/
  chest search -e go,md -i "chest"
  chest search --type video --csv
  chest search --type video --csv --out videos.csv
  chest search --save big-videos --type video --size ">1GB"
  chest search --load big-videos`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if jsonOutput && csvOutput {
				return errors.New("choose one of --json or --csv")
			}
			if saveName != "" && loadName != "" {
				return errors.New("choose one of --save or --load")
			}

			pattern := ""
			rootPath := "."

			if len(args) > 0 {
				pattern = args[0]
			}
			if len(args) > 1 {
				rootPath = args[1]
			}

			// The query as it stands right now: everything --save persists and
			// --load restores comes from these fields.
			query := search.SavedQuery{
				Name:          saveName,
				Pattern:       pattern,
				RootPath:      rootPath,
				Type:          typeName,
				Extensions:    exts,
				SizeCondition: sizeCond,
				DateCondition: dateCond,
				ContentQuery:  contentQuery,
				IgnoreCase:    ignoreCase,
				Regex:         regex,
				Exact:         exact,
				Fuzzy:         fuzzy,
				IncludeHidden: includeHidden,
				Limit:         limit,
			}

			// A loaded query fills in whatever was not typed this time, so a
			// bare --load replays the saved search exactly while
			// `--load movies --limit 5` still honours the flag.
			if loadName != "" {
				loaded, err := search.LoadQuery(loadName)
				if err != nil {
					return err
				}
				query = mergeSavedQuery(cmd.Flags(), query, loaded)
				if len(args) == 0 {
					query.Pattern = loaded.Pattern
				}
				if len(args) < 2 {
					query.RootPath = loaded.RootPath
				}
				query.Name = loaded.Name
			}

			// Persist before walking: --save records the query the user asked
			// for, so it works whether or not that query matches anything.
			// The confirmation goes to stderr so `--save x --csv | ...` still
			// pipes nothing but CSV.
			if saveName != "" {
				path, err := search.SaveQuery(query)
				if err != nil {
					return err
				}
				fmt.Fprintf(cmd.ErrOrStderr(), "\x1b[1;38;5;82m✔\x1b[0m saved search \x1b[38;5;220m%s\x1b[0m to \x1b[38;5;246m%s\x1b[0m\n", saveName, path)
			}

			// The header line reports the directory that was actually walked,
			// which a --load query may have supplied rather than the user.
			rootPath = query.RootPath

			// Build composite classifier with plugin fallback
			var classifyFunc func(string, string) string
			var pluginCleanup func()
			if mgr, err := plugin.NewManager(); err == nil {
				pluginServices, _, cleanup := mgr.LoadAllClassifiers()
				pluginCleanup = cleanup
				if len(pluginServices) > 0 {
					classifyFunc = func(filename, ext string) string {
						cat := classifier.ClassifyExtension(ext)
						if cat != classifier.TypeOther && cat != "" {
							return cat
						}
						if pluginCat := plugin.ClassifyWithPlugins(pluginServices, filename, ext); pluginCat != "" {
							return pluginCat
						}
						return cat
					}
				}
			}
			if pluginCleanup != nil {
				defer pluginCleanup()
			}

			// Live search progress: a spinner on stderr shows how many files
			// have been traversed and matches found so far, so the screen isn't
			// blank during a slow walk. Results still print to stdout, and the
			// spinner is a no-op when output isn't a terminal.
			state := newProgressState()
			spinner := newSpinner(cmd.ErrOrStderr(), func() string {
				scanned, matched := state.get()
				if scanned == 1 {
					return fmt.Sprintf("Searching… %d file scanned, %d matches", scanned, matched)
				}
				return fmt.Sprintf("Searching… %d files scanned, %d matches", scanned, matched)
			})
			defer spinner.stop()

			engine := search.New(query.Options(classifyFunc, func(scanned, matched int) {
				state.set(scanned, matched)
			}))

			matches, err := engine.Search()
			if err != nil {
				return err
			}

			if jsonOutput || csvOutput || outPath != "" {
				return emitReport(cmd.OutOrStdout(), matches, csvRowsForSearch(matches), jsonOutput, csvOutput, outPath)
			}

			if len(matches) == 0 {
				fmt.Println("\x1b[38;5;246mNo matching files found.\x1b[0m")
				return nil
			}

			fmt.Printf("\n\x1b[38;5;254mFound \x1b[1;38;5;82m%d\x1b[0m\x1b[38;5;254m matches in \x1b[38;5;220m%s\x1b[0m\n\n", len(matches), rootPath)

			for _, m := range matches {
				catColor := "\x1b[38;5;75m" // blue/cyan
				switch strings.ToLower(m.Category) {
				case "images":
					catColor = "\x1b[38;5;214m" // orange
				case "videos":
					catColor = "\x1b[38;5;177m" // purple
				case "documents":
					catColor = "\x1b[38;5;114m" // green
				case "archives":
					catColor = "\x1b[38;5;220m" // gold
				case "code":
					catColor = "\x1b[38;5;39m" // cyan
				}

				sizeStr := formatFileSize(m.Size)

				// Header: [CATEGORY] rel/path (size, date)
				fmt.Printf("  %s[%-9s]\x1b[0m \x1b[1;38;5;255m%s\x1b[0m \x1b[38;5;245m(%s)\x1b[0m\n",
					catColor, strings.ToUpper(m.Category), m.RelPath, sizeStr)

				// If content matched, print snippet below
				if m.ContentMatch != "" {
					fmt.Printf("    \x1b[38;5;246mL%-4d:\x1b[0m \x1b[38;5;250m%s\x1b[0m\n", m.ContentLineNo, m.ContentMatch)
				}
			}

			fmt.Println()
			return nil
		},
	}

	cmd.Flags().StringVarP(&typeName, "type", "t", "", "Filter by file category (image, video, document, audio, code, etc.)")
	cmd.Flags().StringSliceVarP(&exts, "ext", "e", nil, "Filter by file extensions (comma-separated, e.g. mkv,mp4)")
	cmd.Flags().StringVarP(&sizeCond, "size", "s", "", "Filter by size (e.g. '>1GB', '<50MB')")
	cmd.Flags().StringVarP(&dateCond, "modified", "m", "", "Filter by modified time (e.g. '>30d', '<7d', '2026-01-01')")
	cmd.Flags().StringVarP(&contentQuery, "content", "c", "", "Search inside file contents (grep)")
	cmd.Flags().BoolVarP(&ignoreCase, "ignore-case", "i", true, "Case-insensitive search (default true)")
	cmd.Flags().BoolVarP(&regex, "regex", "r", false, "Use regular expression for pattern")
	cmd.Flags().BoolVarP(&exact, "exact", "x", false, "Exact substring match instead of fuzzy")
	cmd.Flags().BoolVar(&fuzzy, "fuzzy", true, "Use fzf fuzzy scoring")
	cmd.Flags().BoolVarP(&includeHidden, "hidden", "H", false, "Include hidden files and folders")
	cmd.Flags().IntVarP(&limit, "limit", "l", 0, "Limit number of returned results")
	cmd.Flags().BoolVarP(&jsonOutput, "json", "j", false, "Output results in JSON format")
	cmd.Flags().BoolVar(&csvOutput, "csv", false, "Output results as CSV (category,path,rel_path,size_bytes,modified,score,content_line,content_match)")
	cmd.Flags().StringVar(&outPath, "out", "", "Write the machine-readable results (JSON by default) to FILE instead of stdout")
	cmd.Flags().StringVar(&saveName, "save", "", "Save the current query to ~/.chest/searches/<name>.toml")
	cmd.Flags().StringVar(&loadName, "load", "", "Restore a saved query from ~/.chest/searches/<name>.toml and run it")

	return cmd
}

func formatFileSize(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}
