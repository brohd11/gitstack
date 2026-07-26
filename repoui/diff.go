package repoui

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/brohd11/bubblestack/components"
	"github.com/brohd11/bubblestack/core"
	"github.com/brohd11/gitstack/repo"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// The Diff view: what the Status row can't answer. `git status` names the files that
// changed; this shows what changed inside them, which is the question you actually have
// on the way to a commit.
//
// It reads the working tree against HEAD — staged and unstaged together — so it shows the
// same set of changes the commit flow would include, rather than a subset that depends on
// what has been `add`ed. Like the rest of this menu it is read-only: there is no staging,
// no hunk selection, no editing. Those are decisions, and decisions belong in a terminal.

// keys are the Diff view's own bindings, the ones that aren't part of bubblestack's
// framework keymap (core.Keys) — the same arrangement repoview uses for its screen-level
// keys. Matched with core.MatchKey like every other dispatch site, never as a raw keycode.
//
// "s" is free in core.Keys, and wrap is deliberately NOT here: it's core.Keys.Wrap ("w"),
// because a diff wrapping its long lines is the same gesture as the output pane wrapping
// its long lines, and it should be the same key. DiffScreen implements core.Wrapper to
// receive it.
var keys = struct {
	Layout key.Binding
}{
	Layout: key.NewBinding(key.WithKeys("s"), key.WithHelp("s", "layout")),
}

// layout is how the diff is arranged. Auto is the zero value, and therefore the default,
// without NewDiffScreen having to say so.
//
// Auto and layoutSplit render identically whenever the width allows; they differ in what
// happens when it doesn't. Auto quietly uses the layout that fits, because it never claimed
// side by side in the first place — it claimed "the right one". An explicit layoutSplit is
// a request, so failing to honor it has to be explained, or it reads as a broken key.
type layout int

const (
	layoutAuto layout = iota // the width decides, silently
	layoutUnified
	layoutSplit
)

// layoutCount is the number of modes the s key cycles through.
const layoutCount = 3

// DiffScreen is a scrollable diff for one file, or for a whole repo's worth of them. The
// diff is captured once when the screen opens and parsed once; the layout toggles re-run
// only the render, not git.
//
// It deliberately does not reuse components.DocScreen, which owns exactly this viewport
// plumbing. DocScreen renders a caller's Render(width) closure and re-runs it on width
// changes only — but this screen's content also depends on the layout mode and the wrap
// flag, which it must re-render on, and the Wrap key only reaches a screen that implements
// core.Wrapper itself (the router type-asserts the top screen). Threading both of those
// back out through DocScreen's closure would mean two new escape hatches on a shared
// component to save one viewport field here.
type DiffScreen struct {
	title string
	dir   string // the repo the diff is from; enables the global Terminal key (DirLocator)
	lines []diffLine
	empty string // set when there is nothing to show; rendered in place of the diff

	layout layout // s — cycles auto → unified → side by side
	wrap   bool   // w — fold long lines rather than truncate them (core.Wrapper)

	vp    viewport.Model
	width int // last laid-out terminal width; -1 until the first SetSize
}

var (
	_ core.Crumber    = (*DiffScreen)(nil)
	_ core.Wrapper    = (*DiffScreen)(nil)
	_ core.DirLocator = (*DiffScreen)(nil)
)

// LocateDir reports the repo the diff is from, so the global Terminal key opens a terminal
// there while viewing the diff. Empty dir ⇒ no locator (the key falls through).
func (s *DiffScreen) LocateDir() (string, bool) { return s.dir, s.dir != "" }

// NewDiffScreen captures the diff and builds the screen. A capture failure isn't fatal —
// the screen opens and says what went wrong, which is more use than a status line flashing
// over the menu you just left.
func NewDiffScreen(title, dir, path string, untracked bool) *DiffScreen {
	s := &DiffScreen{
		title: title,
		dir:   dir,
		vp:    viewport.New(0, 0),
		width: -1,
	}

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

func (s *DiffScreen) Init(*core.Shared) tea.Cmd { return nil }

// CrumbLabel names what is being diffed, not the feature: the picker above already
// contributes "Diff", so returning that here too would read "Git › Diff › Diff". The short
// form drops the directories, which is what a long trail needs to give up first.
func (s *DiffScreen) CrumbLabel(short bool) string {
	if short {
		return filepath.Base(s.title)
	}
	return s.title
}

// ToggleWrap folds or truncates long lines (core.Wrapper — the router's w key).
func (s *DiffScreen) ToggleWrap() {
	s.wrap = !s.wrap
	s.rerender()
}

func (s *DiffScreen) Wrapped() bool { return s.wrap }

// splittable reports whether the side-by-side layout fits. Below minSplitWidth each column
// would be too narrow to read code in, so the screen renders unified instead — rather than
// honoring a layout into something unusable.
func (s *DiffScreen) splittable() bool { return s.width >= minSplitWidth }

// effectiveSplit reports whether side by side is what actually gets rendered. Auto and an
// explicit layoutSplit agree here — both want side by side and both defer to the width;
// only layoutUnified rules it out outright.
func (s *DiffScreen) effectiveSplit() bool {
	return s.layout != layoutUnified && s.splittable()
}

func (s *DiffScreen) SetSize(_ *core.Shared, width, bodyHeight int) {
	h := bodyHeight - lipgloss.Height(core.RenderTitleBar(s.titleBar()))
	if h < 1 {
		h = 1
	}
	s.vp.Width = width
	s.vp.Height = h
	if width == s.width {
		return // height-only change (the output pane opening): the content is unaffected
	}
	s.width = width
	s.rerender()
}

// rerender rebuilds the body at the current width, keeping the scroll position: a layout
// or wrap toggle is a question about the lines you are already looking at, so jumping back
// to the top of the file would lose your place every time you pressed s.
func (s *DiffScreen) rerender() {
	if s.width < 0 {
		return // not laid out yet; SetSize will render
	}
	// SetYOffset re-clamps against the new content's length, which matters because the
	// layouts differ in height: side by side puts an edit's two halves on one row, so
	// switching to it from a position near the end of a long unified diff would otherwise
	// leave the offset past the last line.
	y := s.vp.YOffset
	s.vp.SetContent(s.body())
	s.vp.SetYOffset(y)
}

func (s *DiffScreen) body() string {
	if s.empty != "" {
		return metaStyle().Render(s.empty)
	}
	if s.effectiveSplit() {
		return renderSplit(s.lines, s.width, s.wrap)
	}
	return renderUnified(s.lines, s.width, s.wrap)
}

// titleBar names the file, then whatever is worth saying about how it's being shown.
func (s *DiffScreen) titleBar() string {
	parts := []string{s.title}
	if mode := s.modeLabel(); mode != "" {
		parts = append(parts, mode)
	}
	if s.wrap {
		parts = append(parts, "wrap")
	}
	return strings.Join(parts, " · ")
}

// modeLabel is what the title bar says about the layout — and auto deliberately says
// nothing. Auto renders the same thing as one of the two explicit modes, so if it named
// that layout it would be indistinguishable from having chosen it, and pressing s would
// look like it did nothing. Naming only the explicit modes means leaving auto always adds
// a label and returning to it always drops one, which is what makes the cycle legible —
// and it keeps the common case (the default) reading as just the filename.
func (s *DiffScreen) modeLabel() string {
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
	var cmd tea.Cmd
	s.vp, cmd = s.vp.Update(msg)
	return s, core.Async(cmd)
}

func (s *DiffScreen) View(*core.Shared) string {
	return core.WithTitle(s.titleBar(), s.vp.View())
}

func (s *DiffScreen) HelpView(sh *core.Shared) string {
	return sh.BindingHelp([]key.Binding{
		core.Hint("scroll", core.Keys.Up, core.Keys.Down),
		core.Hint("layout", keys.Layout),
		core.Hint("wrap", core.Keys.Wrap),
		core.Hint("back", core.Keys.Back),
	})
}

// ---------- the file picker ----------

// DiffAction is what a row's "d" does: push the per-repo Git menu, then the diff picker on
// top of it. The Git menu underneath is the hub with Commit, so esc from a diff lands on the
// action the diff was read to inform — reading a diff is the step before committing. The
// trail reads "… › Git › Diff", and since esc is a one-level pop, the alternative (pushing
// the picker alone) would make you esc to the list and re-enter via the Git key to reach the
// action the diff just argued for.
func DiffAction(sh *core.Shared, r repo.Repo) core.Action {
	return core.Seq(
		core.Push(RepoMenu(sh, r)),
		core.Push(DiffMenu(sh, r)),
	)
}

// DiffMenu lists what changed, so a diff can be read one file at a time instead of as one
// long scroll. The first row is the whole repo's diff — the common case when the change is
// small, and the one that shows how the files relate.
func DiffMenu(sh *core.Shared, r repo.Repo) *components.PickerScreen {
	return components.NewPicker(diffItems(r), components.PickerOpts{
		Title: r.Name,
		Crumb: "Diff",
		Dir:   r.Dir, // "t" opens a terminal at this repo from the Diff list (DirLocator)
		Refresh: func(sh *core.Shared, payload any) ([]list.Item, bool) {
			if _, ok := payload.(RefreshMsg); !ok {
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
	stats, _ := repo.DiffStats(r.Dir)

	items := make([]list.Item, 0, len(changes)+1)
	if len(changes) > 0 {
		items = append(items, components.Item{
			Name: "◫ " + r.Name,
			Desc: allFilesDesc(changes, stats),
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
			Desc: fileDesc(c, stats[c.Path]),
			Pick: func(*core.Shared) core.Action {
				return core.Push(NewDiffScreen(c.Path, r.Dir, c.Path, c.Untracked()))
			},
		})
	}
	return components.EnsurePlaceholder(items, "working tree is clean", "nothing to diff")
}

// allFilesDesc totals the counts across every file, so the top row reports the size of the
// change before you open it.
func allFilesDesc(changes []repo.GitChange, stats map[string]repo.DiffStat) string {
	var added, deleted int
	for _, st := range stats {
		added += st.Added
		deleted += st.Deleted
	}
	return fmt.Sprintf("every change in one page — %s, %s", plural(len(changes), "file"), counts(added, deleted))
}

func fileDesc(c repo.GitChange, st repo.DiffStat) string {
	switch {
	case c.Untracked():
		return "new file — not tracked yet"
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

func plural(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
