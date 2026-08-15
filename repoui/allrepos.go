package repoui

import (
	"context"
	"fmt"
	"strings"

	"github.com/brohd11/bubblestack/components"
	"github.com/brohd11/bubblestack/core"
	"github.com/brohd11/gitstack/repo"

	"github.com/charmbracelet/bubbles/list"
)

// Scope is one selectable set of repos the all-repos operations act on, named by Label and
// produced fresh by Repos (read on every menu build, confirm, and run — the tree moves under
// us). A consumer supplies the scopes: gdaddon passes clones / submodules / all; a plain
// viewer might pass a single "all repos" scope. When more than one is given the menu shows a
// row that cycles between them.
type Scope struct {
	Label string
	Repos func(*core.Shared) []repo.Repo
	// ExcludeRoot suppresses the include-root toggle for this scope: even while include-root is
	// on, the root is neither offered as a toggle row nor added to the targets. For a scope the
	// root doesn't belong to — e.g. gdaddon's submodules-only scope, where the root is a
	// top-level clone, not a submodule. Zero value (false) keeps the root available, as before.
	ExcludeRoot bool
}

// RootOption optionally augments the batch menu with an "include root" toggle row. When Repo
// returns ok, while the toggle is on that repo is appended to the targets of every operation.
// Zero value (nil Repo) ⇒ no row, no target — the menu is scopes-only, exactly as before.
type RootOption struct {
	Repo func(*core.Shared) (repo.Repo, bool)
}

// RootOptionFor builds a RootOption from a pointer accessor: it yields the pointed-to repo
// when non-nil, nothing otherwise (no toggle row). Consumers that keep a *repo.Repo on their
// context — the scanned base / project root when it's a checkout — pass a one-line getter
// rather than re-spelling the nil-check-and-deref closure.
func RootOptionFor(get func(*core.Shared) *repo.Repo) RootOption {
	return RootOption{Repo: func(sh *core.Shared) (repo.Repo, bool) {
		if r := get(sh); r != nil {
			return *r, true
		}
		return repo.Repo{}, false
	}}
}

// AllReposMenu is the batch git menu: fetch, pull, or push every repo in the active scope,
// cycling through scopes when more than one is given. PopStop makes it the hub the
// confirm/task sub-flows return to.
func AllReposMenu(sh *core.Shared, scopes []Scope, root RootOption) *components.PickerScreen {
	return scopeScreen(sh, scopes, 0, root, false)
}

// scopeScreen builds the menu at a given scope index. Cycling replaces the screen with a
// fresh one at the next index, so the rows (and their live counts) rebuild from the new
// filter.
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
			if _, ok := payload.(RefreshMsg); !ok {
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

	op := func(label, verb string, run func(context.Context, string, repo.Reporter) error, desc string) components.Item {
		return components.Item{
			Name: label,
			Desc: desc,
			Pick: func(sh *core.Shared) core.Action {
				if len(scopeTargets(scopes[i], root, includeRoot, sh)) == 0 {
					return core.SetStatusAndLog("no " + noun + " to " + verb)
				}
				return core.Push(newBatchConfirm(scopes, i, root, includeRoot, verb, label, run))
			},
		}
	}

	items := []list.Item{
		op("⟳ Fetch all", "fetch", func(ctx context.Context, dir string, _ repo.Reporter) error {
			return repo.GitFetch(ctx, dir)
		}, fmt.Sprintf("update remote refs for %d %s", n, noun)),
		op("⇩ Pull all", "pull", repo.GitPull,
			fmt.Sprintf("fast-forward — %d of %d %s behind origin", behind, n, noun)),
		op("⇧ Push all", "push", repo.GitPush,
			fmt.Sprintf("push local commits — %d of %d %s ahead", ahead, n, noun)),
	}

	// The include-root row only earns a place when the provider can yield a root (a base that
	// isn't a checkout adds nothing) and the active scope accepts it (a scope may ExcludeRoot —
	// e.g. a submodules-only scope the root, a clone, has no place in).
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
func newBatchConfirm(scopes []Scope, i int, root RootOption, includeRoot bool, verb, label string, run func(context.Context, string, repo.Reporter) error) *components.DialogScreen {
	return components.CreateConfirmScreen(components.ConfirmSimple{
		Crumb: "Confirm",
		Render: func(sh *core.Shared) string {
			return sh.Box(confirmBody(verb, scopeTargets(scopes[i], root, includeRoot, sh)))
		},
		OnYesLambda: func(sh *core.Shared) core.Action {
			return core.Replace(newBatchTask(scopes, i, root, includeRoot, verb, label, run))
		},
	})
}

// maxConfirmList caps the repo list in the confirm body. A DialogScreen neither scrolls nor
// clips, so an uncapped list would push the chrome off the terminal.
const maxConfirmList = 12

// confirmBody renders the confirm text: how many repos, then each with the divergence that
// makes it worth acting on. A pure function of its inputs, so it's testable and owns the cap.
func confirmBody(verb string, targets []repo.Repo) string {
	head := fmt.Sprintf("%s %d repo(s)", titleWord(verb), len(targets))
	if verb == "pull" {
		head += " — fast-forward only"
	}
	lines := []string{head + ":", ""}

	shown := len(targets)
	if shown > maxConfirmList {
		shown = maxConfirmList
	}
	// Pad the name column so the annotations line up.
	width := 0
	for _, t := range targets[:shown] {
		if len(t.Name) > width {
			width = len(t.Name)
		}
	}
	for _, t := range targets[:shown] {
		lines = append(lines, fmt.Sprintf("  %-*s  %s", width, t.Name, syncNote(verb, t.Sync)))
	}
	if n := len(targets) - shown; n > 0 {
		lines = append(lines, fmt.Sprintf("  … and %d more", n))
	}

	if verb == "pull" {
		lines = append(lines, "", "A repo that has diverged will fail and be skipped; nothing else is touched.")
	}
	return strings.Join(lines, "\n")
}

// syncNote annotates a repo in the confirm with the count relevant to the operation.
func syncNote(verb string, s repo.GitSync) string {
	switch verb {
	case "pull":
		if s.Behind > 0 {
			return fmt.Sprintf("%d behind origin", s.Behind)
		}
		return "up to date"
	case "push":
		if s.Ahead > 0 {
			return fmt.Sprintf("%d to push", s.Ahead)
		}
		return "nothing to push"
	default:
		return ""
	}
}

// newBatchTask runs verb over every repo in scope, sequentially, streaming each repo's output
// under its own header. Sequential is deliberate: interleaved output from concurrent pulls is
// unreadable, and reading what git said is the whole point of this screen (the concurrent,
// no-confirm path is a caller's fetch key). ctx is checked between repos so esc abandons the
// rest.
func newBatchTask(scopes []Scope, i int, root RootOption, includeRoot bool, verb, label string, op func(context.Context, string, repo.Reporter) error) *components.TaskScreen {
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
				report("  %s failed: %v", verb, err)
				failed++
				continue
			}
			done++
		}
		doneCh <- core.TaskEvent{Done: true}
	}
	onDone := func(sh *core.Shared, ev core.TaskEvent) core.Action {
		summary := fmt.Sprintf("%s %d repo(s)", pastTense(verb), done)
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
