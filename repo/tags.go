package repo

import (
	"context"
	"fmt"
	"os/exec"
	"slices"
	"strconv"
	"strings"
)

// LocalTags lists dir's tags, newest first by creator date. A read-only probe in
// the CurrentBranch mold: any error (not a checkout, git unreadable) yields nil,
// so a caller renders "no local tags" without having to distinguish failure from
// empty.
func LocalTags(dir string) []string {
	out := gitOutput(dir, "tag", "--list", "--sort=-creatordate")
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

// RemoteTags lists the tags on dir's origin remote, newest first. It reads
// the remote's ref advertisement directly (ls-remote) rather than the local
// refs, because local refs can't answer "which tags does the remote have": a
// fetch merges the remote's tags into the local namespace, where they become
// indistinguishable from tags that only ever existed locally — and asking the
// question shouldn't mutate anything anyway. It's a network call, so like
// GitFetch it takes a ctx and folds git's stderr into the error.
func RemoteTags(ctx context.Context, dir string) ([]string, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", dir, "ls-remote", "--tags", "origin")
	cmd.Env = gitEnv()
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			return nil, err
		}
		return nil, fmt.Errorf("%w: %s", err, msg)
	}
	return parseLsRemoteTags(string(out)), nil
}

// parseLsRemoteTags turns `git ls-remote --tags` output into a deduped list of
// tag names, ordered newest-first to match LocalTags. Each line is
// "<sha>\trefs/tags/<name>"; an annotated tag also lists a
// "<sha>\trefs/tags/<name>^{}" line for the commit it peels to, which is dropped
// — the tag is already named by its own line. Malformed lines are skipped rather
// than failing the whole listing: a partial read shouldn't poison the tags that
// did parse.
//
// "Newest" is version-descending (see compareVersionTags), not date-descending:
// ls-remote's advertisement carries no dates, so local's creatordate order is
// unreachable here — for the semver release tags this screen is built around,
// the two orders agree in practice.
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

// compareVersionTags orders two tags the way a release listing wants: split
// each into alternating literal and numeric runs ("v1.10.2" → "v", 1, ".", 10,
// ".", 2) and compare run by run — numeric runs by value (so v1.10.0 sorts
// above v1.9.9, where plain string order inverts them), literal runs lexically.
// A numeric run sorts before a literal one ("1.0.1" before "v1.0.1"), and on a
// shared prefix the longer tag wins ("1.0.1" before "1.0.1.1"). Returns
// -1/0/+1 like strings.Compare.
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

// compareDigitRuns orders two all-digit runs by numeric value without parsing
// them (a run can be longer than an int holds): leading zeros are insignificant,
// then more digits means a bigger number, then same-length runs order lexically.
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

// NextTag suggests the tag after the highest semver tag across local and
// remote: the max of the two lists with its patch component bumped —
// "1.0.1" → "1.0.2", "v2.3.9" → "v2.3.10". Only strict three-part versions
// (optional "v" prefix) count; anything else (release-1, nightly) isn't a
// release tag this can increment. The max compares the version triple only, so
// a bare "2.0.0" outranks a "v1.9.9" — the display order's numeric-before-
// literal tiebreak (compareVersionTags) would invert them. "" means neither
// list held one, and the caller leaves its form empty.
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

// GitTag creates a lightweight tag on the current commit. It refuses an
// existing name (git's own "already exists"), which is the backstop for a UI
// that validated against a stale listing.
func GitTag(ctx context.Context, dir, name string, report Reporter) error {
	report("%s", "$ git tag "+name)
	return GitStream(ctx, dir, report, "tag", name)
}

// GitPushTag pushes one tag to origin: `git push origin <name>`. Pushing a tag
// origin already has fails with git's own rejection in the log — which is the
// answer when the "not on remote" diff behind the picker was stale.
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
