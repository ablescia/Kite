// SPDX-License-Identifier: AGPL-3.0-or-later

package app

import (
	"image"
	"strconv"
	"strings"
	"time"

	"github.com/ablescia/kite/internal/engine"
	"github.com/ablescia/kite/internal/selector"
	"github.com/ablescia/kite/internal/x11ui"
)

// Disegno del compito "testo" e della scrittura libera: il testo scritto,
// quello da copiare e la barra di modifica.

const (
	targetBaseline = 112 // testo da copiare (px a 1080p)
	typedBaseline  = 186 // testo scritto
)

// drawTyped disegna il testo scritto con il cursore, allineato a sinistra;
// se è lungo ne mostra la fine. Mentre la voce lo dice è in azzurro. ghost è
// la frase proposta, in grigio chiaro dopo il cursore: restituisce dove è
// stata disegnata (vuoto se non c'è). La frase resta visibile per intero
// (se è molto lunga, il suo inizio): è il testo a scorrere a sinistra.
func (a *App) drawTyped(img *image.RGBA, typed, empty, ghost string) image.Rectangle {
	f := a.F.Big
	maxW := int(0.9 * float64(a.W))
	gap := a.Px(12) // dopo il cursore
	gs := []rune(ghost)
	if len(gs) > 0 && f.Measure(ghost) > 3*maxW/5 {
		for len(gs) > 0 && f.Measure(string(gs)+"…") > 3*maxW/5 {
			gs = gs[:len(gs)-1]
		}
		gs = append(gs, '…')
	}
	avail := maxW
	if len(gs) > 0 {
		avail -= f.Measure(string(gs)) + gap
	}
	rs := []rune(typed)
	for len(rs) > 0 && f.Measure(string(rs)) > avail {
		rs = rs[1:]
	}
	y := a.Px(typedBaseline)
	x := a.Px(60)
	col := colText
	if a.snap.Speaking {
		col = colAccent
	}
	x11ui.Text(img, f, x, y, string(rs), col, x11ui.Left)
	cx := x + f.Measure(string(rs))
	if len(rs) == 0 && empty != "" && len(gs) == 0 {
		x11ui.Text(img, a.F.Body, x+a.Px(16), y-a.Px(14), empty, colFaint, x11ui.Left)
	}
	x11ui.Fill(img, image.Rect(cx+a.Px(4), y-f.Ascent, cx+a.Px(8), y+f.Desc), colAccent)
	if len(gs) == 0 {
		return image.Rectangle{}
	}
	gx := cx + gap
	x11ui.Text(img, f, gx, y, string(gs), colGhost, x11ui.Left)
	return image.Rect(gx, y-f.Ascent, gx+f.Measure(string(gs)), y+f.Desc)
}

// drawPhraseProgress sottolinea la frase grigia man mano che la fissazione
// la accetta.
func (a *App) drawPhraseProgress(img *image.RGBA, ghost image.Rectangle, prog float64) {
	if ghost.Empty() || prog <= 0 {
		return
	}
	y := ghost.Max.Y + a.Px(3)
	a.Dyn(x11ui.RoundRect(img, image.Rect(ghost.Min.X, y, ghost.Max.X, y+a.Px(5)), a.Pf(2), x11ui.Alpha(colGhost, 0.35)))
	w := max(int(prog*float64(ghost.Dx())), a.Px(5))
	a.Dyn(x11ui.RoundRect(img, image.Rect(ghost.Min.X, y, ghost.Min.X+w, y+a.Px(5)), a.Pf(2), colAccent))
}

// norm converte un rettangolo in pixel in coordinate normalizzate.
func (a *App) norm(r image.Rectangle) [4]float64 {
	w, h := float64(a.W), float64(a.H)
	return [4]float64{float64(r.Min.X) / w, float64(r.Min.Y) / h, float64(r.Max.X) / w, float64(r.Max.Y) / h}
}

// drawTarget disegna il testo da copiare, con le parole già scritte in grigio.
func (a *App) drawTarget(img *image.RGBA, target, typed string) {
	f := a.F.Title
	words := strings.Fields(target)
	done := len(strings.Fields(typed))
	if !strings.HasSuffix(typed, " ") && done > 0 {
		done-- // l'ultima parola è ancora aperta
	}
	x, y := a.Px(60), a.Px(targetBaseline)
	space := f.Measure(" ")
	for i, w := range words {
		c := colDim
		switch {
		case i < done:
			c = colFaint
		case i == done:
			c = colAccent
		}
		x11ui.Text(img, f, x, y, w, c, x11ui.Left)
		x += f.Measure(w) + space
	}
}

// drawEditKey disegna un tasto della barra di modifica. Un suggerimento
// spento è vuoto; cancella e parla spenti restano leggibili, in grigio.
func (a *App) drawEditKey(img *image.RGBA, k selector.Key, on bool, sugg []string) {
	r := a.rect(k.Rect)
	label := k.Label
	s, isSugg := selector.SuggestSlot(k.Char)
	if isSugg {
		label = ""
		if s < len(sugg) {
			label = sugg[s]
		}
	}
	if k.Char == selector.KeySpeak && a.snap.Speaking {
		label = "stop"
	}
	if label == "" || isSugg && !on {
		x11ui.RoundRect(img, r, a.Pf(12), colPanel)
		return
	}
	bg, fg := colKey, colKeyTxt
	if !on {
		bg, fg = colPanel, colFaint
	}
	x11ui.RoundRect(img, r, a.Pf(12), bg)
	f := a.F.Title
	if f.Measure(label) > r.Dx()-a.Px(24) {
		f = a.F.Heading
	}
	cx, cy := (r.Min.X+r.Max.X)/2, (r.Min.Y+r.Max.Y)/2
	x11ui.Text(img, f, cx, cy+int(0.39*float64(f.Ascent)), label, fg, x11ui.Center)
}

// drawLastFix ricorda per qualche secondo l'ultima correzione automatica o
// frase accettata, annullabili con cancella, e quello che fa la voce. Il
// messaggio è allineato a destra sulla linea di base baseline (px a 1080p).
func (a *App) drawLastFix(img *image.RGBA, now time.Time, baseline float64) {
	s := a.snap
	msg, col := "", colWarn
	fx := s.LastFix
	if fx.Text != "" && now.Sub(fx.At) <= 4*time.Second {
		msg = "«" + fx.Literal + "» → " + fx.Text + " · canc per annullare"
	}
	if s.Accepted != "" && now.Sub(s.AcceptedAt) <= 4*time.Second && !s.AcceptedAt.Before(fx.At) {
		msg, col = "frase accettata · canc per annullare", colOK
	}
	switch {
	case s.Speaking:
		msg, col = "la voce sta parlando · guarda stop per fermarla", colAccent
	case s.VoiceErr != "" && now.Sub(s.VoiceErrAt) <= 8*time.Second:
		msg, col = "la voce non ha parlato: "+s.VoiceErr, colBad
	case s.Said != "" && s.Typed == "" && now.Sub(s.SaidAt) <= 5*time.Second:
		msg, col = "detto · canc per riaverlo", colOK
	}
	if msg != "" {
		a.Dyn(x11ui.Text(img, a.F.Body, a.W-a.Px(60), a.Px(baseline), msg, col, x11ui.Right))
	}
}

// textState è ciò che, cambiando, richiede di ridisegnare lo statico: il
// testo, i suggerimenti, la voce e i tasti di modifica accesi.
func (a *App) textState(s engine.Snapshot) string {
	var b strings.Builder
	b.WriteString(s.Typed)
	b.WriteByte(0)
	b.WriteString(strings.Join(s.Suggest, "\x00"))
	b.WriteByte(0)
	b.WriteString(strconv.FormatUint(s.PhraseVer, 10))
	b.WriteByte(0)
	if s.Speaking {
		b.WriteByte('v')
	}
	for i := a.KB.Chars; i < len(s.Prior); i++ {
		if s.Prior[i] > 0 {
			b.WriteByte('1')
		} else {
			b.WriteByte('0')
		}
	}
	return b.String()
}
