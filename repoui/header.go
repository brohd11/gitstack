package repoui

import (
	"github.com/brohd11/bubblestack/core"
	"github.com/brohd11/gitstack/repo"

	"charm.land/lipgloss/v2"
)

// RootLineValue renders a header "Root:" value: path left-truncated to budget, with the root
// repo's StatusMarker appended when root is non-nil. The marker's rendered width is taken out
// of the path's budget so the whole value still fits budget columns. root nil ⇒ just the
// truncated path. Both repoview and gdaddon draw their base/project-root line this way.
func RootLineValue(path string, root *repo.Repo, budget int) string {
	if root == nil {
		return core.TruncLeft(path, budget)
	}
	marker := repo.StatusMarker(*root)
	return core.TruncLeft(path, budget-lipgloss.Width(marker)) + marker
}
