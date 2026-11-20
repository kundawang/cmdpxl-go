package main

import (
	"image"
	"image/color"
)

type layer map[image.Point]color.Color

type layeredImage struct {
	l layer
	image.Image
}

func (li *layeredImage) At(x, y int) color.Color {
	if c, ok := li.l[image.Pt(x, y)]; ok {
		return c
	}
	return li.Image.At(x, y)
}

func (li *layeredImage) Set(p image.Point, c color.Color) {
	li.l[p] = c
}

// floodFill replaces the connected region of fromColor containing p with
// toColor. It is iterative so that filling a very large region cannot
// overflow the goroutine stack, and returns the number of pixels that
// changed. A fill whose target color equals the region color changes
// nothing and returns 0.
func floodFill(m *layeredImage, p image.Point, fromColor, toColor color.Color) int {
	bounds := m.Bounds()
	if !p.In(bounds) || fromColor == toColor {
		return 0
	}
	changed := 0
	stack := []image.Point{p}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if !cur.In(bounds) || m.At(cur.X, cur.Y) != fromColor {
			continue
		}
		m.Set(cur, toColor)
		changed++
		stack = append(stack,
			image.Pt(cur.X-1, cur.Y),
			image.Pt(cur.X+1, cur.Y),
			image.Pt(cur.X, cur.Y-1),
			image.Pt(cur.X, cur.Y+1),
		)
	}
	return changed
}
