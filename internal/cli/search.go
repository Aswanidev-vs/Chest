package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/Aswanidev-vs/chest/internal/classifier"
	"github.com/Aswanidev-vs/chest/internal/plugin"
	"github.com/Aswanidev-vs/chest/internal/search"
	"github.com/spf13/cobra"
)

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
	)

	cmd := &cobra.Command{
		Use:   "search [pattern] [path]",
		Short: "High-speed concurrent search using fastwalk & fzf algorithm",
		Long: `Search the filesystem with ultra-fast parallel directory traversal and fzf scoring.

Supports filtering by filename/path, file type, extension, size, modification date,
and content grep (-c).`,
		Example: `  chest search "movie"
  chest search --type video --size ">1GB"
  chest search "*.mkv" ~/Downloads
  chest search -c "TODO" internal/
  chest search -e go,md -i "chest"`,
		RunE: func(cmd *cobra.Command, args []string) error {
			pattern := ""
			rootPath := "."

			if len(args) > 0 {
				pattern = args[0]
			}
			if len(args) > 1 {
				rootPath = args[1]
			}

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

			engine := search.New(search.SearchOptions{
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
				ClassifyFunc:  classifyFunc,
			})

			matches, err := engine.Search()
			if err != nil {
				return err
			}

			if jsonOutput {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(matches)
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
