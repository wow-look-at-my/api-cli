// A <tml> leaf under a watch runs as a tml program rather than through the repaint loop in watch.go.

package main

import (
	"bytes"
	"embed"
	"errors"
	"io/fs"
	"time"

	"github.com/wow-look-at-my/tml"
	"github.com/wow-look-at-my/tml/sema"
)

//go:embed ui/frame
var frameUI embed.FS

// tmlTickMsg asks the model to run the leaf again.
type tmlTickMsg struct{}

type tmlModel struct {
	every         time.Duration
	body          func() error
	frame         string
	width, height int
	code          int
	// cancel stops the tick that waits.
	cancel func()

	// live draws the author's component, and frameView shows the text of a run that gave it nothing to draw.
	live      *tmlLive
	frameView *tml.View
	program   *tml.Program
}

// runTMLProgram runs the leaf every interval and draws its screen, until the
// user quits.
func runTMLProgram(every time.Duration, body func() error, decl *TML) error {
	return runTMLWith(every, body, decl, tml.ProgramOptions{NoMouse: !decl.interactive()})
}

// runTMLWith is runTMLProgram with the program's options. A nil decl shows each run's printed frame as text.
func runTMLWith(every time.Duration, body func() error, decl *TML, opts tml.ProgramOptions) error {
	sub, err := fs.Sub(frameUI, "ui/frame")
	if err != nil {
		return err
	}
	view, err := tml.Load(sub, "Frame.tml", tml.Options{Dark: true})
	if err != nil {
		return err
	}
	m := &tmlModel{every: every, body: body, frameView: view}
	if decl != nil {
		if m.live, err = newTMLLive(decl); err != nil {
			return err
		}
	}
	// The program opens on the frame. The component takes over once a run gives it its props.
	program, err := tml.NewProgram(view, m, opts)
	if err != nil {
		return err
	}
	err = program.Run()
	if m.cancel != nil {
		m.cancel()
	}
	if errors.Is(err, tml.ErrInterrupted) {
		m.code, err = interruptExitCode, nil
	}
	if err != nil {
		return err
	}
	if m.code != 0 {
		exitCode = m.code
	}
	return nil
}

// Props answers for the view on screen: the component's props, or the last frame the leaf printed.
func (m *tmlModel) Props() tml.Props {
	if m.live != nil && m.program != nil && m.program.View() == m.live.view {
		return m.live.props
	}
	return tml.Props{"frame": sema.StringValue(m.frame)}
}

// Update takes one message from the program. A resize runs the leaf again at the new size.
func (m *tmlModel) Update(p *tml.Program, msg tml.Msg) bool {
	m.program = p
	switch msg := msg.(type) {
	case tml.Resize:
		m.width, m.height = msg.Width, msg.Height
		m.refresh(p)
		return true
	case tml.Key:
		switch msg.String() {
		case "ctrl+c":
			m.code = interruptExitCode
			p.Quit()
		case "q", "esc":
			p.Quit()
		case "r":
			m.refresh(p)
		default:
			if m.live != nil && m.live.key(msg.String(), m.height) {
				m.redraw(p)
				return true
			}
			return false
		}
		return true
	case tml.Event:
		if m.live == nil {
			return false
		}
		changed, refresh := m.live.event(msg)
		switch {
		case refresh:
			m.refresh(p)
		case changed:
			m.redraw(p)
		}
		return changed || refresh
	case tml.RepaintMsg, tmlTickMsg:
		m.refresh(p)
		return true
	}
	return false
}

// redraw draws the component again from the last good run, after a change of state.
func (m *tmlModel) redraw(p *tml.Program) {
	if err := m.live.redraw(); err != nil {
		m.fail(p, failure("", err))
	}
}

// fail shows text in place of the component until a run gives it something to draw.
func (m *tmlModel) fail(p *tml.Program, text string) {
	m.frame = text
	if p.View() != m.frameView {
		p.SetView(m.frameView)
	}
}

// refresh runs the leaf into a buffer, keeps the result as the frame, and sets
// the next tick. The run stays on the program's goroutine, because the leaf
// writes through the package's output channels and the runs cannot share them.
func (m *tmlModel) refresh(p *tml.Program) {
	var buf bytes.Buffer
	width, height := m.width, m.height
	if width <= 0 || height <= 0 {
		width, height = 80, 24
	}
	prev := ttyOverride
	ttyOverride = &termSize{width: width, height: height}
	tapped := false
	if m.live != nil {
		tmlTap = func(parsed any, ctx map[string]any) {
			m.live.parsed, m.live.ctx, tapped = parsed, ctx, true
		}
	}
	err := captureInto(&buf, m.body)
	tmlTap = nil
	ttyOverride = prev
	if m.cancel != nil {
		m.cancel()
	}
	m.cancel = p.After(m.every, tmlTickMsg{})

	if m.live == nil {
		if err != nil {
			buf.WriteString("\nerror: " + err.Error() + "\n")
		}
		m.frame = buf.String()
		return
	}
	if err != nil || !tapped {
		m.fail(p, failure(buf.String(), err))
		return
	}
	if rerr := m.live.redraw(); rerr != nil {
		m.fail(p, failure("", rerr))
		return
	}
	if p.View() != m.live.view {
		p.SetView(m.live.view)
	}
}
