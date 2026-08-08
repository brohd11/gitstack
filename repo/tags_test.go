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
			"unsorted input sorted ascending",
			"aaaaaaa\trefs/tags/v2.0.0\nbbbbbbb\trefs/tags/release-1\nccccccc\trefs/tags/v1.0.0",
			[]string{"release-1", "v1.0.0", "v2.0.0"},
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
