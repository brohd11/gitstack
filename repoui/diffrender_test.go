package repoui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// sampleDiff is git's real output for a one-line edit plus an appended line — captured
// from an actual `git diff HEAD`, headers and all, so the parser is tested against the
// format git emits rather than the format this file imagines it emits.
const sampleDiff = `diff --git a/a.txt b/a.txt
index f384549..86ef44e 100644
--- a/a.txt
+++ b/a.txt
@@ -1,4 +1,5 @@
 one
-two
+TWO changed
 three
 four
+five added
`

func TestParseDiffSkipsFileHeaderMarkers(t *testing.T) {
	lines := parseDiff(sampleDiff)

	// "--- a/a.txt" and "+++ b/a.txt" begin with - and +, but they are not content.
	// Reading them as content is the bug this guards: it would prepend two bogus rows.
	for _, l := range lines {
		if l.kind == kindDel && strings.HasPrefix(l.text, "- a/") {
			t.Errorf("read the --- file header as a deletion: %q", l.text)
		}
		if l.kind == kindAdd && strings.HasPrefix(l.text, "++ b/") {
			t.Errorf("read the +++ file header as an addition: %q", l.text)
		}
	}

	var got []string
	for _, l := range lines {
		if l.kind == kindAdd || l.kind == kindDel {
			got = append(got, string(l.kind)+l.text)
		}
	}
	want := []string{"-two", "+TWO changed", "+five added"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("content lines:\n got %q\nwant %q", got, want)
	}
}

func TestParseDiffLineNumbers(t *testing.T) {
	lines := parseDiff(sampleDiff)

	type row struct {
		kind byte
		text string
		o, n int
	}
	// The numbering the gutters show. After "-two"/"+TWO changed" the two sides stay in
	// step; "+five added" is new, so it has no old number at all.
	want := []row{
		{kindFile, "a.txt", 0, 0},
		{kindHunk, "@@ -1,4 +1,5 @@", 0, 0},
		{kindContext, "one", 1, 1},
		{kindDel, "two", 2, 0},
		{kindAdd, "TWO changed", 0, 2},
		{kindContext, "three", 3, 3},
		{kindContext, "four", 4, 4},
		{kindAdd, "five added", 0, 5},
	}
	if len(lines) != len(want) {
		t.Fatalf("got %d lines, want %d: %+v", len(lines), len(want), lines)
	}
	for i, w := range want {
		g := lines[i]
		if g.kind != w.kind || g.text != w.text || g.oldN != w.o || g.newN != w.n {
			t.Errorf("line %d:\n got %c %q old=%d new=%d\nwant %c %q old=%d new=%d",
				i, g.kind, g.text, g.oldN, g.newN, w.kind, w.text, w.o, w.n)
		}
	}
}

// TestParseHunkHeaderTrailingContext: git appends the enclosing declaration to a hunk
// header, and that text is source code — which can begin with a marker character. A parse
// that scans for the first -/+ field would read the code and misnumber the whole hunk.
func TestParseHunkHeaderTrailingContext(t *testing.T) {
	o, n := parseHunkHeader("@@ -12,7 +40,9 @@ func f(a int) { -x; +y }")
	if o != 12 || n != 40 {
		t.Errorf("got old=%d new=%d, want old=12 new=40", o, n)
	}
}

// TestRenderSplitAlignment is the one that matters: every row of the split layout must be
// exactly the same display width, or the separator wanders and the columns stop lining up.
// Widths are measured with ansi.StringWidth because the rows are styled.
func TestRenderSplitAlignment(t *testing.T) {
	lines := parseDiff(sampleDiff)
	// 121 is deliberate: an odd width can't halve evenly, which is where a rounding slip
	// would push the last column one cell past the edge.
	for _, width := range []int{100, 120, 121, 200} {
		for _, wrap := range []bool{false, true} {
			// File/meta rows are full-width text, not two columns; only the paired
			// content rows have to agree, and they're the ones carrying the separator.
			var want, wantAt int
			for i, row := range strings.Split(renderSplit(lines, width, wrap), "\n") {
				w := ansi.StringWidth(row)
				if w > width {
					t.Errorf("width=%d wrap=%v: row %d is %d cells, wider than the terminal:\n%q",
						width, wrap, i, w, row)
				}
				if !strings.Contains(row, strings.TrimSpace(splitSep)) {
					continue
				}
				if want == 0 {
					want, wantAt = w, i
					continue
				}
				if w != want {
					t.Errorf("width=%d wrap=%v: row %d is %d cells but row %d is %d — the columns don't line up:\n%q",
						width, wrap, i, w, wantAt, want, row)
				}
			}
		}
	}
}

// TestRenderSplitUnevenRuns is the padding case: a hunk whose deletion run and addition
// run are different lengths. The leftover additions must pair against blank cells and
// stay in the right-hand column, rather than sliding left.
func TestRenderSplitUnevenRuns(t *testing.T) {
	raw := `diff --git a/x b/x
--- a/x
+++ b/x
@@ -1,2 +1,4 @@
-gone
+one
+two
+three
 tail
`
	out := renderSplit(parseDiff(raw), 120, false)
	rows := strings.Split(out, "\n")

	var paired []string
	for _, r := range rows {
		if strings.Contains(r, strings.TrimSpace(splitSep)) {
			paired = append(paired, r)
		}
	}
	// 3 additions vs 1 deletion => 3 rows, plus the trailing context row.
	if len(paired) != 4 {
		t.Fatalf("got %d paired rows, want 4:\n%s", len(paired), out)
	}

	// Row 0: "gone" left, "one" right. Rows 1-2: blank left, "two"/"three" right.
	sep := strings.Index(ansi.Strip(paired[1]), strings.TrimSpace(splitSep))
	left := ansi.Strip(paired[1])[:sep]
	if strings.TrimSpace(left) != "" {
		t.Errorf("the 2nd extra addition should have a blank left cell, got %q", left)
	}
	if !strings.Contains(ansi.Strip(paired[1]), "two") {
		t.Errorf("row 1 should carry the addition \"two\":\n%q", paired[1])
	}
}

// TestRenderSplitContextNumbering: a context line appears in both columns, and after an
// uneven edit its two numbers differ. Showing the old number on both sides is the bug.
func TestRenderSplitContextNumbering(t *testing.T) {
	raw := `diff --git a/x b/x
--- a/x
+++ b/x
@@ -1,2 +1,4 @@
-gone
+one
+two
+three
 tail
`
	lines := parseDiff(raw)

	var tail diffLine
	for _, l := range lines {
		if l.kind == kindContext {
			tail = l
		}
	}
	// "tail" is line 2 of the old file and line 4 of the new one.
	if tail.oldN != 2 || tail.newN != 4 {
		t.Fatalf("context line numbering: got old=%d new=%d, want old=2 new=4", tail.oldN, tail.newN)
	}

	out := ansi.Strip(renderSplit(lines, 120, false))
	var row string
	for _, r := range strings.Split(out, "\n") {
		if strings.Contains(r, "tail") {
			row = r
		}
	}
	sep := strings.Index(row, strings.TrimSpace(splitSep))
	left, right := row[:sep], row[sep:]
	if !strings.Contains(left, "2") {
		t.Errorf("left cell should show the old number 2, got %q", left)
	}
	if !strings.Contains(right, "4") {
		t.Errorf("right cell should show the new number 4, got %q", right)
	}
}

// TestRenderUnifiedFitsWidth: no row may exceed the terminal, in either mode. A row that
// overflows wraps in the viewport and corrupts the frame.
func TestRenderUnifiedFitsWidth(t *testing.T) {
	long := "diff --git a/x b/x\n--- a/x\n+++ b/x\n@@ -1,1 +1,1 @@\n-" +
		strings.Repeat("verylongtoken", 40) + "\n+short\n"
	lines := parseDiff(long)

	for _, width := range []int{40, 80, 100} {
		for _, wrap := range []bool{false, true} {
			for i, row := range strings.Split(renderUnified(lines, width, wrap), "\n") {
				if w := ansi.StringWidth(row); w > width {
					t.Errorf("width=%d wrap=%v: row %d is %d cells:\n%q", width, wrap, i, w, row)
				}
			}
		}
	}
}

// TestExpandTabs: tabs must become spaces before anything is measured — a raw tab makes
// every width calculation lie, and the split columns would drift apart on Go source.
func TestExpandTabs(t *testing.T) {
	lines := parseDiff("diff --git a/x b/x\n--- a/x\n+++ b/x\n@@ -1 +1 @@\n+\tif x {\n")
	for _, l := range lines {
		if strings.Contains(l.text, "\t") {
			t.Errorf("tab survived into %q", l.text)
		}
	}
}

// TestParseDiffBinary: git reports a binary file with a one-line note and no hunks. It
// should survive as a readable note rather than vanish or be read as content.
func TestParseDiffBinary(t *testing.T) {
	raw := "diff --git a/bin.dat b/bin.dat\nnew file mode 100644\nindex 0000000..c94be36\nBinary files /dev/null and b/bin.dat differ\n"
	lines := parseDiff(raw)

	var kinds []string
	for _, l := range lines {
		kinds = append(kinds, string(l.kind)+":"+l.text)
	}
	if len(lines) != 3 ||
		lines[0].kind != kindFile ||
		lines[1].kind != kindMeta ||
		lines[2].kind != kindMeta ||
		!strings.Contains(lines[2].text, "Binary files") {
		t.Errorf("binary file should parse to a file header + two notes, got %q", kinds)
	}
}

// TestNoNewlineDoesNotBreakPairing is a regression test for the bug that side-by-side
// exists to not have. git emits "\ No newline at end of file" *between* a hunk's
// deletions and its additions; read as a line of its own it cuts the deletion run short,
// and the edited line stops being paired against its replacement — the before and after
// land on separate rows, which is the whole point of the layout, lost.
func TestNoNewlineDoesNotBreakPairing(t *testing.T) {
	// Exactly what `git diff HEAD` prints for a file committed without a trailing newline
	// and then edited.
	raw := "diff --git a/f b/f\n--- a/f\n+++ b/f\n@@ -1 +1,2 @@\n-x\n\\ No newline at end of file\n+y\n+second\n"
	lines := parseDiff(raw)

	// The note must ride on the deletion, not sit between it and the addition.
	for _, l := range lines {
		if strings.Contains(l.text, "No newline") {
			t.Fatalf("the no-newline note is still a line of its own: %+v", lines)
		}
	}
	if len(lines) != 5 { // file, hunk, -x, +y, +second
		t.Fatalf("got %d lines, want 5: %+v", len(lines), lines)
	}
	if !lines[2].noEOL {
		t.Errorf("the note should be flagged on the deletion it describes: %+v", lines[2])
	}

	// -x and +y are one edit, so they must share a row.
	out := ansi.Strip(renderSplit(lines, 120, false))
	var paired string
	for _, r := range strings.Split(out, "\n") {
		if strings.Contains(r, "x") && strings.Contains(r, "y") {
			paired = r
		}
	}
	if paired == "" {
		t.Errorf("the deletion and its replacement should share one row:\n%s", out)
	}

	// And the note itself must survive somewhere — it's real information about the file.
	if !strings.Contains(out, "No newline") {
		t.Errorf("the no-newline note should still be shown:\n%s", out)
	}
}
