//go:build windows

package sessionbus

import (
	"net"
	"os"
	"path/filepath"
	"strings"
)

func listenBus(path string) (net.Listener, string, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, "", err
	}
	addr := ln.Addr().String()
	_ = os.MkdirAll(filepath.Dir(path), 0o700)
	_ = os.WriteFile(path+".port", []byte(addr), 0o600)
	return ln, "tcp://" + addr, nil
}

func dialBus(path string) (net.Conn, error) {
	addr := strings.TrimPrefix(path, "tcp://")
	if strings.HasSuffix(path, ".sock") {
		data, err := os.ReadFile(path + ".port")
		if err != nil {
			return nil, err
		}
		addr = strings.TrimSpace(string(data))
	}
	return net.Dial("tcp", addr)
}

func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	_ = proc
	// On Windows FindProcess always succeeds for numeric PIDs; treat listed
	// presence as live until heartbeat TTL expires.
	return true
}
