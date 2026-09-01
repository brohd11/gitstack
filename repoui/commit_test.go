package repoui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/brohd11/bubblestack/core"
	"github.com/brohd11/gitstack/repo"
)

func rp(name, branch string) repo.Repo {
	return repo.Repo{Name: name, Branch: branch}
}

var sampleChanges = []repo.GitChange{
	{Code: " M", Path: "timeline.gd"},
	{Code: " D", Path: "old.gd"},
	{Code: "??", Path: "new_event.gd"},
}

func TestCommitBodyTrackedOnly(t *testing.T) {
	body := commitBody(rp("dialogic", "main"), sampleChanges, "fix timeline crash", false)

	if !strings.Contains(body, "Commit 2 file(s) in dialogic on main:") {
		t.Errorf("body should count only the 2 tracked files and name the branch:\n%s", body)
	}
	for _, want := range []string{"timeline.gd", "old.gd", "message: fix timeline crash"} {
		if !strings.Contains(body, want) {
			t.Errorf("body is missing %q:\n%s", want, body)
		}
	}
	// The whole point of the -a mode's confirm: the new file is named as excluded, not
	// quietly dropped.
	if !strings.Contains(body, "Not included") || !strings.Contains(body, "new_event.gd") {
		t.Errorf("body must name the untracked file it will leave out:\n%s", body)
	}
}

func TestCommitBodyStageAll(t *testing.T) {
	body := commitBody(rp("dialogic", "main"), sampleChanges, "everything", true)

	if !strings.Contains(body, "Commit 3 file(s)") {
		t.Errorf("stageAll should count the untracked file too:\n%s", body)
	}
	if !strings.Contains(body, "new_event.gd") {
		t.Errorf("stageAll should list the untracked file as included:\n%s", body)
	}
	if strings.Contains(body, "Not included") {
		t.Errorf("stageAll excludes nothing; there should be no exclusion notice:\n%s", body)
	}
}

// TestCommitBodyCaps guards the reason the list is capped at all: a DialogScreen neither
// scrolls nor clips, so an uncapped list would shove the chrome off the terminal.
func TestCommitBodyCaps(t *testing.T) {
	var many []repo.GitChange
	for i := 0; i < 25; i++ {
		many = append(many, repo.GitChange{Code: " M", Path: fmt.Sprintf("file%02d.gd", i)})
	}
	body := commitBody(rp("big", "main"), many, "sweeping change", false)

	if n := strings.Count(body, ".gd"); n != maxCommitList {
		t.Errorf("body lists %d files, want it capped at %d:\n%s", n, maxCommitList, body)
	}
	if !strings.Contains(body, "… and 15 more") {
		t.Errorf("body should say how many it left out:\n%s", body)
	}
	if !strings.Contains(body, "Commit 25 file(s)") {
		t.Errorf("the count must still be the true total, not the shown subset:\n%s", body)
	}
}

func TestCommitBodyCleanNoBranch(t *testing.T) {
	// An unknown branch just omits the " on <branch>" clause rather than printing an empty one.
	body := commitBody(rp("addon", ""), sampleChanges[:1], "msg", false)
	if strings.Contains(body, " on :") || strings.Contains(body, "on \n") {
		t.Errorf("an unknown branch should be omitted, not printed empty:\n%s", body)
	}
	if !strings.Contains(body, "Commit 1 file(s) in addon:") {
		t.Errorf("unexpected header:\n%s", body)
	}
}

// TestCommitFormMessageRoundTrips drives the real form at the real call site. The message
// is the workspace's only TextAreaField, and FormScreen.Value used to assert on *TextField
// concretely — so without the widened lookup a typed message would read back as "" here
// and OnSubmit would reject every commit as missing a message.
func TestCommitFormMessageRoundTrips(t *testing.T) {
	f, _ := newCommitForm(rp("dialogic", "main"))
	sh := core.NewShared(nil)
	f.Init(sh) // the form opens focused on the message field

	for _, r := range "fix timeline crash" {
		f.Update(sh, keyMsg(string(r)))
	}
	if got := f.Value("message"); got != "fix timeline crash" {
		t.Errorf("a typed commit message should reach OnSubmit, Value = %q", got)
	}
}

func TestCommitable(t *testing.T) {
	if got := commitable(sampleChanges, false); len(got) != 2 {
		t.Errorf("commitable(-a) = %v, want the 2 tracked changes", got)
	}
	if got := commitable(sampleChanges, true); len(got) != 3 {
		t.Errorf("commitable(-A) = %v, want all 3", got)
	}
	untrackedOnly := []repo.GitChange{{Code: "??", Path: "new.gd"}}
	if got := commitable(untrackedOnly, false); len(got) != 0 {
		t.Errorf("commitable(-a) over untracked-only = %v, want empty", got)
	}
}

var untrackedOnlyChanges = []repo.GitChange{
	{Code: "??", Path: "new_event.gd"},
	{Code: "??", Path: "new_char.gd"},
}

// TestCommitPaneUntrackedOnly is the regression: "-a" over a tree whose only changes are
// new files used to render a bare status line, hiding the very files the mode excludes.
func TestCommitPaneUntrackedOnly(t *testing.T) {
	lines := strings.Join(commitPaneLines(untrackedOnlyChanges, false), "\n")

	if !strings.Contains(lines, "No existing files to commit") {
		t.Errorf("the pane should say the mode commits nothing:\n%s", lines)
	}
	if !strings.Contains(lines, "Not included") {
		t.Errorf("the pane should still head the excluded section:\n%s", lines)
	}
	for _, want := range []string{"new_event.gd", "new_char.gd"} {
		if !strings.Contains(lines, want) {
			t.Errorf("the pane must name the new file %q it leaves out:\n%s", want, lines)
		}
	}
}

func TestCommitPaneUntrackedOnlyStageAll(t *testing.T) {
	lines := strings.Join(commitPaneLines(untrackedOnlyChanges, true), "\n")

	if strings.Contains(lines, "No existing files to commit") || strings.Contains(lines, "Not included") {
		t.Errorf("-A commits the new files, so neither notice belongs:\n%s", lines)
	}
	if !strings.Contains(lines, "new_event.gd") {
		t.Errorf("-A should list the new file as included:\n%s", lines)
	}
}

// TestCommitPaneMixed: the populated shape is unchanged — tracked rows, then the
// exclusion section under "-a", one list under "-A".
func TestCommitPaneMixed(t *testing.T) {
	tracked := strings.Join(commitPaneLines(sampleChanges, false), "\n")
	if strings.Contains(tracked, "No existing files to commit") {
		t.Errorf("there are tracked files here; the empty-state line must not appear:\n%s", tracked)
	}
	for _, want := range []string{"timeline.gd", "old.gd", "Not included", "new_event.gd"} {
		if !strings.Contains(tracked, want) {
			t.Errorf("the -a pane is missing %q:\n%s", want, tracked)
		}
	}

	all := strings.Join(commitPaneLines(sampleChanges, true), "\n")
	if strings.Contains(all, "Not included") {
		t.Errorf("-A excludes nothing; there should be no exclusion notice:\n%s", all)
	}
}

// TestCommitPaneClean: no changes at all yields no lines, which is the caller's cue to
// show a status line instead of an empty box.
func TestCommitPaneClean(t *testing.T) {
	for _, stageAll := range []bool{false, true} {
		if got := commitPaneLines(nil, stageAll); len(got) != 0 {
			t.Errorf("commitPaneLines(clean, stageAll=%v) = %v, want empty", stageAll, got)
		}
	}
}

// TestCommitPaneTitle: the count rides the border in both modes — "-a" excludes new
// files and "-A" buries them at the bottom, so either way they are easy to miss.
func TestCommitPaneTitle(t *testing.T) {
	if got := commitPaneTitle(sampleChanges); got != "Files to Commit (1 new)" {
		t.Errorf("commitPaneTitle = %q, want the new-file count on the legend", got)
	}
	if got := commitPaneTitle(untrackedOnlyChanges); got != "Files to Commit (2 new)" {
		t.Errorf("commitPaneTitle = %q, want 2 new", got)
	}
	if got := commitPaneTitle(sampleChanges[:2]); got != commitFilesTitle {
		t.Errorf("commitPaneTitle(no untracked) = %q, want the bare title", got)
	}
}
