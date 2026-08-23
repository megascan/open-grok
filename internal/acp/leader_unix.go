//go:build unix

package acp

import "net"

func listenLeader(path string) (net.Listener, error) {
	return net.Listen("unix", path)
}
