// Package task describes durable operations independently of desktop and host adapters.
package task

import (
	"github.com/JeropsWang/local-project-hub/internal/domain/check"
	"github.com/JeropsWang/local-project-hub/internal/domain/preparation"
	"github.com/JeropsWang/local-project-hub/internal/domain/project"
	"time"
)

type ID string
type PlanID string
type Kind string
type State string

const (
	Prepare         Kind  = "prepare"
	Start           Kind  = "start"
	Stop            Kind  = "stop"
	Import          Kind  = "import"
	Export          Kind  = "export"
	Exit            Kind  = "exit"
	Queued          State = "queued"
	Running         State = "running"
	WaitingUser     State = "waiting-user"
	CancelRequested State = "cancel-requested"
	Succeeded       State = "succeeded"
	Failed          State = "failed"
	Cancelled       State = "cancelled"
	Interrupted     State = "interrupted"
)

// AppError is safe for UI display; details never carry raw secrets or SQL.
type AppError struct {
	Code      string            `json:"code"`
	Message   string            `json:"message"`
	Retryable bool              `json:"retryable"`
	Details   map[string]string `json:"details,omitempty"`
}

// View is a task snapshot. Decimal cursors avoid JavaScript integer precision loss.
type View struct {
	ID           ID        `json:"id"`
	Kind         Kind      `json:"kind"`
	State        State     `json:"state"`
	LastEventSeq string    `json:"lastEventSeq"`
	Cancelable   bool      `json:"cancelable"`
	Error        *AppError `json:"error,omitempty"`
}

// Step declares effects and cancellation policy before any action runs.
type Step struct {
	ID                string            `json:"id"`
	Action            string            `json:"action"`
	Effect            string            `json:"effect"`
	CancelPolicy      string            `json:"cancelPolicy"`
	UnitID            string            `json:"unitId,omitempty"`
	RequirementID     string            `json:"requirementId,omitempty"`
	ResourceIDs       []string          `json:"resourceIds"`
	InputFingerprints map[string]string `json:"inputFingerprints"`
}

// Plan is authored and stored by the backend; clients submit only its ID and fingerprint.
type Plan struct {
	ID                 PlanID             `json:"id"`
	Kind               Kind               `json:"kind"`
	WorkspaceID        string             `json:"workspaceId"`
	ProfileRef         *project.Ref       `json:"profileRef,omitempty"`
	Fingerprint        string             `json:"fingerprint"`
	Steps              []Step             `json:"steps"`
	RequiredBindings   []string           `json:"requiredBindings"`
	Warnings           []string           `json:"warnings"`
	ResourceIDs        []string           `json:"resourceIds"`
	SourceConstraints  []check.Constraint `json:"sourceConstraints"`
	ProjectPreparation *preparation.Plan  `json:"projectPreparation,omitempty"`
	CreatedAt          time.Time          `json:"createdAt"`
}

// SubmitRequest is idempotent per workspace and binds a request ID to the exact request body.
type SubmitRequest struct {
	PlanID      PlanID `json:"planId"`
	Fingerprint string `json:"fingerprint"`
	RequestID   string `json:"requestId"`
}

// ResolveRequest resumes the same waiting task using an optimistic event cursor.
type ResolveRequest struct {
	TaskID           ID     `json:"taskId"`
	ExpectedEventSeq string `json:"expectedEventSeq"`
	Action           string `json:"action"`
}

// EventData is a controlled progress payload, not arbitrary process output.
type EventData struct {
	Code    string `json:"code"`
	StepID  string `json:"stepId,omitempty"`
	Message string `json:"message,omitempty"`
}

// Event is persisted before publication and can be replayed from a cursor.
type Event struct {
	TaskID     ID        `json:"taskId"`
	Seq        string    `json:"seq"`
	Kind       string    `json:"kind"`
	OccurredAt time.Time `json:"occurredAt"`
	Payload    EventData `json:"payload"`
}

// Record contains backend-only request and resume bookkeeping.
type Record struct {
	View         View      `json:"view"`
	PlanID       PlanID    `json:"planId"`
	RequestID    string    `json:"requestId"`
	RequestHash  string    `json:"requestHash"`
	ResumeAction string    `json:"resumeAction,omitempty"`
	CreatedAt    time.Time `json:"createdAt"`
}

// WaitingError returns control to the scheduler without holding its execution slot.
type WaitingError struct {
	Code    string
	Message string
}

func (e *WaitingError) Error() string { return e.Code }

// Terminal identifies states that release task resource reservations.
func Terminal(state State) bool {
	return state == Succeeded || state == Failed || state == Cancelled || state == Interrupted
}

// CanTransition constrains durable task state changes including safe cancellation.
func CanTransition(from, to State) bool {
	switch from {
	case Queued:
		return to == Running || to == Cancelled || to == Interrupted || to == Failed
	case Running:
		return to == CancelRequested || to == WaitingUser || to == Succeeded || to == Failed || to == Cancelled ||
			to == Interrupted
	case CancelRequested:
		return to == Cancelled || to == Succeeded || to == Failed || to == WaitingUser || to == Interrupted
	case WaitingUser:
		return to == Queued || to == Cancelled || to == Interrupted
	}
	return false
}
