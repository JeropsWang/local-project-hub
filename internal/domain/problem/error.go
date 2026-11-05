// Package problem defines stable, safe errors shared across core boundaries.
package problem

// Error carries a machine-readable code and a non-secret subject identifier.
// Error() deliberately avoids echoing untrusted command or credential content.
type Error struct {
	Code    string
	Subject string
}

func (e *Error) Error() string { return e.Code }

// New identifies a rejected operation without exposing raw input.
func New(code, subject string) error { return &Error{Code: code, Subject: subject} }
