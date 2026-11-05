package ports

import (
	"context"
	"io"
)

// ToolRunner is a backend-only broker for disposable controller tools. Cancelling
// a tool may terminate its own process group, never a business process/container.
type ToolRunner interface {
	RunTool(context.Context, ProcessCommand, io.Writer, io.Writer) error
}
