package executor

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/dennis/myfilesorter/internal/domain"
)

const preflightDiskReserve = uint64(16 * 1024 * 1024)

func (s *Service) preflightTargets(plan domain.OperationPlan) error {
	type destinationGroup struct {
		root     string
		required uint64
		parents  map[string]struct{}
		mode     os.FileMode
	}
	type filesystemRequirement struct {
		representative string
		required       uint64
	}

	groups := make(map[string]*destinationGroup)
	seenTargets := make(map[string]string, len(plan.Operations))
	cleanupBytes := uint64(0)
	for _, operation := range plan.Operations {
		if operation.Size < 0 {
			return fmt.Errorf("ungültige Dateigröße im Zielplan: %s", operation.Source)
		}
		if operation.Action == "remove" {
			cleanupBytes += uint64(operation.Size)
			continue
		}
		if operation.Action == "deduplicate" {
			root := plan.TargetRoot
			if strings.TrimSpace(root) == "" {
				root = filepath.Dir(operation.Target)
			}
			if _, _, err := validateTargetLocation(root, operation.Target); err != nil {
				return err
			}
			if err := validateExistingFileWithin(root, operation.Target); err != nil {
				return err
			}
			cleanupBytes += uint64(operation.Size)
			continue
		}
		root := plan.TargetRoot
		if strings.TrimSpace(root) == "" {
			root = filepath.Dir(operation.Target)
		}
		root, _, err := validateTargetLocation(root, operation.Target)
		if err != nil {
			return err
		}
		targetKey := portablePathKey(operation.Target)
		if previous, exists := seenTargets[targetKey]; exists {
			return fmt.Errorf("Zieldatei ist mehrfach im Operationsplan enthalten: %s und %s", previous, operation.Target)
		}
		seenTargets[targetKey] = operation.Target
		group := groups[root]
		if group == nil {
			group = &destinationGroup{root: root, parents: make(map[string]struct{}), mode: 0o755}
			groups[root] = group
		}
		group.required += uint64(operation.Size)
		group.parents[filepath.Dir(operation.Target)] = struct{}{}
		if err := validateTargetAbsent(root, operation.Target); err != nil {
			return err
		}
	}

	if cleanupBytes > 0 {
		directory, err := s.directory()
		if err != nil {
			return err
		}
		root := filepath.Join(filepath.Dir(directory), "quarantine")
		groups["quarantine:"+root] = &destinationGroup{
			root: root, required: cleanupBytes, parents: map[string]struct{}{root: {}}, mode: 0o700,
		}
	}

	filesystems := make(map[string]*filesystemRequirement)
	for _, group := range groups {
		for parent := range group.parents {
			if err := prepareDirectoryWithin(group.root, parent, group.mode); err != nil {
				return fmt.Errorf("Zielordner vorab prüfen: %w", err)
			}
			if err := probeWritableDirectory(group.root, parent); err != nil {
				return fmt.Errorf("Zielordner ist nicht sicher beschreibbar: %w", err)
			}
		}
		key, err := diskSpaceKey(group.root)
		if err != nil {
			return fmt.Errorf("Zieldateisystem bestimmen: %w", err)
		}
		filesystem := filesystems[key]
		if filesystem == nil {
			filesystem = &filesystemRequirement{representative: group.root}
			filesystems[key] = filesystem
		}
		filesystem.required += group.required
	}
	for _, filesystem := range filesystems {
		available, known, err := availableDiskBytes(filesystem.representative)
		if err != nil {
			return fmt.Errorf("freien Zielspeicher prüfen: %w", err)
		}
		if known && filesystem.required > 0 && (available < filesystem.required || available-filesystem.required < preflightDiskReserve) {
			return fmt.Errorf(
				"zu wenig freier Speicher im Ziel %s: benötigt mindestens %d Byte plus %d Byte Reserve, verfügbar %d Byte",
				filesystem.representative, filesystem.required, preflightDiskReserve, available,
			)
		}
	}

	// Recheck every final name after creating/probing all destination directories.
	// This prevents a later conflict from producing an avoidable partial run.
	for _, operation := range plan.Operations {
		if operation.Action == "remove" {
			continue
		}
		root := plan.TargetRoot
		if strings.TrimSpace(root) == "" {
			root = filepath.Dir(operation.Target)
		}
		if operation.Action == "deduplicate" {
			if err := validateExistingFileWithin(root, operation.Target); err != nil {
				return err
			}
		} else {
			if err := validateTargetAbsent(root, operation.Target); err != nil {
				return err
			}
		}
	}
	return nil
}

func prepareTargetDestination(root, target string, private bool) error {
	root, target, err := validateTargetLocation(root, target)
	if err != nil {
		return err
	}
	if err := validateTargetAbsent(root, target); err != nil {
		return err
	}
	mode := os.FileMode(0o755)
	if private {
		mode = 0o700
	}
	if err := prepareDirectoryWithin(root, filepath.Dir(target), mode); err != nil {
		return fmt.Errorf("Zielordner anlegen: %w", err)
	}
	return validateTargetAbsent(root, target)
}

func validateTemporaryTarget(root, temporary string, expected os.FileInfo) error {
	root, temporary, err := validateTargetLocation(root, temporary)
	if err != nil {
		return err
	}
	if err := validatePathComponents(root, filepath.Dir(temporary), false); err != nil {
		return err
	}
	actual, err := os.Lstat(temporary)
	if err != nil {
		return fmt.Errorf("temporäre Zieldatei erneut prüfen: %w", err)
	}
	if !actual.Mode().IsRegular() || !os.SameFile(expected, actual) {
		return fmt.Errorf("temporäre Zieldatei wurde ausgetauscht: %s", temporary)
	}
	return nil
}

func validateExistingFileWithin(root, path string) error {
	root, path, err := validateTargetLocation(root, path)
	if err != nil {
		return err
	}
	if err := validatePathComponents(root, filepath.Dir(path), false); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("Pfad ist keine reguläre Datei: %s", path)
	}
	return nil
}

func validateTargetAbsent(root, target string) error {
	root, target, err := validateTargetLocation(root, target)
	if err != nil {
		return err
	}
	if err := validatePathComponents(root, filepath.Dir(target), true); err != nil {
		return err
	}
	if _, err := os.Lstat(target); err == nil {
		return fmt.Errorf("Zieldatei existiert bereits: %s", target)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("Ziel prüfen: %w", err)
	}
	return nil
}

func prepareDirectoryWithin(root, directory string, mode os.FileMode) error {
	root, directory, err := validateTargetLocation(root, directory)
	if err != nil {
		return err
	}
	if err := validatePathComponents(root, directory, true); err != nil {
		return err
	}
	if err := os.MkdirAll(directory, mode); err != nil {
		return err
	}
	if err := validatePathComponents(root, directory, false); err != nil {
		return err
	}
	if mode.Perm() == 0o700 {
		if err := chmodDirectoriesWithin(root, directory, mode); err != nil {
			return err
		}
	}
	return nil
}

func chmodDirectoriesWithin(root, directory string, mode os.FileMode) error {
	relative, err := filepath.Rel(root, directory)
	if err != nil {
		return err
	}
	current := root
	if err := os.Chmod(current, mode); err != nil {
		return err
	}
	if relative == "." {
		return nil
	}
	for _, component := range strings.Split(relative, string(filepath.Separator)) {
		current = filepath.Join(current, component)
		if err := os.Chmod(current, mode); err != nil {
			return err
		}
	}
	return nil
}

func probeWritableDirectory(root, directory string) error {
	root, directory, err := validateTargetLocation(root, directory)
	if err != nil {
		return err
	}
	if err := validatePathComponents(root, directory, false); err != nil {
		return err
	}
	probe, err := os.CreateTemp(directory, ".myfilesorter-preflight-*.tmp")
	if err != nil {
		return err
	}
	name := probe.Name()
	published := name + ".installed"
	defer os.Remove(name)
	defer os.Remove(published)
	if _, err := probe.Write([]byte("MyFileSorter preflight\n")); err != nil {
		probe.Close()
		return err
	}
	if err := probe.Sync(); err != nil {
		probe.Close()
		return err
	}
	info, err := probe.Stat()
	if err != nil {
		probe.Close()
		return err
	}
	if err := probe.Close(); err != nil {
		return err
	}
	if err := validatePathComponents(root, directory, false); err != nil {
		return err
	}
	current, err := os.Lstat(name)
	if err != nil || !current.Mode().IsRegular() || !os.SameFile(info, current) {
		if err == nil {
			err = fmt.Errorf("Schreibprobe wurde ausgetauscht")
		}
		return err
	}
	// Exercise the same atomic, no-replace primitive used by real transfers.
	// A filesystem that cannot provide this guarantee is rejected before any
	// source file is moved.
	if err := installFileNoReplace(name, published); err != nil {
		return fmt.Errorf("atomare Zielveröffentlichung wird nicht unterstützt: %w", err)
	}
	installed, err := os.Lstat(published)
	if err != nil || !installed.Mode().IsRegular() || !os.SameFile(info, installed) {
		if err == nil {
			err = fmt.Errorf("veröffentlichte Schreibprobe wurde ausgetauscht")
		}
		return err
	}
	if err := os.Remove(published); err != nil {
		return err
	}
	return syncDirectory(directory)
}

func validateTargetLocation(root, target string) (string, string, error) {
	if strings.TrimSpace(target) == "" {
		return "", "", fmt.Errorf("Zielpfad ist leer")
	}
	cleanTarget := filepath.Clean(target)
	if cleanTarget != target || !filepath.IsAbs(cleanTarget) {
		return "", "", fmt.Errorf("Zielpfad ist nicht absolut und normalisiert: %s", target)
	}
	if strings.TrimSpace(root) == "" {
		root = filepath.Dir(cleanTarget)
	}
	cleanRoot := filepath.Clean(root)
	if cleanRoot != root || !filepath.IsAbs(cleanRoot) {
		return "", "", fmt.Errorf("Zielroot ist nicht absolut und normalisiert: %s", root)
	}
	relative, err := filepath.Rel(cleanRoot, cleanTarget)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return "", "", fmt.Errorf("Ziel liegt außerhalb des freigegebenen Roots: %s", cleanTarget)
	}
	return cleanRoot, cleanTarget, nil
}

func portablePathKey(path string) string {
	// Conservatively fold case and common decomposed Unicode forms. This also
	// prevents plans that only work on a case-sensitive development filesystem
	// from colliding when executed on typical macOS or Windows volumes.
	return strings.ToLower(canonicalPathName(filepath.Clean(path)))
}

func validatePhysicalSourceRoot(root string) (string, error) {
	if !isCleanAbsolutePath(root) {
		return "", fmt.Errorf("Quellroot ist nicht absolut und normalisiert: %s", root)
	}
	root = filepath.Clean(root)
	if filepath.Dir(root) == root {
		return "", fmt.Errorf("Dateisystemroot darf nicht als Quellroot verwendet werden")
	}
	info, err := os.Lstat(root)
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return "", fmt.Errorf("Quellroot ist kein physischer regulärer Ordner: %s", root)
	}
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	resolved = filepath.Clean(resolved)
	if portablePathKey(resolved) != portablePathKey(root) {
		return "", fmt.Errorf("Quellroot wurde seit der Planerstellung über einen Symlink oder Reparse-Point umgeleitet: %s", root)
	}
	return resolved, nil
}

func validatePathComponents(root, destination string, allowMissing bool) error {
	root, destination, err := validateTargetLocation(root, destination)
	if err != nil {
		return err
	}
	if err := validateNearestExistingAncestor(root); err != nil {
		return err
	}
	relative, err := filepath.Rel(root, destination)
	if err != nil {
		return err
	}
	current := root
	components := []string{}
	if relative != "." {
		components = strings.Split(relative, string(filepath.Separator))
	}
	for index := -1; index < len(components); index++ {
		if index >= 0 {
			current = filepath.Join(current, components[index])
		}
		info, statErr := os.Lstat(current)
		if os.IsNotExist(statErr) && allowMissing {
			return nil
		}
		if statErr != nil {
			return statErr
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("Symlink oder Reparse-Point im Zielpfad ist nicht erlaubt: %s", current)
		}
		if !info.IsDir() {
			return fmt.Errorf("Zielpfad-Komponente ist kein Ordner: %s", current)
		}
	}
	return nil
}

func validateNearestExistingAncestor(path string) error {
	current := path
	for {
		info, err := os.Lstat(current)
		if err == nil {
			if info.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("Symlink oder Reparse-Point als Zielroot ist nicht erlaubt: %s", current)
			}
			if !info.IsDir() {
				return fmt.Errorf("Zielroot-Vorfahre ist kein Ordner: %s", current)
			}
			return nil
		}
		if !os.IsNotExist(err) {
			return err
		}
		parent := filepath.Dir(current)
		if parent == current {
			return fmt.Errorf("kein bestehender Zielroot-Vorfahre gefunden: %s", path)
		}
		current = parent
	}
}

func ensurePrivateDirectory(directory string) error {
	info, err := os.Lstat(directory)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("privater App-Ordner ist kein sicherer regulärer Ordner: %s", directory)
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		return fmt.Errorf("Rechte des privaten App-Ordners setzen: %w", err)
	}
	return nil
}
