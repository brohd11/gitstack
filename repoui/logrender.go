package repoui

import (
	"fmt"
	"strings"

	"github.com/brohd11/bubblestack/core"
	"github.com/brohd11/gitstack/repo"

	"charm.land/lipgloss/v2"
)

// Rendering the commit log as pure functions over []repo.Commit. One line mirrors the
// oneline log format (many commits at once); standard is git's full block.

// ---------- the message indent ----------

// msgIndent is the four spaces git indents a commit message by in its default format. Kept
// as a constant because the width the message is fitted to has to subtract exactly it.
const msgIndent = "    "

// ---------- palette ----------

// Git's decorate colors (commit yellow, HEAD cyan, local green, remote red, tags bold
// yellow), as raw ANSI so they match the user's terminal, like the diff colors.
const (
	shaColor    = lipgloss.ANSIColor(3) // yellow, git's commit color
	headColor   = lipgloss.ANSIColor(6) // cyan
	branchColor = lipgloss.ANSIColor(2) // green
	remoteColor = lipgloss.ANSIColor(1) // red
	tagColor    = lipgloss.ANSIColor(3) // yellow, bolded to separate it from the sha
)

// Styles are built per call, never cached — see the note in diffrender.go: a cached
// lipgloss.Style holds the color it was built with, so a theme switch wouldn't repaint.

func shaStyle() lipgloss.Style { return lipgloss.NewStyle().Foreground(shaColor) }

func refStyle(k repo.RefKind) lipgloss.Style {
	st := lipgloss.NewStyle().Bold(true)
	switch k {
	case repo.RefHead:
		return st.Foreground(headColor)
	case repo.RefRemote:
		return st.Foreground(remoteColor)
	case repo.RefTag:
		return st.Foreground(tagColor)
	default:
		return st.Foreground(branchColor)
	}
}

// ---------- one line ----------

// renderOneline is one commit per row: abbreviated hash, decorations, subject. Nothing else
// fits on a row and stays readable, which is the whole point of the mode.
func renderOneline(commits []repo.Commit, truncated bool, width int, wrap bool) string {
	rows := make([]string, 0, len(commits)+2)
	for _, c := range commits {
		row := shaStyle().Render(c.Short) + " "
		if refs := renderRefs(c.Refs); refs != "" {
			row += refs + " "
		}
		row += c.Subject
		rows = append(rows, fit(row, width, wrap))
	}
	return strings.Join(append(rows, truncNote(len(commits), truncated, width, wrap)...), "\n")
}

// ---------- standard ----------

// renderStandard is git's default block, in git's order: commit and refs, merge parents,
// author and date, message indented four.
func renderStandard(commits []repo.Commit, truncated bool, width int, wrap bool) string {
	var rows []string
	for i, c := range commits {
		if i > 0 {
			rows = append(rows, "") // air between blocks; without it they read as one wall
		}

		head := shaStyle().Render("commit "+c.Hash) + " "
		if refs := renderRefs(c.Refs); refs != "" {
			head += refs
		}
		rows = append(rows, fit(strings.TrimRight(head, " "), width, wrap))

		if c.Merge() {
			rows = append(rows, metaStyle().Render(fit("Merge: "+mergeParents(c.Parents), width, wrap)))
		}
		rows = append(rows, metaStyle().Render(fit(fmt.Sprintf("Author: %s <%s>", c.Author, c.Email), width, wrap)))
		rows = append(rows, metaStyle().Render(fit("Date:   "+c.Date, width, wrap)))
		rows = append(rows, "")

		// Fitted to the width the indent leaves, then indented — so a wrapped body's
		// continuation rows line up under the message rather than under the headers.
		msg := c.Subject
		if c.Body != "" {
			msg += "\n\n" + c.Body
		}
		for _, line := range strings.Split(msg, "\n") {
			rows = append(rows, core.IndentLines(fit(line, width-len(msgIndent), wrap), msgIndent))
		}
	}
	return strings.Join(append(rows, truncNote(len(commits), truncated, width, wrap)...), "\n")
}

// mergeParents is the "Merge: abc1234 def5678" line's parents, abbreviated the way git
// abbreviates them there.
func mergeParents(parents []string) string {
	short := make([]string, len(parents))
	for i, p := range parents {
		short[i] = shortHash(p)
	}
	return strings.Join(short, " ")
}

// shortHashLen shortens the full parent hashes on the Merge line.
const shortHashLen = 7

func shortHash(h string) string {
	if len(h) > shortHashLen {
		return h[:shortHashLen]
	}
	return h
}

// ---------- shared ----------

// renderRefs renders "(HEAD -> main, tag: v1.0.0, origin/main)", each ref colored; empty
// when there are none.
func renderRefs(refs []repo.Ref) string {
	if len(refs) == 0 {
		return ""
	}
	parts := make([]string, 0, len(refs))
	for _, r := range refs {
		switch {
		case r.Kind == repo.RefHead && r.Name == "":
			parts = append(parts, refStyle(repo.RefHead).Render("HEAD"))
		case r.Kind == repo.RefHead:
			// HEAD in its color, the branch it points at in the branch color — the arrow
			// is what makes the pair one decoration, so it's rendered as one part.
			parts = append(parts, refStyle(repo.RefHead).Render("HEAD -> ")+
				refStyle(repo.RefBranch).Render(r.Name))
		case r.Kind == repo.RefTag:
			parts = append(parts, refStyle(repo.RefTag).Render("tag: "+r.Name))
		default:
			parts = append(parts, refStyle(r.Kind).Render(r.Name))
		}
	}
	sep := metaStyle().Render(", ")
	paren := metaStyle()
	return paren.Render("(") + strings.Join(parts, sep) + paren.Render(")")
}

// truncNote says when the capture hit its limit, since a cut log looks like a short
// history.
func truncNote(n int, truncated bool, width int, wrap bool) []string {
	if !truncated {
		return nil
	}
	note := fmt.Sprintf("… showing the %d most recent commits", n)
	return []string{"", metaStyle().Render(fit(note, width, wrap))}
}
