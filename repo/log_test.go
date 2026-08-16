package repo

import (
	"errors"
	"os/exec"
	"strings"
	"testing"
)

// Parsing the log capture. Log itself shells out to git, so these exercise parseLog on a
// hand-built record stream — the parsing is about the separators and the decoration
// grammar, not about where the bytes came from.

// record builds one log record in logFormat's field order.
func record(hash, short, parents, refs, author, email, date, subject, body string) string {
	return strings.Join([]string{hash, short, parents, refs, author, email, date, subject, body},
		fieldSep) + recordSep
}

// sampleLog is what git writes for three commits: a decorated HEAD, a merge, and a commit
// with a multi-paragraph body. Records are joined by a newline, the way git joins them.
var sampleLog = strings.Join([]string{
	record("aaaa111122223333444455556666777788889999", "aaaa111",
		"bbbb222", "HEAD -> main, tag: v1.0.0, origin/main, feature/parser",
		"Ada", "ada@example.com", "Fri Aug 15 10:00:00 2026 -0700",
		"add the parser", ""),
	record("bbbb222233334444555566667777888899990000", "bbbb222",
		"cccc333 dddd444", "",
		"Ada", "ada@example.com", "Thu Aug 14 09:00:00 2026 -0700",
		"Merge branch 'feature/parser'", ""),
	record("cccc333344445555666677778888999900001111", "cccc333",
		"eeee555", "tag: v0.9.0",
		"Grace", "grace@example.com", "Wed Aug 13 08:00:00 2026 -0700",
		"rework the scanner",
		"The old one re-read the buffer on every token.\n\nFixes #12\n\n"),
}, "\n")

func remotes(names ...string) map[string]bool {
	set := make(map[string]bool, len(names))
	for _, n := range names {
		set[n] = true
	}
	return set
}

func TestParseLogFields(t *testing.T) {
	commits := parseLog(sampleLog, remotes("origin"))
	if len(commits) != 3 {
		t.Fatalf("parseLog returned %d commits, want 3", len(commits))
	}

	c := commits[0]
	if c.Hash != "aaaa111122223333444455556666777788889999" || c.Short != "aaaa111" {
		t.Errorf("hashes = %q / %q", c.Hash, c.Short)
	}
	if c.Author != "Ada" || c.Email != "ada@example.com" {
		t.Errorf("author = %q <%q>", c.Author, c.Email)
	}
	if c.Date != "Fri Aug 15 10:00:00 2026 -0700" {
		t.Errorf("date = %q", c.Date)
	}
	if c.Subject != "add the parser" {
		t.Errorf("subject = %q", c.Subject)
	}
	if c.Body != "" {
		t.Errorf("body = %q, want empty", c.Body)
	}
	if c.Merge() {
		t.Error("a single-parent commit should not report as a merge")
	}
}

// The trailing newlines %b carries (both the separator's and the message's own) would show
// up as blank rows under every commit in the standard view.
func TestParseLogTrimsBodyButKeepsItsBlankLines(t *testing.T) {
	c := parseLog(sampleLog, remotes("origin"))[2]
	want := "The old one re-read the buffer on every token.\n\nFixes #12"
	if c.Body != want {
		t.Errorf("body = %q, want %q", c.Body, want)
	}
}

func TestParseLogMerge(t *testing.T) {
	c := parseLog(sampleLog, remotes("origin"))[1]
	if !c.Merge() {
		t.Fatal("a two-parent commit should report as a merge")
	}
	if len(c.Parents) != 2 || c.Parents[0] != "cccc333" || c.Parents[1] != "dddd444" {
		t.Errorf("parents = %q", c.Parents)
	}
	if len(c.Refs) != 0 {
		t.Errorf("an undecorated commit should have no refs, got %v", c.Refs)
	}
}

// The decoration grammar, which is what the colors are keyed off.
func TestParseRefs(t *testing.T) {
	got := parseRefs("HEAD -> main, tag: v1.0.0, origin/main, feature/parser", remotes("origin"))
	want := []Ref{
		{Name: "main", Kind: RefHead},
		{Name: "v1.0.0", Kind: RefTag},
		{Name: "origin/main", Kind: RefRemote},
		// The one a "does it contain a slash" test would get wrong: a local branch may
		// have slashes in its name, and only the remote list can tell them apart.
		{Name: "feature/parser", Kind: RefBranch},
	}
	if len(got) != len(want) {
		t.Fatalf("parseRefs returned %d refs, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("ref %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// A detached checkout: HEAD names a commit, not a branch.
func TestParseRefsDetachedHead(t *testing.T) {
	got := parseRefs("HEAD, tag: v1.0.0", remotes("origin"))
	if len(got) != 2 {
		t.Fatalf("parseRefs returned %v", got)
	}
	if got[0].Kind != RefHead || got[0].Name != "" {
		t.Errorf("bare HEAD = %+v, want an unnamed RefHead", got[0])
	}
}

// With no remotes configured, every slash-bearing ref is a local branch — which is the
// truth in that repo, not a fallback.
func TestParseRefsWithoutRemotes(t *testing.T) {
	got := parseRefs("origin/main", nil)
	if len(got) != 1 || got[0].Kind != RefBranch {
		t.Errorf("parseRefs without remotes = %+v, want a local branch", got)
	}
}

func TestParseRefsEmpty(t *testing.T) {
	if got := parseRefs("", remotes("origin")); got != nil {
		t.Errorf("parseRefs(\"\") = %v, want nil", got)
	}
}

// Against real git — the parse tests above can't catch a wrong format string, which is the
// one part of the capture that only git can confirm.
func TestLogAgainstGit(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir, "https://example.com/x.git", "main")
	commit(t, dir, "second")
	git(t, dir, "tag", "v1.0.0")

	commits, err := Log(dir, 0)
	if err != nil {
		t.Fatalf("Log: %v", err)
	}
	if len(commits) != 2 {
		t.Fatalf("Log returned %d commits, want 2", len(commits))
	}

	// Newest first, and every field lands in its own column.
	c := commits[0]
	if c.Subject != "second" {
		t.Errorf("subject = %q, want the newest commit's", c.Subject)
	}
	if len(c.Hash) != 40 || c.Short == "" || !strings.HasPrefix(c.Hash, c.Short) {
		t.Errorf("hashes = %q / %q", c.Hash, c.Short)
	}
	if c.Author != "t" || c.Email != "t@t" || c.Date == "" {
		t.Errorf("author/date = %q <%q> %q", c.Author, c.Email, c.Date)
	}
	if len(c.Parents) != 1 || c.Parents[0] != commits[1].Hash {
		t.Errorf("parents = %q, want the previous commit", c.Parents)
	}

	// The decorations git actually emits, classified: HEAD -> main and the tag.
	var kinds []RefKind
	for _, r := range c.Refs {
		kinds = append(kinds, r.Kind)
	}
	if len(kinds) != 2 || kinds[0] != RefHead || kinds[1] != RefTag {
		t.Errorf("refs = %+v, want HEAD then the tag", c.Refs)
	}

	// The limit is honored, which is what keeps a large repo's history from being read whole.
	if one, err := Log(dir, 1); err != nil || len(one) != 1 {
		t.Errorf("Log(dir, 1) = %d commits, %v", len(one), err)
	}
}

// A repo with no commits is an empty history, not a failed read — callers distinguish the
// two to decide whether to apologize.
func TestLogNoCommits(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	git(t, dir, "init", "-q", "-b", "main")

	_, err := Log(dir, 0)
	if !errors.Is(err, ErrNoCommits) {
		t.Errorf("Log on an empty repo = %v, want ErrNoCommits", err)
	}
}

// A record that doesn't split into logFields fields is skipped rather than read at the
// wrong offsets, which would put a date in the subject.
func TestParseLogSkipsMalformedRecords(t *testing.T) {
	raw := "short" + recordSep + "\n" + record(
		"aaaa111122223333444455556666777788889999", "aaaa111", "bbbb222", "",
		"Ada", "ada@example.com", "Fri Aug 15 10:00:00 2026 -0700", "add the parser", "")

	commits := parseLog(raw, nil)
	if len(commits) != 1 {
		t.Fatalf("parseLog returned %d commits, want the 1 well-formed one", len(commits))
	}
	if commits[0].Subject != "add the parser" {
		t.Errorf("subject = %q", commits[0].Subject)
	}
}
