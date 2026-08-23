//go:build unix

package config

import "os"

func setOwnerOnly(path string) {
	_ = os.Chmod(path, 0o700)
}

func createDirAllOwnerOnly(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	setOwnerOnly(dir)
	return nil
}
