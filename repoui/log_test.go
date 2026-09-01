package repoui

import (
	"strings"
	"testing"

	"github.com/brohd11/gitstack/repo"

	"charm.land/bubbles/v2/viewport"
	"github.com/charmbracelet/x/ansi"
)

// The two log modes. NewLogScreen shells out to git, so these build the screen directly
// from a fixture — the mode logic is about what each mode draws, not about where the
// commits came from.

const logWidth = 100

var sampleCommits = []repo.Commit{{
	Hash:    "aaaa111122223333444455556666777788889999",
	Short:   "aaaa111",
	Parents: []string{"bbbb222233334444555566667777888899990000"},
	Refs: []repo.Ref{
		{Name: "main", Kind: repo.RefHead},
		{Name: "v1.0.0", Kind: repo.RefTag},
		{Name: "origin/main", Kind: repo.RefRemote},
	},
	Author:  "Ada",
	Email:   "ada@example.com",
	Date:    "Fri Aug 15 10:00:00 2026 -0700",
	Subject: "add the parser",
}, {
	Hash:  "bbbb222233334444555566667777888899990000",
	Short: "bbbb222",
	Parents: []string{
		"cccc333344445555666677778888999900001111",
		"dddd444455556666777788889999000011112222",
	},
	Author:  "Ada",
	Email:   "ada@example.com",
	Date:    "Thu Aug 14 09:00:00 2026 -0700",
	Subject: "Merge branch 'feature/parser'",
	Body:    "The old one re-read the buffer on every token.\n\nFixes #12",
}}

func logScreen(t *testing.T, truncated bool) *LogScreen {
	t.Helper()
	s := &LogScreen{
		title:     "gitstack",
		dir:       "/tmp/gitstack",
		commits:   sampleCommits,
		truncated: truncated,
		vp:        viewport.New(),
		width:     -1,
	}
	s.SetSize(nil, logWidth, 40)
	return s
}

func pressMode(s *LogScreen) {
	s.Update(nil, keyMsg("m"))
}

// The default mode is the whole point of the screen: opening it gives you as many commits
// as the terminal has rows, not one commit's worth of headers.
func TestLogDefaultModeIsOneline(t *testing.T) {
	s := logScreen(t, false)
	if s.mode != modeOneline {
		t.Fatalf("a new LogScreen should start in one line (the zero value), got %v", s.mode)
	}

	view := ansi.Strip(s.View(nil))
	if !strings.Contains(view, "aaaa111 (HEAD -> main, tag: v1.0.0, origin/main) add the parser") {
		t.Errorf("one line should render sha, decorations and subject on one row:\n%s", view)
	}
	if strings.Contains(view, "Author:") || strings.Contains(view, s.commits[0].Hash) {
		t.Errorf("one line should show neither the headers nor the full hash:\n%s", view)
	}
}

func TestLogModeKeyTogglesToStandardAndBack(t *testing.T) {
	s := logScreen(t, false)

	pressMode(s)
	if s.mode != modeStandard {
		t.Fatalf("mode after one press = %v, want standard", s.mode)
	}
	view := ansi.Strip(s.View(nil))
	for _, want := range []string{
		"commit " + sampleCommits[0].Hash,
		"(HEAD -> main, tag: v1.0.0, origin/main)",
		"Author: Ada <ada@example.com>",
		"Date:   Fri Aug 15 10:00:00 2026 -0700",
		"    add the parser",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("standard should contain %q:\n%s", want, view)
		}
	}

	pressMode(s)
	if s.mode != modeOneline {
		t.Fatalf("mode after two presses = %v, want one line again", s.mode)
	}
	if strings.Contains(ansi.Strip(s.View(nil)), "Author:") {
		t.Error("toggling back should return to the one-line rows")
	}
}

// The title bar names both modes — the toggle is otherwise invisible until you notice the
// rows changed shape.
func TestLogTitleBarNamesTheMode(t *testing.T) {
	s := logScreen(t, false)
	if got := s.titleBar(); got != "gitstack · one line" {
		t.Errorf("titleBar = %q", got)
	}
	pressMode(s)
	if got := s.titleBar(); got != "gitstack · standard" {
		t.Errorf("titleBar after toggle = %q", got)
	}
	s.ToggleWrap()
	if got := s.titleBar(); got != "gitstack · standard · wrap" {
		t.Errorf("titleBar wrapped = %q", got)
	}
}

// Standard mode renders a merge's parents, abbreviated the way git does; one line doesn't
// mention them at all.
func TestLogStandardShowsMergeParents(t *testing.T) {
	s := logScreen(t, false)
	pressMode(s)
	// Scrolled to the bottom because the second commit's block is below the first's.
	s.vp.GotoBottom()
	if view := ansi.Strip(s.View(nil)); !strings.Contains(view, "Merge: cccc333 dddd444") {
		t.Errorf("standard should abbreviate a merge's parents:\n%s", view)
	}
}

// The body is git's own four-space indent, including the blank line inside it.
func TestLogStandardIndentsTheBody(t *testing.T) {
	s := logScreen(t, false)
	pressMode(s)
	s.vp.GotoBottom()
	view := ansi.Strip(s.View(nil))
	for _, want := range []string{
		"    The old one re-read the buffer on every token.",
		"    Fixes #12",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("standard should indent %q:\n%s", want, view)
		}
	}
}

// A capped history looks exactly like a short one unless the cap is named.
func TestLogTruncationNote(t *testing.T) {
	s := logScreen(t, true)
	s.vp.GotoBottom()
	if view := ansi.Strip(s.View(nil)); !strings.Contains(view, "… showing the 2 most recent commits") {
		t.Errorf("a truncated log should say so:\n%s", view)
	}
	if view := ansi.Strip(logScreen(t, false).View(nil)); strings.Contains(view, "most recent commits") {
		t.Errorf("an untruncated log should not:\n%s", view)
	}
}

// Toggling is a question about the commits you're already looking at, so it must not throw
// away your place. The fixture is long enough that both modes overflow the viewport, which
// is the case the offset has to survive — standard renders a commit as several rows where
// one line renders it as one, so the two disagree sharply on how far down the content the
// same offset lands.
func TestLogTogglePreservesScroll(t *testing.T) {
	s := &LogScreen{
		title:   "gitstack",
		commits: repeatCommits(30),
		vp:      viewport.New(),
		width:   -1,
	}
	s.SetSize(nil, logWidth, 8)
	s.vp.SetYOffset(3)

	pressMode(s) // standard
	if got := s.vp.YOffset(); got != 3 {
		t.Fatalf("YOffset after toggling to standard = %d, want 3", got)
	}
	pressMode(s) // and back
	if got := s.vp.YOffset(); got != 3 {
		t.Errorf("YOffset after toggling back = %d, want 3 — the toggle scrolled the view", got)
	}
}

// repeatCommits is the fixture, long enough to scroll in either mode.
func repeatCommits(n int) []repo.Commit {
	out := make([]repo.Commit, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, sampleCommits[i%len(sampleCommits)])
	}
	return out
}

// A capture failure opens the screen and explains itself rather than rendering an empty log,
// which would read as a repo with no history.
func TestLogEmptyStateRenders(t *testing.T) {
	s := &LogScreen{
		title: "gitstack",
		empty: "this repo has no commits yet",
		vp:    viewport.New(),
		width: -1,
	}
	s.SetSize(nil, logWidth, 20)
	if view := ansi.Strip(s.View(nil)); !strings.Contains(view, "this repo has no commits yet") {
		t.Errorf("the empty state should be rendered in place of the log:\n%s", view)
	}
}
