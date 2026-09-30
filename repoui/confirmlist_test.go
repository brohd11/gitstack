package repoui

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/brohd11/bubblestack/core"
)

func TestListConfirmKeys(t *testing.T) {
	sh := core.NewShared(&refreshRecorder{})
	var lines []string
	for i := range 30 {
		lines = append(lines, fmt.Sprintf("row%02d", i))
	}
	for _, tc := range []struct {
		key       string
		confirmed bool
		popped    bool
	}{
		{"y", true, false},
		{"enter", true, false},
		{"n", false, true},
		{"esc", false, true},
		{"down", false, false},
	} {
		t.Run(tc.key, func(t *testing.T) {
			confirmed := false
			s := newListConfirm(sh, "Confirm",
				func(*core.Shared) (string, []string) { return "Title", lines },
				func(*core.Shared) core.Action { confirmed = true; return core.Action{} })
			s.SetSize(sh, 60, 8)
			before := s.View(sh)
			_, act := s.Update(sh, keyMsg(tc.key))
			if confirmed != tc.confirmed {
				t.Errorf("confirmed = %v, want %v", confirmed, tc.confirmed)
			}
			if popped := reflect.DeepEqual(act.Msg, core.Pop().Msg); popped != tc.popped {
				t.Errorf("popped = %v, want %v", popped, tc.popped)
			}
			if tc.key == "down" && s.View(sh) == before {
				t.Error("down should scroll the list")
			}
		})
	}
}

func TestListConfirmShowsTitle(t *testing.T) {
	sh := core.NewShared(&refreshRecorder{})
	s := newListConfirm(sh, "Confirm",
		func(*core.Shared) (string, []string) { return "Pull 3 repo(s)", []string{"a", "b", "c"} },
		func(*core.Shared) core.Action { return core.Action{} })
	s.SetSize(sh, 60, 10)
	if v := s.View(sh); !strings.Contains(v, "Pull 3 repo(s)") {
		t.Errorf("legend missing:\n%s", v)
	}
}
