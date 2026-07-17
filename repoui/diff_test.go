package repoui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// The layout modes. NewDiffScreen shells out to git, so these build the screen directly
// from the parsed fixture in diffrender_test.go — the mode logic is about width and state,
// not about where the bytes came from.

// wide is comfortably over minSplitWidth; narrow is comfortably under it.
const (
	wideWidth   = 140
	narrowWidth = 90
)

func diffScreen(t *testing.T, width int) *DiffScreen {
	t.Helper()
	s := &DiffScreen{
		title: "a.txt",
		lines: parseDiff(sampleDiff),
		vp:    viewport.New(0, 0),
		width: -1,
	}
	s.SetSize(nil, width, 20)
	return s
}

// pressLayout sends the layout key and reports whether the screen raised a status line.
// core keeps its status message type unexported, so a test outside that package can see
// that a status was set but not what it says — which is exactly the distinction these
// tests care about: quiet or not.
func pressLayout(t *testing.T, s *DiffScreen) (warned bool) {
	t.Helper()
	_, act := s.Update(nil, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	return act.Msg != nil
}

// TestLayoutDefaultIsAuto pins the whole point of the mode: opening a diff on a wide
// terminal gives side by side without a keypress, and on a narrow one gives unified —
// same state, decided by width.
func TestLayoutDefaultIsAuto(t *testing.T) {
	if got := diffScreen(t, wideWidth).layout; got != layoutAuto {
		t.Fatalf("a new DiffScreen should start in auto (the zero value), got %v", got)
	}

	wide := diffScreen(t, wideWidth)
	if !wide.effectiveSplit() {
		t.Error("auto should render side by side on a wide terminal")
	}
	if !strings.Contains(ansi.Strip(wide.View(nil)), strings.TrimSpace(splitSep)) {
		t.Errorf("auto at %d cols should actually render the split columns:\n%s", wideWidth, wide.View(nil))
	}

	narrow := diffScreen(t, narrowWidth)
	if narrow.effectiveSplit() {
		t.Error("auto should render unified on a narrow terminal")
	}
}

// TestLayoutAutoIsUnlabeled: auto renders the same thing as one of the explicit modes, so
// it stays out of the title bar — otherwise it would be indistinguishable from having
// chosen that mode, and pressing s would look like it did nothing.
func TestLayoutAutoIsUnlabeled(t *testing.T) {
	for _, width := range []int{wideWidth, narrowWidth} {
		s := diffScreen(t, width)
		if got := s.titleBar(); got != "a.txt" {
			t.Errorf("auto at %d cols should show only the filename, got %q", width, got)
		}
	}

	// Every explicit mode names itself, so leaving auto always adds a label.
	s := diffScreen(t, wideWidth)
	pressLayout(t, s)
	if got := s.titleBar(); !strings.Contains(got, "unified") {
		t.Errorf("explicit unified should name itself, got %q", got)
	}
	pressLayout(t, s)
	if got := s.titleBar(); !strings.Contains(got, "side by side") {
		t.Errorf("explicit side by side should name itself, got %q", got)
	}
}

// TestLayoutCycle walks the order the help bar promises.
func TestLayoutCycle(t *testing.T) {
	s := diffScreen(t, wideWidth)
	want := []layout{layoutUnified, layoutSplit, layoutAuto}
	for i, w := range want {
		pressLayout(t, s)
		if s.layout != w {
			t.Fatalf("press %d: got layout %v, want %v", i+1, s.layout, w)
		}
	}
}

// TestLayoutAutoNeverWarns is the behavior this mode was added for. Auto reaching "unified"
// on a narrow terminal is the mode working, not failing — an explicit request that can't be
// honored is the only thing worth interrupting for.
func TestLayoutAutoNeverWarns(t *testing.T) {
	s := diffScreen(t, narrowWidth)

	// auto -> unified: nothing to say, the request was honored.
	if pressLayout(t, s) {
		t.Error("switching to unified should be silent")
	}
	// unified -> side by side: can't fit, so say so.
	if !pressLayout(t, s) {
		t.Error("an explicit side by side that can't fit should explain itself")
	}
	if got := s.titleBar(); !strings.Contains(got, "needs") {
		t.Errorf("the title should carry the fallback too — the status line is transient: %q", got)
	}
	// side by side -> auto: back to deciding by width, and silent about it.
	if pressLayout(t, s) {
		t.Error("returning to auto should be silent")
	}
	if s.layout != layoutAuto {
		t.Fatalf("expected to be back in auto, got %v", s.layout)
	}
	if got := s.titleBar(); got != "a.txt" {
		t.Errorf("auto should drop the label on the way back, got %q", got)
	}
}

// TestLayoutAutoFollowsResize: auto is a standing decision, not one made when the screen
// opened. Widening the terminal past minSplitWidth must flip the layout on its own.
func TestLayoutAutoFollowsResize(t *testing.T) {
	s := diffScreen(t, narrowWidth)
	if s.effectiveSplit() {
		t.Fatal("precondition: auto should be unified at the narrow width")
	}

	s.SetSize(nil, wideWidth, 20)
	if !s.effectiveSplit() {
		t.Error("auto should switch to side by side when the terminal is widened")
	}
	if !strings.Contains(ansi.Strip(s.View(nil)), strings.TrimSpace(splitSep)) {
		t.Error("the re-render after a resize should actually produce the split columns")
	}

	s.SetSize(nil, narrowWidth, 20)
	if s.effectiveSplit() {
		t.Error("auto should fall back to unified when the terminal is narrowed again")
	}
}

// TestLayoutWrapComposes: wrap is orthogonal to the layout, and reads after it.
func TestLayoutWrapComposes(t *testing.T) {
	s := diffScreen(t, wideWidth)
	s.ToggleWrap()
	if got := s.titleBar(); got != "a.txt · wrap" {
		t.Errorf("auto + wrap should read %q, got %q", "a.txt · wrap", got)
	}

	pressLayout(t, s) // -> unified
	if got := s.titleBar(); got != "a.txt · unified · wrap" {
		t.Errorf("unified + wrap should read %q, got %q", "a.txt · unified · wrap", got)
	}
}
