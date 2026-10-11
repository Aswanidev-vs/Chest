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
	// explicitPolicy records whether the caller passed a concrete collision
	// policy. New maps "" to CollisionSkip internally, so the two are
	// indistinguishable from collisionPolicy alone; the default (auto-rename
	// on intra-plan conflicts) must only apply to the implicit case.
	explicitPolicy  bool
	dateSource      string
	dateGranularity string
	dateMonthFormat string
	// flatDestination, when set, routes every matched file directly into
	// baseDestination (ignoring the rule's category subfolder). Used by --name
	// so users get a single named folder instead of named/category/.
	flatDestination bool
}

// Option configures planner behavior without changing the existing constructor signature.
type Option func(*Planner)

// WithDate configures date placeholders and generated date sorting.
// monthFormat is "name" (Jan) or "number" (01); anything else behaves as "name".
func WithDate(source, granularity, monthFormat string) Option {
	return func(p *Planner) {
		p.dateSource = source
		p.dateGranularity = granularity
		p.dateMonthFormat = monthFormat
	}
}

// New creates a new Planner
func New(engine *rules.Engine, baseDestination string, collisionPolicy models.CollisionPolicy, opts ...Option) *Planner {
	explicitPolicy := collisionPolicy != ""
	if collisionPolicy == "" {
		collisionPolicy = models.CollisionSkip
	}
	p := &Planner{
		engine:          engine,
		baseDestination: baseDestination,
		collisionPolicy: collisionPolicy,
		explicitPolicy:  explicitPolicy,
		dateSource:      "modified",
		dateGranularity: "year",
		dateMonthFormat: "name",
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
		destDir = expandDatePlaceholders(destDir, date, p.dateGranularity, p.dateMonthFormat)
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

func expandDatePlaceholders(destination string, date time.Time, granularity, monthFormat string) string {
	if granularity == "" {
		granularity = "year"
	}
	month := date.Format("Jan")
	if monthFormat == "number" {
		month = fmt.Sprintf("%02d", date.Month())
	}

	year := strconv.Itoa(date.Year())
	day := fmt.Sprintf("%02d", date.Day())

	datePart := year
	switch granularity {
	case "month":
		datePart = year + "/" + month
	case "day":
		datePart = year + "/" + month + "/" + day
	}

	destDir := strings.ReplaceAll(destination, "{date}", datePart)
	destDir = strings.ReplaceAll(destDir, "{year}", year)
	destDir = strings.ReplaceAll(destDir, "{month}", month)
	destDir = strings.ReplaceAll(destDir, "{day}", day)
	return destDir
}

// resolveUniqueTarget returns the first variant of targetFile that is free on
// disk and unclaimed by earlier operations in this plan
// (photo.jpg -> photo (1).jpg -> photo (2).jpg ...), plus the source whose
// in-plan claim forced the rename ("" when no claim was hit).
// filesystem.ResolveCollision is disk-aware only, so it would return the same
// free name for two files that both target photo.jpg and the collision would
// reappear at execute time.
func resolveUniqueTarget(targetFile string, claimed map[string]string) (string, string) {
	var blockedBy string
	if !filesystem.Exists(targetFile) {
		prev, taken := claimed[filesystem.CaseKey(targetFile)]
		if !taken {
			return targetFile, ""
		}
		blockedBy = prev
	}

	dir := filepath.Dir(targetFile)
	base := filepath.Base(targetFile)
	ext := filepath.Ext(base)
	stem := strings.TrimSuffix(base, ext)

	for counter := 1; ; counter++ {
		candidate := filepath.Join(dir, fmt.Sprintf("%s (%d)%s", stem, counter, ext))
		if filesystem.Exists(candidate) {
			continue
		}
		if prev, taken := claimed[filesystem.CaseKey(candidate)]; taken {
			blockedBy = prev
			continue
		}
		return candidate, blockedBy
	}
}

// Plan creates the proposed set of operations
func (p *Planner) Plan(root string, files []models.File) (models.Plan, error) {
	plan := models.Plan{
		RootPath: root,
	}

	foldersMap := make(map[string]bool)
	// claimed maps destinations already taken by earlier operations in this
	// plan (case-folded key) to the source that claimed them. Without it two
	// files resolving to the same destination are both planned and the second
	// move silently overwrites the first at execute time.
	claimed := make(map[string]string)

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

		// Check for collision, both against the disk and against destinations
		// already claimed by earlier operations in this plan. Keys are
		// case-folded so Photo.jpg / photo.jpg cannot slip through as two
		// distinct targets on case-insensitive filesystems.
		onDisk := filesystem.Exists(targetFile)
		firstClaimer, claimedInPlan := claimed[filesystem.CaseKey(targetFile)]
		isCollision := onDisk || claimedInPlan
		actualTarget := targetFile
		action := p.collisionPolicy

		if isCollision {
			switch p.collisionPolicy {
			case models.CollisionSkip:
				// A file already on disk keeps today's behavior: skip.
				if !claimedInPlan {
					plan.SkippedFiles++
					continue
				}
				// Intra-plan-only conflict: an explicit skip policy leaves the
				// second file in place; the implicit default auto-renames it.
				if p.explicitPolicy {
					plan.Conflicts = append(plan.Conflicts, models.PlanConflict{
						Target:     file.Path,
						Sources:    []string{firstClaimer, file.Path},
						Resolution: "second file left in place under skip policy",
					})
					plan.SkippedFiles++
					continue
				}
				actualTarget, _ = resolveUniqueTarget(targetFile, claimed)
				action = models.CollisionRename
				plan.Conflicts = append(plan.Conflicts, models.PlanConflict{
					Target:     actualTarget,
					Sources:    []string{firstClaimer, file.Path},
					Resolution: "auto-renamed second file to " + filepath.Base(actualTarget),
				})
			case models.CollisionRename:
				var blockedBy string
				actualTarget, blockedBy = resolveUniqueTarget(targetFile, claimed)
				if blockedBy != "" {
					plan.Conflicts = append(plan.Conflicts, models.PlanConflict{
						Target:     actualTarget,
						Sources:    []string{blockedBy, file.Path},
						Resolution: "auto-renamed second file to " + filepath.Base(actualTarget),
					})
				}
			case models.CollisionReplace:
				if claimedInPlan {
					plan.Conflicts = append(plan.Conflicts, models.PlanConflict{
						Target:     actualTarget,
						Sources:    []string{firstClaimer, file.Path},
						Resolution: "overwrite under replace policy",
					})
				}
			case models.CollisionAbort:
				return plan, fmt.Errorf("file collision detected: '%s' already exists in '%s'", file.Name, targetDir)
			}
		}

		claimed[filesystem.CaseKey(actualTarget)] = file.Path
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
