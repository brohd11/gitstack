package repo

import (
	"errors"
	"testing"
)

func TestFetchLine(t *testing.T) {
	for _, tc := range []struct {
		name string
		r    FetchResult
		want string
	}{
		{"up to date", FetchResult{Name: "a"}, "[a] fetched · up to date"},
		{"behind", FetchResult{Name: "a", Sync: GitSync{Behind: 2}}, "[a] fetched · 2 behind"},
		{"ahead", FetchResult{Name: "a", Sync: GitSync{Ahead: 1}}, "[a] fetched · 1 ahead"},
		{"behind+ahead", FetchResult{Name: "a", Sync: GitSync{Behind: 2, Ahead: 1}}, "[a] fetched · 2 behind, 1 ahead"},
		{"error", FetchResult{Name: "a", Err: errors.New("boom")}, "[a] fetch failed: boom"},
	} {
		if got := FetchLine(tc.r); got != tc.want {
			t.Errorf("FetchLine(%s) = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestFetchSummary(t *testing.T) {
	fail := FetchResult{Name: "x", Err: errors.New("nope")}
	behind := FetchResult{Name: "y", Sync: GitSync{Behind: 3}}
	clean := FetchResult{Name: "z"}

	for _, tc := range []struct {
		name       string
		results    []FetchResult
		noun       string
		wantLine   string
		wantFailed bool
	}{
		{"empty", nil, "repo(s)", "fetched 0 repo(s)", false},
		{"all clean", []FetchResult{clean, clean}, "repo(s)", "fetched 2 repo(s)", false},
		{"noun carries through", []FetchResult{clean}, "git checkout(s)", "fetched 1 git checkout(s)", false},
		{"behind counted", []FetchResult{clean, behind}, "repo(s)", "fetched 2 repo(s) · 1 behind origin", false},
		{"failure excluded and flagged", []FetchResult{clean, fail}, "repo(s)", "fetched 1 repo(s) · 1 failed", true},
		{"behind and failure", []FetchResult{behind, fail}, "repo(s)", "fetched 1 repo(s) · 1 behind origin · 1 failed", true},
	} {
		line, failed := FetchSummary(tc.results, tc.noun)
		if line != tc.wantLine || failed != tc.wantFailed {
			t.Errorf("FetchSummary(%s) = (%q, %v), want (%q, %v)", tc.name, line, failed, tc.wantLine, tc.wantFailed)
		}
	}
}
