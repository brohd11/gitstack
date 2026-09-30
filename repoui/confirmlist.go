package repoui

import (
	"github.com/brohd11/bubblestack/components"
	"github.com/brohd11/bubblestack/core"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
)

// confirmPanel is a y/n confirm whose body scrolls: a ScrollContainer that also answers
// Keys.Yes and Keys.No. A DialogScreen neither scrolls nor clips, so a long list there
// would push the chrome off the terminal.
type confirmPanel struct {
	*components.ScrollContainer
	onYes func(*core.Shared) core.Action
}

var _ components.Panel = (*confirmPanel)(nil)
var _ components.Focusable = (*confirmPanel)(nil)
var _ components.PanelUpdater = (*confirmPanel)(nil)

func (p *confirmPanel) UpdatePanel(sh *core.Shared, msg tea.Msg) (core.Action, bool) {
	if km, ok := msg.(tea.KeyPressMsg); ok {
		switch k := km.String(); {
		case core.MatchKey(k, core.Keys.Yes):
			return p.onYes(sh), true
		case core.MatchKey(k, core.Keys.No):
			return core.Pop(), true
		}
	}
	return p.ScrollContainer.UpdatePanel(sh, msg)
}

// newListConfirm builds a full-screen confirm: body's title is the pane legend and its
// lines scroll. body is re-read whenever a git op reports back (RefreshMsg,
// RepoRefreshMsg), so the list never shows state that has since changed.
func newListConfirm(sh *core.Shared, crumb string, body func(*core.Shared) (string, []string), onYes func(*core.Shared) core.Action) *components.ModularScreen {
	panel := &confirmPanel{ScrollContainer: components.NewScrollContainer(""), onYes: onYes}
	fill := func(sh *core.Shared) {
		title, lines := body(sh)
		panel.SetTitle(title)
		panel.SetLines(lines)
	}
	fill(sh)
	return components.NewModularScreen(
		[][]components.Slot{{{Panel: panel, Weight: 1, ExpandV: true}}},
		components.ModularOpts{
			Crumb:     crumb,
			ColWidths: []int{0}, // one flex column: full width
			// The screen already hints "back", which n/esc match here.
			Help: []key.Binding{core.Hint("confirm", core.Keys.Yes)},
			Refresh: func(sh *core.Shared, payload any) bool {
				switch payload.(type) {
				case RefreshMsg, RepoRefreshMsg:
					fill(sh)
					return true
				}
				return false
			},
		},
	)
}
