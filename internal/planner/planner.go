package planner

import (
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Aswanidev-vs/chest/internal/filesystem"
	"github.com/Aswanidev-vs/chest/internal/models"
	"github.com/Aswanidev-vs/chest/internal/rules"
)

// Planner builds the filesystem execution plan
type Planner struct {
	engine          *rules.Engine
	baseDestination string
	collisionPolicy models.CollisionPolicy
	dateSource      string
	dateGranularity string
	// flatDestination, when set, routes every matched file directly into
	// baseDestination (ignoring the rule's category subfolder). Used by --name
	// so users get a single named folder instead of named/category/.
	flatDestination bool
}

// Option configures planner behavior without changing the existing constructor signature.
type Option func(*Planner)

// WithDate configures date placeholders and generated date sorting.
func WithDate(source, granularity string) Option {
	return func(p *Planner) {
		p.dateSource = source
		p.dateGranularity = granularity
	}
}

// New creates a new Planner
func New(engine *rules.Engine, baseDestination string, collisionPolicy models.CollisionPolicy, opts ...Option) *Planner {
	if collisionPolicy == "" {
		collisionPolicy = models.CollisionSkip
	}
	p := &Planner{
		engine:          engine,
		baseDestination: baseDestination,
		collisionPolicy: collisionPolicy,
		dateSource:      "modified",
		dateGranularity: "year",
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// NewFlat creates a Planner that routes all matches directly into
// baseDestination, without appending each rule's category folder.
func NewFlat(engine *rules.Engine, baseDestination string, collisionPolicy models.CollisionPolicy, opts ...Option) *Planner {
	p := New(engine, baseDestination, collisionPolicy, opts...)
	p.flatDestination = true
	return p
}

func (p *Planner) expandDestination(destination string, file models.File) (string, bool) {
	destDir := destination
	if strings.Contains(destDir, "{ext}") {
		extName := strings.ToUpper(file.Extension)
		if extName == "" {
			extName = "NO_EXT"
		}
		destDir = strings.ReplaceAll(destDir, "{ext}", extName)
	}

	date, ok := p.dateFor(file)
	if !ok {
		return "", false
	}
	if containsDatePlaceholder(destDir) {
		destDir = expandDatePlaceholders(destDir, date, p.dateGranularity)
	}
	return destDir, true
}

func (p *Planner) dateFor(file models.File) (time.Time, bool) {
	source := p.dateSource
	if source == "" {
		source = "modified"
	}
	switch source {
	case "auto":
		if file.TakenDate != nil {
			return *file.TakenDate, true
		}
		return file.ModTime, true
	case "taken":
		if file.TakenDate == nil {
			return time.Time{}, false
		}
		return *file.TakenDate, true
	default:
		return file.ModTime, true
	}
}

func containsDatePlaceholder(destination string) bool {
	return strings.Contains(destination, "{date}") ||
		strings.Contains(destination, "{year}") ||
		strings.Contains(destination, "{month}") ||
		strings.Contains(destination, "{day}")
}

func expandDatePlaceholders(destination string, date time.Time, granularity string) string {
	if granularity == "" {
		granularity = "year"
	}
	datePart := date.Format("2006")
	switch granularity {
	case "month":
		datePart = date.Format("2006/01")
	case "day":
		datePart = date.Format("2006/01/02")
	}

	destDir := strings.ReplaceAll(destination, "{date}", datePart)
	destDir = strings.ReplaceAll(destDir, "{year}", strconv.Itoa(date.Year()))
	destDir = strings.ReplaceAll(destDir, "{month}", fmt.Sprintf("%02d", date.Month()))
	destDir = strings.ReplaceAll(destDir, "{day}", fmt.Sprintf("%02d", date.Day()))
	return destDir
}

// Plan creates the proposed set of operations
func (p *Planner) Plan(root string, files []models.File) (models.Plan, error) {
	plan := models.Plan{
		RootPath: root,
	}

	foldersMap := make(map[string]bool)

	for _, file := range files {
		plan.TotalFiles++

		rule, reason, matched := p.engine.Evaluate(file)
		if !matched {
			plan.SkippedFiles++
			continue
		}

		// Calculate destination folder
		destDir, ok := p.expandDestination(rule.Destination, file)
		if !ok {
			plan.SkippedFiles++
			continue
		}

		// In flat mode every match goes straight into the base destination,
		// dropping the rule's category folder entirely.
		if p.flatDestination {
			if p.baseDestination != "" {
				destDir = p.baseDestination
			}
		} else if p.baseDestination != "" {
			destDir = filepath.Join(p.baseDestination, destDir)
		}

		// Absolute or root-relative destination
		var targetDir string
		if filepath.IsAbs(destDir) {
			targetDir = destDir
		} else {
			targetDir = filepath.Join(root, destDir)
		}

		targetFile := filepath.Join(targetDir, file.Name)

		// Check if already in target location
		if filepath.Clean(file.Path) == filepath.Clean(targetFile) {
			plan.SkippedFiles++
			continue
		}

		// Check for collision
		isCollision := filesystem.Exists(targetFile)
		actualTarget := targetFile
		action := p.collisionPolicy

		if isCollision {
			switch p.collisionPolicy {
			case models.CollisionSkip:
				plan.SkippedFiles++
				continue
			case models.CollisionRename:
				actualTarget = filesystem.ResolveCollision(targetFile)
			case models.CollisionReplace:
				// will overwrite
			case models.CollisionAbort:
				return plan, fmt.Errorf("file collision detected: '%s' already exists in '%s'", file.Name, targetDir)
			}
		}

		foldersMap[targetDir] = true

		plan.Operations = append(plan.Operations, models.Operation{
			Source:      file.Path,
			Destination: actualTarget,
			Reason:      reason,
			Action:      action,
			IsCollision: isCollision,
		})
	}

	// Collect unique folders to create
	for folder := range foldersMap {
		if !filesystem.Exists(folder) {
			plan.FoldersToCreate = append(plan.FoldersToCreate, folder)
		}
	}
	sort.Strings(plan.FoldersToCreate)

	return plan, nil
}
