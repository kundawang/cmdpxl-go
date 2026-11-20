package main

import (
	"image"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
)

const (
	simWidth  = 80
	simHeight = 25
)

// newSimulationEditor builds a CmdPxl driven by an in-memory simulation
// screen, sized before Run() starts so the first Show() delivers a resize
// event and the image box is initialized exactly as in a real terminal.
func newSimulationEditor(t *testing.T, w, h, maxHistory int) (*CmdPxl, tcell.SimulationScreen) {
	t.Helper()
	m, err := createImage(itoa(w) + "," + itoa(h))
	if err != nil {
		t.Fatalf("createImage: %v", err)
	}
	sim := tcell.NewSimulationScreen("UTF-8")
	// Init sets up the encoder and event channel; Run() skips Init when a
	// screen is injected, so the test must initialize it explicitly.
	if err := sim.Init(); err != nil {
		t.Fatalf("simulation Init: %v", err)
	}
	sim.SetSize(simWidth, simHeight)
	// A real terminal delivers a resize event at startup; post it
	// explicitly so the editor builds its image box before the first key.
	if err := sim.PostEvent(tcell.NewEventResize(simWidth, simHeight)); err != nil {
		t.Fatalf("post resize: %v", err)
	}
	c := NewCmdPxl("sim.png", m, func(string, image.Image) error { return nil },
		WithMaxHistory(maxHistory), withScreen(sim))
	return c, sim
}

type editorHarness struct {
	t    *testing.T
	sim  tcell.SimulationScreen
	done chan struct{}
}

// wait blocks until Run() has exited, failing the test on timeout.
func (s *editorHarness) wait() {
	select {
	case <-s.done:
	case <-time.After(2 * time.Second):
		s.t.Fatal("editor did not shut down within 2s")
	}
}

// quit drives the real exit key sequence through the event loop: Escape
// exits immediately on a clean canvas, and otherwise opens the
// confirmation which 'y' accepts (saving and exiting). It then waits for
// the loop to terminate.
func (s *editorHarness) quit() {
	injectNamed(s.sim, tcell.KeyEscape)
	injectKey(s.sim, 'y')
	s.wait()
}

// runEditor pumps the event loop in a goroutine and returns a harness
// whose quit() terminates the loop through synthetic key events.
func runEditor(t *testing.T, c *CmdPxl) *editorHarness {
	t.Helper()
	h := &editorHarness{t: t, sim: c.s.(tcell.SimulationScreen), done: make(chan struct{})}
	go func() {
		_ = c.Run()
		close(h.done)
	}()
	return h
}

func injectKey(sim tcell.SimulationScreen, r rune) {
	sim.InjectKey(tcell.KeyRune, r, tcell.ModNone)
}

func injectNamed(sim tcell.SimulationScreen, k tcell.Key) {
	sim.InjectKey(k, 0, tcell.ModNone)
}

// waitFor polls the simulated screen until it contains want, failing the
// test after a timeout. SimulationScreen access is internally
// synchronized, so reading from the test goroutine is race-free.
func waitFor(t *testing.T, sim tcell.SimulationScreen, want string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if screenContains(sim, want) {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for screen text %q\nscreen:\n%s", want, screenDump(sim))
}

// simScreenView reaches the unexported simscreen fields needed for a
// race-free screen snapshot. SimulationScreen.GetContents returns the live
// front buffer without holding its lock, so the fields are read directly
// under the embedded mutex via unsafe.
type simScreenView struct {
	mu    *sync.Mutex
	front *[]tcell.SimCell
	physw *int
	physh *int
}

func simView(sim tcell.SimulationScreen) simScreenView {
	v := reflect.ValueOf(sim).Elem()
	return simScreenView{
		mu:    (*sync.Mutex)(v.FieldByName("Mutex").Addr().UnsafePointer()),
		front: (*[]tcell.SimCell)(v.FieldByName("front").Addr().UnsafePointer()),
		physw: (*int)(v.FieldByName("physw").Addr().UnsafePointer()),
		physh: (*int)(v.FieldByName("physh").Addr().UnsafePointer()),
	}
}

// snapshot returns an independent copy of the rendered cells under the
// simulation lock, so an editor-goroutine Show() cannot race the read.
// TryLock is used because the editor goroutine may be mid-render holding
// this mutex; a failed attempt just yields and the caller retries, which
// avoids inverting the lock order and deadlocking.
func (v simScreenView) snapshot() ([]tcell.SimCell, int, int, bool) {
	if !v.mu.TryLock() {
		return nil, 0, 0, false
	}
	w, h := *v.physw, *v.physh
	snapshot := make([]tcell.SimCell, w*h)
	copy(snapshot, *v.front)
	v.mu.Unlock()
	return snapshot, w, h, true
}

func screenContains(sim tcell.SimulationScreen, want string) bool {
	return strings.Contains(screenDump(sim), want)
}

func screenDump(sim tcell.SimulationScreen) string {
	snapshot, w, h, ok := simView(sim).snapshot()
	if !ok {
		return ""
	}
	var b strings.Builder
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			cell := snapshot[y*w+x]
			if len(cell.Runes) > 0 {
				b.WriteRune(cell.Runes[0])
			} else {
				b.WriteRune(' ')
			}
		}
		b.WriteRune('\n')
	}
	return b.String()
}

// pixelStyle returns the rendered style of canvas pixel (x,y), accounting
// for the image box border and the two-column pixel width.
func pixelStyle(c *CmdPxl, sim tcell.SimulationScreen, x, y int) (tcell.Style, bool) {
	snapshot, w, _, ok := simView(sim).snapshot()
	if !ok {
		return tcell.StyleDefault, false
	}
	box := c.imageBox
	col := box.Min.X + borderSize + x*2
	row := box.Min.Y + borderSize + y
	return snapshot[row*w+col].Style, true
}

// pixelBackground returns the rendered background color of canvas pixel
// (x,y). In tcell v2.4 Style is a value type, so colors are read through
// Decompose rather than a Background getter. The bool is false when the
// screen could not be sampled because the editor was mid-render.
func pixelBackground(c *CmdPxl, sim tcell.SimulationScreen, x, y int) (tcell.Color, bool) {
	style, ok := pixelStyle(c, sim, x, y)
	if !ok {
		return tcell.ColorDefault, false
	}
	_, bg, _ := style.Decompose()
	return bg, true
}

// waitForBackground polls until the rendered background of pixel (x,y)
// equals want, retrying samples taken while the editor holds the screen
// lock.
func waitForBackground(t *testing.T, c *CmdPxl, sim tcell.SimulationScreen, x, y int, want tcell.Color) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if got, ok := pixelBackground(c, sim, x, y); ok && got == want {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	got, _ := pixelBackground(c, sim, x, y)
	t.Fatalf("pixel %d,%d background = %v, want %v", x, y, got, want)
}

func Test_Simulation_BrushUndoRedoRenderedPixelsAndStatus(t *testing.T) {
	c, sim := newSimulationEditor(t, 5, 5, defaultMaxHistory)
	h := runEditor(t, c)
	defer h.quit()

	waitFor(t, sim, "CMDPXL-GO")

	// Brush action on the cursor pixel (0,0).
	injectKey(sim, 'e')
	waitFor(t, sim, statusDrawn)
	penBg := tcell.FromImageColor(c.penColor.c)
	waitForBackground(t, c, sim, 0, 0, penBg)
	waitFor(t, sim, "undo 1/"+itoa(defaultMaxHistory)+" redo 0")

	// Undo: the cell returns to the base background and the prompt flips.
	injectKey(sim, 'z')
	waitFor(t, sim, statusUndo)
	baseBg := tcell.FromImageColor(c.m.Image.At(0, 0))
	waitForBackground(t, c, sim, 0, 0, baseBg)
	waitFor(t, sim, "undo 0/"+itoa(defaultMaxHistory)+" redo 1")

	// Redo: the pen color returns and the redo counter drains.
	injectKey(sim, 'y')
	waitFor(t, sim, statusRedo)
	waitForBackground(t, c, sim, 0, 0, penBg)
	waitFor(t, sim, "redo 0")

	// The redone edit makes the canvas dirty again, so escape opens the
	// confirmation rather than exiting; decline it to end the loop cleanly.
	injectNamed(sim, tcell.KeyEscape)
	waitFor(t, sim, "Do you want to exit? [y/n]")
	injectKey(sim, 'n')
}

func Test_Simulation_FillUndoAndNoopPrompts(t *testing.T) {
	c, sim := newSimulationEditor(t, 6, 6, defaultMaxHistory)
	h := runEditor(t, c)
	defer h.quit()

	waitFor(t, sim, "CMDPXL-GO")

	// One fill action covers the whole canvas and renders as one step.
	injectKey(sim, 'f')
	waitFor(t, sim, statusFillPrefix+" 36 pixel(s)")
	penBg := tcell.FromImageColor(c.penColor.c)
	for y := 0; y < 6; y++ {
		for x := 0; x < 6; x++ {
			waitForBackground(t, c, sim, x, y, penBg)
		}
	}
	waitFor(t, sim, "undo 1/")

	// Repeating the fill with the same color is an explicit no-op prompt.
	injectKey(sim, 'f')
	waitFor(t, sim, statusFillNoop)

	// A single undo restores every rendered pixel.
	injectKey(sim, 'z')
	waitFor(t, sim, statusUndo)
	baseBg := tcell.FromImageColor(c.m.Image.At(0, 0))
	waitForBackground(t, c, sim, 5, 5, baseBg)
}

func Test_Simulation_EmptyHistoryPrompts(t *testing.T) {
	c, sim := newSimulationEditor(t, 4, 4, defaultMaxHistory)
	h := runEditor(t, c)
	defer h.quit()

	waitFor(t, sim, "CMDPXL-GO")
	waitFor(t, sim, "undo 0/"+itoa(defaultMaxHistory)+" redo 0")

	injectKey(sim, 'z')
	waitFor(t, sim, statusUndoUnavailable)
	injectKey(sim, 'y')
	waitFor(t, sim, statusRedoUnavailable)
}

func Test_Simulation_PanDoesNotEdit(t *testing.T) {
	c, sim := newSimulationEditor(t, 8, 8, defaultMaxHistory)
	h := runEditor(t, c)
	defer h.quit()

	waitFor(t, sim, "CMDPXL-GO")
	injectKey(sim, 'e')
	waitFor(t, sim, "undo 1/")

	// Arrow-key panning must leave the history counters and save state
	// untouched on the status line.
	injectNamed(sim, tcell.KeyRight)
	injectNamed(sim, tcell.KeyDown)
	waitFor(t, sim, "undo 1/"+itoa(defaultMaxHistory)+" redo 0")
	waitFor(t, sim, statusDirty)
}

func Test_Simulation_UnsavedExitConfirmationFlow(t *testing.T) {
	c, sim := newSimulationEditor(t, 5, 5, defaultMaxHistory)
	saved := false
	c.saveImage = func(string, image.Image) error {
		saved = true
		return nil
	}
	h := runEditor(t, c)

	waitFor(t, sim, "CMDPXL-GO")
	injectKey(sim, 'e')
	waitFor(t, sim, statusDrawn)

	// Dirty exit opens the confirmation instead of quitting.
	injectKey(sim, 'x')
	waitFor(t, sim, "Do you want to exit? [y/n]")

	// Decline returns to the editor with pixels intact.
	injectKey(sim, 'n')
	// Wait until the next frame has removed the dialog instead of reading
	// the previous frame immediately.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if !screenContains(sim, "Do you want to exit?") {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	if screenContains(sim, "Do you want to exit?") {
		t.Fatal("declining exit should dismiss the confirmation dialog")
	}
	penBg := tcell.FromImageColor(c.penColor.c)
	waitForBackground(t, c, sim, 0, 0, penBg)

	// Confirm saves and terminates the loop.
	injectKey(sim, 'x')
	waitFor(t, sim, "Do you want to exit? [y/n]")
	injectKey(sim, 'y')
	h.wait()
	if !saved {
		t.Error("confirming exit did not invoke the save callback")
	}
	if c.history.isDirty() {
		t.Error("saved canvas should be clean on exit")
	}
}

func Test_Simulation_CleanExitIsImmediate(t *testing.T) {
	c, sim := newSimulationEditor(t, 5, 5, defaultMaxHistory)
	h := runEditor(t, c)

	waitFor(t, sim, "CMDPXL-GO")
	waitFor(t, sim, statusClean)
	injectNamed(sim, tcell.KeyEscape)
	h.wait() // returns only if Run() exited; a confirmation would time out
	if c.currentState == stateQuit {
		t.Error("a clean canvas must exit without a confirmation dialog")
	}
}
