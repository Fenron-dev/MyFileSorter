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
	targetRoot, err := filepath.Abs(filepath.Clean(target))
	if err != nil {
		return domain.OperationPlan{}, fmt.Errorf("resolve target: %w", err)
	}
	if strings.TrimSpace(target) == "" {
		return domain.OperationPlan{}, fmt.Errorf("target is required")
	}

	plan := domain.OperationPlan{TargetRoot: targetRoot, CreatedAt: time.Now(), Executable: true}
	seen := make(map[string]string)
	for _, proposal := range proposals {
		if proposal.Status != domain.StatusConfirmed {
			continue
		}
		if overlaps(targetRoot, proposal.SourceRoot) {
			return domain.OperationPlan{}, fmt.Errorf("source and target must not overlap")
		}
		bookDir, dirErr := naming.BookDirectory(proposal.Metadata)
		if dirErr != nil {
			plan.Executable = false
			plan.Warnings = append(plan.Warnings, proposal.Metadata.Title+": "+dirErr.Error())
			continue
		}
		for index, file := range proposal.Files {
			targetPath := filepath.Join(targetRoot, bookDir, naming.TrackName(index+1, len(proposal.Files), proposal.Metadata.Title, file.Extension))
			key := strings.ToLower(filepath.Clean(targetPath))
			if previous, exists := seen[key]; exists {
				plan.Executable = false
				plan.Warnings = append(plan.Warnings, fmt.Sprintf("Zielkonflikt zwischen %s und %s", previous, file.Path))
				continue
			}
			seen[key] = file.Path
			if _, statErr := os.Lstat(targetPath); statErr == nil {
				plan.Executable = false
				plan.Warnings = append(plan.Warnings, "Zieldatei existiert bereits: "+targetPath)
				continue
			} else if !os.IsNotExist(statErr) {
				return domain.OperationPlan{}, fmt.Errorf("inspect target: %w", statErr)
			}
			plan.Operations = append(plan.Operations, domain.PlannedOperation{
				ProposalID: proposal.ID,
				Source:     file.Path,
				Target:     targetPath,
				Size:       file.Size,
			})
			plan.TotalBytes += file.Size
		}
	}
	if len(plan.Operations) == 0 {
		plan.Executable = false
		plan.Warnings = append(plan.Warnings, "Keine bestätigten, konfliktfreien Hörbücher im Plan.")
	}
	return plan, nil
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
