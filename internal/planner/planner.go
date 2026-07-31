package planner

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dennis/myfilesorter/internal/domain"
	"github.com/dennis/myfilesorter/internal/naming"
)

func Build(target string, proposals []domain.BookProposal) (domain.OperationPlan, error) {
	return BuildWithOptions(target, proposals, domain.PlanOptions{})
}

func BuildWithOptions(target string, proposals []domain.BookProposal, options domain.PlanOptions) (domain.OperationPlan, error) {
	if strings.TrimSpace(target) == "" {
		return domain.OperationPlan{}, fmt.Errorf("target is required")
	}
	if err := validateOptions(options); err != nil {
		return domain.OperationPlan{}, err
	}
	targetRoot, err := physicalPath(target)
	if err != nil {
		return domain.OperationPlan{}, fmt.Errorf("resolve target: %w", err)
	}
	if filepath.Dir(filepath.Clean(targetRoot)) == filepath.Clean(targetRoot) {
		return domain.OperationPlan{}, fmt.Errorf("filesystem root cannot be used as target")
	}
	if info, statErr := os.Stat(targetRoot); statErr == nil && !info.IsDir() {
		return domain.OperationPlan{}, fmt.Errorf("target is not a directory")
	} else if statErr != nil && !os.IsNotExist(statErr) {
		return domain.OperationPlan{}, fmt.Errorf("inspect target: %w", statErr)
	}

	plan := domain.OperationPlan{TargetRoot: targetRoot, CreatedAt: time.Now(), Executable: true}
	seen := make(map[string]string)
	cleanupOperations := make([]domain.PlannedOperation, 0)
	seriesWidths := automaticSeriesWidths(proposals)
	for _, proposal := range proposals {
		if proposal.Status != domain.StatusConfirmed {
			continue
		}
		sourceRoot, sourceErr := physicalPath(proposal.SourceRoot)
		if sourceErr != nil {
			return domain.OperationPlan{}, fmt.Errorf("resolve source: %w", sourceErr)
		}
		if filepath.Dir(filepath.Clean(sourceRoot)) == filepath.Clean(sourceRoot) {
			return domain.OperationPlan{}, fmt.Errorf("filesystem root cannot be used as source")
		}
		if overlaps(targetRoot, sourceRoot) {
			return domain.OperationPlan{}, fmt.Errorf("source and target must not overlap")
		}
		bookWidth := options.BookNumberWidth
		if bookWidth == 0 {
			bookWidth = 2
		} else if bookWidth < 0 {
			bookWidth = seriesWidths[seriesKey(proposal)]
		}
		bookDir, dirErr := naming.BookDirectoryWithWidth(proposal.Metadata, bookWidth)
		if dirErr != nil {
			plan.Executable = false
			plan.Warnings = append(plan.Warnings, proposal.Metadata.Title+": "+dirErr.Error())
			continue
		}
		files := includedAudioFiles(proposal.Files)
		if len(files) == 0 {
			plan.Executable = false
			plan.Warnings = append(plan.Warnings, proposal.Metadata.Title+": keine Audiodateien für die Übertragung ausgewählt")
			continue
		}
		trackWidth := options.TrackNumberWidth
		trackNumbers := effectiveTrackNumbers(files)
		if trackWidth == 0 {
			trackWidth = 2
		} else if trackWidth < 0 {
			trackWidth = automaticTrackWidth(trackNumbers)
		}
		for index, file := range files {
			physicalSource, modifiedNanos, sourceErr := validateSource(sourceRoot, file.Path, file.Size)
			if sourceErr != nil {
				plan.Executable = false
				plan.Warnings = append(plan.Warnings, proposal.Metadata.Title+": "+sourceErr.Error())
				continue
			}
			trackNumber := trackNumbers[index]
			fileName := naming.TrackNameWithWidth(trackNumber, len(files), proposal.Metadata.Title, file.Extension, trackWidth)
			if options.AudioFileNaming == "source_title" {
				fileName = naming.SourceTrackName(file, trackNumber, len(files), trackWidth)
			}
			if strings.TrimSpace(file.TargetTitle) != "" {
				fileName = naming.TrackNameWithWidth(trackNumber, len(files), file.TargetTitle, file.Extension, trackWidth)
			}
			targetPath := filepath.Join(targetRoot, bookDir, fileName)
			if !withinRoot(targetRoot, targetPath) {
				plan.Executable = false
				plan.Warnings = append(plan.Warnings, "Ungültiger Zielpfad außerhalb des Zielordners: "+targetPath)
				continue
			}
			appendMove(&plan, seen, proposal.ID, "audio", sourceRoot, physicalSource, modifiedNanos, targetPath, file.Size, options.ExistingFilePolicy)
		}
		if options.MoveEbooks {
			ebooks := companionsOfKind(proposal.Companions, domain.CompanionEbook)
			for index, file := range ebooks {
				physicalSource, modifiedNanos, sourceErr := validateSource(sourceRoot, file.Path, file.Size)
				if sourceErr != nil {
					plan.Executable = false
					plan.Warnings = append(plan.Warnings, proposal.Metadata.Title+": "+sourceErr.Error())
					continue
				}
				targetPath := filepath.Join(targetRoot, "# Ebooks", bookDir, naming.EbookName(index+1, len(ebooks), proposal.Metadata.Title, file.Extension))
				if !withinRoot(targetRoot, targetPath) {
					plan.Executable = false
					plan.Warnings = append(plan.Warnings, "Ungültiger E-Book-Zielpfad außerhalb des Zielordners: "+targetPath)
					continue
				}
				appendMove(&plan, seen, proposal.ID, "ebook", sourceRoot, physicalSource, modifiedNanos, targetPath, file.Size, options.ExistingFilePolicy)
			}
		}
		if options.CleanupSidecars {
			for _, file := range companionsOfKind(proposal.Companions, domain.CompanionDiscard) {
				physicalSource, modifiedNanos, sourceErr := validateSource(sourceRoot, file.Path, file.Size)
				if sourceErr != nil {
					plan.Executable = false
					plan.Warnings = append(plan.Warnings, proposal.Metadata.Title+": "+sourceErr.Error())
					continue
				}
				cleanupOperations = append(cleanupOperations, domain.PlannedOperation{
					ProposalID: proposal.ID, Action: "remove", Category: "sidecar", SourceRoot: sourceRoot,
					Source: physicalSource, SourceModifiedNanos: modifiedNanos, Size: file.Size,
				})
			}
		}
	}
	for _, operation := range cleanupOperations {
		plan.Operations = append(plan.Operations, operation)
		plan.TotalBytes += operation.Size
	}
	if len(plan.Operations) == 0 {
		plan.Executable = false
		plan.Warnings = append(plan.Warnings, "Keine bestätigten, konfliktfreien Hörbücher im Plan.")
	}
	return plan, nil
}

func validateOptions(options domain.PlanOptions) error {
	for _, width := range []int{options.BookNumberWidth, options.TrackNumberWidth} {
		if width != 0 && width != -1 && (width < 1 || width > 6) {
			return fmt.Errorf("number width must be automatic or between 1 and 6")
		}
	}
	if options.AudioFileNaming != "" && options.AudioFileNaming != "source_title" && options.AudioFileNaming != "book_title" {
		return fmt.Errorf("unsupported audio file naming %q", options.AudioFileNaming)
	}
	if options.ExistingFilePolicy != "" && options.ExistingFilePolicy != "error" && options.ExistingFilePolicy != "skip_identical" {
		return fmt.Errorf("unsupported existing file policy %q", options.ExistingFilePolicy)
	}
	return nil
}

// physicalPath resolves all existing path components while retaining a clean
// suffix that does not exist yet. It prevents lexical symlink aliases from
// bypassing source/target overlap checks.
func physicalPath(path string) (string, error) {
	absolute, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return "", err
	}
	current := absolute
	suffix := make([]string, 0)
	for {
		resolved, resolveErr := filepath.EvalSymlinks(current)
		if resolveErr == nil {
			for index := len(suffix) - 1; index >= 0; index-- {
				resolved = filepath.Join(resolved, suffix[index])
			}
			return filepath.Clean(resolved), nil
		}
		if !os.IsNotExist(resolveErr) {
			return "", resolveErr
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", resolveErr
		}
		suffix = append(suffix, filepath.Base(current))
		current = parent
	}
}

func withinRoot(root, candidate string) bool {
	relative, err := filepath.Rel(root, candidate)
	return err == nil && (relative == "." || inside(relative))
}

func validateSource(sourceRoot, source string, expectedSize int64) (string, int64, error) {
	info, err := os.Lstat(source)
	if err != nil {
		return "", 0, fmt.Errorf("Quelldatei prüfen: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", 0, fmt.Errorf("Quelle ist keine reguläre Datei: %s", source)
	}
	if expectedSize > 0 && info.Size() != expectedSize {
		return "", 0, fmt.Errorf("Quelldatei wurde seit dem Scan verändert: %s", source)
	}
	physical, err := physicalPath(source)
	if err != nil {
		return "", 0, fmt.Errorf("Quelldatei auflösen: %w", err)
	}
	if !withinRoot(sourceRoot, physical) {
		return "", 0, fmt.Errorf("Quelldatei liegt außerhalb des Scanordners: %s", source)
	}
	return physical, info.ModTime().UnixNano(), nil
}

func effectiveTrackNumbers(files []domain.AudioFile) []int {
	numbers := make([]int, len(files))
	seen := make(map[int]struct{}, len(files))
	useDetected := true
	for index, file := range files {
		if file.Track <= 0 {
			useDetected = false
			break
		}
		if _, duplicate := seen[file.Track]; duplicate {
			useDetected = false
			break
		}
		seen[file.Track] = struct{}{}
		numbers[index] = file.Track
	}
	if !useDetected {
		for index := range numbers {
			numbers[index] = index + 1
		}
	}
	return numbers
}

func automaticTrackWidth(numbers []int) int {
	maximum := len(numbers)
	for _, number := range numbers {
		if number > maximum {
			maximum = number
		}
	}
	if maximum < 1 {
		return 1
	}
	return len(fmt.Sprintf("%d", maximum))
}

func includedAudioFiles(files []domain.AudioFile) []domain.AudioFile {
	result := make([]domain.AudioFile, 0, len(files))
	for _, file := range files {
		if !file.Excluded {
			result = append(result, file)
		}
	}
	return result
}

func automaticSeriesWidths(proposals []domain.BookProposal) map[string]int {
	maximum := make(map[string]int)
	for _, proposal := range proposals {
		if strings.TrimSpace(proposal.Metadata.Series) == "" {
			continue
		}
		value := strings.SplitN(strings.ReplaceAll(proposal.Metadata.SeriesSequence, ",", "."), ".", 2)[0]
		var number int
		if _, err := fmt.Sscanf(value, "%d", &number); err == nil && number > maximum[seriesKey(proposal)] {
			maximum[seriesKey(proposal)] = number
		}
	}
	widths := make(map[string]int, len(maximum))
	for key, number := range maximum {
		widths[key] = len(fmt.Sprintf("%d", number))
	}
	return widths
}

func seriesKey(proposal domain.BookProposal) string {
	return strings.ToLower(strings.TrimSpace(proposal.Metadata.Author) + "\x00" + strings.TrimSpace(proposal.Metadata.Series))
}

func appendMove(plan *domain.OperationPlan, seen map[string]string, proposalID, category, sourceRoot, source string, sourceModifiedNanos int64, target string, size int64, existingFilePolicy string) {
	key := strings.ToLower(filepath.Clean(target))
	if previous, exists := seen[key]; exists {
		plan.Executable = false
		plan.Warnings = append(plan.Warnings, fmt.Sprintf("Zielkonflikt zwischen %s und %s", previous, source))
		return
	}
	seen[key] = source
	if info, statErr := os.Lstat(target); statErr == nil {
		if existingFilePolicy != "skip_identical" {
			plan.Executable = false
			plan.Warnings = append(plan.Warnings, "Zieldatei existiert bereits: "+target)
			return
		}
		if !info.Mode().IsRegular() || info.Size() != size {
			plan.Executable = false
			plan.Warnings = append(plan.Warnings, "Vorhandene Zieldatei ist nicht identisch (Typ oder Größe weicht ab): "+target)
			return
		}
		plan.Operations = append(plan.Operations, domain.PlannedOperation{
			ProposalID: proposalID, Action: "deduplicate", Category: category, SourceRoot: sourceRoot,
			Source: source, SourceModifiedNanos: sourceModifiedNanos, Target: target, Size: size,
		})
		plan.TotalBytes += size
		return
	} else if !os.IsNotExist(statErr) {
		plan.Executable = false
		plan.Warnings = append(plan.Warnings, "Zieldatei konnte nicht geprüft werden: "+target)
		return
	}
	plan.Operations = append(plan.Operations, domain.PlannedOperation{
		ProposalID: proposalID, Action: "move", Category: category, SourceRoot: sourceRoot,
		Source: source, SourceModifiedNanos: sourceModifiedNanos, Target: target, Size: size,
	})
	plan.TotalBytes += size
}

func companionsOfKind(files []domain.CompanionFile, kind domain.CompanionKind) []domain.CompanionFile {
	result := make([]domain.CompanionFile, 0)
	for _, file := range files {
		if file.Kind == kind {
			result = append(result, file)
		}
	}
	return result
}

func overlaps(left, right string) bool {
	left = filepath.Clean(left)
	right = filepath.Clean(right)
	if strings.EqualFold(left, right) {
		return true
	}
	leftRel, leftErr := filepath.Rel(left, right)
	rightRel, rightErr := filepath.Rel(right, left)
	return (leftErr == nil && inside(leftRel)) || (rightErr == nil && inside(rightRel))
}

func inside(relative string) bool {
	return relative != "." && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}
