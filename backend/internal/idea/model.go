package idea

import "time"

// Limits from IDEA-010 and the idea / idea_version column sizes (migration 000006).
const (
	maxTitleRunes    = 100
	maxSummaryRunes  = 1000
	maxFileNameRunes = 255 // idea_version.original_file_name VARCHAR(255); longer names are rejected
)

// Actor is the logged-in user as idea needs it. idea may not import auth (ADR 0003), so
// main.go adapts auth's session user to this type.
type Actor struct {
	EmployeeID string
	Role       string // "PARTICIPANT" or "ADMIN"
}

func (a Actor) isAdmin() bool { return a.Role == roleAdmin }

// UploadInput is the raw first-upload request. Title and summary stay strings so every
// problem with them is reported at once.
type UploadInput struct {
	TeamID      int64
	Title       string
	Summary     string
	FileName    string // as sent by the client; reduced to a plain name before storing
	ContentType string // declared type of the file part
	Data        []byte // the whole file; already bounded by the handler
}

// Version is one stored revision of the Idea file (IDEA-020).
type Version struct {
	No               int
	UploadedBy       string // Employee ID
	UploadedAt       time.Time
	OriginalFileName string
	SizeBytes        int64
}

// Idea is a team's single entry (IDEA-001).
type Idea struct {
	ID             int64
	TeamID         int64
	Title          string
	Summary        string
	CurrentVersion Version
	// Revision is the optimistic-lock counter exposed as the ETag (IDEA-030).
	Revision int64
}
