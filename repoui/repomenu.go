package repoui

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/brohd11/bubblestack/components"
	"github.com/brohd11/bubblestack/core"
	"github.com/brohd11/gitstack/repo"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
)

// The per-repo Git submenu: the routine half of working on a checkout — see what changed,
// fetch, pull, commit, push — without leaving the app for a terminal. It is deliberately not
// a git client. Every operation here either succeeds on the boring path or fails having
// changed nothing, and says so; a divergence, a conflict, or a rebase is a decision, and
// decisions belong in a real terminal.

// stageOptions is the commit form's staging toggle, in index order. The default (index 0) is
// the conservative one — see repo.GitCommit for why the distinction is load-bearing.
const stageAllOption = "all, incl. new files (-A)"

var stageOptions = []string{"tracked changes (-a)", stageAllOption}

// Derived from the slice, not hardcoded: reordering stageOptions can't silently flip -a/-A.
var stageAllIndex = slices.Index(stageOptions, stageAllOption)

// RepoMenu builds the Git command hub for one checkout. Each row's Desc reads the repo's
// current local git state (recomputed via the engine on build), so the menu itself answers
// "what shape is this repo in" before you pick anything. PopStop makes it the hub the
// sub-flows (task screens, the commit form) return to. It rebuilds on RefreshMsg so popping
// out of a finished pull doesn't land on rows that still say "3 behind". The breadcrumb
// segment defaults to "Git"; hosts that reach the menu directly (a repo row, "ctrl+v") pass
// a crumb — the repo name — so the trail still says where you are.
func RepoMenu(sh *core.Shared, r repo.Repo, crumb ...string) *components.PickerScreen {
	crumbSeg := "Git"
	if len(crumb) > 0 && crumb[0] != "" {
		crumbSeg = crumb[0]
	}
	return components.NewPicker(repoItems(r), components.PickerOpts{
		Title:   r.Name,
		Crumb:   crumbSeg,
		Dir:     r.Dir, // "t" opens a terminal at this repo from the Git menu (DirLocator)
		PopStop: true,
		Refresh: func(sh *core.Shared, payload any) ([]list.Item, bool) {
			if _, ok := payload.(RefreshMsg); !ok {
				return nil, false
			}
			return repoItems(r), true
		},
	})
}

func repoItems(r repo.Repo) []list.Item {
	name, dir := r.Name, r.Dir
	// Recomputed fresh on every build (open and post-op refresh), so the descriptions reflect
	// the repo's real state without a caller-owned cache to keep current. All local reads.
	sync := repo.GitSyncStatus(dir)
	dirty := repo.HasUncommittedChanges(dir)

	// A task row: run op, stream git's output to the log, refresh the state on success.
	task := func(label, verb string, op func(context.Context, string, repo.Reporter) error) func(*core.Shared) core.Action {
		return func(*core.Shared) core.Action {
			return core.Push(Task(label, verb, dir, op))
		}
	}

	return []list.Item{
		components.Item{
			Name: "⟳ Fetch",
			Desc: "update this repo's remote refs (the whole project: \"f\" on the list)",
			Pick: task("fetching "+name+"…", "fetched", func(ctx context.Context, dir string, report repo.Reporter) error {
				report("fetching %s…", name)
				return repo.GitFetch(ctx, dir)
			}),
		},
		components.Item{
			Name: "◉ Status",
			Desc: statusDesc(dirty),
			Pick: task("reading status…", "", repo.GitStatus),
		},
		components.Item{
			// Sits under Status because it answers the next question: Status names the
			// files that changed, Diff shows what changed in them.
			Name: "◧ Diff",
			Desc: diffDesc(dir),
			Pick: func(sh *core.Shared) core.Action { return core.Push(DiffMenu(sh, r)) },
		},
		components.Item{
			Name: "⇩ Pull",
			Desc: pullDesc(sync),
			Pick: task("pulling "+name+"…", "pulled", repo.GitPull),
		},
		components.Item{
			Name: "⇧ Push",
			Desc: pushDesc(sync),
			Pick: task("pushing "+name+"…", "pushed", repo.GitPush),
		},
		components.Item{
			Name: "✎ Commit",
			Desc: commitDesc(dir),
			Pick: func(sh *core.Shared) core.Action { return core.Push(newCommitScreen(r)) },
		},
		components.Item{
			Name: "@ Tags",
			Desc: tagsDesc(dir),
			Pick: func(sh *core.Shared) core.Action { return core.Push(TagsScreen(sh, r)) },
		},
	}
}

// The row descriptions read the current state, so the menu is a status report in itself. The
// ahead/behind counts carry the same caveat as any marker: they're as fresh as the last
// fetch, which is why Fetch sits at the top of this menu.

func statusDesc(dirty bool) string {
	if dirty {
		return "show the working tree (it has uncommitted changes)"
	}
	return "show the working tree"
}

func pullDesc(sync repo.GitSync) string {
	if sync.Behind > 0 {
		return fmt.Sprintf("fast-forward — %d commit(s) behind origin", sync.Behind)
	}
	return "fast-forward — nothing to pull (as of the last fetch)"
}

func pushDesc(sync repo.GitSync) string {
	if sync.Ahead > 0 {
		return fmt.Sprintf("push %d local commit(s) to origin", sync.Ahead)
	}
	return "nothing to push"
}

func diffDesc(dir string) string {
	changes, err := repo.GitChanges(dir)
	if err != nil || len(changes) == 0 {
		return "nothing has changed since the last commit"
	}
	return fmt.Sprintf("see what changed in %d file(s)", len(changes))
}

func commitDesc(dir string) string {
	changes, err := repo.GitChanges(dir)
	if err != nil || len(changes) == 0 {
		return "working tree is clean"
	}
	return fmt.Sprintf("commit %d changed file(s)", len(changes))
}

func tagsDesc(dir string) string {
	if tags := repo.LocalTags(dir); len(tags) > 0 {
		return fmt.Sprintf("%d local tag(s)", len(tags))
	}
	return "no local tags"
}

// ---------- commit ----------

// newCommitForm asks for the message and what to stage. The staging toggle is not a
// convenience: `git commit -a` stages only *tracked* files, so a file you just created would
// silently miss the commit. Rather than pick a surprise for the user, the form makes the
// choice explicit and the confirm screen shows its consequences. The toggle is returned
// alongside the form so the host screen (newCommitScreen) can watch it and keep the
// file list in sync.
func newCommitForm(r repo.Repo) (*components.FormScreen, *components.ToggleField) {
	// A commit message is the one field long enough to need it: NewTextAreaField wraps and
	// grows downward where NewTextField would scroll the start of the line out of view.
	msgF := components.NewTextAreaField("message", "Message: ", "what changed?")
	stageF := components.NewToggleField("stage", "Stage:   ", stageOptions, "|")

	return components.NewForm(components.FormOpts{
		Crumb: "Commit",
		Fields: []components.FormField{
			components.NewHeading("Commit " + r.Name),
			components.NewSpacer(),
			msgF, stageF,
		},
		Focus: "message",
		Help: []key.Binding{
			core.Hint("field", core.Keys.PrevField, core.Keys.NextField),
			core.Hint("stage", core.Keys.Left, core.Keys.Right),
			core.Hint("commit", core.Keys.Select),
			core.Hint("cancel", core.Keys.Back),
		},
		OnSubmit: func(sh *core.Shared, f *components.FormScreen) core.Action {
			msg := strings.TrimSpace(f.Value("message"))
			if msg == "" {
				return core.Seq(
					core.SetStatusAndLog("a commit message is required"),
					core.Async(f.Focus("message")),
				)
			}
			// The toggle's value comes off the captured field: FormScreen.Value reads text
			// fields only.
			stageAll := stageF.Index() == stageAllIndex

			changes, err := repo.GitChanges(r.Dir)
			if err != nil {
				return core.SeqErr(err, core.Async(f.Focus("message")))
			}
			if len(commitable(changes, stageAll)) == 0 {
				return core.SetStatusAndLog("nothing to commit in this mode")
			}
			confirm, err := newCommitConfirm(r, msg, stageAll)
			if err != nil {
				return core.SeqErr(err, core.Async(f.Focus("message")))
			}
			return core.Push(confirm)
		},
	}), stageF
}

// newCommitScreen is the commit form as a ModularScreen: the form itself up top,
// and below it a scrolling list of the files the commit will contain — the
// answer the confirm screen could only truncate (maxCommitList), live, following
// the stage toggle. The form's own behavior (validate → confirm → task) is
// unchanged; esc still cancels, enter still submits.
func newCommitScreen(r repo.Repo) *components.ModularScreen {
	form, stageF := newCommitForm(r)
	files := components.NewScrollContainer("Files to Commit")
	panel := &commitPanel{
		ScreenPanel: components.NewScreenPanel(form),
		form:        form,
		stage:       stageF,
		files:       files,
		dir:         r.Dir,
		last:        stageF.Index(),
	}
	panel.refreshFiles()
	return components.NewModularScreen(
		[][]components.Slot{
			// The form renders only as tall as its box; Expand hands the file
			// list whatever rows the form doesn't use, so the pane reaches the
			// bottom of the terminal instead of pooling slack below it.
			{{Panel: panel, Weight: 1}, {Panel: files, Weight: 1, ExpandV: true}},
		},
		components.ModularOpts{
			Crumb:     "Commit",
			Dir:       r.Dir,
			ColWidths: []int{0}, // one flex column: full width
		},
	)
}

// commitPanel is the commit form's ScreenPanel plus the one domain behavior a
// component can't carry: keeping the sibling file list in sync with the stage
// toggle. It used to release tab to the host's pane cycle as well; the host owns
// shift+arrows now instead, so tab is unconditionally the form's next-field key
// and the file list is shift+↓ away from any row.
type commitPanel struct {
	*components.ScreenPanel
	form  *components.FormScreen
	stage *components.ToggleField
	files *components.ScrollContainer
	dir   string
	last  int // last-seen stage toggle index
}

var _ components.Panel = (*commitPanel)(nil)
var _ components.Focusable = (*commitPanel)(nil)
var _ components.PanelUpdater = (*commitPanel)(nil)
var _ components.Capturing = (*commitPanel)(nil)

func (p *commitPanel) UpdatePanel(sh *core.Shared, msg tea.Msg) (core.Action, bool) {
	act, handled := p.ScreenPanel.UpdatePanel(sh, msg)
	if idx := p.stage.Index(); idx != p.last {
		p.last = idx
		p.refreshFiles()
	}
	return act, handled
}

// refreshFiles rebuilds the file list for the current staging mode, mirroring
// the confirm body's sections: the files the commit will contain, then — when
// "-a" leaves new files behind — the untracked set it won't, so the exclusion
// is visible before submit, not just at confirm. Uncapped (fileLines max 0):
// scrolling is the point of the pane. Empty and error states render as a
// status line instead.
func (p *commitPanel) refreshFiles() {
	changes, err := repo.GitChanges(p.dir)
	if err != nil {
		p.files.SetStatus(err.Error())
		return
	}
	stageAll := p.stage.Index() == stageAllIndex
	in := commitable(changes, stageAll)
	if len(in) == 0 {
		p.files.SetStatus("nothing to commit in this mode")
		return
	}
	lines := fileLines(in, 0)
	if !stageAll {
		if untracked := excludedUntracked(changes); len(untracked) > 0 {
			lines = append(lines, "", "Not included — new files, which \"-a\" does not stage:")
			lines = append(lines, fileLines(untracked, 0)...)
		}
	}
	p.files.SetLines(lines)
}

// commitable is the subset of changes the chosen staging mode will actually commit: with
// `-a`, tracked changes only; with `add -A`, everything.
func commitable(changes []repo.GitChange, stageAll bool) []repo.GitChange {
	out := make([]repo.GitChange, 0, len(changes))
	for _, c := range changes {
		if stageAll || !c.Untracked() {
			out = append(out, c)
		}
	}
	return out
}

// newCommitConfirm shows exactly what the commit will contain — and, when the mode excludes
// them, exactly which new files it will leave behind. The re-read error is returned, not
// swallowed: a confirm built on a failed read would claim "Commit 0 file(s)" while OnYes
// commits the real tree — misrepresenting the one thing this screen exists to show.
func newCommitConfirm(r repo.Repo, msg string, stageAll bool) (*components.DialogScreen, error) {
	changes, err := repo.GitChanges(r.Dir) // re-read: the tree may have moved since the form opened
	if err != nil {
		return nil, err
	}
	return components.CreateConfirmScreen(components.ConfirmSimple{
		// No Crumb: it defaults to "Conf", so the trail reads "Git › Commit › Conf" rather
		// than repeating "Commit" twice.
		Text: commitBody(r, changes, msg, stageAll),
		OnYes: core.Replace(Task("committing "+r.Name+"…", "committed", r.Dir,
			func(ctx context.Context, dir string, report repo.Reporter) error {
				return repo.GitCommit(ctx, dir, msg, stageAll, report)
			})),
	}), nil
}

// maxCommitList caps each file list in the confirm body. A DialogScreen neither scrolls nor
// clips (its SetSize is a no-op), so a repo with a hundred changed files would push the
// status line and help bar off the terminal; the cap is what keeps the box a box.
const maxCommitList = 10

// commitBody renders the confirm text: the files this commit will contain, then — only when
// the mode leaves them out — the untracked files it won't, named so the omission is a choice
// rather than a surprise.
func commitBody(r repo.Repo, changes []repo.GitChange, msg string, stageAll bool) string {
	included := commitable(changes, stageAll)

	head := fmt.Sprintf("Commit %d file(s) in %s", len(included), r.Name)
	if r.Branch != "" {
		head += " on " + r.Branch
	}

	lines := []string{head + ":", ""}
	lines = append(lines, fileLines(included, maxCommitList)...)

	if !stageAll {
		if untracked := excludedUntracked(changes); len(untracked) > 0 {
			lines = append(lines, "", "Not included — new files, which \"-a\" does not stage.")
			lines = append(lines, "Pick \"all, incl. new files\" to commit these too:", "")
			lines = append(lines, fileLines(untracked, maxCommitList)...)
		}
	}

	return strings.Join(append(lines, "", "message: "+msg), "\n")
}

// excludedUntracked is the set "-a" leaves behind: the untracked changes. The
// confirm body names them as a warning; the commit screen's file pane lists them
// below the included files, same idea, live.
func excludedUntracked(changes []repo.GitChange) []repo.GitChange {
	var out []repo.GitChange
	for _, c := range changes {
		if c.Untracked() {
			out = append(out, c)
		}
	}
	return out
}

// fileLines renders "  XY path" rows, capped at max rows with a trailing
// "… and N more" when it truncates; max <= 0 renders them all (the commit
// screen's scrolling pane, where truncating would defeat the point).
func fileLines(changes []repo.GitChange, max int) []string {
	n := len(changes)
	shown := n
	if max > 0 && shown > max {
		shown = max
	}
	lines := make([]string, 0, shown+1)
	for _, c := range changes[:shown] {
		lines = append(lines, fmt.Sprintf("  %s  %s", c.Code, c.Path))
	}
	if n > shown {
		lines = append(lines, fmt.Sprintf("  … and %d more", n-shown))
	}
	return lines
}
