//go:build !windows && !darwin && !linux

package executor

func installFileNoReplace(temporary, target string) error {
	return installFileViaHardlink(temporary, target)
}
