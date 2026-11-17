// SPDX-License-Identifier: AGPL-3.0-or-later

package app

import (
	"fmt"
	"image"
	"math"
	"time"

	"github.com/ablescia/kite/internal/engine"
	"github.com/ablescia/kite/internal/metrics"
	"github.com/ablescia/kite/internal/oculo"
	"github.com/ablescia/kite/internal/x11ui"
)

// Disegno condiviso dall'esercizio di copiatura e dalla scrittura libera.

const stripBaseline = 152 // linea di base del testo da copiare (px a 1080p)

// drawKeyboard disegna i tasti. Con regions (debug) mostra i confini
// effettivi delle aree di tocco e il priore di ogni tasto.
func (a *App) drawKeyboard(img *image.RGBA, prior []float64, debug, expand bool) {
	for i, k := range a.KB.Keys {
		if i >= a.KB.Chars {
			a.drawEditKey(img, k, prior != nil && prior[i] > 0, a.snap.Suggest)
			continue
		}
		r := a.rect(k.Rect)
		bg, fg := colKey, colKeyTxt
		if prior != nil && prior[i] == 0 {
			bg, fg = colPanel, colFaint // spento: mentre la voce parla la tastiera tace
		}
		x11ui.RoundRect(img, r, a.Pf(14), bg)
		cx, cy := (r.Min.X+r.Max.X)/2, (r.Min.Y+r.Max.Y)/2
		f := a.KeyFont
		if k.Char == ' ' {
			f = a.F.Heading
		}
		x11ui.Text(img, f, cx, cy+int(0.39*float64(f.Ascent)), k.Label, fg, x11ui.Center)
		if debug && prior != nil {
			x11ui.Text(img, a.F.Small, r.Min.X+a.Px(10), r.Min.Y+a.Px(24), it(100*prior[i], 0)+"%", colWarn, x11ui.Left)
		}
	}
	if debug {
		a.drawRegions(img, prior, expand)
	}
}

// drawRegions traccia i confini delle aree di tocco come le vede il decoder
// (con il priore: l'espansione invisibile resa visibile).
func (a *App) drawRegions(img *image.RGBA, prior []float64, expand bool) {
	step := a.Px(5)
	nx, ny := a.W/step, a.H/step
	out := make([]int, nx*ny)
	dec := a.Eng.Decoder()
	if !expand {
		prior = nil
	}
	dec.Regions(prior, nx, ny, out)
	c := colWarn
	for y := 0; y < ny; y++ {
		for x := 0; x < nx; x++ {
			k := out[y*nx+x]
			if k < 0 {
				continue
			}
			if (x+1 < nx && out[y*nx+x+1] != k) || (y+1 < ny && out[(y+1)*nx+x] != k) {
				px, py := x*a.W/nx+step/2, y*a.H/ny+step/2
				x11ui.Fill(img, image.Rect(px-1, py-1, px+2, py+2), c)
			}
		}
	}
}

// drawStrip disegna la striscia superiore: riga di stato e testo da copiare
// con il carattere corrente evidenziato.
func (a *App) drawStrip(img *image.RGBA, status, target string, pos int) {
	x11ui.Text(img, a.F.Small, a.Px(40), a.Px(34), status, colDim, x11ui.Left)
	rs := []rune(target)
	f := a.KeyFont
	if f.Measure(target) > int(0.92*float64(a.W)) {
		f = a.F.Title
	}
	y := a.Px(stripBaseline)
	x := (a.W - f.Measure(target)) / 2
	for i, r := range rs {
		ch := string(r)
		w := f.Measure(ch)
		col := colText
		switch {
		case i < pos:
			col = colFaint
		case i == pos:
			box := image.Rect(x-a.Px(4), y-f.Ascent-a.Px(4), x+w+a.Px(4), y+f.Desc+a.Px(6))
			x11ui.RoundRect(img, box, a.Pf(8), x11ui.Alpha(colAccent, 0.4))
			if r == ' ' {
				x11ui.Fill(img, image.Rect(x, y+a.Px(2), x+w, y+a.Px(6)), colText)
			}
		}
		x11ui.Text(img, f, x, y, ch, col, x11ui.Left)
		x += w
	}
}

// drawFeedback disegna l'avanzamento del tasto candidato, il lampo della
// selezione, lo sguardo e il pannello diagnostico (livello dinamico).
func (a *App) drawFeedback(img *image.RGBA, now time.Time) {
	s := a.snap
	if c := s.Dec.Candidate; c >= 0 && c < len(a.KB.Keys) {
		r := a.rect(a.KB.Keys[c].Rect)
		cx, cy := float64(r.Min.X+r.Max.X)/2, float64(r.Min.Y+r.Max.Y)/2
		rad := 0.36 * math.Min(float64(r.Dx()), float64(r.Dy()))
		a.Dyn(x11ui.Ring(img, cx, cy, rad, a.Pf(4), x11ui.Alpha(colText, 0.18)))
		if p := s.Dec.Progress; p > 0 {
			a.Dyn(x11ui.Arc(img, cx, cy, rad, a.Pf(6), -math.Pi/2, -math.Pi/2+2*math.Pi*math.Min(p, 1), colAccent))
		}
	}
	if k := s.LastSelKey; k >= 0 && k < len(a.KB.Keys) {
		if el := now.Sub(s.LastSelAt); el < 350*time.Millisecond {
			c := colOK
			if !s.LastSelOK {
				c = colBad
			}
			alpha := 0.5 * (1 - float64(el)/float64(350*time.Millisecond))
			a.Dyn(x11ui.RoundRect(img, a.rect(a.KB.Keys[k].Rect), a.Pf(14), x11ui.Alpha(c, alpha)))
		}
	}
	if a.Opt.ShowGaze && s.GazeValid && now.Sub(s.LastSample) < 200*time.Millisecond {
		x, y := s.Gaze[0]*float64(a.W), s.Gaze[1]*float64(a.H)
		a.Dyn(x11ui.Ring(img, x, y, a.Pf(20), a.Pf(2), x11ui.Alpha(colText, 0.6)))
		if s.Oc.Phase == oculo.Fixation {
			fx, fy := s.Oc.FixCentroid[0]*float64(a.W), s.Oc.FixCentroid[1]*float64(a.H)
			a.Dyn(x11ui.Circle(img, fx, fy, a.Pf(5), colAccent))
		}
	}
	if a.Opt.Debug {
		a.Dyn(x11ui.Text(img, a.F.Small, a.Px(40), a.Px(62), a.hudLine(), colWarn, x11ui.Left))
	}
	if msg, col := a.trackerStatus(); col != colOK {
		a.Dyn(x11ui.Text(img, a.F.Body, a.W/2, a.H-a.Px(8), msg, col, x11ui.Center))
	}
}

// hudLine è la riga diagnostica: stato oculare, decisione, pupilla.
func (a *App) hudLine() string {
	s := a.snap
	oc := s.Oc
	line := oc.Phase.String()
	if oc.Phase == oculo.Fixation {
		line += fmt.Sprintf(" #%d (%d campioni, disp. %s°)", oc.FixID, oc.FixN, it(oc.FixRMSDeg, 2))
	}
	line += fmt.Sprintf(" · %s°/s, soglia saccade %s", it(oc.VelDeg, 0), it(oc.PeakThr, 0))
	if c := s.Dec.Candidate; c >= 0 {
		line += fmt.Sprintf(" · tasto %s da %s/%s ms, post %s, r %s", a.KB.Keys[c].Label,
			it(s.Dec.ElapsedMs, 0), it(s.Dec.RequiredMs, 0), it(s.Dec.Posterior, 2), it(s.Dec.PriorRatio, 1))
		if s.Dec.Aim < 1 {
			line += fmt.Sprintf(", mira %s", it(s.Dec.Aim, 2))
		}
		if s.Dec.Wait != "" {
			line += ", attesa: " + s.Dec.Wait
		}
	}
	p := s.Pupil
	if p.Valid {
		line += fmt.Sprintf(" · pupilla %s mm", it(p.MeasMM, 2))
		if p.Compensated {
			line += fmt.Sprintf(" (luce %s) Δ %s z %s", it(p.PredMM, 2), it(p.ResidMM, 2), it(p.Z, 1))
		}
	}
	line += fmt.Sprintf(" · L %s", it(p.LumEff, 3))
	return line
}

// ---------------------------------------------------------------------------
// Esercizio di copiatura: un blocco è una frase da copiare

type blockPhase int

const (
	bIntro blockPhase = iota
	bRunning
	bDone
)

type block struct {
	phase     blockPhase
	phrase    string
	number    int
	result    metrics.Result
	lastPos   int
	lastPrior uint64
	lastText  string
	scheduled bool
}

func newBlock() *block { return &block{} }

func (b *block) label(a *App) string {
	return engine.HybridLabel(a.Opt.UsePrediction, a.Opt.UsePupil, a.Opt.UseCorrector && a.Task == metrics.TaskText)
}

func (b *block) text(a *App) bool { return a.Task == metrics.TaskText }

func (b *block) Static(a *App, img *image.RGBA) {
	cx := a.W / 2
	switch b.phase {
	case bIntro:
		if b.phrase == "" {
			b.phrase = a.NewPhrase()
			b.number = len(a.Results) + 1
		}
		a.Disp.ShowCursor(false)
		title := fmt.Sprintf("Esercizio · frase %d", b.number)
		x11ui.Text(img, a.F.Title, cx, a.Px(300), title, colText, x11ui.Center)
		x11ui.Text(img, a.F.Heading, cx, a.Px(352), b.label(a), colAccent, x11ui.Center)
		desc := "Guarda il tasto: si attiva appena lo sguardo si ferma dopo la saccade, prima se la lettera è " +
			"probabile. Per le doppie esci dal tasto e rientra, oppure resta fermo."
		if b.text(a) && a.Opt.UseCorrector {
			desc += " Allo spazio il correttore sistema la parola (canc annulla); sopra la tastiera ci sono i suggerimenti."
		}
		if b.text(a) {
			desc += " Gli errori restano nel testo: correggili con canc. Chiudi l'ultima parola con lo spazio."
		}
		a.paragraph(img, a.F.Body, cx, a.Px(420), a.Px(1100), desc, colDim, x11ui.Center)
		x11ui.Text(img, a.F.Body, cx, a.Px(560), "Testo da copiare:", colFaint, x11ui.Center)
		x11ui.Text(img, a.KeyFont, cx, a.Px(650), b.phrase, colText, x11ui.Center)
		a.drawHints(img, cx, a.Px(780), []keyHint{{"Spazio", "inizia"}, {"Esc", "menu"}}, x11ui.Center)
		if !b.scheduled {
			b.scheduled = true
			a.AutoAdvance(1200*time.Millisecond, func() {
				if b.phase == bIntro && a.screen == b {
					b.start(a)
				}
			})
		}
	case bRunning:
		s := a.snap
		if b.text(a) {
			status := fmt.Sprintf("Frase %d · %s · errori commessi %d", b.number, b.label(a), s.Errors)
			x11ui.Text(img, a.F.Small, a.Px(40), a.Px(34), status, colDim, x11ui.Left)
			a.drawTarget(img, b.phrase, s.Typed)
			a.drawTyped(img, s.Typed, "", "")
		} else {
			status := fmt.Sprintf("Frase %d · %s · carattere %d di %d · errori %d", b.number, b.label(a),
				min(s.Pos+1, len([]rune(b.phrase))), len([]rune(b.phrase)), s.Errors)
			a.drawStrip(img, status, b.phrase, s.Pos)
		}
		a.drawKeyboard(img, s.Prior, a.Opt.Debug, a.Opt.UsePrediction)
	case bDone:
		a.drawBlockResult(img, b.number, b.result)
		a.drawHints(img, cx, a.Px(900), []keyHint{{"Invio", "continua"}, {"Esc", "menu"}}, x11ui.Center)
	}
}

func (b *block) start(a *App) {
	a.Eng.StartBlock(b.phrase)
	b.phase = bRunning
	a.snap = a.Eng.Snapshot()
	b.lastPos, b.lastPrior = 0, a.snap.PriorVersion
	a.RefreshStatic()
}

func (b *block) Frame(a *App, img *image.RGBA, now time.Time) {
	if b.phase != bRunning {
		return
	}
	s := a.snap
	if st := a.textState(s); s.Pos != b.lastPos || st != b.lastText || (a.Opt.Debug && s.PriorVersion != b.lastPrior) {
		b.lastPos, b.lastPrior, b.lastText = s.Pos, s.PriorVersion, st
		a.RefreshStatic()
	}
	a.drawFeedback(img, now)
	a.drawLastFix(img, now, typedBaseline)
}

func (b *block) EngineEvent(a *App, ev engine.Event) {
	if ev.Kind != engine.EvBlockDone || b.phase != bRunning {
		return
	}
	b.result = ev.Result
	b.phase = bDone
	a.AddResult(ev.Result)
	a.RefreshStatic()
	a.AutoAdvance(1500*time.Millisecond, func() {
		if a.screen == b {
			a.Next()
		}
	})
}

func (b *block) Event(a *App, ev x11ui.Event) {
	if ev.Kind != x11ui.KeyDown {
		return
	}
	switch b.phase {
	case bIntro:
		switch ev.Sym {
		case x11ui.KeySpace, x11ui.KeyReturn, x11ui.KeyKPEnter:
			b.start(a)
		case x11ui.KeyEscape:
			a.Home()
		}
	case bRunning:
		switch ev.Sym {
		case x11ui.KeyEscape:
			a.Eng.AbortBlock()
			a.Home()
		case 'd':
			a.Opt.Debug = !a.Opt.Debug
			a.RefreshStatic()
		case 'g':
			a.Opt.ShowGaze = !a.Opt.ShowGaze
		}
	case bDone:
		switch ev.Sym {
		case x11ui.KeySpace, x11ui.KeyReturn, x11ui.KeyKPEnter:
			a.Next()
		case x11ui.KeyEscape:
			a.Home()
		}
	}
}

// drawBlockResult disegna l'esito di un blocco.
func (a *App) drawBlockResult(img *image.RGBA, n int, r metrics.Result) {
	cx := a.W / 2
	x11ui.Text(img, a.F.Title, cx, a.Px(250), fmt.Sprintf("Frase %d completata", n), colText, x11ui.Center)
	x11ui.Text(img, a.F.Heading, cx, a.Px(300), r.Label, colAccent, x11ui.Center)
	items := []struct{ v, u string }{
		{it(r.CharsPerMin, 1), "caratteri/min"},
		{it(100*r.Accuracy, 0) + "%", "accuratezza"},
		{it(r.ITR, 0), "bit/min (ITR)"},
		{it(r.MeanDwellMs, 0), "ms di dwell medio"},
	}
	if r.Task == metrics.TaskText {
		items[1] = struct{ v, u string }{it(100*r.Accuracy, 0) + "%", "testo finale corretto"}
		items[3] = struct{ v, u string }{fmt.Sprintf("%d / %d", r.ErrorsMade, r.AutoFixes+r.Suggestions),
			"errori commessi / correzioni"}
		x11ui.Text(img, a.F.Heading, cx, a.Px(380), "«"+r.Text+"»", colText, x11ui.Center)
	}
	w := a.Px(330)
	x0 := cx - w*len(items)/2
	for i, it := range items {
		x := x0 + i*w + w/2
		x11ui.Text(img, a.F.Big, x, a.Px(480), it.v, colText, x11ui.Center)
		x11ui.Text(img, a.F.Body, x, a.Px(530), it.u, colDim, x11ui.Center)
	}
	line := fmt.Sprintf("%d selezioni, %d errori · %s s dalla prima all'ultima lettera · %s WPM · un carattere ogni %s s",
		r.Selections, r.Errors, it(r.SpanS, 1), it(r.WPM, 1), it(r.MeanIntervalS, 2))
	x11ui.Text(img, a.F.Body, cx, a.Px(640), line, colDim, x11ui.Center)
	if r.PupilComp {
		x11ui.Text(img, a.F.Small, cx, a.Px(690), fmt.Sprintf("pupilla al netto della luce: %s mm in media",
			it(r.PupilResidMM, 2)), colFaint, x11ui.Center)
	}
}
