//go:build windows

package hook

import (
	"os/exec"
	"syscall"
)

func alive(int) bool { return false } // unused: Await polls refs on Windows

func detach(c *exec.Cmd) {
	c.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x00000008}
}
