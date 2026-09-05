package models

import "time"

// HistoryOperation stores an individual reversed/applied file move.
type HistoryOperation struct {
	OriginalSource string `json:"original_source"`
	Destination    string `json:"destination"`
	Reason         string `json:"reason"`
}

// HistoryEntry stores a batch organization run.
type HistoryEntry struct {
	ID         int                `json:"id"`
	Timestamp  time.Time          `json:"timestamp"`
	Directory  string             `json:"directory"`
	Operations []HistoryOperation `json:"operations"`
	FilesCount int                `json:"files_count"`
	Status     string             `json:"status"` // "Complete", "Undone"
}
