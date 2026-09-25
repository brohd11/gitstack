package repo

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// LocalTags lists dir's tags, newest first by creator date; nil on any error.
func LocalTags(dir string) []string {
	out := GitOutput(dir, "tag", "--list", "--sort=-creatordate")
	if out == "" {
		return nil
	}
	var tags []string
	for _, line := range strings.Split(out, "\n") {
		if t := strings.TrimSpace(line); t != "" {
			tags = append(tags, t)
		}
	}
	return tags
}

// RemoteTags lists origin's tags, newest first, via ls-remote: local refs cannot tell which
// tags the remote has, and asking should not mutate anything. A network call, so it takes
// ctx and returns errors.
func RemoteTags(ctx context.Context, dir string) ([]string, error) {
	out, err := runGitCtx(ctx, dir, "ls-remote", "--tags", "origin")
	if err != nil {
		return nil, err
	}
	return parseLsRemoteTags(out), nil
}

// parseLsRemoteTags turns `ls-remote --tags` output into deduped tag names, newest first.
// Peeled "^{}" lines are dropped and malformed lines skipped. Ordering is by version
// (compareVersionTags), since ls-remote has no dates.
func parseLsRemoteTags(out string) []string {
	seen := map[string]bool{}
	var tags []string
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Split(line, "\t")
		if len(fields) != 2 {
			continue
		}
		name, ok := strings.CutPrefix(fields[1], "refs/tags/")
		if !ok || strings.HasSuffix(name, "^{}") {
			continue
		}
		if !seen[name] {
			seen[name] = true
			tags = append(tags, name)
		}
	}
	slices.SortStableFunc(tags, func(a, b string) int {
		return -compareVersionTags(a, b) // newest first
	})
	return tags
}

// compareVersionTags compares tags by alternating literal and numeric runs ("v1.10.2" is
// "v", 1, ".", 10, ".", 2), numbers by value, literals lexically. A numeric run sorts before
// a literal one, and a longer tag wins on a shared prefix. Returns -1, 0 or +1.
func compareVersionTags(a, b string) int {
	ra, rb := versionRuns(a), versionRuns(b)
	for i := 0; i < len(ra) && i < len(rb); i++ {
		x, y := ra[i], rb[i]
		xIsNum, yIsNum := isDigits(x), isDigits(y)
		switch {
		case xIsNum && yIsNum:
			if c := compareDigitRuns(x, y); c != 0 {
				return c
			}
		case xIsNum != yIsNum:
			if xIsNum {
				return -1
			}
			return 1
		default:
			if c := strings.Compare(x, y); c != 0 {
				return c
			}
		}
	}
	switch {
	case len(ra) < len(rb):
		return -1
	case len(ra) > len(rb):
		return 1
	}
	return 0
}

// versionRuns splits s into alternating literal and numeric runs:
// "v1.10.2" → ["v", "1", ".", "10", ".", "2"].
func versionRuns(s string) []string {
	var runs []string
	start := 0
	for i := 1; i <= len(s); i++ {
		if i == len(s) || isDigit(s[i]) != isDigit(s[start]) {
			runs = append(runs, s[start:i])
			start = i
		}
	}
	return runs
}

// compareDigitRuns compares digit runs numerically without parsing (they may overflow
// int).
func compareDigitRuns(a, b string) int {
	a = strings.TrimLeft(a, "0")
	b = strings.TrimLeft(b, "0")
	switch {
	case len(a) < len(b):
		return -1
	case len(a) > len(b):
		return 1
	}
	return strings.Compare(a, b)
}

// isDigits reports whether s is non-empty and all ASCII digits.
func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if !isDigit(s[i]) {
			return false
		}
	}
	return true
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

// NextTag suggests the tag after the highest strict three-part version (optional "v")
// across local and remote, bumping the patch: "v2.3.9" → "v2.3.10". "" when neither has
// one.
func NextTag(local, remote []string) string {
	best := ""
	for _, tags := range [][]string{local, remote} {
		for _, t := range tags {
			if isSemverTag(t) && (best == "" || compareSemverTags(t, best) > 0) {
				best = t
			}
		}
	}
	if best == "" {
		return ""
	}
	prefix, num := splitTagPrefix(best)
	parts := strings.Split(num, ".")
	patch, _ := strconv.Atoi(parts[2]) // isSemverTag guaranteed the shape
	return fmt.Sprintf("%s%s.%s.%d", prefix, parts[0], parts[1], patch+1)
}

// compareSemverTags orders two isSemverTag-shaped tags by their version triple,
// ignoring any "v" prefix.
func compareSemverTags(a, b string) int {
	_, an := splitTagPrefix(a)
	_, bn := splitTagPrefix(b)
	ap, bp := strings.Split(an, "."), strings.Split(bn, ".")
	for i := range ap {
		if c := compareDigitRuns(ap[i], bp[i]); c != 0 {
			return c
		}
	}
	return 0
}

// isSemverTag reports whether t is a three-part dotted version with an optional
// "v" prefix: "1.0.1", "v2.3.9".
func isSemverTag(t string) bool {
	_, num := splitTagPrefix(t)
	parts := strings.Split(num, ".")
	if len(parts) != 3 {
		return false
	}
	for _, p := range parts {
		if !isDigits(p) {
			return false
		}
	}
	return true
}

// splitTagPrefix peels a leading "v" off a tag name, returning the prefix and
// the remainder.
func splitTagPrefix(t string) (prefix, num string) {
	if strings.HasPrefix(t, "v") {
		return "v", t[1:]
	}
	return "", t
}

// GitTag creates a lightweight tag on HEAD; an existing name fails.
func GitTag(ctx context.Context, dir, name string, report Reporter) error {
	report("%s", "$ git tag "+name)
	return GitStream(ctx, dir, report, "tag", name)
}

// GitPushTag pushes one tag to origin; git rejects one origin already has.
func GitPushTag(ctx context.Context, dir, name string, report Reporter) error {
	report("%s", "$ git push origin "+name)
	return GitStream(ctx, dir, report, "push", "origin", name)
}

// GitDeleteTag deletes a local tag. A tag that was never pushed is gone for
// good, which is why the UI confirms first; git itself has no undo here.
func GitDeleteTag(ctx context.Context, dir, name string, report Reporter) error {
	report("%s", "$ git tag -d "+name)
	return GitStream(ctx, dir, report, "tag", "-d", name)
}
