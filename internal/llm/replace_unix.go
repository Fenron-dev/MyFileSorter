//go:build !windows

package llm

import "os"

func replaceStoreFile(temporary, target string) error {
	return os.Rename(temporary, target)
}
