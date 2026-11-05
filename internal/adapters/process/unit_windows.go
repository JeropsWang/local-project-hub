package process

import (
	"context"
	"path"

	"github.com/JeropsWang/local-project-hub/internal/adapters/filesystem"
	"github.com/JeropsWang/local-project-hub/internal/domain/control"
	"github.com/JeropsWang/local-project-hub/internal/domain/problem"
	"github.com/JeropsWang/local-project-hub/internal/ports"
)

type nativeUnitRunner struct{ workspace string }

// NewUnitRunner freezes one-shot scripts and logs without treating a saving task
// as a disposable build. The scheduler chooses the declared cancellation policy.
func NewUnitRunner(workspace string) ports.UnitRunner { return nativeUnitRunner{workspace: workspace} }

func (r nativeUnitRunner) RunUnit(ctx context.Context, execution ports.UnitExecution) error {
	spec := execution.Process
	if !control.ValidIdentity(spec.RunID) || !processKey.MatchString(spec.UnitID) ||
		(execution.CancelPolicy != "wait" && execution.CancelPolicy != "checkpoint") {
		return problem.New("UNIT_SPEC_INVALID", "")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	root, err := filesystem.OpenLocalRoot(r.workspace)
	if err != nil {
		return err
	}
	defer root.Close()
	if err = root.MkdirAll("runs", 0700); err != nil {
		return err
	}
	relative := path.Join("runs", spec.RunID)
	if err = root.Mkdir(relative, 0700); err != nil {
		return problem.New("RUN_SNAPSHOT_EXISTS", "")
	}
	owner := NativeProcessAdapter{workspace: r.workspace, root: root}
	scripts, err := owner.snapshotScripts(relative, spec.Scripts)
	if err != nil {
		return err
	}
	command, err := snapshotCommand(spec.Command, scripts)
	if err != nil {
		return err
	}
	log, err := NewRunLog(r.workspace, spec.RunID, spec.SecretValues)
	if err != nil {
		return err
	}
	defer log.Close()
	// Use the same small OS allowlist as business launches; no parent secret
	// variables are inherited. Runtime cache values come from the frozen command.
	command.Environment = environmentMap(businessEnvironment(command.Environment))
	stdout, stderr := log.Writer("stdout"), log.Writer("stderr")
	defer stdout.Close()
	defer stderr.Close()
	runner := nativeToolRunner{waitSafe: execution.CancelPolicy == "wait"}
	return runner.RunTool(ctx, command, stdout, stderr)
}

func environmentMap(entries []string) map[string]string {
	result := map[string]string{}
	for _, entry := range entries {
		for index := 0; index < len(entry); index++ {
			if entry[index] == '=' {
				result[entry[:index]] = entry[index+1:]
				break
			}
		}
	}
	return result
}
