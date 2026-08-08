package repo

import (
	"context"
	"fmt"
	"os/exec"
	"sort"
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

// RemoteTags lists the tags on dir's origin remote, sorted ascending. It reads
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

// parseLsRemoteTags turns `git ls-remote --tags` output into a sorted, deduped
// list of tag names. Each line is "<sha>\trefs/tags/<name>"; an annotated tag
// also lists a "<sha>\trefs/tags/<name>^{}" line for the commit it peels to,
// which is dropped — the tag is already named by its own line. Malformed lines
// are skipped rather than failing the whole listing: a partial read shouldn't
// poison the tags that did parse.
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
	sort.Strings(tags)
	return tags
}
