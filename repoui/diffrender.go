package repoui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/brohd11/bubblestack/core"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// Parsing and rendering the unified diff format. Pure functions — no bubbletea, no
// viewport — so the layout logic (which is the hard part, and the part with edge cases)
// is testable on strings alone. DiffScreen owns the terminal side.
//
// The two layouts answer different questions. Unified is git's own form, reads at any
// width, and shows a change as a deletion followed by an addition. Side-by-side puts the
// before and after on one row, which is what you want when a line was *edited* rather
// than added or removed — but it costs half the width per side, hence minSplitWidth.

// kind classifies a parsed line. It doubles as the marker character for content lines,
// which is what git itself uses in column 0.
const (
	kindFile    = 'F' // a file's header: the path this hunk sequence belongs to
	kindMeta    = 'M' // a note about the file: new/deleted/renamed/binary
	kindHunk    = '@' // a hunk header (@@ -a,b +c,d @@ context)
	kindContext = ' '
	kindAdd     = '+'
	kindDel     = '-'
)

// diffLine is one parsed line of a diff, with the line numbers it occupies on each side.
// A number is 0 when the line doesn't exist on that side: an addition has no old number,
// a deletion has no new one, and headers have neither. That's what lets the split
// renderer put a line in the correct column without re-reading the marker.
type diffLine struct {
	kind  byte
	text  string // the content, marker stripped and tabs expanded
	oldN  int
	newN  int
	noEOL bool // git's "\ No newline at end of file" applies to this line
}

// tabStop is what a tab expands to. Diff text lands in fixed-width cells, so tabs have to
// become spaces before anything can be measured or truncated — a raw tab would make every
// width calculation lie and knock the split columns out of alignment. 4 matches gofmt's
// rendering, which is what this repo's own diffs are made of.
const tabStop = 4

// binaryNote replaces git's own binary line, which says nothing the page doesn't already
// show: "Binary files a/x and b/x differ" restates — twice — the path the file header
// spells out one line up, and the --no-index capture that renders an untracked file makes
// it worse ("Binary files /dev/null and b/x differ"), leaking the null device the diff was
// taken against. That's a detail of how the answer was obtained, not part of the answer.
//
// What the note is actually for is the absence beneath it: a file header with no hunks
// under it needs a reason, and "it's binary" is the whole reason. "binary file" is the
// wording fileDesc already uses for this condition on the picker row.
const binaryNote = "binary file — contents not shown"

// parseDiff turns git's raw unified output into lines the renderers can lay out.
//
// The subtlety is that "---" and "+++" (the file's from/to header) begin with the same
// characters as content lines. They're distinguished by position, not spelling: content
// only exists inside a hunk, so a marker is only content once a @@ header has opened one.
// Reading them as content would silently prepend two bogus rows to every file.
func parseDiff(raw string) []diffLine {
	var (
		lines  []diffLine
		inHunk bool
		oldN   int
		newN   int
	)

	for _, line := range strings.Split(raw, "\n") {
		switch {
		case strings.HasPrefix(line, "diff --git "):
			inHunk = false
			lines = append(lines, diffLine{kind: kindFile, text: gitHeaderPath(line)})

		// Everything git prints between the "diff --git" line and the first hunk. The
		// blob hashes and the a/ b/ paths restate the header, so they're dropped; the
		// mode/rename/binary notes say something the header doesn't, so they're kept.
		case !inHunk && (strings.HasPrefix(line, "index ") ||
			strings.HasPrefix(line, "--- ") ||
			strings.HasPrefix(line, "+++ ") ||
			strings.HasPrefix(line, "similarity index ")):
			// dropped

		case !inHunk && (strings.HasPrefix(line, "new file") ||
			strings.HasPrefix(line, "deleted file") ||
			strings.HasPrefix(line, "old mode") ||
			strings.HasPrefix(line, "new mode") ||
			strings.HasPrefix(line, "rename ") ||
			strings.HasPrefix(line, "copy ")):
			lines = append(lines, diffLine{kind: kindMeta, text: line})

		// The one note git writes that the page can't use as written — see binaryNote.
		case !inHunk && strings.HasPrefix(line, "Binary files "):
			lines = append(lines, diffLine{kind: kindMeta, text: binaryNote})

		case strings.HasPrefix(line, "@@"):
			inHunk = true
			oldN, newN = parseHunkHeader(line)
			lines = append(lines, diffLine{kind: kindHunk, text: line})

		case !inHunk:
			// Anything else before the first hunk (a --no-index preamble, say). Keep it
			// rather than swallow it: an unrecognized line is better shown than lost.
			if strings.TrimSpace(line) != "" {
				lines = append(lines, diffLine{kind: kindMeta, text: line})
			}

		case strings.HasPrefix(line, "\\"):
			// "\ No newline at end of file" is a note about the line above, not a line of
			// its own — and git emits it *between* a hunk's deletions and its additions.
			// Left as a line it would cut the deletion run short, and the split layout
			// would stop pairing an edited line against its own replacement: the before
			// and after would land on separate rows, which is the one thing that layout
			// exists to avoid. So it rides on the line it describes.
			if n := len(lines) - 1; n >= 0 {
				lines[n].noEOL = true
			}

		case strings.HasPrefix(line, "-"):
			lines = append(lines, diffLine{kind: kindDel, text: expand(line[1:]), oldN: oldN})
			oldN++

		case strings.HasPrefix(line, "+"):
			lines = append(lines, diffLine{kind: kindAdd, text: expand(line[1:]), newN: newN})
			newN++

		case strings.HasPrefix(line, " "):
			lines = append(lines, diffLine{kind: kindContext, text: expand(line[1:]), oldN: oldN, newN: newN})
			oldN++
			newN++

		case line == "":
			// git writes a context line that is itself empty as " ", so a truly empty
			// line here is the trailing newline of the output, not content.

		default:
			lines = append(lines, diffLine{kind: kindMeta, text: line})
		}
	}
	return lines
}

// gitHeaderPath pulls the readable path out of `diff --git a/x b/y`. It reports the
// b-side (where the file ended up), or "old → new" for a rename, so the header says what
// happened without the reader decoding two prefixed paths.
func gitHeaderPath(line string) string {
	rest := strings.TrimPrefix(line, "diff --git ")
	// Paths containing " b/" can't be split reliably; git quotes those, and the header is
	// cosmetic, so fall back to the raw text rather than guess wrong.
	i := strings.Index(rest, " b/")
	if i < 0 {
		return rest
	}
	from := strings.TrimPrefix(rest[:i], "a/")
	to := strings.TrimPrefix(rest[i+1:], "b/")
	if from == to {
		return to
	}
	return from + " → " + to
}

// parseHunkHeader reads the starting line numbers out of "@@ -oldStart,n +newStart,n @@".
// A malformed header yields 1,1 — the numbers are a reading aid, so a wrong gutter beats
// refusing to show the hunk.
//
// Only fields 1 and 2 are read, never a scan for the first "-"/"+" field: git appends the
// enclosing context to the header ("@@ -12,7 +12,9 @@ func Run() {"), and that trailing
// text is source code, which can itself begin with a marker character.
func parseHunkHeader(line string) (oldN, newN int) {
	oldN, newN = 1, 1
	fields := strings.Fields(line)
	if len(fields) < 3 {
		return oldN, newN
	}
	if strings.HasPrefix(fields[1], "-") {
		oldN = leadingInt(fields[1][1:], oldN)
	}
	if strings.HasPrefix(fields[2], "+") {
		newN = leadingInt(fields[2][1:], newN)
	}
	return oldN, newN
}

// leadingInt reads the number before the comma in "12,7" (or all of "12"), falling back
// to def when there isn't one.
func leadingInt(s string, def int) int {
	if i := strings.IndexByte(s, ','); i >= 0 {
		s = s[:i]
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return n
}

func expand(s string) string { return strings.ReplaceAll(s, "\t", strings.Repeat(" ", tabStop)) }

// eolNote is what a line carrying noEOL prints beneath itself — git's own wording, since
// this is one of the places a reader is best served by seeing exactly what git says.
const eolNote = `\ No newline at end of file`

// ---------- styles ----------

// Add/delete color stays here rather than in core.Theme. The theme's five colors are
// semantic framework roles (muted, log, border, accent, on-accent); "added" and "removed"
// are a diff's concepts, and pushing them into core would mean editing all six presets
// for one screen's benefit. ANSI 2/1 resolve against the user's own terminal palette, so
// they read correctly under every theme — the same reasoning the "mono" theme uses in
// leaning on the terminal's colors.
const (
	addColor = lipgloss.ANSIColor(2)
	delColor = lipgloss.ANSIColor(1)
)

// Built per call from the current palette rather than cached, per the convention in
// core/styles.go: colors are read at render time so a theme switch repaints.
func addStyle() lipgloss.Style  { return lipgloss.NewStyle().Foreground(addColor) }
func delStyle() lipgloss.Style  { return lipgloss.NewStyle().Foreground(delColor) }
func metaStyle() lipgloss.Style { return lipgloss.NewStyle().Foreground(core.MutedColor) }
func fileStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(core.FocusedColor).Bold(true)
}

// lineStyle is the style for a content line's text, by kind.
func lineStyle(kind byte) lipgloss.Style {
	switch kind {
	case kindAdd:
		return addStyle()
	case kindDel:
		return delStyle()
	default:
		return lipgloss.NewStyle()
	}
}

// ---------- unified ----------

// minGutter is the floor for a line-number column, so a short file's gutter doesn't
// wobble narrower than a reader expects.
const minGutter = 3

// fileSepRule chooses what divides one file's diff from the next in a multi-file view:
// a muted rule across the width, or a plain blank line. Compile-time because this is a
// taste check, not a setting — flip it and rebuild to compare.
const fileSepRule = true

// fileRule is the rule drawn between files when fileSepRule is on. Muted, like the hunk
// headers and the gutter's │ — it's chrome, not content.
func fileRule(width int) string {
	if width < 1 {
		width = 1
	}
	return metaStyle().Render(strings.Repeat("─", width))
}

// renderUnified lays the diff out in git's own form: one column, each content line kept
// under its marker, with the old and new line numbers in a gutter. The marker stays here
// (unlike the split layout) because with one column there's no left/right to tell an
// addition from a deletion — only the marker and the color do.
func renderUnified(lines []diffLine, width int, wrap bool) string {
	gw := gutterWidth(lines)
	textW := width - unifiedGutter(gw) - 1 // the gutter, plus the marker column
	if textW < 8 {
		textW = 8
	}

	var b strings.Builder
	seenFile := false
	for i, l := range lines {
		if i > 0 {
			b.WriteByte('\n')
		}
		switch l.kind {
		case kindFile:
			// A file after the first gets air before its header — otherwise a whole-repo
			// diff reads as one unbroken wall.
			if seenFile {
				b.WriteByte('\n')
				if fileSepRule {
					b.WriteString(fileRule(width) + "\n")
				}
			}
			seenFile = true
			b.WriteString(fileStyle().Render(fit(l.text, width, wrap)))
		case kindMeta, kindHunk:
			b.WriteString(metaStyle().Render(fit(l.text, width, wrap)))
		default:
			b.WriteString(unifiedRow(l, gw, textW, wrap))
			if l.noEOL {
				b.WriteString("\n" + metaStyle().Render(fit(eolNote, width, wrap)))
			}
		}
	}
	return b.String()
}

// unifiedGutter is the cell width of the "old new │" number gutter, kept in one place so
// the width the text is fitted to and the blank a continuation row is padded with can't
// drift apart.
func unifiedGutter(gw int) int { return 2*gw + 3 }

// unifiedRow renders one content line: the two number columns, a rule, then the marker
// and the text. When wrapped, the continuation rows get a blank gutter and a blank marker
// column, so the numbers still mark where each real line begins and the marker isn't
// repeated down a folded line as though each row were its own change.
//
// Each row is styled on its own rather than styling the whole folded block at once:
// lipgloss pads a multi-line render out to its widest line, so styling the block would
// silently widen every row to match the longest — pushing them past the terminal.
func unifiedRow(l diffLine, gw, textW int, wrap bool) string {
	gutter := metaStyle().Render(fmt.Sprintf("%s %s │", num(l.oldN, gw), num(l.newN, gw)))
	blank := metaStyle().Render(strings.Repeat(" ", unifiedGutter(gw)-2) + " │")
	st := lineStyle(l.kind)

	rows := strings.Split(fit(l.text, textW, wrap), "\n")
	for i, r := range rows {
		g, marker := gutter, string(l.kind)
		if i > 0 {
			g, marker = blank, " "
		}
		rows[i] = g + st.Render(marker+r)
	}
	return strings.Join(rows, "\n")
}

// ---------- side by side ----------

// minSplitWidth is the terminal width below which the split layout stops being worth its
// own cost: two columns plus two gutters leave each side under ~40 cells, at which point
// nearly every line of code is truncated and the comparison the layout exists for is the
// thing you can no longer do. DiffScreen renders unified instead and says so.
const minSplitWidth = 100

// splitSep divides the two columns.
const splitSep = " │ "

// renderSplit lays the old and new text side by side. Markers are dropped: the column a
// line sits in already says which side it's on, and the line-number gutters confirm it,
// so a +/- would only spend a cell repeating them.
func renderSplit(lines []diffLine, width int, wrap bool) string {
	gw := gutterWidth(lines)
	colW := (width - lipgloss.Width(splitSep)) / 2
	textW := colW - gw - 3 // number column + " │ "
	if textW < 8 {
		textW = 8
		colW = textW + gw + 3
	}

	var b strings.Builder
	write := func(s string) {
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(s)
	}

	seenFile := false
	for i := 0; i < len(lines); i++ {
		l := lines[i]
		switch l.kind {
		case kindFile:
			// Same break as the unified layout: a file after the first gets air before
			// its header.
			if seenFile {
				write("")
				if fileSepRule {
					write(fileRule(width))
				}
			}
			seenFile = true
			write(fileStyle().Render(fit(l.text, width, wrap)))
		case kindMeta, kindHunk:
			write(metaStyle().Render(fit(l.text, width, wrap)))
		case kindContext:
			// The same text on both sides — but under its own number on each, which for
			// a context line after an uneven edit are different numbers.
			write(splitRow(&l, &l, gw, colW, textW, wrap))
			if l.noEOL {
				write(metaStyle().Render(fit(eolNote, width, wrap)))
			}

		case kindDel, kindAdd:
			// A run of deletions followed by a run of additions is one edit, so pair the
			// two runs off index-wise: the n-th line before against the n-th line after.
			// The runs are rarely the same length, and the leftover on the longer side
			// pairs against a blank — which is exactly how a pure add or pure delete
			// (an empty opposite run) falls out of the same code.
			dels, adds, next := changeRuns(lines, i)
			for j := 0; j < max(len(dels), len(adds)); j++ {
				var d, a *diffLine
				if j < len(dels) {
					d = &dels[j]
				}
				if j < len(adds) {
					a = &adds[j]
				}
				write(splitRow(d, a, gw, colW, textW, wrap))
				// Emitted from the flag at render time, after the row it belongs to —
				// so the note is kept without ever sitting between a run's halves.
				if (d != nil && d.noEOL) || (a != nil && a.noEOL) {
					write(metaStyle().Render(fit(eolNote, width, wrap)))
				}
			}
			i = next - 1
		}
	}
	return b.String()
}

// changeRuns collects the run of deletions starting at i and the run of additions that
// follows it, returning the index of the first line after both. git emits a hunk's
// deletions before its additions, so this is the shape every edit arrives in.
func changeRuns(lines []diffLine, i int) (dels, adds []diffLine, next int) {
	for ; i < len(lines) && lines[i].kind == kindDel; i++ {
		dels = append(dels, lines[i])
	}
	for ; i < len(lines) && lines[i].kind == kindAdd; i++ {
		adds = append(adds, lines[i])
	}
	return dels, adds, i
}

// splitRow renders one row of the split layout: oldL in the left column under its old
// line number, newL in the right under its new one. Either side may be nil, which renders
// as an empty cell — the padding that keeps a lopsided edit's rows lined up. When
// wrapped, the two cells will disagree on height, so both are padded to the taller one;
// without that, every row after the first long line would be offset from its counterpart
// and the two columns would stop meaning anything.
func splitRow(oldL, newL *diffLine, gw, colW, textW int, wrap bool) string {
	left := splitCell(oldL, oldSide, gw, colW, textW, wrap)
	right := splitCell(newL, newSide, gw, colW, textW, wrap)

	lr, rr := strings.Split(left, "\n"), strings.Split(right, "\n")
	h := max(len(lr), len(rr))
	empty := strings.Repeat(" ", colW)

	rows := make([]string, h)
	for i := range rows {
		l, r := empty, empty
		if i < len(lr) {
			l = lr[i]
		}
		if i < len(rr) {
			r = rr[i]
		}
		rows[i] = l + metaStyle().Render(splitSep) + r
	}
	return strings.Join(rows, "\n")
}

// side selects which of a line's two numbers a column shows. It's a parameter rather than
// something splitCell infers from the kind, because a context line carries both numbers
// and appears in both columns — inferring would print its old number on both sides, which
// silently drifts wrong the moment a hunk's additions and deletions are uneven.
type side bool

const (
	oldSide side = false
	newSide side = true
)

// splitCell renders one side of a row: its line number, a rule, and its text, padded to
// exactly colW cells so the separator lands in the same column on every row.
func splitCell(l *diffLine, s side, gw, colW, textW int, wrap bool) string {
	if l == nil {
		return strings.Repeat(" ", colW)
	}
	n := l.oldN
	if s == newSide {
		n = l.newN
	}
	gutter := metaStyle().Render(num(n, gw) + " │ ")
	blank := metaStyle().Render(strings.Repeat(" ", gw) + " │ ")
	st := lineStyle(l.kind)

	// Styled per row, not per block — see unifiedRow. Each row is then padded out to the
	// full cell width so the separator lands in the same column on every line.
	rows := strings.Split(fit(l.text, textW, wrap), "\n")
	for i, r := range rows {
		g := gutter
		if i > 0 {
			g = blank
		}
		rows[i] = g + st.Render(r) + strings.Repeat(" ", max(0, textW-ansi.StringWidth(r)))
	}
	return strings.Join(rows, "\n")
}

// ---------- shared helpers ----------

// gutterWidth sizes the line-number column to the largest number the diff will show.
func gutterWidth(lines []diffLine) int {
	high := 0
	for _, l := range lines {
		high = max(high, max(l.oldN, l.newN))
	}
	w := len(strconv.Itoa(high))
	return max(w, minGutter)
}

// num right-aligns a line number in w cells, rendering 0 (a line absent from that side)
// as blanks.
func num(n, w int) string {
	if n == 0 {
		return strings.Repeat(" ", w)
	}
	return fmt.Sprintf("%*d", w, n)
}

// fit makes text occupy at most width cells per row: wrapped across rows, or truncated
// with an ellipsis marking what was cut. ansi.Wrap (as the log pane uses) breaks inside a
// token when it has to, so an unbroken 300-character line folds instead of being clipped
// straight back off by the viewport.
func fit(text string, width int, wrap bool) string {
	if width < 1 {
		width = 1
	}
	if wrap {
		return ansi.Wrap(text, width, "")
	}
	return ansi.Truncate(text, width, "…")
}
