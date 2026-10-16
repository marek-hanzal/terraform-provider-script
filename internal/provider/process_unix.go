//go:build darwin || linux

package provider

import (
	"os/exec"
	"syscall"
)

func startSupervised(command *exec.Cmd) (func(), error) {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := command.Start(); err != nil {
		return nil, err
	}
	// Kill remaining descendants on success too. Builders must await their work,
	// and must not detach children into a new session/process group.
	return func() { _ = syscall.Kill(-command.Process.Pid, syscall.SIGKILL) }, nil
}
