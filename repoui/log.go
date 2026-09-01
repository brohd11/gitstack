package repoui

import (
	"errors"
	"strings"

	"github.com/brohd11/bubblestack/core"
	"github.com/brohd11/gitstack/repo"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// The Log view: what the rest of this menu can't answer. Status and Diff describe the
// working tree — what has changed and hasn't been committed — and the ahead/behind counts
// describe how far this checkout has drifted from origin. None of them say what was actually
// done here, which is the question you have before writing the next commit message, or when
// working out which release a fix landed in.
//
// Like the rest of the menu it is read-only: no checkout, no revert, no cherry-pick. Those
// are decisions, and decisions belong in a terminal.

// logKeys are the Log view's own bindings. Separate from diff.go's `keys`, which is
// documented there as the Diff view's own — one package-level bag shared by two unrelated
// screens would make either screen's keys look like the package's.
//
// "m" is free in core.Keys and in the host apps' screen keys, and a bare key is safe here
// for the same reason DiffScreen's bare "s" is: nothing on this screen takes text input.
var logKeys = struct {
	Mode key.Binding
}{
	Mode: key.NewBinding(key.WithKeys("m"), key.WithHelp("m", "mode")),
}

// logMode is how a commit is drawn. One line is the zero value, and therefore the default
// without NewLogScreen having to say so: it's the mode that answers the question you opened
// the screen with, and standard is the one you toggle into once you've found the commit.
type logMode int

const (
	modeOneline logMode = iota
	modeStandard
)

// logModeCount is the number of modes the mode key cycles through.
const logModeCount = 2

// LogScreen is a scrollable commit history. The log is captured once when the screen opens
// and parsed once; the mode toggle re-runs only the render, not git.
//
// It shares DiffScreen's shape rather than components.DocScreen's for the same two reasons
// DiffScreen gives: DocScreen re-runs its Render closure on width changes only, and this
// screen's content also depends on the mode and the wrap flag, and the Wrap key only reaches
// a screen that implements core.Wrapper itself.
type LogScreen struct {
	title   string
	dir     string // the repo the log is from; enables the global Terminal key (DirLocator)
	commits []repo.Commit
	// truncated says the capture hit LogLimit, so the history shown is a suffix of the
	// real one. Rendered as a note under the last row — see truncNote.
	truncated bool
	empty     string // set when there is nothing to show; rendered in place of the log

	mode logMode // m — one line ⇄ standard
	wrap bool    // w — fold long lines rather than truncate them (core.Wrapper)

	vp    viewport.Model
	width int // last laid-out terminal width; -1 until the first SetSize
}

var (
	_ core.Crumber    = (*LogScreen)(nil)
	_ core.Wrapper    = (*LogScreen)(nil)
	_ core.DirLocator = (*LogScreen)(nil)
)

// LocateDir reports the repo the log is from, so the global Terminal key opens a terminal
// there while reading it — the log is where you find the commit you then go act on.
func (s *LogScreen) LocateDir() (string, bool) { return s.dir, s.dir != "" }

// NewLogScreen captures the log and builds the screen. A capture failure isn't fatal: the
// screen opens and says what went wrong, which is more use than a status line flashing over
// the menu you just left.
func NewLogScreen(r repo.Repo) *LogScreen {
	s := &LogScreen{
		title: r.Name,
		dir:   r.Dir,
		vp:    viewport.New(),
		width: -1,
	}

	commits, err := repo.Log(r.Dir, repo.LogLimit)
	switch {
	case errors.Is(err, repo.ErrNoCommits):
		// Not a failure: there is nothing to read, and prefixing it with "could not read
		// the log" would describe an empty history as a broken one.
		s.empty = err.Error()
	case err != nil:
		s.empty = "could not read the log:\n\n" + err.Error()
	case len(commits) == 0:
		s.empty = "no commits to show"
	default:
		s.commits = commits
		s.truncated = len(commits) >= repo.LogLimit
	}
	return s
}

func (s *LogScreen) Init(*core.Shared) tea.Cmd { return nil }

// CrumbLabel names the feature, in both forms: unlike the Diff view there's no picker
// between the menu and this screen contributing the word, so the trail reads
// "… › <repo> › Log" only if it says it here.
func (s *LogScreen) CrumbLabel(bool) string { return "Log" }

// ToggleWrap folds or truncates long lines (core.Wrapper — the router's w key).
func (s *LogScreen) ToggleWrap() {
	s.wrap = !s.wrap
	s.rerender()
}

func (s *LogScreen) Wrapped() bool { return s.wrap }

func (s *LogScreen) SetSize(_ *core.Shared, width, bodyHeight int) {
	h := bodyHeight - lipgloss.Height(core.RenderTitleBar(s.titleBar()))
	if h < 1 {
		h = 1
	}
	s.vp.SetWidth(width)
	s.vp.SetHeight(h)
	if width == s.width {
		return // height-only change (the output pane opening): the content is unaffected
	}
	s.width = width
	s.rerender()
}

// rerender rebuilds the body at the current width, keeping the scroll position: a mode or
// wrap toggle is a question about the commits you are already looking at, so jumping back to
// HEAD would lose your place every time you pressed m.
func (s *LogScreen) rerender() {
	if s.width < 0 {
		return // not laid out yet; SetSize will render
	}
	// SetYOffset re-clamps against the new content's length, which matters more here than
	// in the diff: standard renders each commit as six-odd rows where one line renders it as
	// one, so toggling back from a position deep in a standard log would otherwise leave the
	// offset far past the last row.
	y := s.vp.YOffset()
	s.vp.SetContent(s.body())
	s.vp.SetYOffset(y)
}

func (s *LogScreen) body() string {
	if s.empty != "" {
		return metaStyle().Render(s.empty)
	}
	if s.mode == modeStandard {
		return renderStandard(s.commits, s.truncated, s.width, s.wrap)
	}
	return renderOneline(s.commits, s.truncated, s.width, s.wrap)
}

// titleBar names the repo, then how the log is being shown. Both modes are named — unlike
// the diff's layouts, where the default is deliberately silent because it resolves to one of
// the explicit modes and naming it would make them indistinguishable. These two always
// render differently, so labeling both is what makes the toggle legible.
func (s *LogScreen) titleBar() string {
	parts := []string{s.title, s.modeLabel()}
	if s.wrap {
		parts = append(parts, "wrap")
	}
	return strings.Join(parts, " · ")
}

func (s *LogScreen) modeLabel() string {
	if s.mode == modeStandard {
		return "standard"
	}
	return "one line"
}

func (s *LogScreen) Update(sh *core.Shared, msg tea.Msg) (core.Screen, core.Action) {
	if msg, ok := msg.(tea.KeyMsg); ok {
		k := msg.String()
		switch {
		case core.MatchKey(k, core.Keys.Back):
			return s, core.Pop()
		case core.MatchKey(k, logKeys.Mode):
			s.mode = (s.mode + 1) % logModeCount
			s.rerender()
			return s, core.Action{}
		}
	}
	var cmd tea.Cmd
	s.vp, cmd = s.vp.Update(msg)
	return s, core.Async(cmd)
}

func (s *LogScreen) View(*core.Shared) string {
	return core.WithTitle(s.titleBar(), s.vp.View())
}

func (s *LogScreen) HelpView(sh *core.Shared) string {
	binds := []key.Binding{
		core.Hint("scroll", core.Keys.Up, core.Keys.Down),
		core.Hint("mode", logKeys.Mode),
		core.Hint("wrap", core.Keys.Wrap),
	}
	// The log carries the repo it's from (DirLocator), so the terminal/open-dir keys fire
	// here, but they stay off the bar — it is kept sparse (see core.ShortHelp).
	binds = append(binds, core.Hint("back", core.Keys.Back))
	return sh.BindingHelp(binds)
}
