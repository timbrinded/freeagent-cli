//go:build windows

package auth

import "os"

func makePrivateDirectory(path string) error {
	return os.MkdirAll(path, 0o700)
}
