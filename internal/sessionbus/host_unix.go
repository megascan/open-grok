//go:build unix

package sessionbus

import (
	"net"
	"os"
	"syscall"
)

func listenBus(path string) (net.Listener, string, error) {
	_ = os.Remove(path)
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, "", err
	}
	_ = os.Chmod(path, 0o600)
	return ln, path, nil
}

func dialBus(path string) (net.Conn, error) {
	return net.Dial("unix", path)
}

func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}
