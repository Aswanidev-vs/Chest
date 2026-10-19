package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"github.com/Aswanidev-vs/chest/internal/manifest"
	"github.com/spf13/cobra"
)

// treeNode is one directory in the rendered tree, with the size of everything
// beneath it already rolled up. Files are children too, which is what lets a
// reader see not just that a folder is large but what inside it is.
type treeNode struct {
	Name string `json:"name"`
	Path string `json:"path"`
	// Size is the subtree total: this directory's own files plus every
	// descendant that was read.
	Size int64 `json:"size"`
	// Files and Dirs count what was read, not what exists. Below the depth limit
	// nothing was read, which is why NotExpanded exists alongside them.
	Files int `json:"files"`
	Dirs  int `json:"dirs"`
	// Expanded is false for a directory the walk stopped above, so a reader can
	// tell "empty" apart from "not looked at".
	Expanded bool `json:"expanded"`
	// Unreadable is true when the listing itself could not be read, which is a
	// different fact from not having been looked at.
	Unreadable bool `json:"unreadable,omitempty"`
	// NotExpanded is the number of subdirectories left unread inside this one.
	NotExpanded int  `json:"not_expanded_dirs,omitempty"`
	IsFile      bool `json:"is_file"`

	Children []*treeNode `json:"children,omitempty"`
}

// treeReport is the whole `chest tree` result.
type treeReport struct {
	Root string `json:"root"`
	// Depth is the --depth the tree was rendered at.
	Depth int `json:"depth"`
	// Sort is the --sort order applied at every level.
	Sort string `json:"sort"`
	// TotalBytes is the subtree total of the root.
	TotalBytes int64 `json:"total_bytes"`
	// FilesRead and DirsRead say how much was actually looked at, so the totals
	// are never mistaken for more than they are.
	FilesRead int `json:"files_read"`
	DirsRead  int `json:"dirs_read"`
	Excluded  int `json:"excluded_entries"`
	// NotExpandedDirs counts directories left unread because of --depth. Their
	// sizes are NOT in TotalBytes, and the report says so rather than letting a
	// truncated total read as a whole one.
	NotExpandedDirs int `json:"not_expanded_dirs"`
	// UnreadableDirs counts directories whose listing could not be read at all.
	UnreadableDirs int `json:"unreadable_dirs"`
	// BrokenSymlinks counts symlinks whose target does not exist.
	BrokenSymlinks int       `json:"broken_symlinks"`
	Note           string    `json:"note,omitempty"`
	Node           *treeNode `json:"node"`
}

// Box-drawing connectors. A tree drawn with asterisks and dashes is the same
// tree, but these are the characters the rest of the terminal output already
// uses for structure, and they align in a fixed-width font where ASCII pipes
// do not.
const (
	treeBranch = "├── "
	treeLast   = "└── "
	treePipe   = "│   "
	treeBlank  = "    "
)

// treeBarWidth is how wide the size bar is drawn beside each row. It is the
// chart renderer's `bar` at a width that leaves room for the name and the size
// in an eighty-column terminal.
const treeBarWidth = 10

func newTreeCmd() *cobra.Command {
	var (
		depth   int
		sortBy  string
		exclude []string
		jsonOut bool
	)

	cmd := &cobra.Command{
		Use:   "tree [path]",
		Short: "Draw a directory as a box-drawing tree with rolled-up sizes",
		Long: `Show a directory as a tree, each line annotated with the size it holds.

Sizes are rolled up: a folder's figure is its own files plus everything read
below it, which is the only form in which "where did the space go" is
answerable. Each row also carries a small bar drawn with the same renderer the
battery and speed charts use, scaled against the whole tree.

Strictly read-only, and fast for the reason: it reads directory listings and
file metadata only. No file is opened and no content is read, so the cost is
one stat per entry rather than one read per byte.

Directories below --depth are left unread. They are counted and the total says
so — a truncated sum is never presented as a whole one.`,
		Example: `  chest tree
  chest tree ~/Downloads --depth 1
  chest tree . --sort name --depth 2
  chest tree /var/log --json > tree.json
  chest tree . -x node_modules,.git`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if depth < 0 {
				return errors.New("--depth must be zero or greater")
			}
			if sortBy != "size" && sortBy != "name" {
				return fmt.Errorf("--sort must be size or name, got %q", sortBy)
			}
			root := "."
			if len(args) > 0 {
				root = args[0]
			}
			abs, err := filepath.Abs(filepath.Clean(root))
			if err != nil {
				return fmt.Errorf("resolving %s: %w", root, err)
			}
			info, err := os.Stat(abs)
			if err != nil {
				return fmt.Errorf("cannot read directory %s: %w", abs, err)
			}
			if !info.IsDir() {
				return fmt.Errorf("%s is not a directory", abs)
			}

			report, err := buildTree(abs, depth, sortBy, exclude)
			if err != nil {
				return err
			}

			if jsonOut {
				return emitReport(cmd.OutOrStdout(), report, nil, true, false, "")
			}
			printTree(cmd.OutOrStdout(), report)
			return nil
		},
	}

	cmd.Flags().IntVar(&depth, "depth", 3, "How many directory levels to expand (0 = the root only)")
	cmd.Flags().StringVar(&sortBy, "sort", "size", "Order siblings by size or name")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Output the tree as JSON instead of the drawn tree")
	cmd.Flags().StringSliceVarP(&exclude, "exclude", "x", nil,
		"Comma-separated directories/files to skip, e.g. node_modules,venv,.git (glob patterns allowed)")
	return cmd
}

// buildTree reads the directory listings under root and returns the report.
// It opens no file for reading: entries come from ReadDir and Info.
func buildTree(root string, depth int, sortBy string, exclude []string) (*treeReport, error) {
	excl := manifest.NewExcluder(exclude)
	rep := &treeReport{Root: root, Depth: depth, Sort: sortBy}
	rep.Node = scanTreeNode(root, 0, depth, sortBy, excl, rep)
	rep.TotalBytes = rep.Node.Size
	switch {
	case rep.NotExpandedDirs == 1:
		rep.Note = fmt.Sprintf("1 directory below depth %d left unread; its size is not in the totals", depth)
	case rep.NotExpandedDirs > 1:
		rep.Note = fmt.Sprintf("%d directories below depth %d left unread; their sizes are not in the totals",
			rep.NotExpandedDirs, depth)
	}
	return rep, nil
}

// scanTreeNode reads one directory and, while there is depth left, its
// subdirectories. Symlinks are read as files, never followed: following one
// can walk straight back up into the tree being drawn, and a cycle would never
// terminate.
func scanTreeNode(path string, level, maxDepth int, sortBy string, excl *manifest.Excluder, rep *treeReport) *treeNode {
	name := filepath.Base(path)
	node := &treeNode{Name: name, Path: path, Expanded: true}

	entries, err := os.ReadDir(path)
	if err != nil {
		// A directory nobody can read is reported, not fatal: the rest of the
		// tree is still worth drawing. Saying nothing about it would make a
		// permissions failure look like an empty folder.
		node.Expanded = false
		node.Unreadable = true
		if path != rep.Root {
			rep.UnreadableDirs++
		}
		return node
	}

	for _, entry := range entries {
		rel := treeRel(rep.Root, filepath.Join(path, entry.Name()))
		if excl.Match(entry.Name(), rel) {
			rep.Excluded++
			continue
		}

		childPath := filepath.Join(path, entry.Name())
		if !entry.IsDir() {
			info, ierr := entry.Info()
			if ierr != nil {
				// An entry whose metadata cannot be read is counted, not drawn:
				// a row with no size would read as a zero-byte file.
				rep.UnreadableDirs++
				continue
			}
			if info.Mode()&os.ModeSymlink != 0 {
				if _, serr := os.Stat(childPath); serr != nil {
					rep.BrokenSymlinks++
				}
			}
			child := &treeNode{
				Name:   entry.Name(),
				Path:   childPath,
				Size:   info.Size(),
				Files:  1,
				IsFile: true,
			}
			node.Size += child.Size
			node.Files++
			rep.FilesRead++
			node.Children = append(node.Children, child)
			continue
		}

		node.Dirs++
		if level+1 > maxDepth {
			// Past the depth limit: counted, named, and explicitly not read.
			node.NotExpanded++
			rep.NotExpandedDirs++
			node.Children = append(node.Children, &treeNode{
				Name:        entry.Name(),
				Path:        childPath,
				Expanded:    false,
				IsFile:      false,
				NotExpanded: 0,
			})
			continue
		}

		rep.DirsRead++
		child := scanTreeNode(childPath, level+1, maxDepth, sortBy, excl, rep)
		node.Size += child.Size
		node.Files += child.Files
		node.Children = append(node.Children, child)
	}

	sortTreeChildren(node.Children, sortBy)
	return node
}

// sortTreeChildren orders one level. Name order falls back to a path compare
// for equal names, and size order falls back to the same, so two runs over the
// same tree always draw the same tree — including the order of a hundred
// equal-sized files.
func sortTreeChildren(children []*treeNode, sortBy string) {
	sort.SliceStable(children, func(i, j int) bool {
		a, b := children[i], children[j]
		if sortBy == "name" {
			return treeKey(a, sortBy) < treeKey(b, sortBy)
		}
		// Directories before files at equal size: a folder and one file of the
		// same size are not the same kind of thing, and grouping the folders
		// together reads better than interleaving them.
		if (a.Size == b.Size) && a.IsFile != b.IsFile {
			return !a.IsFile
		}
		return treeKey(a, sortBy) < treeKey(b, sortBy)
	})
}

// treeKey is the comparison key for a level: descending size for `size`, the
// name for `name`, with the path as a final tie-break in both.
func treeKey(n *treeNode, sortBy string) string {
	if sortBy == "name" {
		return n.Name
	}
	// A fixed-width, zero-padded descending size keeps this a plain string
	// compare without pulling the sort into a float comparison.
	return fmt.Sprintf("%020d", int64(1<<62)-n.Size) + "\x00" + n.Name
}

// printTree draws the report with box-drawing connectors and a size bar per
// row, reusing the chart renderer's `bar` so the mark next to a directory is
// the same mark the battery report uses.
func printTree(w io.Writer, rep *treeReport) {
	total := rep.Node.Size
	if total < 0 {
		total = 0
	}

	fmt.Fprintf(w, "\n\x1b[1;38;5;255m%s\x1b[0m  \x1b[38;5;220m%s\x1b[0m  \x1b[38;5;246m%d files · %d folders\x1b[0m\n",
		rep.Node.Name, formatFileSize(total), rep.FilesRead, rep.DirsRead)

	if len(rep.Node.Children) == 0 {
		fmt.Fprintf(w, "  \x1b[38;5;246m(empty)\x1b[0m\n\n")
		return
	}
	for i, child := range rep.Node.Children {
		printTreeRow(w, child, "", i == len(rep.Node.Children)-1, total)
	}
	fmt.Fprintln(w)

	if rep.Excluded > 0 {
		fmt.Fprintf(w, "  \x1b[38;5;246m· %d entries excluded by -x/--exclude\x1b[0m\n", rep.Excluded)
	}
	if rep.UnreadableDirs > 0 {
		fmt.Fprintf(w, "  \x1b[38;5;196m· %d unreadable entries, not counted\x1b[0m\n", rep.UnreadableDirs)
	}
	if rep.BrokenSymlinks > 0 {
		fmt.Fprintf(w, "  \x1b[38;5;214m· %d broken symlink(s)\x1b[0m\n", rep.BrokenSymlinks)
	}
	if rep.Note != "" {
		fmt.Fprintf(w, "  \x1b[38;5;246m· %s\x1b[0m\n", rep.Note)
	}
	fmt.Fprintln(w)
}

// printTreeRow draws one directory line and, beneath it, its children. The
// prefix is the connector run built by the caller's depth, so the shape of the
// tree is carried by the indentation rather than re-derived at every row.
func printTreeRow(w io.Writer, n *treeNode, prefix string, last bool, total int64) {
	connector := treeBranch
	if last {
		connector = treeLast
	}

	name := n.Name
	colour := "\x1b[1;38;5;214m"
	if n.IsFile {
		colour = "\x1b[38;5;246m"
	}
	fmt.Fprintf(w, "  %s%s%s%s%s%s%s\n",
		prefix, connector, colour, name, chestReset,
		treeSizeCell(n, total), treeCountCell(n))

	if !n.Expanded || len(n.Children) == 0 {
		return
	}
	childPrefix := prefix + treePipe
	if last {
		childPrefix = prefix + treeBlank
	}
	for i, child := range n.Children {
		printTreeRow(w, child, childPrefix, i == len(n.Children)-1, total)
	}
}

// treeSizeCell is the right-hand annotation: a bar scaled against the whole
// tree, the size, and the bar's own colour. A file that is a rounding error
// still draws a cell — `bar` guarantees that — so "almost nothing" never looks
// like "nothing at all".
func treeSizeCell(n *treeNode, total int64) string {
	frac := 0.0
	if total > 0 {
		frac = float64(n.Size) / float64(total)
	}
	if n.Size == 0 {
		frac = 0
	}
	return fmt.Sprintf("  \x1b[38;5;75m%s\x1b[0m %s",
		bar(frac, treeBarWidth), formatFileSize(n.Size))
}

// treeCountCell names what the size covers. A directory that was never read
// says so on its own line instead of pretending to be empty.
func treeCountCell(n *treeNode) string {
	if n.IsFile {
		return ""
	}
	if !n.Expanded {
		if n.Unreadable {
			return "  \x1b[38;5;196m· unreadable\x1b[0m"
		}
		return "  \x1b[38;5;246m· below depth limit\x1b[0m"
	}
	plural := "s"
	if n.Files == 1 {
		plural = ""
	}
	return fmt.Sprintf("  \x1b[38;5;246m· %d file%s%s\x1b[0m", n.Files, plural, treeSubdirNote(n))
}

func treeSubdirNote(n *treeNode) string {
	if n.NotExpanded <= 0 {
		return ""
	}
	if n.NotExpanded == 1 {
		return ", 1 folder below depth"
	}
	return fmt.Sprintf(", %d folders below depth", n.NotExpanded)
}

// treeRel expresses path relative to the root with forward slashes, which is
// the form exclusion patterns are matched against.
func treeRel(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(rel)
}
