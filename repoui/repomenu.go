package repoui

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/brohd11/bubblestack/components"
	"github.com/brohd11/bubblestack/core"
	"github.com/brohd11/gitstack/repo"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
)

// The per-repo Git menu: status, fetch, pull, commit and push without leaving the app.
// Each operation succeeds on the plain path or fails having changed nothing; divergence,
// conflicts and rebases belong in a terminal.

// stageOptions is the commit form's staging toggle, in index order. The default (index 0) is
// the conservative one — see repo.GitCommit for why the distinction is load-bearing.
const stageAllOption = "all, incl. new files (-A)"

var stageOptions = []string{"tracked changes (-a)", stageAllOption}

// Derived from the slice, not hardcoded: reordering stageOptions can't silently flip -a/-A.
var stageAllIndex = slices.Index(stageOptions, stageAllOption)

// RepoMenu builds one checkout's Git hub. Row descriptions read the repo's current state,
// and the menu rebuilds on matching refreshes, so it doubles as a status report. It is a
// PopStop hub. The crumb defaults to "Git"; hosts reaching it from a repo row pass the
// repo name.
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
			if !refreshesRepo(payload, r.Dir) {
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
	task := func(label string, o Op, op func(context.Context, string, repo.Reporter) error) func(*core.Shared) core.Action {
		return func(*core.Shared) core.Action {
			return core.Push(Task(label, o, dir, op))
		}
	}

	return []list.Item{
		components.Item{
			Name: "⟳ Fetch",
			Desc: "update this repo's remote refs (the whole project: \"f\" on the list)",
			Pick: task("fetching "+name+"…", opFetch, func(ctx context.Context, dir string, report repo.Reporter) error {
				report("fetching %s…", name)
				return repo.GitFetch(ctx, dir)
			}),
		},
		components.Item{
			Name: "◉ Status",
			Desc: statusDesc(dirty),
			Pick: task("reading status…", opStatus, repo.GitStatus),
		},
		components.Item{
			// Sits under Status because it answers the next question: Status names the
			// files that changed, Diff shows what changed in them.
			Name: "◧ Diff",
			Desc: diffDesc(dir),
			Pick: func(sh *core.Shared) core.Action { return core.Push(DiffMenu(sh, r)) },
		},
		components.Item{
			// Log follows Diff: uncommitted changes, then what was committed.
			Name: "≡ Log",
			Desc: logDesc(dir),
			Pick: func(sh *core.Shared) core.Action { return core.Push(NewLogScreen(r)) },
		},
		components.Item{
			Name: "⇩ Pull",
			Desc: pullDesc(sync),
			Pick: task("pulling "+name+"…", opPull, repo.GitPull),
		},
		components.Item{
			Name: "⇧ Push",
			Desc: pushDesc(sync),
			Pick: task("pushing "+name+"…", opPush, repo.GitPush),
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

// Row descriptions show current state; ahead/behind counts are as fresh as the last fetch,
// hence Fetch at the top.

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

// logDesc is the last commit; GitOutput's "" covers both errors and an empty repo, and
// "no commits yet" suits both.
func logDesc(dir string) string {
	if last := repo.GitOutput(dir, "log", "-1", "--pretty=%h %s"); last != "" {
		return "history — last: " + last
	}
	return "no commits yet"
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

// newCommitForm asks for the message and what to stage. The -a mode skips new files, so
// the choice is explicit and the confirm shows its effect. The toggle is returned so
// newCommitScreen can keep the file list in sync.
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
			confirm, err := newCommitConfirm(sh, r, msg, stageAll)
			if err != nil {
				return core.SeqErr(err, core.Async(f.Focus("message")))
			}
			return core.Push(confirm)
		},
	}), stageF
}

// newCommitScreen is the commit form above a scrolling list of the files the commit will
// contain, following the stage toggle.
func newCommitScreen(r repo.Repo) *components.ModularScreen {
	form, stageF := newCommitForm(r)
	files := components.NewScrollContainer(commitFilesTitle)
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
			// The file list takes the rows the form does not use.
			{{Panel: panel, Weight: 1}, {Panel: files, Weight: 1, ExpandV: true}},
		},
		components.ModularOpts{
			Crumb:     "Commit",
			Dir:       r.Dir,
			ColWidths: []int{0}, // one flex column: full width
		},
	)
}

// commitPanel is the commit form's ScreenPanel plus keeping the file list in sync with
// the stage toggle.
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

// commitFilesTitle is the file pane's legend with nothing to flag; commitPaneTitle
// counts new files onto it.
const commitFilesTitle = "Files to Commit"

// refreshFiles rebuilds the file pane's legend and body; only a failed read or a clean
// tree shows a status line.
func (p *commitPanel) refreshFiles() {
	preview, err := repo.CommitPreview(p.dir)
	if err != nil {
		p.files.SetTitle(commitFilesTitle)
		p.files.SetStatus(err.Error())
		return
	}
	changes, stats := commitPreviewRows(preview)
	p.files.SetTitle(commitPaneTitle(changes))
	lines := commitPaneLines(changes, p.stage.Index() == stageAllIndex, stats)
	if len(lines) == 0 {
		p.files.SetStatus("nothing to commit — the working tree is clean")
		return
	}
	p.files.SetLines(lines)
}

// Keep raw paths in the repository snapshot; escape control characters only at
// the display boundary so a filename cannot inject rows into the file pane.
func commitPreviewRows(files []repo.CommitFile) ([]repo.GitChange, map[string]*repo.DiffStat) {
	changes := make([]repo.GitChange, 0, len(files))
	stats := make(map[string]*repo.DiffStat, len(files))
	displayPath := func(path string) string {
		if strings.ContainsAny(path, "\t\r\n\x1b") {
			return strconv.Quote(path)
		}
		return path
	}
	for _, file := range files {
		f := file.Status
		code := strings.ReplaceAll(string([]byte{f.Index, f.Worktree}), ".", " ")
		if f.Untracked {
			code = "??"
		}
		path := displayPath(f.Path)
		if f.OriginalPath != "" {
			path = displayPath(f.OriginalPath) + " -> " + path
		}
		changes = append(changes, repo.GitChange{Code: code, Path: path})
		stats[path] = file.Stat
	}
	return changes, stats
}

// commitPaneLines is the file pane's body: the files the commit contains, then (under -a)
// the untracked files it leaves out. Uncapped, since the pane scrolls. Empty means nothing
// changed.
func commitPaneLines(changes []repo.GitChange, stageAll bool, stats map[string]*repo.DiffStat) []string {
	included := commitable(changes, stageAll)
	untracked := excludedUntracked(changes)
	if len(included) == 0 && len(untracked) == 0 {
		return nil
	}

	// Size columns across both sections, so toggling staging keeps them aligned.
	addedWidth, deletedWidth := 2, 2
	for _, st := range stats {
		if st != nil && !st.Binary {
			addedWidth = max(addedWidth, len(fmt.Sprintf("+%d", st.Added)))
			deletedWidth = max(deletedWidth, len(fmt.Sprintf("-%d", st.Deleted)))
		}
	}
	labels := make(map[string]string, len(changes))
	width := addedWidth + 2 + deletedWidth
	for _, c := range changes {
		label := "counts unavailable"
		if st := stats[c.Path]; st != nil {
			if st.Binary {
				label = "binary"
			} else {
				label = fmt.Sprintf("%*s  %*s", addedWidth, fmt.Sprintf("+%d", st.Added), deletedWidth, fmt.Sprintf("-%d", st.Deleted))
			}
		}
		labels[c.Path] = label
		width = max(width, len(label))
	}
	rows := func(files []repo.GitChange) []string {
		lines := make([]string, 0, len(files))
		for _, c := range files {
			lines = append(lines, fmt.Sprintf("  %s  %-*s  %s", c.Code, width, labels[c.Path], c.Path))
		}
		return lines
	}
	lines := rows(included)
	if len(included) == 0 {
		lines = []string{"No existing files to commit"}
	}
	if !stageAll && len(untracked) > 0 {
		lines = append(lines, "", "Not included — new files, which \"-a\" does not stage:")
		lines = append(lines, rows(untracked)...)
	}
	return lines
}

// commitPaneTitle counts new files in the legend in both modes, since otherwise they are
// either excluded or at the bottom of a long list.
func commitPaneTitle(changes []repo.GitChange) string {
	if n := len(excludedUntracked(changes)); n > 0 {
		return fmt.Sprintf("%s (%d new)", commitFilesTitle, n)
	}
	return commitFilesTitle
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

// newCommitConfirm shows what the commit contains and which new files it leaves out. A
// failed re-read is returned rather than showing a misleading empty list.
func newCommitConfirm(sh *core.Shared, r repo.Repo, msg string, stageAll bool) (*components.ModularScreen, error) {
	changes, err := repo.GitChanges(r.Dir) // re-read: the tree may have moved since the form opened
	if err != nil {
		return nil, err
	}
	// Crumb "Conf" so the trail reads "Git › Commit › Conf" rather than repeating "Commit".
	return newListConfirm(sh, "Conf",
		func(*core.Shared) (string, []string) {
			return commitTitle(r, changes, stageAll), commitLines(changes, msg, stageAll)
		},
		func(*core.Shared) core.Action {
			return core.Replace(Task("committing "+r.Name+"…", opCommit, r.Dir,
				func(ctx context.Context, dir string, report repo.Reporter) error {
					return repo.GitCommit(ctx, dir, msg, stageAll, report)
				}))
		}), nil
}

// commitTitle is the confirm pane's legend: how many files, in which repo and branch.
func commitTitle(r repo.Repo, changes []repo.GitChange, stageAll bool) string {
	head := fmt.Sprintf("Commit %d file(s) in %s", len(commitable(changes, stageAll)), r.Name)
	if r.Branch != "" {
		head += " on " + r.Branch
	}
	return head
}

// commitLines is the confirm body: the included files, then any untracked files the mode
// leaves out, then the message. Uncapped, since the pane scrolls.
func commitLines(changes []repo.GitChange, msg string, stageAll bool) []string {
	lines := fileLines(commitable(changes, stageAll))
	if !stageAll {
		if untracked := excludedUntracked(changes); len(untracked) > 0 {
			lines = append(lines, "", "Not included — new files, which \"-a\" does not stage.")
			lines = append(lines, "Pick \"all, incl. new files\" to commit these too:", "")
			lines = append(lines, fileLines(untracked)...)
		}
	}
	return append(lines, "", "message: "+msg)
}

// excludedUntracked is what -a leaves behind: the untracked changes.
func excludedUntracked(changes []repo.GitChange) []repo.GitChange {
	var out []repo.GitChange
	for _, c := range changes {
		if c.Untracked() {
			out = append(out, c)
		}
	}
	return out
}

// fileLines renders "  XY path" rows, indented like the commit screen's file pane.
func fileLines(changes []repo.GitChange) []string {
	lines := make([]string, 0, len(changes))
	for _, c := range changes {
		lines = append(lines, fmt.Sprintf("  %s  %s", c.Code, c.Path))
	}
	return lines
}
