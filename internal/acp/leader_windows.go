//go:build windows

package acp

import (
	"net"
	"os"
	"path/filepath"
)

func listenLeader(path string) (net.Listener, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	_ = os.MkdirAll(filepath.Dir(path), 0o700)
	_ = os.WriteFile(path+".port", []byte(ln.Addr().String()), 0o600)
	return ln, nil
}
