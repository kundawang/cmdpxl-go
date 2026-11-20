package main

import (
	"fmt"
	"image"
	"image/color"
	"strings"

	"github.com/gdamore/tcell/v2"
)

type direction bool
type state int
type saveImageCallback = func(fileName string, m image.Image) error

const (
	maxHue     = 380
	borderSize = 1

	dirIncrease  direction = true
	dirDecrease  direction = false
	stateDrawing state     = iota
	stateQuit
)

// Status messages shown in the history status line. They are constants so
// that tests can assert the exact prompt the user sees.
const (
	statusReady           = "ready"
	statusDrawn           = "painted a pixel"
	statusFillPrefix      = "fill:"
	statusFillNoop        = "fill: same color, nothing changed"
	statusUndo            = "undo"
	statusRedo            = "redo"
	statusUndoUnavailable = "nothing to undo"
	statusRedoUnavailable = "nothing to redo"
	statusSaved           = "saved"
	statusDirty           = "unsaved changes"
	statusClean           = "all changes saved"
)

// cmdPxlOption customizes a CmdPxl instance at construction time.
type cmdPxlOption func(*CmdPxl)

// WithMaxHistory configures the memory cap for edit history: at most n
// editor states (one per user action) are retained for undo/redo. Values
// below minimumMaxHistory fall back to defaultMaxHistory.
func WithMaxHistory(n int) cmdPxlOption {
	return func(c *CmdPxl) { c.maxHistory = n }
}

// withScreen injects a screen, which lets tests drive the editor with
// synthetic events instead of opening a real terminal.
func withScreen(s tcell.Screen) cmdPxlOption {
	return func(c *CmdPxl) { c.s = s }
}

type CmdPxl struct {
	currentState state

	screenWidth  int
	screenHeight int

	imageWidth  int
	imageHeight int

	maxDrawWidth  int
	maxDrawHeight int

	imageBox *drawBox

	cursorX int
	cursorY int

	paddingX int
	paddingY int

	panX int
	panY int

	paletteSize    int
	m              layeredImage
	fileName       string
	interfaceStyle tcell.Style
	s              tcell.Screen
	penColor       cmdColor
	history        *editorHistory
	maxHistory     int
	statusMessage  string

	saveImage saveImageCallback
}

func NewCmdPxl(fileName string, m image.Image, saveImage saveImageCallback, opts ...cmdPxlOption) *CmdPxl {
	b := m.Bounds()
	paletteSize := 11

	c := &CmdPxl{
		currentState:   stateDrawing,
		interfaceStyle: tcell.StyleDefault.Foreground(tcell.ColorWhite).Background(tcell.ColorReset),
		fileName:       fileName,
		m:              layeredImage{make(layer), m},
		imageWidth:     b.Max.X,
		imageHeight:    b.Max.Y,
		panX:           0,
		panY:           0,
		maxDrawWidth:   0,
		maxDrawHeight:  0,
		paddingY:       1,
		cursorX:        0,
		cursorY:        0,
		paletteSize:    paletteSize,
		penColor:       *NewCmdColor(color.White, paletteSize),
		maxHistory:     defaultMaxHistory,
		statusMessage:  statusReady,
		saveImage:      saveImage,
	}
	for _, opt := range opts {
		opt(c)
	}
	// The initial snapshot is the untouched canvas, so undo on an empty
	// history is a well-defined no-op rather than an error.
	c.history = newEditorHistory(c.m.l, c.maxHistory)
	return c
}

func (c *CmdPxl) Run() error {
	var err error

	// Initialize screen unless one was injected (tests inject a
	// simulation screen).
	if c.s == nil {
		c.s, err = tcell.NewScreen()
		if err != nil {
			return err
		}
		if err := c.s.Init(); err != nil {
			return err
		}
	}

	c.s.SetStyle(c.interfaceStyle)

	defer c.s.Fini()

mainLoop:
	for {
		// Update screen
		c.s.Show()

		// Poll event
		ev := c.s.PollEvent()

		if quit := c.handleEvent(ev); quit {
			break mainLoop
		}
		c.draw()
	}
	return nil
}

// handleEvent dispatches a single terminal event. It returns true when
// the main loop should terminate.
func (c *CmdPxl) handleEvent(ev tcell.Event) bool {
	switch ev := ev.(type) {
	case *tcell.EventResize:
		c.screenWidth, c.screenHeight = ev.Size()
		c.paddingX = max(0, (c.screenWidth-max(48, c.imageWidth*2))/2)

		c.maxDrawWidth = c.screenWidth - 2*borderSize
		c.maxDrawHeight = c.screenHeight - 13 // chrome

		c.imageBox = c.getImageBox()
		c.s.Sync()
	case *tcell.EventKey:
		return c.handleKey(ev)
	}
	return false
}

// handleKey applies one key press. It returns true when the key requests
// that the application exit.
func (c *CmdPxl) handleKey(ev *tcell.EventKey) bool {
	if c.currentState == stateDrawing {
		// quit: only interrupt with a save confirmation when there are
		// unsaved changes; otherwise exit directly.
		if ev.Key() == tcell.KeyEscape || ev.Key() == tcell.KeyCtrlC || ev.Rune() == 'x' {
			if c.history.isDirty() {
				c.currentState = stateQuit
			} else {
				return true
			}
			return false
		}
		// move cursor
		if ev.Rune() == 'w' {
			c.cursorY = mod(c.cursorY-1, c.imageHeight)
		}
		if ev.Rune() == 's' {
			c.cursorY = mod(c.cursorY+1, c.imageHeight)
		}
		if ev.Rune() == 'a' {
			c.cursorX = mod(c.cursorX-1, c.imageWidth)
		}
		if ev.Rune() == 'd' {
			c.cursorX = mod(c.cursorX+1, c.imageWidth)
		}
		if ev.Rune() == 'e' || ev.Rune() == ' ' {
			c.paintPixel()
		}
		if ev.Rune() == 'z' {
			c.undo()
		}
		if ev.Rune() == 'y' {
			c.redo()
		}
		if ev.Rune() == 'D' {
			// debug
			newDrawBox(0, 0, 5, 3).draw(c.s, c.interfaceStyle)
			drawText(c.s, 0, 10, c.interfaceStyle, "box: "+c.imageBox.String())
			drawText(c.s, 0, 11, c.interfaceStyle, "canvas: "+c.imageBox.getCanvas().String())
			drawText(c.s, 0, 12, c.interfaceStyle, fmt.Sprintf("WxH: %dx%d", c.imageBox.getCanvas().Dx(), c.imageBox.getCanvas().Dy()))
			drawText(c.s, 0, 13, c.interfaceStyle, fmt.Sprintf("Pan XxY: %dx%d", c.panX, c.panY))
			drawText(c.s, 0, 14, c.interfaceStyle, fmt.Sprintf("image XxY: %dx%d", c.imageWidth, c.imageHeight))
		}

		// colors

		// hue
		if ev.Rune() == 'j' {
			c.penColor.changeHue(dirIncrease)
		}
		if ev.Rune() == 'u' {
			c.penColor.changeHue(dirDecrease)
		}

		// saturation
		if ev.Rune() == 'k' {
			c.penColor.changeSaturation(dirIncrease)
		}
		if ev.Rune() == 'i' {
			c.penColor.changeSaturation(dirDecrease)
		}

		// value
		if ev.Rune() == 'l' {
			c.penColor.changeValue(dirIncrease)
		}
		if ev.Rune() == 'o' {
			c.penColor.changeValue(dirDecrease)
		}

		// panning changes only the viewport, never the canvas, so it is
		// not recorded as an edit and cannot make the image dirty. The
		// image box only exists after the first resize event, so the
		// wrap-around bounds are skipped until then.
		if ev.Key() == tcell.KeyUp {
			c.panY -= 1
			if c.imageBox != nil && c.panY < 0 {
				c.panY = c.imageHeight - (c.imageBox.getCanvas().Dy() + 1)
			}
		}
		if ev.Key() == tcell.KeyDown {
			c.panY += 1
			if c.imageBox != nil && c.panY > c.imageHeight-(c.imageBox.getCanvas().Dy()+1) {
				c.panY = 0
			}
		}
		if ev.Key() == tcell.KeyLeft {
			c.panX -= 1
			if c.imageBox != nil && c.panX < 0 {
				c.panX = c.imageWidth - ((c.imageBox.getCanvas().Dx() + 1) / 2)
			}
		}
		if ev.Key() == tcell.KeyRight {
			c.panX += 1
			if c.imageBox != nil && c.panX > c.imageWidth-((c.imageBox.getCanvas().Dx()+1)/2) {
				c.panX = 0
			}
		}

		if ev.Rune() == 'f' {
			c.fillAtCursor()
		}

	} else if c.currentState == stateQuit {
		if ev.Rune() == 'y' || ev.Rune() == 'Y' {
			if err := c.saveImage(c.fileName, &c.m); err != nil {
				// Surface save failures through the status line and stay
				// in the editor instead of silently losing work.
				c.statusMessage = "save failed: " + err.Error()
				c.currentState = stateDrawing
				return false
			}
			c.history.markSaved()
			c.statusMessage = statusSaved
			return true
		}
		if ev.Rune() == 'n' || ev.Rune() == 'N' || ev.Key() == tcell.KeyEscape {
			c.currentState = stateDrawing
			if c.s != nil {
				c.s.Clear()
			}
		}
	}
	return false
}

// paintPixel performs one brush action. A single keypress is one user
// action, so it lands in history as exactly one undo step. Painting over
// a pixel that already has the pen color is a no-op and does not pollute
// the history.
func (c *CmdPxl) paintPixel() {
	pt := image.Pt(c.cursorX+c.panX, c.cursorY+c.panY)
	if c.m.At(pt.X, pt.Y) == c.penColor.c {
		return
	}
	c.m.Set(pt, c.penColor.c)
	if c.history.commit(c.m.l) {
		c.statusMessage = statusDrawn
		// The history owns the snapshot; keep the working layer
		// independent so later edits cannot mutate stored history.
		c.m.l = cloneLayer(c.history.current())
	}
}

// fillAtCursor performs one flood-fill action. However many pixels the
// fill touches, it is a single user action and therefore a single undo
// step.
func (c *CmdPxl) fillAtCursor() {
	fromColor := c.m.At(c.cursorX, c.cursorY)
	if fromColor == c.penColor.c {
		c.statusMessage = statusFillNoop
		return
	}
	changed := floodFill(&c.m, image.Pt(c.cursorX, c.cursorY), fromColor, c.penColor.c)
	if changed == 0 {
		c.statusMessage = statusFillNoop
		return
	}
	if c.history.commit(c.m.l) {
		c.statusMessage = fmt.Sprintf("%s %d pixel(s)", statusFillPrefix, changed)
		c.m.l = cloneLayer(c.history.current())
	}
}

// undo reverts one user action. On an empty undo stack it leaves the
// canvas untouched and reports that no undo is available.
func (c *CmdPxl) undo() {
	if l, ok := c.history.undo(); ok {
		c.m.l = cloneLayer(l)
		c.statusMessage = statusUndo
	} else {
		c.statusMessage = statusUndoUnavailable
	}
}

// redo re-applies one user action from the redo branch. When the branch
// is empty it leaves the canvas untouched and reports that no redo is
// available.
func (c *CmdPxl) redo() {
	if l, ok := c.history.redo(); ok {
		c.m.l = cloneLayer(l)
		c.statusMessage = statusRedo
	} else {
		c.statusMessage = statusRedoUnavailable
	}
}

func (c *CmdPxl) draw() {
	c.drawInterface()
	c.drawColorSelect()
	c.imageBox.draw(c.s, c.interfaceStyle)
	c.drawImage(c.imageBox)
	if c.currentState == stateQuit {
		c.drawExitConfirmation()
	}
}

func (c *CmdPxl) drawExitConfirmation() *drawBox {
	confirmation := "Do you want to exit? [y/n]"
	dBox := newDrawBox(0, 0, len(confirmation)+2+borderSize*2, borderSize*2+1).draw(c.s, c.interfaceStyle)
	p := dBox.getPoint(1, 0)
	drawText(c.s, p.X, p.Y, c.interfaceStyle, confirmation)
	return dBox
}

func (c *CmdPxl) getImageBox() *drawBox {
	offsetY := 5
	interfaceRows := 12
	width := min(c.imageWidth*2+2, c.screenWidth)
	height := min(c.imageHeight+2, c.screenHeight-interfaceRows)

	x := c.paddingX
	y := offsetY + c.paddingY
	return newDrawBox(x, y, width, height)
}

func (c *CmdPxl) drawImage(dBox *drawBox) {
	canvas := dBox.getCanvas()
	const pixelWidth = 2
	xBoundary := min(c.imageWidth*2, canvas.Dx()/pixelWidth+1)
	yBoundary := min(c.imageHeight, canvas.Dy()+1)
	for y := 0; y < yBoundary; y++ {
		for x := 0; x < xBoundary; x++ {
			imageColor := c.m.At(x+c.panX, y+c.panY)
			bgColor := tcell.FromImageColor(imageColor)
			style := tcell.StyleDefault.Background(bgColor)
			p := dBox.getPoint(x*2, y)
			if c.cursorX == x && c.cursorY == y {
				style = style.Foreground(tcell.FromImageColor(getFgColor(imageColor)))
				c.s.SetContent(p.X, p.Y, '[', nil, style)
				c.s.SetContent(p.X+1, p.Y, ']', nil, style)
			} else {
				c.s.SetContent(p.X, p.Y, ' ', nil, style)
				c.s.SetContent(p.X+1, p.Y, ' ', nil, style)
			}
		}
	}
}

func (c *CmdPxl) drawInterface() {
	drawText(c.s, c.paddingX, 1, c.interfaceStyle, fmt.Sprintf("CMDPXL-GO: %s (%dx%d) | pos: %03d,%03d", c.fileName, c.imageWidth, c.imageHeight, c.cursorX, c.cursorY))
	p := newDrawBox(c.paddingX, c.screenHeight-4, 100, 4).getPoint(0, 0)
	drawText(c.s, p.X, p.Y, c.interfaceStyle, "[wasd] move | [e] draw | [f] fill | [arrows] pan")
	drawText(c.s, p.X, p.Y+1, c.interfaceStyle, "[z] undo | [y] redo | [t] filters | [x] quit")
	drawText(c.s, p.X, p.Y+2, c.interfaceStyle, c.historyStatusText())
}

// historyStatusText renders the persistent history/status line: the last
// action prompt, the number of undo/redo steps still available against
// the configured cap, and whether the canvas holds unsaved changes.
func (c *CmdPxl) historyStatusText() string {
	saveState := statusClean
	if c.history.isDirty() {
		saveState = statusDirty
	}
	return fmt.Sprintf("history: undo %d/%d redo %d | %s | %s",
		c.history.undoCount(), c.history.maxStates, c.history.redoCount(), c.statusMessage, saveState)
}

func (c *CmdPxl) drawColorSelect() {
	sectionWidth := 12
	boxHeight := 4
	numBoxes := 4
	x1 := c.paddingX
	y1 := c.paddingY + 1
	// box
	dBox := newDrawBox(x1, y1, numBoxes*sectionWidth+borderSize, boxHeight).draw(c.s, c.interfaceStyle)
	p := dBox.getPoint(0, 0)
	// instructions
	instructions := "[u/j]: hue  [i/k]: sat  [o/l]: val  current"
	drawText(c.s, p.X, p.Y, c.interfaceStyle, instructions)

	// Color selection
	p = dBox.getPoint(0, 1)
	for offset, cl := range c.penColor.huePalette {
		style := tcell.StyleDefault.Background(tcell.FromImageColor(cl))
		text := ' '
		if offset == c.penColor.huePaletteIndex {
			style.Foreground(tcell.FromImageColor(getFgColor(cl)))
			text = '●'
		}
		c.s.SetContent(p.X+offset, p.Y, text, nil, style)
	}

	// Saturation
	p = dBox.getPoint(sectionWidth*1, 1)
	c.s.SetContent(p.X-1, p.Y-2, '┬', nil, c.interfaceStyle)
	c.s.SetContent(p.X-1, p.Y-1, '│', nil, c.interfaceStyle)
	c.s.SetContent(p.X-1, p.Y+0, '│', nil, c.interfaceStyle)
	c.s.SetContent(p.X-1, p.Y+1, '┴', nil, c.interfaceStyle)
	for offset, cl := range c.penColor.saturationPalette {
		style := tcell.StyleDefault.Background(tcell.FromImageColor(cl))
		text := ' '
		if offset == c.penColor.saturationPaletteIndex {
			style.Foreground(tcell.FromImageColor(getFgColor(cl)))
			text = '●'
		}
		c.s.SetContent(p.X+offset, p.Y, text, nil, style)
	}

	// Value
	p = dBox.getPoint(sectionWidth*2, 1)
	c.s.SetContent(p.X-1, p.Y-2, '┬', nil, c.interfaceStyle)
	c.s.SetContent(p.X-1, p.Y-1, '│', nil, c.interfaceStyle)
	c.s.SetContent(p.X-1, p.Y+0, '│', nil, c.interfaceStyle)
	c.s.SetContent(p.X-1, p.Y+1, '┴', nil, c.interfaceStyle)
	for offset, cl := range c.penColor.valuePalette {
		style := tcell.StyleDefault.Background(tcell.FromImageColor(cl))
		text := ' '
		if offset == c.penColor.valuePaletteIndex {
			style.Foreground(tcell.FromImageColor(getFgColor(cl)))
			text = '●'
		}
		c.s.SetContent(p.X+offset, p.Y, text, nil, style)
	}

	// Current color
	style := tcell.StyleDefault.Background((tcell.FromImageColor(c.penColor.c)))
	p = dBox.getPoint(sectionWidth*3, 1)
	c.s.SetContent(p.X-1, p.Y-2, '┬', nil, c.interfaceStyle)
	c.s.SetContent(p.X-1, p.Y-1, '│', nil, c.interfaceStyle)
	c.s.SetContent(p.X-1, p.Y+0, '│', nil, c.interfaceStyle)
	c.s.SetContent(p.X-1, p.Y+1, '┴', nil, c.interfaceStyle)

	drawText(c.s, p.X, p.Y, style, strings.Repeat(" ", c.paletteSize))
}

func drawText(s tcell.Screen, x, y int, style tcell.Style, text string) {
	row := y
	col := x
	for _, r := range text {
		s.SetContent(col, row, r, nil, style)
		col++
	}
}

func min(numbers ...int) int {
	min := numbers[0]

	for _, i := range numbers {
		if min > i {
			min = i
		}
	}

	return min
}

func max(numbers ...int) int {
	max := numbers[0]

	for _, i := range numbers {
		if max < i {
			max = i
		}
	}

	return max
}

func mod(a, b int) int {
	if a < 0 {
		return b - 1
	}
	if a > b-1 {
		return 0
	}
	return a
}

func getFgColor(c color.Color) color.Color {
	// https://socketloop.com/tutorials/golang-find-relative-luminance-or-color-brightness
	red, green, blue, _ := c.RGBA()
	lum := float64(float64(0.299)*float64(red) + float64(0.587)*float64(green) + float64(0.114)*float64(blue))
	if lum < .5 {
		return color.White
	}
	return color.Black
}
