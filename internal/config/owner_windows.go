//go:build windows

package config

import "os"

func setOwnerOnly(path string) {
	_ = path
}

func createDirAllOwnerOnly(dir string) error {
	return os.MkdirAll(dir, 0o700)
}
