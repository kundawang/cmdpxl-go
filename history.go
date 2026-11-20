package main

// defaultMaxHistory is the default number of editor states kept for
// undo/redo. Each state holds a copy of the pixels changed relative to the
// base image, so the cap bounds the memory used by edit history.
const defaultMaxHistory = 50

// minimumMaxHistory is the smallest cap that can be configured. The
// current state must always be retained, so the cap can never be below 1.
const minimumMaxHistory = 1

// editorHistory stores undo/redo states of the editable layer.
//
// states holds committed layer snapshots, states[position] being the
// currently visible state. states[:position] are undo targets and
// states[position+1:] is the redo branch. A new action truncates the
// redo branch, and when the number of retained states exceeds maxStates
// the oldest snapshot is evicted.
type editorHistory struct {
	states    []layer
	position  int
	maxStates int

	// savedLayer is the layer captured at the last successful save. The
	// canvas is dirty whenever the visible layer differs from it. Storing
	// the snapshot (rather than an index) keeps the answer correct after
	// old states have been evicted and after undoing every edit.
	savedLayer layer
}

func newEditorHistory(initial layer, maxStates int) *editorHistory {
	if maxStates < minimumMaxHistory {
		maxStates = defaultMaxHistory
	}
	h := &editorHistory{
		states:    make([]layer, 1, maxStates),
		position:  0,
		maxStates: maxStates,
	}
	h.states[0] = cloneLayer(initial)
	h.savedLayer = cloneLayer(initial)
	return h
}

// commit stores the current layer as one user action. The layer passed
// in becomes the owned current snapshot; the caller must not mutate it
// afterwards. It returns true when the action actually changed pixels
// (no-op actions are not recorded).
func (h *editorHistory) commit(current layer) bool {
	if !h.isDirtyFrom(current) {
		return false
	}
	// A new action clears the redo branch.
	h.states = h.states[:h.position+1]
	h.states = append(h.states, current)
	h.position++
	h.evict()
	return true
}

// canUndo reports whether an earlier state is available.
func (h *editorHistory) canUndo() bool { return h.position > 0 }

// canRedo reports whether a redo branch is available.
func (h *editorHistory) canRedo() bool { return h.position < len(h.states)-1 }

// undo moves one state back and returns the layer to display. It returns
// false when no earlier state exists.
func (h *editorHistory) undo() (layer, bool) {
	if !h.canUndo() {
		return nil, false
	}
	h.position--
	return h.states[h.position], true
}

// redo re-applies the next state on the redo branch. It returns false
// when the redo branch is empty.
func (h *editorHistory) redo() (layer, bool) {
	if !h.canRedo() {
		return nil, false
	}
	h.position++
	return h.states[h.position], true
}

// current returns the layer snapshot for the visible state.
func (h *editorHistory) current() layer { return h.states[h.position] }

// evict drops the oldest snapshots until the configured memory cap is
// respected. The current state is always retained.
func (h *editorHistory) evict() {
	for len(h.states) > h.maxStates {
		copy(h.states, h.states[1:])
		h.states = h.states[:len(h.states)-1]
		h.position--
	}
}

// markSaved records that the currently visible state has been saved.
func (h *editorHistory) markSaved() {
	h.savedLayer = cloneLayer(h.current())
}

// isDirty reports whether the visible state differs from the last saved
// state. Undoing every edit therefore makes the canvas clean, and
// redoing them makes it dirty again regardless of eviction.
func (h *editorHistory) isDirty() bool {
	return !layersEqual(h.current(), h.savedLayer)
}

// isDirtyFrom reports whether next differs from the currently visible
// snapshot.
func (h *editorHistory) isDirtyFrom(next layer) bool {
	return !layersEqual(h.current(), next)
}

// layersEqual reports whether two layers hold the same point/color pairs.
func layersEqual(a, b layer) bool {
	if len(a) != len(b) {
		return false
	}
	for p, c := range a {
		if existing, ok := b[p]; !ok || existing != c {
			return false
		}
	}
	return true
}

// undoCount returns the number of undo steps available.
func (h *editorHistory) undoCount() int { return h.position }

// redoCount returns the number of redo steps available.
func (h *editorHistory) redoCount() int { return len(h.states) - 1 - h.position }

// cloneLayer returns an independent copy of an edit layer.
func cloneLayer(l layer) layer {
	clone := make(layer, len(l))
	for p, c := range l {
		clone[p] = c
	}
	return clone
}
