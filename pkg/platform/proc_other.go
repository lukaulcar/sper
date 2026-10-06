//go:build !linux

package platform

import "os/exec"

func SetSysProcAttr(cmd *exec.Cmd) {
}
