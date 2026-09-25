package repoui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/brohd11/bubblestack/core"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// Parsing and rendering unified diffs as pure functions, testable on strings; DiffScreen
// owns the terminal. Unified is git's form and works at any width; side by side shows an
// edited line's before and after on one row but needs minSplitWidth.

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

// diffLine is one parsed line with its old and new line numbers (0 where it does not
// exist: additions have no old number, deletions no new), which places it in a column.
type diffLine struct {
	kind  byte
	text  string // the content, marker stripped and tabs expanded
	oldN  int
	newN  int
	noEOL bool // git's "\ No newline at end of file" applies to this line
}

// tabStop is the tab width; tabs become spaces before measuring, or widths and columns
// would be wrong. 4 matches gofmt.
const tabStop = 4

// binaryNote replaces git's "Binary files a/x and b/x differ", which repeats the path
// (and leaks /dev/null for untracked files). It explains a file header with no hunks.
const binaryNote = "binary file — contents not shown"

// parseDiff turns git's unified output into lines. "---"/"+++" headers look like content,
// so they are told apart by position: content exists only after a @@ header.
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

		// Between "diff --git" and the first hunk: index and path lines repeat the header and are
		// dropped; mode, rename and binary notes are kept.
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
			// "\ No newline at end of file" attaches to the line above. Git emits it between a hunk's
			// deletions and additions, and as its own line it would break the split layout's pairing.
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

// gitHeaderPath extracts the b-side path from the "diff --git a/x b/y" line, or
// "old → new" for a rename.
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

// parseHunkHeader reads the start lines from "@@ -a,n +b,n @@", or 1,1 if malformed. Only
// fields 1 and 2 are read: the trailing context text is source code.
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

// Add/delete colors are ANSI 2 and 1 rather than theme roles: they are diff semantics and
// follow the user's terminal palette under every theme.
const (
	addColor = lipgloss.ANSIColor(2)
	delColor = lipgloss.ANSIColor(1)
)

// Built per call from the current palette rather than cached, per the convention in
// core/styles.go: colors are read at render time so a theme switch repaints.
func addStyle() lipgloss.Style  { return lipgloss.NewStyle().Foreground(addColor) }
func delStyle() lipgloss.Style  { return lipgloss.NewStyle().Foreground(delColor) }
func metaStyle() lipgloss.Style { return core.MutedStyle() }
func fileStyle() lipgloss.Style {
	return core.AccentStyle()
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

// fileSepRule picks a muted rule (true) or a blank line between files; a compile-time
// style choice.
const fileSepRule = true

// fileRule is the rule drawn between files when fileSepRule is on. Muted, like the hunk
// headers and the gutter's │ — it's chrome, not content.
func fileRule(width int) string {
	if width < 1 {
		width = 1
	}
	return metaStyle().Render(strings.Repeat("─", width))
}

// renderUnified renders git's one-column form with an old/new line-number gutter. The
// marker stays, since with one column only it and color tell additions from deletions.
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

// unifiedGutter is the "old new │" gutter width, shared by fitting and padding.
func unifiedGutter(gw int) int { return 2*gw + 3 }

// unifiedRow renders one content line: numbers, rule, marker, text. Wrapped continuations
// get a blank gutter and marker. Rows are styled individually: styling the block would
// pad every row to the widest.
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

// minSplitWidth is the width below which side by side leaves each side too narrow for
// code; DiffScreen renders unified instead and says so.
const minSplitWidth = 100

// splitSep divides the two columns.
const splitSep = " │ "

// renderSplit lays old and new text side by side, without markers (the column says which
// side).
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
			// Pair a deletion run with the following addition run line by line; leftovers pair with a
			// blank, which also covers pure adds and deletes.
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

// changeRuns collects the deletions at i and the additions after them (git's order within
// a hunk), returning the next index.
func changeRuns(lines []diffLine, i int) (dels, adds []diffLine, next int) {
	for ; i < len(lines) && lines[i].kind == kindDel; i++ {
		dels = append(dels, lines[i])
	}
	for ; i < len(lines) && lines[i].kind == kindAdd; i++ {
		adds = append(adds, lines[i])
	}
	return dels, adds, i
}

// splitRow renders one split row: oldL left, newL right, nil as an empty cell. Wrapped
// cells are padded to the taller one so the columns stay aligned.
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

// side picks which line number a column shows: a context line appears in both columns
// with different numbers.
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

// fit makes text at most width cells per row, wrapped (breaking long tokens) or truncated
// with an ellipsis.
func fit(text string, width int, wrap bool) string {
	if width < 1 {
		width = 1
	}
	if wrap {
		return ansi.Wrap(text, width, "")
	}
	return ansi.Truncate(text, width, "…")
}
