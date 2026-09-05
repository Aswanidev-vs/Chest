package models

// CollisionPolicy defines how to handle existing destination files.
type CollisionPolicy string

const (
	CollisionSkip    CollisionPolicy = "skip"
	CollisionRename  CollisionPolicy = "rename"
	CollisionReplace CollisionPolicy = "replace"
	CollisionAbort   CollisionPolicy = "abort"
)

// Operation represents a single proposed or executed file move.
type Operation struct {
	Source      string          `json:"source"`
	Destination string          `json:"destination"`
	Reason      string          `json:"reason"`
	Action      CollisionPolicy `json:"action"` // what will happen if collision exists
	IsCollision bool            `json:"is_collision"`
}

// Plan represents the complete set of proposed filesystem changes.
type Plan struct {
	RootPath        string      `json:"root_path"`
	Operations      []Operation `json:"operations"`
	FoldersToCreate []string    `json:"folders_to_create"`
	TotalFiles      int         `json:"total_files"`
	SkippedFiles    int         `json:"skipped_files"`
}
