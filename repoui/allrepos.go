package repoui

import (
	"context"
	"fmt"
	"strings"

	"github.com/brohd11/bubblestack/components"
	"github.com/brohd11/bubblestack/core"
	"github.com/brohd11/gitstack/repo"

	"charm.land/bubbles/v2/list"
)

// Scope is a named set of repos the batch operations act on. Repos is re-read on every
// build, confirm and run, since the tree changes. With several scopes the menu gets a row
// cycling between them.
type Scope struct {
	Label string
	Repos func(*core.Shared) []repo.Repo
	// ExcludeRoot keeps the include-root toggle and target out of this scope (e.g. a
	// submodules-only scope where the root is not a submodule).
	ExcludeRoot bool
}

// RootOption adds an "include root" toggle; while on, the repo Repo returns joins every
// operation's targets. A nil Repo means no toggle.
type RootOption struct {
	Repo func(*core.Shared) (repo.Repo, bool)
}

// RootOptionFor builds a RootOption from a pointer accessor: no toggle while it is nil.
func RootOptionFor(get func(*core.Shared) *repo.Repo) RootOption {
	return RootOption{Repo: func(sh *core.Shared) (repo.Repo, bool) {
		if r := get(sh); r != nil {
			return *r, true
		}
		return repo.Repo{}, false
	}}
}

// AllReposMenu is the batch git menu: fetch, pull or push every repo in the active scope.
// It is the PopStop hub its sub-flows return to.
func AllReposMenu(sh *core.Shared, scopes []Scope, root RootOption) *components.PickerScreen {
	return scopeScreen(sh, scopes, 0, root, false)
}

// scopeScreen builds the menu at scope index i; cycling replaces it with a fresh screen,
// so counts are recomputed.
func scopeScreen(sh *core.Shared, scopes []Scope, i int, root RootOption, includeRoot bool) *components.PickerScreen {
	if len(scopes) == 0 {
		scopes = []Scope{{Label: "repos", Repos: func(*core.Shared) []repo.Repo { return nil }}}
		i = 0
	}
	rootOK := func(sh *core.Shared) bool {
		if root.Repo == nil {
			return false
		}
		_, ok := root.Repo(sh)
		return ok
	}
	return components.NewPicker(menuItems(scopes, i, scopeTargets(scopes[i], root, includeRoot, sh), root, includeRoot, rootOK(sh)), components.PickerOpts{
		Title:   "Git — all repos (" + scopes[i].Label + ")",
		Crumb:   "Git all",
		PopStop: true,
		// Rebuild after any git op reports back, so popping out of a finished pull doesn't land
		// on rows that still say "2 behind".
		Refresh: func(sh *core.Shared, payload any) ([]list.Item, bool) {
			switch payload.(type) {
			case RefreshMsg, RepoRefreshMsg:
			default:
				return nil, false
			}
			return menuItems(scopes, i, scopeTargets(scopes[i], root, includeRoot, sh), root, includeRoot, rootOK(sh)), true
		},
	})
}

// scopeTargets is the one place the batch's target set is decided: the active scope's repos,
// plus the optional root when the include-root toggle is on and the root provider yields one.
func scopeTargets(scope Scope, root RootOption, includeRoot bool, sh *core.Shared) []repo.Repo {
	targets := scope.Repos(sh)
	if includeRoot && !scope.ExcludeRoot && root.Repo != nil {
		if r, ok := root.Repo(sh); ok {
			targets = append(targets, r)
		}
	}
	return targets
}

func menuItems(scopes []Scope, i int, targets []repo.Repo, root RootOption, includeRoot, rootOK bool) []list.Item {
	behind, ahead := 0, 0
	for _, t := range targets {
		if t.Sync.Behind > 0 {
			behind++
		}
		if t.Sync.Ahead > 0 {
			ahead++
		}
	}
	n := len(targets)
	noun := scopes[i].Label

	op := func(o Op, label string, run func(context.Context, string, repo.Reporter) error, desc string) components.Item {
		return components.Item{
			Name: label,
			Desc: desc,
			Pick: func(sh *core.Shared) core.Action {
				if len(scopeTargets(scopes[i], root, includeRoot, sh)) == 0 {
					return core.SetStatusAndLog("no " + noun + " to " + o.present)
				}
				return core.Push(newBatchConfirm(sh, scopes, i, root, includeRoot, o, label, run))
			},
		}
	}

	items := []list.Item{
		op(opFetch, "⟳ Fetch all", func(ctx context.Context, dir string, _ repo.Reporter) error {
			return repo.GitFetch(ctx, dir)
		}, fmt.Sprintf("update remote refs for %d %s", n, noun)),
		op(opPull, "⇩ Pull all", repo.GitPull,
			fmt.Sprintf("fast-forward — %d of %d %s behind origin", behind, n, noun)),
		op(opPush, "⇧ Push all", repo.GitPush,
			fmt.Sprintf("push local commits — %d of %d %s ahead", ahead, n, noun)),
	}

	// Offer the include-root row only when there is a root and the scope accepts it.
	if rootOK && !scopes[i].ExcludeRoot {
		state := "off"
		if includeRoot {
			state = "on"
		}
		items = append(items, components.Item{
			Name: "⌂ Include root: " + state,
			Desc: "add the base repo itself to every batch op",
			Pick: func(sh *core.Shared) core.Action {
				return core.Replace(scopeScreen(sh, scopes, i, root, !includeRoot))
			},
		})
	}

	// The scope switch only earns a row when there's more than one to cycle between.
	if len(scopes) > 1 {
		next := (i + 1) % len(scopes)
		items = append(items, components.Item{
			Name: "⚙ Scope: " + scopes[i].Label,
			Desc: "enter to cycle → " + scopes[next].Label,
			Pick: func(sh *core.Shared) core.Action {
				return core.Replace(scopeScreen(sh, scopes, next, root, includeRoot))
			},
		})
	}
	return items
}

// ---------- confirm + batch ----------

// newBatchConfirm lists every repo the operation will touch, then runs the batch on confirm.
func newBatchConfirm(sh *core.Shared, scopes []Scope, i int, root RootOption, includeRoot bool, o Op, label string, run func(context.Context, string, repo.Reporter) error) *components.ModularScreen {
	return newListConfirm(sh, "Confirm",
		func(sh *core.Shared) (string, []string) {
			targets := scopeTargets(scopes[i], root, includeRoot, sh)
			return confirmTitle(o, targets), confirmLines(o, targets)
		},
		func(sh *core.Shared) core.Action {
			return core.Replace(newBatchTask(scopes, i, root, includeRoot, o, label, run))
		})
}

// confirmTitle is the confirm pane's legend: the operation and how many repos it touches.
func confirmTitle(o Op, targets []repo.Repo) string {
	head := fmt.Sprintf("%s %d repo(s)", titleWord(o.present), len(targets))
	if o == opPull {
		head += " — fast-forward only"
	}
	return head
}

// confirmLines is the confirm body: each repo with the divergence that makes it worth
// acting on. Uncapped, since the pane scrolls. A pure function of its inputs, so it's
// testable.
func confirmLines(o Op, targets []repo.Repo) []string {
	// Pad the name column so the annotations line up.
	width := 0
	for _, t := range targets {
		width = max(width, len(t.Name))
	}
	lines := make([]string, 0, len(targets)+2)
	for _, t := range targets {
		lines = append(lines, strings.TrimRight(fmt.Sprintf("  %-*s  %s", width, t.Name, syncNote(o, t.Sync)), " "))
	}
	if o == opPull {
		lines = append(lines, "", "A repo that has diverged will fail and be skipped; nothing else is touched.")
	}
	return lines
}

// syncNote annotates a repo in the confirm with the count relevant to the operation.
func syncNote(o Op, s repo.GitSync) string {
	switch o {
	case opPull:
		if s.Behind > 0 {
			return fmt.Sprintf("%d behind origin", s.Behind)
		}
		return "up to date"
	case opPush:
		if s.Ahead > 0 {
			return fmt.Sprintf("%d to push", s.Ahead)
		}
		return "nothing to push"
	default:
		return ""
	}
}

// newBatchTask runs the operation on each repo in turn, streaming each under its own
// header (concurrent output would be unreadable). ctx is checked between repos so esc
// stops the rest.
func newBatchTask(scopes []Scope, i int, root RootOption, includeRoot bool, o Op, label string, op func(context.Context, string, repo.Reporter) error) *components.TaskScreen {
	var done, failed int
	run := func(ctx context.Context, sh *core.Shared, report func(string, ...any), doneCh chan<- core.TaskEvent) {
		targets := scopeTargets(scopes[i], root, includeRoot, sh)
		for j, t := range targets {
			if ctx.Err() != nil {
				report("aborted — %d repo(s) not reached", len(targets)-j)
				break
			}
			if j > 0 {
				report("")
			}
			report("── %s ──", t.Name)
			if err := op(ctx, t.Dir, report); err != nil {
				report("  %s: %v", o.failure, err)
				failed++
				continue
			}
			done++
		}
		doneCh <- core.TaskEvent{Done: true}
	}
	onDone := func(sh *core.Shared, ev core.TaskEvent) core.Action {
		summary := fmt.Sprintf("%s %d repo(s)", o.past, done)
		if failed > 0 {
			summary += fmt.Sprintf(" · %d failed", failed)
		}
		return core.Seq(
			core.SetStatusAndLog(summary, failed > 0),
			core.PropagateAll(RefreshMsg{}),
		)
	}
	onDismiss := func(*core.Shared) core.Action { return core.PopTo() }
	return components.NewStayTask(label+"…", "done — esc to go back", run, onDone, onDismiss)
}
