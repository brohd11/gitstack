package repoui

import (
	"path/filepath"
	"strings"
)

// RepoRefreshMsg refreshes one checkout and the known enclosing repos whose
// working-tree state may have changed with it (for example, a submodule's parent).
// Hosts retain RefreshMsg for operations that require a full reload.
type RepoRefreshMsg struct{ Dir string }

// Targets reports whether dir is the checkout the operation ran in.
func (m RepoRefreshMsg) Targets(dir string) bool {
	a, b := refreshPath(m.Dir), refreshPath(dir)
	return a != "" && a == b
}

// Affects includes the target and its enclosing directories, never siblings or
// descendants. Callers apply this only to repos they already know about.
func (m RepoRefreshMsg) Affects(dir string) bool {
	target, parent := refreshPath(m.Dir), refreshPath(dir)
	if target == "" || parent == "" {
		return false
	}
	rel, err := filepath.Rel(parent, target)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func refreshPath(dir string) string {
	if dir == "" {
		return ""
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return ""
	}
	return abs
}

func refreshesRepo(payload any, dir string) bool {
	switch p := payload.(type) {
	case RefreshMsg:
		return true
	case RepoRefreshMsg:
		return p.Affects(dir)
	}
	return false
}
