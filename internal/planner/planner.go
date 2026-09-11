package planner

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Aswanidev-vs/chest/internal/filesystem"
	"github.com/Aswanidev-vs/chest/internal/models"
	"github.com/Aswanidev-vs/chest/internal/rules"
)

// Planner builds the filesystem execution plan
type Planner struct {
	engine          *rules.Engine
	baseDestination string
	collisionPolicy models.CollisionPolicy
	// flatDestination, when set, routes every matched file directly into
	// baseDestination (ignoring the rule's category subfolder). Used by --name
	// so users get a single named folder instead of named/category/.
	flatDestination bool
}

// New creates a new Planner
func New(engine *rules.Engine, baseDestination string, collisionPolicy models.CollisionPolicy) *Planner {
	if collisionPolicy == "" {
		collisionPolicy = models.CollisionSkip
	}
	return &Planner{
		engine:          engine,
		baseDestination: baseDestination,
		collisionPolicy: collisionPolicy,
	}
}

// NewFlat creates a Planner that routes all matches directly into
// baseDestination, without appending each rule's category folder.
func NewFlat(engine *rules.Engine, baseDestination string, collisionPolicy models.CollisionPolicy) *Planner {
	p := New(engine, baseDestination, collisionPolicy)
	p.flatDestination = true
	return p
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
		destDir := rule.Destination
		if strings.Contains(destDir, "{ext}") {
			extName := strings.ToUpper(file.Extension)
			if extName == "" {
				extName = "NO_EXT"
			}
			destDir = strings.ReplaceAll(destDir, "{ext}", extName)
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
