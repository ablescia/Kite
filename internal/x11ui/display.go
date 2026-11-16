// SPDX-License-Identifier: MIT

package x11ui

import "image"

// Display è ciò su cui l'applicazione presenta i frame: la finestra X11 o,
// senza server grafico, un display headless che non mostra nulla (test
// automatici e simulazione, il rendering resta identico).
type Display interface {
	Size() (w, h int)
	Present(img *image.RGBA, rects ...image.Rectangle) error
	ShowCursor(show bool)
	EventChan() <-chan Event
	Close()
}

// Size restituisce le dimensioni della finestra in pixel.
func (w *Window) Size() (int, int) { return w.W, w.H }

// EventChan restituisce il canale degli eventi d'ingresso.
func (w *Window) EventChan() <-chan Event { return w.Events }

// Headless è un Display senza server grafico. Gli eventi si iniettano con Send.
type Headless struct {
	W, H   int
	events chan Event
}

// NewHeadless crea un display finto di w×h pixel.
func NewHeadless(w, h int) *Headless {
	return &Headless{W: w, H: h, events: make(chan Event, 64)}
}

func (h *Headless) Size() (int, int)                              { return h.W, h.H }
func (h *Headless) Present(*image.RGBA, ...image.Rectangle) error { return nil }
func (h *Headless) ShowCursor(bool)                               {}
func (h *Headless) EventChan() <-chan Event                       { return h.events }
func (h *Headless) Close()                                        {}

// Send inietta un evento (non blocca: se la coda è piena l'evento è perso).
func (h *Headless) Send(e Event) {
	select {
	case h.events <- e:
	default:
	}
}
