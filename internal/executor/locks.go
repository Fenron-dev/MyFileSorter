package executor

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dennis/myfilesorter/internal/domain"
)

const (
	maxLockBytes     = 16 * 1024
	unknownLockStale = 48 * time.Hour
)

type lockRecord struct {
	PID       int       `json:"pid"`
	Token     string    `json:"token"`
	CreatedAt time.Time `json:"createdAt"`
}

type executionLock struct {
	path  string
	token string
}

type lockSnapshot struct {
	info  os.FileInfo
	token string
}

func planLockScope(plan domain.OperationPlan) string {
	root := strings.TrimSpace(plan.TargetRoot)
	if root == "" {
		for _, operation := range plan.Operations {
			if strings.TrimSpace(operation.Target) != "" {
				root = filepath.Dir(operation.Target)
				break
			}
		}
	}
	if root == "" {
		root = "cleanup-only"
	}
	return "target:" + portablePathKey(root)
}

func journalTargetLockScope(journal *Journal) string {
	if strings.TrimSpace(journal.TargetRoot) != "" {
		return "target:" + portablePathKey(journal.TargetRoot)
	}
	for _, operation := range journal.Operations {
		if operation.Action == "deduplicate" && strings.TrimSpace(operation.DuplicateOf) != "" {
			return "target:" + portablePathKey(filepath.Dir(operation.DuplicateOf))
		}
		if operation.Action != "remove" && strings.TrimSpace(operation.Target) != "" {
			return "target:" + portablePathKey(filepath.Dir(operation.Target))
		}
	}
	return "target:" + portablePathKey("cleanup-only")
}

func (s *Service) acquireExecutionLock(scope string) (*executionLock, error) {
	directory, err := s.directory()
	if err != nil {
		return nil, err
	}
	lockDirectory := filepath.Join(filepath.Dir(directory), "locks")
	if err := os.MkdirAll(lockDirectory, 0o700); err != nil {
		return nil, fmt.Errorf("Executor-Lockordner anlegen: %w", err)
	}
	if err := ensurePrivateDirectory(lockDirectory); err != nil {
		return nil, err
	}
	digest := sha256.Sum256([]byte(scope))
	path := filepath.Join(lockDirectory, hex.EncodeToString(digest[:16])+".lock")

	for attempt := 0; attempt < 2; attempt++ {
		token, err := randomLockToken()
		if err != nil {
			return nil, err
		}
		record := lockRecord{PID: os.Getpid(), Token: token, CreatedAt: s.now().UTC()}
		file, openErr := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if openErr == nil {
			data, marshalErr := json.Marshal(record)
			if marshalErr == nil {
				_, marshalErr = file.Write(append(data, '\n'))
			}
			if marshalErr == nil {
				marshalErr = file.Sync()
			}
			closeErr := file.Close()
			if marshalErr != nil || closeErr != nil {
				_ = os.Remove(path)
				if marshalErr != nil {
					return nil, fmt.Errorf("Executor-Lock schreiben: %w", marshalErr)
				}
				return nil, fmt.Errorf("Executor-Lock schließen: %w", closeErr)
			}
			_ = syncDirectory(lockDirectory)
			return &executionLock{path: path, token: token}, nil
		}
		if !os.IsExist(openErr) {
			return nil, fmt.Errorf("Executor-Lock anlegen: %w", openErr)
		}
		stale, owner, snapshot, staleErr := staleLock(path, s.now())
		if staleErr != nil {
			return nil, staleErr
		}
		if !stale {
			return nil, fmt.Errorf("eine andere MyFileSorter-Instanz verarbeitet dieses Ziel bereits%s", owner)
		}
		if err := removeStaleLock(path, snapshot); err != nil {
			return nil, err
		}
	}
	return nil, fmt.Errorf("Executor-Lock konnte nach Bereinigung nicht übernommen werden")
}

func staleLock(path string, now time.Time) (bool, string, *lockSnapshot, error) {
	pathInfo, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return true, "", nil, nil
		}
		return false, "", nil, fmt.Errorf("bestehenden Executor-Lock prüfen: %w", err)
	}
	if !pathInfo.Mode().IsRegular() {
		return false, "", nil, fmt.Errorf("unsicherer Executor-Lock ist keine reguläre Datei: %s", path)
	}
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return true, "", nil, nil
		}
		return false, "", nil, fmt.Errorf("bestehenden Executor-Lock lesen: %w", err)
	}
	openedInfo, statErr := file.Stat()
	if statErr != nil || !openedInfo.Mode().IsRegular() || !os.SameFile(pathInfo, openedInfo) {
		_ = file.Close()
		if statErr == nil {
			statErr = fmt.Errorf("Executor-Lock wurde beim Öffnen ausgetauscht")
		}
		return false, "", nil, statErr
	}
	data, readErr := io.ReadAll(io.LimitReader(file, maxLockBytes+1))
	closeErr := file.Close()
	if readErr != nil {
		return false, "", nil, readErr
	}
	if closeErr != nil {
		return false, "", nil, closeErr
	}
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return true, "", nil, nil
		}
		return false, "", nil, err
	}
	if !info.Mode().IsRegular() || !os.SameFile(openedInfo, info) {
		return false, "", nil, fmt.Errorf("Executor-Lock wurde beim Lesen ausgetauscht")
	}
	age := now.Sub(info.ModTime())
	snapshot := &lockSnapshot{info: info}
	if len(data) > maxLockBytes {
		return age > unknownLockStale, " (Lockdatei ist beschädigt)", snapshot, nil
	}
	var record lockRecord
	if err := json.Unmarshal(data, &record); err != nil || record.PID <= 0 || record.Token == "" {
		return age > unknownLockStale, " (Lockdatei ist beschädigt)", snapshot, nil
	}
	snapshot.token = record.Token
	alive, known := processAlive(record.PID)
	if known {
		if alive {
			return false, fmt.Sprintf(" (PID %d)", record.PID), snapshot, nil
		}
		return true, "", snapshot, nil
	}
	return age > unknownLockStale, fmt.Sprintf(" (PID %d, Alter %s)", record.PID, age.Round(time.Minute)), snapshot, nil
}

func removeStaleLock(path string, expected *lockSnapshot) error {
	if expected == nil {
		return nil
	}
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer file.Close()
	openedInfo, err := file.Stat()
	if err != nil || !openedInfo.Mode().IsRegular() || !os.SameFile(expected.info, openedInfo) {
		return fmt.Errorf("Executor-Lock wurde während der Bereinigung ersetzt")
	}
	if expected.token != "" {
		data, readErr := io.ReadAll(io.LimitReader(file, maxLockBytes+1))
		if readErr != nil {
			return readErr
		}
		var record lockRecord
		if json.Unmarshal(data, &record) != nil || record.Token != expected.token {
			return fmt.Errorf("Executor-Lock wurde während der Bereinigung verändert")
		}
	}
	pathInfo, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil || !pathInfo.Mode().IsRegular() || !os.SameFile(openedInfo, pathInfo) {
		return fmt.Errorf("Executor-Lock wurde unmittelbar vor der Bereinigung ersetzt")
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("Executor-Lock vor der Bereinigung schließen: %w", err)
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("verwaisten Executor-Lock entfernen: %w", err)
	}
	return nil
}

func (l *executionLock) Touch() {
	if l == nil || !lockHasToken(l.path, l.token) {
		return
	}
	now := time.Now()
	_ = os.Chtimes(l.path, now, now)
}

func (l *executionLock) Release() {
	if l == nil {
		return
	}
	snapshot, owned := lockSnapshotWithToken(l.path, l.token)
	if !owned {
		return
	}
	_ = removeStaleLock(l.path, snapshot)
}

func lockHasToken(path, token string) bool {
	_, found := lockSnapshotWithToken(path, token)
	return found
}

func lockSnapshotWithToken(path, token string) (*lockSnapshot, bool) {
	pathInfo, err := os.Lstat(path)
	if err != nil || !pathInfo.Mode().IsRegular() {
		return nil, false
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, false
	}
	openedInfo, statErr := file.Stat()
	data, err := io.ReadAll(io.LimitReader(file, maxLockBytes+1))
	_ = file.Close()
	if statErr != nil || !openedInfo.Mode().IsRegular() || !os.SameFile(pathInfo, openedInfo) || err != nil || len(data) > maxLockBytes {
		return nil, false
	}
	currentInfo, err := os.Lstat(path)
	if err != nil || !currentInfo.Mode().IsRegular() || !os.SameFile(openedInfo, currentInfo) {
		return nil, false
	}
	var record lockRecord
	if json.Unmarshal(data, &record) != nil || record.Token != token {
		return nil, false
	}
	return &lockSnapshot{info: currentInfo, token: token}, true
}

func randomLockToken() (string, error) {
	data := make([]byte, 16)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	return hex.EncodeToString(data), nil
}
