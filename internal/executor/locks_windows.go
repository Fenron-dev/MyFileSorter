//go:build windows

package executor

func processAlive(int) (bool, bool) {
	// O_EXCL still provides the cross-process exclusion. Windows lock recovery
	// uses the lock heartbeat/age because process liveness needs a wider native
	// API wrapper than the standard library exposes consistently.
	return false, false
}
