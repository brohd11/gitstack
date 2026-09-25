package repoui

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/brohd11/bubblestack/components"
	"github.com/brohd11/bubblestack/core"
	"github.com/brohd11/gitstack/repo"
	"github.com/brohd11/goutil/strutil"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
)

// The Diff view: what changed inside the files git status names, working tree against
// HEAD (staged and unstaged), matching what a commit would include. Read-only.

// keys are the Diff view's own bindings, outside core.Keys. Wrap is core.Keys.Wrap, the
// same gesture as wrapping the output pane (DiffScreen is a core.Wrapper).
var keys = struct {
	Layout key.Binding
}{
	Layout: key.NewBinding(key.WithKeys("s"), key.WithHelp("s", "layout")),
}

// layout is the diff arrangement; auto is the zero value. Auto and layoutSplit agree when
// the width allows; when it does not, auto silently goes unified, while an explicit split
// explains why it cannot be honored.
type layout int

const (
	layoutAuto layout = iota // the width decides, silently
	layoutUnified
	layoutSplit
)

// layoutCount is the number of modes the s key cycles through.
const layoutCount = 3

// DiffScreen is a scrollable diff for one file or a whole repo, captured and parsed once
// when the screen opens; the layout toggle re-runs only the render.
type DiffScreen struct {
	pager
	lines  []diffLine
	empty  string // set when there is nothing to show; rendered in place of the diff
	layout layout // s — cycles auto → unified → side by side
}

var (
	_ core.Crumber    = (*DiffScreen)(nil)
	_ core.Wrapper    = (*DiffScreen)(nil)
	_ core.DirLocator = (*DiffScreen)(nil)
)

// NewDiffScreen captures the diff and builds the screen; a capture failure is shown on the
// screen rather than flashed on the status line.
func NewDiffScreen(title, dir, path string, untracked bool) *DiffScreen {
	s := (&DiffScreen{pager: newPager(title, dir)}).wire()

	raw, err := repo.Diff(dir, path, untracked)
	switch {
	case err != nil:
		s.empty = "could not read the diff:\n\n" + err.Error()
	case strings.TrimSpace(raw) == "":
		// A file can be listed as changed and still diff to nothing — a mode change, or
		// a change that was staged and then reverted in the working tree.
		s.empty = "no textual changes to show"
	default:
		s.lines = parseDiff(raw)
	}
	return s
}

// CrumbLabel names what is diffed ("Diff" already comes from the picker); the short form
// drops directories.
func (s *DiffScreen) CrumbLabel(short bool) string {
	if short {
		return filepath.Base(s.title)
	}
	return s.title
}

// wire points the pager at this screen's render and layout label.
func (s *DiffScreen) wire() *DiffScreen {
	s.body, s.modeLabel = s.render, s.layoutName
	return s
}

// splittable reports whether side by side fits; narrower, columns are too narrow for code.
func (s *DiffScreen) splittable() bool { return s.width >= minSplitWidth }

// effectiveSplit reports whether side by side is rendered: auto and split both want it
// when it fits.
func (s *DiffScreen) effectiveSplit() bool {
	return s.layout != layoutUnified && s.splittable()
}

func (s *DiffScreen) render() string {
	if s.empty != "" {
		return metaStyle().Render(s.empty)
	}
	if s.effectiveSplit() {
		return renderSplit(s.lines, s.width, s.wrap)
	}
	return renderUnified(s.lines, s.width, s.wrap)
}

// layoutName is the title bar's layout label. Auto says nothing: it renders like one of
// the explicit modes, and naming that would make the s cycle look like it did nothing.
func (s *DiffScreen) layoutName() string {
	switch {
	case s.layout == layoutAuto:
		return ""
	case s.layout == layoutSplit && !s.splittable():
		// An explicit request the width can't honor. The title carries it as well as the
		// status line, because the status line is transient and this state isn't.
		return fmt.Sprintf("unified — side by side needs %d cols", minSplitWidth)
	case s.layout == layoutSplit:
		return "side by side"
	default:
		return "unified"
	}
}

func (s *DiffScreen) Update(sh *core.Shared, msg tea.Msg) (core.Screen, core.Action) {
	if msg, ok := msg.(tea.KeyMsg); ok {
		k := msg.String()
		switch {
		case core.MatchKey(k, core.Keys.Back):
			return s, core.Pop()
		case core.MatchKey(k, keys.Layout):
			s.layout = (s.layout + 1) % layoutCount
			s.rerender()
			// Only an explicit request gets an explanation. Auto reaching the same
			// conclusion is the mode working, not failing, so it stays quiet.
			if s.layout == layoutSplit && !s.splittable() {
				return s, core.SetStatus(fmt.Sprintf(
					"side by side needs a %d-column terminal — this one is %d", minSplitWidth, s.width))
			}
			return s, core.Action{}
		}
	}
	return s, s.scroll(msg)
}

func (s *DiffScreen) HelpView(sh *core.Shared) string { return s.help(sh, "layout", keys.Layout) }

// ---------- the file picker ----------

// DiffAction is a row's "d": push the repo's Git menu, then the diff picker, so esc from
// a diff lands on the menu with Commit (reading a diff precedes committing). crumb is
// passed to the Git menu.
func DiffAction(sh *core.Shared, r repo.Repo, crumb ...string) core.Action {
	return core.Seq(
		core.Push(RepoMenu(sh, r, crumb...)),
		core.Push(DiffMenu(sh, r)),
	)
}

// DiffMenu lists the changed files to diff one at a time; the first row is the whole
// repo's diff.
func DiffMenu(sh *core.Shared, r repo.Repo) *components.PickerScreen {
	return components.NewPicker(diffItems(r), components.PickerOpts{
		Title: r.Name,
		Crumb: "Diff",
		Dir:   r.Dir, // "t" opens a terminal at this repo from the Diff list (DirLocator)
		Refresh: func(sh *core.Shared, payload any) ([]list.Item, bool) {
			if !refreshesRepo(payload, r.Dir) {
				return nil, false
			}
			return diffItems(r), true
		},
	})
}

func diffItems(r repo.Repo) []list.Item {
	changes, err := repo.GitChanges(r.Dir)
	if err != nil {
		return components.EnsurePlaceholder(nil, "◫ "+r.Name, "could not read the working tree: "+err.Error())
	}
	// Untracked files aren't in `diff HEAD`, so they have no numstat; the rows fall back
	// to "new file" for them rather than showing a misleading 0/0.
	stats, statsErr := repo.DiffStats(r.Dir)
	statsFailed := statsErr != nil

	items := make([]list.Item, 0, len(changes)+1)
	if len(changes) > 0 {
		items = append(items, components.Item{
			Name: "◫ " + r.Name,
			Desc: allFilesDesc(changes, stats, statsFailed),
			// untracked=true: this row counts the untracked files in its description and
			// says "every change", so it has to ask for the diff that includes them.
			Pick: func(*core.Shared) core.Action {
				return core.Push(NewDiffScreen(r.Name, r.Dir, "", true))
			},
		})
	}

	for _, c := range changes {
		items = append(items, components.Item{
			Name: c.Code + "  " + c.Path,
			Desc: fileDesc(c, stats[c.Path], statsFailed),
			Pick: func(*core.Shared) core.Action {
				return core.Push(NewDiffScreen(c.Path, r.Dir, c.Path, c.Untracked()))
			},
		})
	}
	return components.EnsurePlaceholder(items, "working tree is clean", "nothing to diff")
}

// allFilesDesc totals the counts for the top row; after a failed numstat read it omits
// counts rather than claim none.
func allFilesDesc(changes []repo.GitChange, stats map[string]repo.DiffStat, statsFailed bool) string {
	if statsFailed {
		return fmt.Sprintf("every change in one page — %s", strutil.Count(len(changes), "file"))
	}
	var added, deleted int
	for _, st := range stats {
		added += st.Added
		deleted += st.Deleted
	}
	return fmt.Sprintf("every change in one page — %s, %s", strutil.Count(len(changes), "file"), counts(added, deleted))
}

func fileDesc(c repo.GitChange, st repo.DiffStat, statsFailed bool) string {
	switch {
	case c.Untracked():
		return "new file — not tracked yet"
	case statsFailed:
		// A failed numstat read surfaces here as a zero DiffStat; name the failure rather
		// than misdescribing real changes as "no textual changes".
		return "could not read the line counts"
	case st.Binary:
		return "binary file"
	case st.Added == 0 && st.Deleted == 0:
		return "no textual changes"
	default:
		return counts(st.Added, st.Deleted)
	}
}

func counts(added, deleted int) string {
	if added == 0 && deleted == 0 {
		return "no line changes"
	}
	return fmt.Sprintf("+%d  -%d", added, deleted)
}
