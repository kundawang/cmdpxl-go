# cmdpxl-go

Go port of the terminal image editor [cmdpxl](https://github.com/knosmos/cmdpxl)

![cmdpxl-go screenshot](screenshots/screenshot.png)
![prior art](screenshots/30x30.png)

## Usage

```
cmdpxl-go -f image.png                 # open an existing image
cmdpxl-go -f new.png -res 20,10        # create a 20x10 image
cmdpxl-go -f new.png -res 20,10 -history 100
```

`-history` caps how many editor states (one per user action) are kept in
memory for undo/redo. The oldest states are evicted once the cap is
reached; the current canvas is always retained.

### Keys

* `wasd` — move cursor
* `e` / space — paint one pixel
* `f` — flood fill (one undo step regardless of region size)
* `z` / `y` — undo / redo
* arrow keys — pan the viewport (panning is never recorded as an edit)
* `x` / Esc / Ctrl-C — quit; a save confirmation appears only when there are unsaved changes

## TODO

* [x] Panning
* [x] ~Better layout management
* [x] ~Fill
* [X] ~Save
* [x] Undo fill (and redo, with a bounded history)
* [ ] Pick color
* [ ] Continuous draw
* [ ] Layers
* [ ] Filters
