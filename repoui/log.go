package repoui

import (
	"errors"

	"github.com/brohd11/bubblestack/core"
	"github.com/brohd11/gitstack/repo"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
)

// The Log view: what was committed here, which status and diff do not show. Read-only.

// logKeys are the Log view's own bindings. "m" is free, and a bare key is safe because
// nothing here takes text input.
var logKeys = struct {
	Mode key.Binding
}{
	Mode: key.NewBinding(key.WithKeys("m"), key.WithHelp("m", "mode")),
}

// logMode is how a commit is drawn; one line is the default, standard the detailed view.
type logMode int

const (
	modeOneline logMode = iota
	modeStandard
)

// logModeCount is the number of modes the mode key cycles through.
const logModeCount = 2

// LogScreen is a scrollable commit history, captured and parsed once when the screen
// opens; the mode toggle re-runs only the render.
type LogScreen struct {
	pager
	commits []repo.Commit
	// truncated says the capture hit LogLimit, so the history shown is a suffix of the
	// real one (see truncNote).
	truncated bool
	empty     string // set when there is nothing to show; rendered in place of the log
	mode      logMode
}

var (
	_ core.Crumber    = (*LogScreen)(nil)
	_ core.Wrapper    = (*LogScreen)(nil)
	_ core.DirLocator = (*LogScreen)(nil)
)

// NewLogScreen captures the log and builds the screen; a capture failure is shown on the
// screen.
func NewLogScreen(r repo.Repo) *LogScreen {
	s := (&LogScreen{pager: newPager(r.Name, r.Dir)}).wire()

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

// CrumbLabel is "Log": no picker above contributes the word.
func (s *LogScreen) CrumbLabel(bool) string { return "Log" }

// wire points the pager at this screen's render and mode label.
func (s *LogScreen) wire() *LogScreen {
	s.body, s.modeLabel = s.render, s.modeName
	return s
}

func (s *LogScreen) render() string {
	if s.empty != "" {
		return metaStyle().Render(s.empty)
	}
	if s.mode == modeStandard {
		return renderStandard(s.commits, s.truncated, s.width, s.wrap)
	}
	return renderOneline(s.commits, s.truncated, s.width, s.wrap)
}

// modeName labels both modes: they always render differently, so naming both is what
// makes the toggle legible.
func (s *LogScreen) modeName() string {
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
	return s, s.scroll(msg)
}

func (s *LogScreen) HelpView(sh *core.Shared) string { return s.help(sh, "mode", logKeys.Mode) }
