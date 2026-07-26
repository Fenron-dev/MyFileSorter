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
	targetRoot, err := filepath.Abs(filepath.Clean(target))
	if err != nil {
		return domain.OperationPlan{}, fmt.Errorf("resolve target: %w", err)
	}
	if strings.TrimSpace(target) == "" {
		return domain.OperationPlan{}, fmt.Errorf("target is required")
	}

	plan := domain.OperationPlan{TargetRoot: targetRoot, CreatedAt: time.Now(), Executable: true}
	seen := make(map[string]string)
	cleanupOperations := make([]domain.PlannedOperation, 0)
	seriesWidths := automaticSeriesWidths(proposals)
	for _, proposal := range proposals {
		if proposal.Status != domain.StatusConfirmed {
			continue
		}
		if overlaps(targetRoot, proposal.SourceRoot) {
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
		trackWidth := options.TrackNumberWidth
		if trackWidth == 0 {
			trackWidth = 2
		} else if trackWidth < 0 {
			trackWidth = automaticTrackWidth(proposal.Files)
		}
		for index, file := range proposal.Files {
			fileName := naming.TrackNameWithWidth(index+1, len(proposal.Files), proposal.Metadata.Title, file.Extension, trackWidth)
			if options.AudioFileNaming == "source_title" {
				fileName = naming.SourceTrackName(file, index+1, len(proposal.Files), trackWidth)
			}
			targetPath := filepath.Join(targetRoot, bookDir, fileName)
			appendMove(&plan, seen, proposal.ID, "audio", file.Path, targetPath, file.Size)
		}
		if options.MoveEbooks {
			ebooks := companionsOfKind(proposal.Companions, domain.CompanionEbook)
			for index, file := range ebooks {
				targetPath := filepath.Join(targetRoot, "# Ebooks", bookDir, naming.EbookName(index+1, len(ebooks), proposal.Metadata.Title, file.Extension))
				appendMove(&plan, seen, proposal.ID, "ebook", file.Path, targetPath, file.Size)
			}
		}
		if options.CleanupSidecars {
			for _, file := range companionsOfKind(proposal.Companions, domain.CompanionDiscard) {
				cleanupOperations = append(cleanupOperations, domain.PlannedOperation{
					ProposalID: proposal.ID, Action: "remove", Category: "sidecar", Source: file.Path, Size: file.Size,
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

func automaticTrackWidth(files []domain.AudioFile) int {
	maximum := len(files)
	for _, file := range files {
		if file.Track > maximum {
			maximum = file.Track
		}
	}
	if maximum < 1 {
		return 1
	}
	return len(fmt.Sprintf("%d", maximum))
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

func appendMove(plan *domain.OperationPlan, seen map[string]string, proposalID, category, source, target string, size int64) {
	key := strings.ToLower(filepath.Clean(target))
	if previous, exists := seen[key]; exists {
		plan.Executable = false
		plan.Warnings = append(plan.Warnings, fmt.Sprintf("Zielkonflikt zwischen %s und %s", previous, source))
		return
	}
	seen[key] = source
	if _, statErr := os.Lstat(target); statErr == nil {
		plan.Executable = false
		plan.Warnings = append(plan.Warnings, "Zieldatei existiert bereits: "+target)
		return
	} else if !os.IsNotExist(statErr) {
		plan.Executable = false
		plan.Warnings = append(plan.Warnings, "Zieldatei konnte nicht geprüft werden: "+target)
		return
	}
	plan.Operations = append(plan.Operations, domain.PlannedOperation{
		ProposalID: proposalID, Action: "move", Category: category, Source: source, Target: target, Size: size,
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
