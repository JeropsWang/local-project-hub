package run

import "time"

// LogEntry has a decimal cursor safe for the desktop's JavaScript boundary.
type LogEntry struct {
	Seq        string    `json:"seq"`
	Stream     string    `json:"stream"`
	Text       string    `json:"text"`
	OccurredAt time.Time `json:"occurredAt"`
}

// LogPage reports retention gaps instead of implying that rotated history is complete.
type LogPage struct {
	Entries []LogEntry `json:"entries"`
	Next    string     `json:"next"`
	Gap     bool       `json:"gap"`
}
