package process

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JeropsWang/local-project-hub/internal/ports"
)

func TestUnitRunnerWaitPolicySavesDespiteCallerCancellation(t *testing.T) {
	for _, policy := range []string{"checkpoint", "wait"} {
		t.Run(policy, func(t *testing.T) {
			root := t.TempDir()
			source := t.TempDir()
			script := []byte(
				"Set-Content -LiteralPath (Join-Path $PWD 'started.txt') -Value 'started'\nStart-Sleep -Milliseconds 250\nSet-Content -LiteralPath (Join-Path $PWD 'completed.txt') -Value 'saved'\nWrite-Output 'fixture-token'\n",
			)
			sum := sha256.Sum256(script)
			spec := ports.UnitExecution{
				CancelPolicy: policy,
				Process: ports.ProcessSpec{
					RunID:  fixtureControlRun,
					UnitID: "build",
					Command: ports.ProcessCommand{
						Executable: filepath.Join(
							os.Getenv("SYSTEMROOT"),
							"System32",
							"WindowsPowerShell",
							"v1.0",
							"powershell.exe",
						),
						Args: []string{"-NoProfile", "-NonInteractive", "-File", "${file:build}"},
						CWD:  source,
					},
					Scripts: map[string]ports.ProcessScript{
						"build": {Name: "build.ps1", Bytes: script, SHA256: hex.EncodeToString(sum[:])},
					},
					SecretValues: []string{"fixture-token"},
				},
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			go func() {
				for {
					if _, err := os.Stat(filepath.Join(source, "started.txt")); err == nil {
						cancel()
						return
					}
					select {
					case <-ctx.Done():
						return
					case <-time.After(10 * time.Millisecond):
					}
				}
			}()
			err := NewUnitRunner(root).RunUnit(ctx, spec)
			if policy == "wait" {
				if err != nil {
					t.Fatal("interrupted wait-policy task", err)
				}
				if _, err = os.Stat(filepath.Join(source, "completed.txt")); err != nil {
					t.Fatal("lost one-shot save", err)
				}
				page, err := ReadRunLog(root, fixtureControlRun, "0", 2000)
				if err != nil {
					t.Fatal(err)
				}
				for _, entry := range page.Entries {
					if strings.Contains(entry.Text, "fixture-token") {
						t.Fatal("task secret reached logs")
					}
				}
			} else if err == nil {
				t.Fatal("checkpoint task ignored cancellation")
			}
		})
	}
}
