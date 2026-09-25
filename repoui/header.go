package repoui

import (
	"github.com/brohd11/bubblestack/core"
	"github.com/brohd11/gitstack/repo"

	"charm.land/lipgloss/v2"
)

// RootLineValue renders a header "Root:" value: path left-truncated to budget, plus the
// root repo's StatusMarker (its width taken from the budget) when root is non-nil.
func RootLineValue(path string, root *repo.Repo, budget int) string {
	if root == nil {
		return core.TruncLeft(path, budget)
	}
	marker := repo.StatusMarker(*root)
	return core.TruncLeft(path, budget-lipgloss.Width(marker)) + marker
}
