package organizer

import (
	"fmt"
	"path/filepath"

	"github.com/Aswanidev-vs/chest/internal/filesystem"
	"github.com/Aswanidev-vs/chest/internal/history"
	"github.com/Aswanidev-vs/chest/internal/models"
)

// Result represents execution summary
type Result struct {
	FilesMoved     int
	FoldersCreated int
	Errors         []string
	HistoryID      int
}

// ProgressFunc callback for reporting progress
type ProgressFunc func(op models.Operation, index, total int)

// Execute runs the plan and records history
func Execute(plan models.Plan, historyMgr *history.Manager, progress ProgressFunc) (*Result, error) {
	res := &Result{}
	var executedOps []models.HistoryOperation

	// 1. Create missing folders
	for _, folder := range plan.FoldersToCreate {
		if err := filesystem.EnsureDir(folder); err != nil {
			return nil, fmt.Errorf("failed creating directory %s: %w", folder, err)
		}
		res.FoldersCreated++
	}

	// 2. Move files
	total := len(plan.Operations)
	for i, op := range plan.Operations {
		if progress != nil {
			progress(op, i+1, total)
		}

		// Ensure parent folder in case dynamic rename changed path
		if err := filesystem.EnsureDir(filepath.Dir(op.Destination)); err != nil {
			res.Errors = append(res.Errors, fmt.Sprintf("mkdir %s: %v", filepath.Dir(op.Destination), err))
			continue
		}

		if err := filesystem.MoveFile(op.Source, op.Destination); err != nil {
			res.Errors = append(res.Errors, fmt.Sprintf("move %s -> %s: %v", op.Source, op.Destination, err))
			continue
		}

		res.FilesMoved++
		executedOps = append(executedOps, models.HistoryOperation{
			OriginalSource: op.Source,
			Destination:    op.Destination,
			Reason:         op.Reason,
		})
	}

	// 3. Save history if any files moved
	if len(executedOps) > 0 && historyMgr != nil {
		entry, err := historyMgr.Record(plan.RootPath, executedOps)
		if err == nil && entry != nil {
			res.HistoryID = entry.ID
		}
	}

	return res, nil
}
