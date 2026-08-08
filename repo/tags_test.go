package repo

import (
	"reflect"
	"testing"
)

func TestParseLsRemoteTags(t *testing.T) {
	for _, tc := range []struct {
		name string
		out  string
		want []string
	}{
		{"empty", "", nil},
		{
			"lightweight tag",
			"a94a8fe5ccb19ba61c4c0873d391e987982fbbd3\trefs/tags/v0.9.0",
			[]string{"v0.9.0"},
		},
		{
			// An annotated tag advertises two lines; the peeled ^{} line names the
			// commit, not another tag, so it must not double the entry.
			"annotated tag drops the peeled line",
			"6dcb09b5b57875f334f61aebed695e2e4193db5e\trefs/tags/v1.0.0\n" +
				"a94a8fe5ccb19ba61c4c0873d391e987982fbbd3\trefs/tags/v1.0.0^{}",
			[]string{"v1.0.0"},
		},
		{
			// Newest first, version-aware: v1.10.0 outranks v1.9.9, and a
			// non-semver name falls below any version.
			"unsorted input sorted newest first",
			"aaaaaaa\trefs/tags/v2.0.0\nbbbbbbb\trefs/tags/release-1\nccccccc\trefs/tags/v1.0.0\nddddddd\trefs/tags/v1.10.0\neeeeeee\trefs/tags/v1.9.9",
			[]string{"v2.0.0", "v1.10.0", "v1.9.9", "v1.0.0", "release-1"},
		},
		{
			"duplicate refs deduped",
			"aaaaaaa\trefs/tags/v1.0.0\nbbbbbbb\trefs/tags/v1.0.0",
			[]string{"v1.0.0"},
		},
		{
			"garbage lines skipped",
			"not a ref line\n\nrefs/tags/missing-sha\naaaaaaa\trefs/heads/not-a-tag\n" +
				"bbbbbbb\trefs/tags/v1.0.0",
			[]string{"v1.0.0"},
		},
	} {
		if got := parseLsRemoteTags(tc.out); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("parseLsRemoteTags(%s) = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestCompareVersionTags(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want int
	}{
		{"v1.0.0", "v1.0.0", 0},
		// Numeric runs compare by value, not lexically.
		{"v1.10.0", "v1.9.9", 1},
		{"v2.0.0", "v1.9.9", 1},
		// A numeric run sorts before a literal one.
		{"1.0.1", "v1.0.1", -1},
		// A shared prefix loses to the longer tag.
		{"1.0.1", "1.0.1.1", -1},
		// Literal runs compare lexically.
		{"release-1", "v1.0.0", -1},
		// Leading zeros don't change the value.
		{"v1.01.0", "v1.1.0", 0},
	} {
		if got := compareVersionTags(tc.a, tc.b); got != tc.want {
			t.Errorf("compareVersionTags(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
		if got := compareVersionTags(tc.b, tc.a); got != -tc.want {
			t.Errorf("compareVersionTags(%q, %q) = %d, want %d", tc.b, tc.a, got, -tc.want)
		}
	}
}

func TestNextTag(t *testing.T) {
	for _, tc := range []struct {
		name          string
		local, remote []string
		want          string
	}{
		{"no tags", nil, nil, ""},
		{"no semver tags", []string{"release-1", "nightly"}, nil, ""},
		{"patch bump", []string{"1.0.1"}, nil, "1.0.2"},
		{"v prefix preserved", []string{"v2.3.9"}, nil, "v2.3.10"},
		{"patch rolls past 9", []string{"v1.0.9"}, nil, "v1.0.10"},
		{
			"remote higher than local wins",
			[]string{"v1.0.5"}, []string{"v1.10.0"},
			"v1.10.1",
		},
		{
			"local higher than remote wins",
			[]string{"2.0.0"}, []string{"v1.9.9"},
			"2.0.1",
		},
		{
			"non-semver tags ignored",
			[]string{"latest", "1.0.1"}, []string{"1.0"},
			"1.0.2",
		},
	} {
		if got := NextTag(tc.local, tc.remote); got != tc.want {
			t.Errorf("NextTag(%s) = %q, want %q", tc.name, got, tc.want)
		}
	}
}
