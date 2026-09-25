package repoui

import (
	"strings"

	"github.com/brohd11/bubblestack/core"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// pager is the viewport plumbing DiffScreen and LogScreen share: a titled, scrolling body
// that depends on the width, the wrap flag and a screen-owned mode. Re-renders keep the
// scroll position. (components.DocScreen re-renders on width alone and is not a
// core.Wrapper, so it cannot serve here.)
type pager struct {
	title string
	dir   string // the repo shown; enables the global Terminal key (DirLocator)
	wrap  bool   // w — fold long lines rather than truncate them (core.Wrapper)

	vp    viewport.Model
	width int // last laid-out width; -1 until the first SetSize

	body      func() string
	modeLabel func() string // "" leaves the mode out of the title bar
}

func newPager(title, dir string) pager {
	return pager{title: title, dir: dir, vp: viewport.New(), width: -1}
}

func (p *pager) Init(*core.Shared) tea.Cmd { return nil }

// LocateDir reports the repo shown, so the global Terminal key opens a terminal there.
func (p *pager) LocateDir() (string, bool) { return p.dir, p.dir != "" }

// ToggleWrap folds or truncates long lines (core.Wrapper — the router's w key).
func (p *pager) ToggleWrap() {
	p.wrap = !p.wrap
	p.rerender()
}

func (p *pager) Wrapped() bool { return p.wrap }

func (p *pager) SetSize(_ *core.Shared, width, bodyHeight int) {
	p.vp.SetWidth(width)
	p.vp.SetHeight(max(bodyHeight-lipgloss.Height(core.RenderTitleBar(p.titleBar())), 1))
	if width == p.width {
		return // a height-only change leaves the content alone
	}
	p.width = width
	p.rerender()
}

// rerender rebuilds the body at the current width and keeps the scroll position;
// SetYOffset re-clamps it when the new render is shorter.
func (p *pager) rerender() {
	if p.width < 0 {
		return // not laid out yet; SetSize will render
	}
	y := p.vp.YOffset()
	p.vp.SetContent(p.body())
	p.vp.SetYOffset(y)
}

// titleBar is "title · mode · wrap", leaving out what does not apply.
func (p *pager) titleBar() string {
	parts := []string{p.title}
	if mode := p.modeLabel(); mode != "" {
		parts = append(parts, mode)
	}
	if p.wrap {
		parts = append(parts, "wrap")
	}
	return strings.Join(parts, " · ")
}

func (p *pager) View(*core.Shared) string { return core.WithTitle(p.titleBar(), p.vp.View()) }

// scroll hands msg to the viewport.
func (p *pager) scroll(msg tea.Msg) core.Action {
	var cmd tea.Cmd
	p.vp, cmd = p.vp.Update(msg)
	return core.Async(cmd)
}

// help is the sparse help bar: scroll, the screen's mode key, wrap, back. The terminal
// keys work here (DirLocator) but stay off the bar.
func (p *pager) help(sh *core.Shared, modeDesc string, mode key.Binding) string {
	return sh.BindingHelp([]key.Binding{
		core.Hint("scroll", core.Keys.Up, core.Keys.Down),
		core.Hint(modeDesc, mode),
		core.Hint("wrap", core.Keys.Wrap),
		core.Hint("back", core.Keys.Back),
	})
}
