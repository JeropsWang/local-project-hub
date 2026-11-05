package environment

import (
	"os/exec"
	"syscall"
)

// Version probes are background work and must not flash a terminal window.
func hideProbeWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
}
