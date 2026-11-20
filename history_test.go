package main

import (
	"image"
	"image/color"
	"testing"

	"github.com/gdamore/tcell/v2"
)

func newTestCmdPxl(t *testing.T, w, h, maxHistory int) *CmdPxl {
	t.Helper()
	m, err := createImage(itoa(w) + "," + itoa(h))
	if err != nil {
		t.Fatalf("createImage: %v", err)
	}
	return NewCmdPxl("test.png", m, func(string, image.Image) error { return nil },
		WithMaxHistory(maxHistory))
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

func keyRune(r rune) *tcell.EventKey {
	return tcell.NewEventKey(tcell.KeyRune, r, tcell.ModNone)
}

func keyNamed(k tcell.Key) *tcell.EventKey {
	return tcell.NewEventKey(k, 0, tcell.ModNone)
}

func Test_History_EmptyUndoRedo(t *testing.T) {
	c := newTestCmdPxl(t, 5, 5, defaultMaxHistory)

	// No edits have been made: both stacks are empty and the canvas is
	// clean, so undo/redo must be explicit no-ops with a prompt.
	if c.history.isDirty() {
		t.Fatal("fresh canvas should not be dirty")
	}
	if c.history.canUndo() || c.history.canRedo() {
		t.Fatal("fresh canvas must have no undo or redo states")
	}

	c.handleKey(keyRune('z'))
	if c.statusMessage != statusUndoUnavailable {
		t.Errorf("undo on empty history: got status %q, want %q", c.statusMessage, statusUndoUnavailable)
	}
	c.handleKey(keyRune('y'))
	if c.statusMessage != statusRedoUnavailable {
		t.Errorf("redo on empty history: got status %q, want %q", c.statusMessage, statusRedoUnavailable)
	}
	if c.history.undoCount() != 0 || c.history.redoCount() != 0 {
		t.Error("empty history must report zero undo/redo steps")
	}
}

func Test_History_BrushUndoRedo(t *testing.T) {
	c := newTestCmdPxl(t, 5, 5, defaultMaxHistory)
	baseColor := c.m.At(0, 0)

	// One key press = one user action = one history entry.
	c.handleKey(keyRune('e'))
	pt := image.Pt(0, 0)
	if c.m.At(pt.X, pt.Y) != c.penColor.c {
		t.Fatal("pen press did not paint the cursor pixel")
	}
	if c.history.undoCount() != 1 || !c.history.isDirty() {
		t.Fatalf("after one brush action: undo=%d dirty=%v", c.history.undoCount(), c.history.isDirty())
	}

	// Undo restores the original pixel.
	c.handleKey(keyRune('z'))
	if c.m.At(pt.X, pt.Y) != baseColor {
		t.Error("undo did not restore the original pixel color")
	}
	if c.statusMessage != statusUndo {
		t.Errorf("undo status = %q, want %q", c.statusMessage, statusUndo)
	}
	if c.history.undoCount() != 0 || c.history.redoCount() != 1 {
		t.Errorf("after undo: undo=%d redo=%d, want 0,1", c.history.undoCount(), c.history.redoCount())
	}

	// Redo re-applies the pixel.
	c.handleKey(keyRune('y'))
	if c.m.At(pt.X, pt.Y) != c.penColor.c {
		t.Error("redo did not re-apply the painted pixel")
	}
	if c.statusMessage != statusRedo {
		t.Errorf("redo status = %q, want %q", c.statusMessage, statusRedo)
	}
}

func Test_History_NewActionClearsRedoBranch(t *testing.T) {
	c := newTestCmdPxl(t, 5, 5, defaultMaxHistory)

	// Two distinct brush actions, then undo both, leaving a redo branch.
	c.handleKey(keyRune('e'))
	c.handleKey(keyRune('d'))
	c.handleKey(keyRune('e'))
	c.handleKey(keyRune('z'))
	c.handleKey(keyRune('z'))
	if c.history.redoCount() != 2 {
		t.Fatalf("redoCount after two undos = %d, want 2", c.history.redoCount())
	}

	// A fresh action must discard the redo branch.
	c.handleKey(keyRune('s'))
	c.handleKey(keyRune('e'))
	if c.history.redoCount() != 0 {
		t.Errorf("new action did not clear redo branch: redo=%d", c.history.redoCount())
	}
	if c.history.undoCount() != 1 {
		t.Errorf("undoCount after branch-clearing action = %d, want 1", c.history.undoCount())
	}
	c.handleKey(keyRune('y'))
	if c.statusMessage != statusRedoUnavailable {
		t.Errorf("redo after cleared branch: status=%q want %q", c.statusMessage, statusRedoUnavailable)
	}
}

func Test_History_FillIsSingleUndoStep(t *testing.T) {
	c := newTestCmdPxl(t, 5, 5, defaultMaxHistory)

	// A fill over the empty canvas touches all 25 pixels but must be
	// recorded as exactly one user action.
	c.handleKey(keyRune('f'))
	changed := 0
	for y := 0; y < 5; y++ {
		for x := 0; x < 5; x++ {
			if c.m.At(x, y) == c.penColor.c {
				changed++
			}
		}
	}
	if changed != 25 {
		t.Fatalf("fill changed %d pixels, want 25", changed)
	}
	if c.history.undoCount() != 1 {
		t.Fatalf("fill produced %d undo steps, want 1", c.history.undoCount())
	}

	// One undo reverts the entire fill, all pixels back to the base color.
	c.handleKey(keyRune('z'))
	for y := 0; y < 5; y++ {
		for x := 0; x < 5; x++ {
			if c.m.At(x, y) == c.penColor.c {
				t.Fatalf("undo fill left pen color at %d,%d", x, y)
			}
		}
	}
	// Redo brings the whole fill back.
	c.handleKey(keyRune('y'))
	if c.m.At(4, 4) != c.penColor.c {
		t.Error("redo did not restore the filled region")
	}
}

func Test_History_NoopFillAndBrushAreNotRecorded(t *testing.T) {
	c := newTestCmdPxl(t, 5, 5, defaultMaxHistory)

	// Fill once, then fill again with the same pen color: no new state.
	c.handleKey(keyRune('f'))
	c.handleKey(keyRune('f'))
	if c.statusMessage != statusFillNoop {
		t.Errorf("repeat fill status = %q, want %q", c.statusMessage, statusFillNoop)
	}
	if c.history.undoCount() != 1 {
		t.Errorf("no-op fill added history: undo steps = %d, want 1", c.history.undoCount())
	}

	// Painting a pixel that already has the pen color is also a no-op:
	// after undoing the fill, press the pen twice on the same base pixel
	// and assert only one state is created.
	c.handleKey(keyRune('z'))
	c.handleKey(keyRune('e'))
	c.handleKey(keyRune('e'))
	if c.history.undoCount() != 1 {
		t.Errorf("repeated brush on identical pixel: undo steps = %d, want 1", c.history.undoCount())
	}
}

func Test_History_MemoryCapEvictsOldestStates(t *testing.T) {
	const cap = 3
	c := newTestCmdPxl(t, 10, 10, cap)

	// Five separate brush actions on five different pixels, but only the
	// cap most recent states may be retained.
	positions := []image.Point{
		image.Pt(0, 0), image.Pt(1, 0), image.Pt(2, 0), image.Pt(3, 0), image.Pt(4, 0),
	}
	for _, p := range positions {
		c.cursorX, c.cursorY = p.X, p.Y
		c.handleKey(keyRune('e'))
	}
	if got := len(c.history.states); got != cap {
		t.Fatalf("retained states = %d, want cap %d", got, cap)
	}
	if c.history.undoCount() != cap-1 {
		t.Errorf("undo steps at cap = %d, want %d", c.history.undoCount(), cap-1)
	}
	// Eviction only ever removes old states: all five pixels painted so
	// far remain visible on the canvas, and the most recent action is
	// always reachable via undo.
	for _, p := range positions {
		if c.m.At(p.X, p.Y) != c.penColor.c {
			t.Errorf("painted pixel %s missing from the current canvas", p)
		}
	}

	// Walking back through every retained state undoes the most recent
	// actions first; the two oldest actions are gone for good.
	c.handleKey(keyRune('z')) // reverts pixel (4,0)
	c.handleKey(keyRune('z')) // reverts pixel (3,0)
	if c.history.canUndo() {
		t.Error("undo should stop once the retained history is exhausted")
	}
	if c.m.At(4, 0) == c.penColor.c || c.m.At(3, 0) == c.penColor.c {
		t.Error("the two most recent actions were not undone")
	}
	// The evicted actions cannot be undone: those pixels stay painted.
	for _, p := range positions[:3] {
		if c.m.At(p.X, p.Y) != c.penColor.c {
			t.Errorf("painted pixel %s should survive after old states were evicted", p)
		}
	}

	// The canvas still differs from the saved base, so it stays dirty
	// even though no further undo is possible.
	if !c.history.isDirty() {
		t.Error("canvas should remain dirty after the oldest state was evicted")
	}
}

func Test_History_RepeatedBrushPressesStayBounded(t *testing.T) {
	const cap = 2
	c := newTestCmdPxl(t, 20, 1, cap)

	// Continuous drawing modeled as one key event per cell, far beyond
	// the configured memory cap.
	for x := 0; x < 20; x++ {
		c.cursorX = x
		c.handleKey(keyRune('e'))
	}
	if len(c.history.states) > cap {
		t.Errorf("history grew to %d states under cap %d", len(c.history.states), cap)
	}
	// The latest pixel must always be present regardless of eviction.
	if c.m.At(19, 0) != c.penColor.c {
		t.Error("most recent brush action missing after heavy editing")
	}
}

func Test_History_PanIsNotAnEdit(t *testing.T) {
	c := newTestCmdPxl(t, 10, 10, defaultMaxHistory)
	c.handleKey(keyRune('e'))
	before := c.history.undoCount()

	// Panning moves the viewport only: no new history state, no dirty
	// transition, and the undo stack must remain unchanged.
	c.handleKey(keyNamed(tcell.KeyRight))
	c.handleKey(keyNamed(tcell.KeyDown))
	c.handleKey(keyNamed(tcell.KeyLeft))
	c.handleKey(keyNamed(tcell.KeyUp))
	if c.history.undoCount() != before {
		t.Errorf("panning altered history: undo=%d want %d", c.history.undoCount(), before)
	}
	if !c.history.isDirty() {
		t.Error("panning should not clear the dirty flag of pending edits")
	}
	// Undo still targets the brush action, not a pan step.
	c.handleKey(keyRune('z'))
	if c.m.At(0, 0) == c.penColor.c {
		t.Error("undo after panning did not revert the brush action")
	}
}

func Test_History_UndoRedoAcrossDifferentColors(t *testing.T) {
	c := newTestCmdPxl(t, 3, 3, defaultMaxHistory)

	// White pixel, then switch hue and paint the same pixel a different
	// color; undo/redo must restore each exact color.
	c.handleKey(keyRune('e'))
	white := c.m.At(0, 0)
	c.handleKey(keyRune('j')) // change hue
	c.handleKey(keyRune('e'))
	colored := c.m.At(0, 0)
	if white == colored {
		t.Fatal("hue change did not produce a different pen color")
	}

	c.handleKey(keyRune('z'))
	if c.m.At(0, 0) != white {
		t.Error("undo did not restore the previous color at the pixel")
	}
	c.handleKey(keyRune('y'))
	if c.m.At(0, 0) != colored {
		t.Error("redo did not restore the newer color at the pixel")
	}
}

func Test_History_DirtyLifecycleAndSave(t *testing.T) {
	c := newTestCmdPxl(t, 4, 4, defaultMaxHistory)
	saved := false
	c.saveImage = func(string, image.Image) error {
		saved = true
		return nil
	}

	// Undoing back to the original canvas makes the image clean even
	// though history entries still exist.
	c.handleKey(keyRune('e'))
	if !c.history.isDirty() {
		t.Fatal("canvas should be dirty after an edit")
	}
	c.handleKey(keyRune('z'))
	if c.history.isDirty() {
		t.Error("canvas should be clean after undoing all edits")
	}

	// Re-apply and simulate a save through the quit-confirmation path.
	c.handleKey(keyRune('y'))
	c.handleKey(keyRune('x'))
	if c.currentState != stateQuit {
		t.Fatal("quit with unsaved changes should open the confirmation state")
	}
	if quit := c.handleKey(keyRune('y')); !quit {
		t.Error("confirming save-and-exit should request application exit")
	}
	if !saved {
		t.Error("save callback was not invoked on confirmed exit")
	}
	if c.history.isDirty() {
		t.Error("canvas should be clean immediately after saving")
	}

	// Declining the confirmation instead returns to editing without loss.
	c2 := newTestCmdPxl(t, 4, 4, defaultMaxHistory)
	c2.handleKey(keyRune('e'))
	c2.handleKey(keyNamed(tcell.KeyEscape))
	if c2.currentState != stateQuit {
		t.Fatal("escape should open the quit confirmation when dirty")
	}
	c2.handleKey(keyRune('n'))
	if c2.currentState != stateDrawing {
		t.Error("declining quit should return to the drawing state")
	}
	if c2.m.At(0, 0) != c2.penColor.c {
		t.Error("declining quit must preserve the edited pixels")
	}
}

func Test_History_CleanExitNeedsNoConfirmation(t *testing.T) {
	c := newTestCmdPxl(t, 4, 4, defaultMaxHistory)

	// Only viewport/color/cursor activity: nothing to save, so exit is
	// immediate and no confirmation dialog appears.
	c.handleKey(keyNamed(tcell.KeyRight))
	c.handleKey(keyRune('j'))
	c.handleKey(keyRune('d'))
	if quit := c.handleKey(keyNamed(tcell.KeyEscape)); !quit {
		t.Error("exiting a clean canvas should happen without confirmation")
	}
	if c.currentState == stateQuit {
		t.Error("clean exit must not enter the quit confirmation state")
	}
}

func Test_History_DimensionChangeStartsIndependentHistory(t *testing.T) {
	// History is bound to one editor session/canvas: a new image with a
	// different size starts from a clean snapshot and cannot undo pixels
	// from the previous canvas.
	first := newTestCmdPxl(t, 5, 5, defaultMaxHistory)
	first.handleKey(keyRune('e'))
	if !first.history.isDirty() {
		t.Fatal("first canvas should be dirty after painting")
	}

	second := newTestCmdPxl(t, 4, 8, defaultMaxHistory)
	if second.imageWidth != 8 || second.imageHeight != 4 {
		t.Fatalf("new canvas dimensions = %dx%d, want 8x4", second.imageWidth, second.imageHeight)
	}
	if second.history.isDirty() || second.history.canUndo() {
		t.Error("a freshly opened canvas of a different size must start with empty history")
	}
	second.handleKey(keyRune('z'))
	if second.statusMessage != statusUndoUnavailable {
		t.Errorf("new canvas undo prompt = %q, want %q", second.statusMessage, statusUndoUnavailable)
	}
}

func Test_History_SaveReloadRoundTrip(t *testing.T) {
	m, err := createImage("6,6")
	if err != nil {
		t.Fatal(err)
	}
	file := t.TempDir() + "/roundtrip.png"
	c := NewCmdPxl(file, m, saveImage, WithMaxHistory(defaultMaxHistory))

	// Fill, then paint one pixel with a different hue, and save exactly
	// what is visible after the two actions.
	c.handleKey(keyRune('f'))
	c.handleKey(keyRune('j'))
	c.cursorX, c.cursorY = 1, 1
	c.handleKey(keyRune('e'))
	wantFill := c.m.At(0, 0)
	wantPen := c.m.At(1, 1)
	if err := saveImage(file, &c.m); err != nil {
		t.Fatalf("saveImage: %v", err)
	}

	reloaded, err := loadImage(file)
	if err != nil {
		t.Fatalf("loadImage: %v", err)
	}
	b := reloaded.Bounds()
	if b.Dx() != 6 || b.Dy() != 6 {
		t.Fatalf("reloaded dimensions = %s, want 6x6", b)
	}
	if got := reloaded.At(0, 0); !sameColor(got, wantFill) {
		t.Errorf("reloaded fill pixel = %v, want %v", got, wantFill)
	}
	if got := reloaded.At(1, 1); !sameColor(got, wantPen) {
		t.Errorf("reloaded brush pixel = %v, want %v", got, wantPen)
	}
}

// sameColor compares colors by their 8-bit RGBA channel values. PNG
// round-trips re-encode values into 8-bit color.RGBA, so concrete type
// equality and raw 16-bit channel equality both fail across save/reload.
func sameColor(a, b color.Color) bool {
	ar, ag, ab, aa := a.RGBA()
	br, bg, bb, ba := b.RGBA()
	return uint8(ar>>8) == uint8(br>>8) &&
		uint8(ag>>8) == uint8(bg>>8) &&
		uint8(ab>>8) == uint8(bb>>8) &&
		uint8(aa>>8) == uint8(ba>>8)
}

func Test_History_InvalidCapFallsBackToDefault(t *testing.T) {
	c := newTestCmdPxl(t, 3, 3, 0)
	if c.history.maxStates != defaultMaxHistory {
		t.Errorf("maxStates for cap 0 = %d, want default %d", c.history.maxStates, defaultMaxHistory)
	}
	c2 := newTestCmdPxl(t, 3, 3, -5)
	if c2.history.maxStates != defaultMaxHistory {
		t.Errorf("maxStates for negative cap = %d, want default %d", c2.history.maxStates, defaultMaxHistory)
	}
}
