// SPDX-License-Identifier: AGPL-3.0-or-later

package app

import (
	"fmt"
	"image"
	"image/color"
	"math"
	"strings"
	"time"

	"github.com/ablescia/kite/internal/engine"
	"github.com/ablescia/kite/internal/metrics"
	"github.com/ablescia/kite/internal/x11ui"
)

// ---------------------------------------------------------------------------
// Menu

type home struct {
	gaze    string // stato della calibrazione dello sguardo
	gazeCol color.RGBA
}

func newHome() *home { return &home{} }

func onOff(v bool) string {
	if v {
		return "attiva"
	}
	return "spenta"
}

func (h *home) Static(a *App, img *image.RGBA) {
	a.Disp.ShowCursor(true)
	cx := a.W / 2
	x11ui.Text(img, a.F.Title, cx, a.Px(150), "Kite", colText, x11ui.Center)
	x11ui.Text(img, a.F.Body, cx, a.Px(196),
		"Tastiera a sguardo ibrida: tempi guidati dalle saccadi, dalla lingua e dalla pupilla", colDim, x11ui.Center)

	x := cx - a.Px(430)
	y := a.Px(330)
	items := []keyHint{
		{"1", "Calibrazione dello sguardo (driver tobii4c, circa 2 minuti)"},
		{"2", "Calibrazione pupilla–luminanza (30 s)"},
		{"3", "Scrittura libera con la voce"},
		{"4", fmt.Sprintf("Esercizio: copia %d frasi e misura velocità e accuratezza", max(1, a.Opt.Blocks))},
		{"5", "Laboratorio della pupilla"},
		{"R", fmt.Sprintf("Risultati della sessione (%d frasi)", len(a.Results))},
	}
	for _, it := range items {
		a.drawHints(img, x, y, []keyHint{it}, x11ui.Left)
		y += a.Px(52)
	}
	y += a.Px(30)
	x11ui.Text(img, a.F.Heading, x, y, "Componenti del selettore ibrido", colText, x11ui.Left)
	y += a.Px(46)
	a.drawHints(img, x, y, []keyHint{{"P", "predizione e aree di tocco dinamiche: " + onOff(a.Opt.UsePrediction)}}, x11ui.Left)
	y += a.Px(46)
	a.drawHints(img, x, y, []keyHint{{"U", "modulazione dal carico pupillare: " + onOff(a.Opt.UsePupil)}}, x11ui.Left)
	y += a.Px(46)
	a.drawHints(img, x, y, []keyHint{{"C", "correttore italiano e suggerimenti: " + onOff(a.Opt.UseCorrector)}}, x11ui.Left)
	y += a.Px(46)
	a.drawHints(img, x, y, []keyHint{{"V", a.speedLabel()}}, x11ui.Left)
	y += a.Px(46)
	a.drawHints(img, x, y, []keyHint{{"D", "diagnostica: " + onOff(a.Opt.Debug)}, {"G", "sguardo: " + onOff(a.Opt.ShowGaze)},
		{"Esc", "esci"}}, x11ui.Left)

	x11ui.Text(img, a.F.Small, cx, a.H-a.Px(30), fmt.Sprintf("sorgente: %s · sessione: %s", a.Source, a.Log.Dir),
		colFaint, x11ui.Center)
	h.gaze, h.gazeCol = a.gazeCalStatus()
}

func (h *home) Frame(a *App, img *image.RGBA, now time.Time) {
	msg, col := a.trackerStatus()
	a.Dyn(x11ui.Text(img, a.F.Body, a.W/2, a.Px(258), msg, col, x11ui.Center))
	s := a.snap
	m := "Pupilla: calibrazione della luminanza da fare (tasto 2): senza, il carico non è separato dalla luce"
	c := colWarn
	if s.ModelValid {
		m = fmt.Sprintf("Pupilla compensata per la luminanza: R² %s, %s mm per decade di luce (calibrata alle %s)",
			it(s.Model.R2, 2), it(s.Model.B, 2), s.Model.FittedAt.Format("15:04"))
		c = colOK
	}
	a.Dyn(x11ui.Text(img, a.F.Small, a.W/2, a.H-a.Px(70), m, c, x11ui.Center))
	a.Dyn(x11ui.Text(img, a.F.Small, a.W/2, a.H-a.Px(104), h.gaze, h.gazeCol, x11ui.Center))
}

func (h *home) Event(a *App, ev x11ui.Event) {
	if ev.Kind != x11ui.KeyDown {
		return
	}
	switch ev.Sym {
	case '1':
		a.Flow(func() Screen { return newGazeCal() })
	case '2':
		a.Flow(func() Screen { return newCalib() })
	case '3':
		a.SetScreen(newFree())
	case '4':
		a.Exercise()
	case '5':
		a.SetScreen(newLab())
	case 'r':
		a.SetScreen(newResults())
	case 'p', 'u', 'c':
		a.toggle(ev.Sym)
		a.RefreshStatic()
	case 'v':
		a.cycleSpeed()
		a.RefreshStatic()
	case 'd':
		a.Opt.Debug = !a.Opt.Debug
		a.RefreshStatic()
	case 'g':
		a.Opt.ShowGaze = !a.Opt.ShowGaze
		a.RefreshStatic()
	case x11ui.KeyEscape:
		a.Quit()
	}
}

// ---------------------------------------------------------------------------
// Calibrazione pupilla–luminanza

// Livelli di grigio (valore sRGB) e durata di ciascuno: alternano salti
// verso il chiaro (costrizione, veloce) e verso lo scuro (dilatazione, lenta).
var calibLevels = []float64{0.45, 0.05, 0.85, 0.25, 1.0, 0.0, 0.6, 0.15, 0.45}

const calibStep = 3300 * time.Millisecond

type calibPhase int

const (
	cIntro calibPhase = iota
	cRunning
	cFitting
	cDone
)

type calib struct {
	phase     calibPhase
	start     time.Time
	level     int
	res       engine.CalibResult
	scheduled bool
}

func newCalib() *calib { return &calib{} }

func gray(v float64) color.RGBA {
	g := uint8(math.Round(255 * math.Max(0, math.Min(1, v))))
	return color.RGBA{g, g, g, 255}
}

// contrast restituisce un colore leggibile su uno sfondo di valore v.
func contrast(v float64) color.RGBA {
	if v > 0.55 {
		return x11ui.Hex(0x202326)
	}
	return colText
}

func (c *calib) Static(a *App, img *image.RGBA) {
	cx, cy := a.W/2, a.H/2
	switch c.phase {
	case cIntro:
		a.Disp.ShowCursor(false)
		x11ui.Text(img, a.F.Title, cx, a.Px(330), "Calibrazione pupilla–luminanza", colText, x11ui.Center)
		a.paragraph(img, a.F.Body, cx, a.Px(410), a.Px(1050),
			"Guarda la croce al centro per circa 30 secondi, senza pensare a nulla di impegnativo. Lo sfondo "+
				"cambierà più volte luminosità: così si misura quanto la tua pupilla risponde alla luce di questo "+
				"monitor e di questa stanza, per poterla poi separare dal carico cognitivo.", colDim, x11ui.Center)
		a.drawHints(img, cx, a.Px(620), []keyHint{{"Spazio", "inizia"}, {"Esc", "menu"}}, x11ui.Center)
		if !c.scheduled {
			c.scheduled = true
			a.AutoAdvance(1200*time.Millisecond, func() {
				if a.screen == c && c.phase == cIntro {
					c.begin(a)
				}
			})
		}
	case cRunning:
		v := calibLevels[c.level]
		x11ui.Fill(img, img.Bounds(), gray(v))
		col := contrast(v)
		l, w := a.Pf(16), a.Pf(3)
		x11ui.Line(img, float64(cx)-l, float64(cy), float64(cx)+l, float64(cy), w, col)
		x11ui.Line(img, float64(cx), float64(cy)-l, float64(cx), float64(cy)+l, w, col)
	case cFitting:
		x11ui.Text(img, a.F.Title, cx, cy, "Calcolo del modello…", colText, x11ui.Center)
	case cDone:
		c.drawResult(a, img)
	}
}

func (c *calib) begin(a *App) {
	c.phase, c.start, c.level = cRunning, time.Now(), 0
	a.Eng.SetCue([2]float64{0.5, 0.5}, true, 0)
	a.Eng.StartCalib()
	a.RefreshStatic()
}

func (c *calib) Frame(a *App, img *image.RGBA, now time.Time) {
	if c.phase != cRunning {
		return
	}
	el := now.Sub(c.start)
	lv := int(el / calibStep)
	if lv >= len(calibLevels) {
		c.phase = cFitting
		a.RefreshStatic()
		Go(a, a.Eng.FinishCalib, func(r engine.CalibResult) {
			c.res, c.phase = r, cDone
			a.Eng.SetCue([2]float64{}, false, 0)
			if r.Err == nil {
				a.SaveModel(r.Model)
			}
			a.RefreshStatic()
			a.AutoAdvance(1500*time.Millisecond, func() {
				if a.screen == c {
					a.Next()
				}
			})
		})
		return
	}
	if lv != c.level {
		c.level = lv
		a.RefreshStatic()
	}
	// avanzamento discreto in basso
	frac := float64(el) / float64(calibStep*time.Duration(len(calibLevels)))
	v := calibLevels[c.level]
	r := image.Rect(a.W/2-a.Px(150), a.H-a.Px(40), a.W/2-a.Px(150)+int(frac*float64(a.Px(300))), a.H-a.Px(36))
	a.Dyn(x11ui.Blend(img, r, x11ui.Alpha(contrast(v), 0.5)))
	if msg, col := a.trackerStatus(); col != colOK {
		a.Dyn(x11ui.Text(img, a.F.Body, a.W/2, a.H-a.Px(70), msg, col, x11ui.Center))
	}
}

func (c *calib) drawResult(a *App, img *image.RGBA) {
	cx := a.W / 2
	r := c.res
	if r.Err != nil {
		x11ui.Text(img, a.F.Title, cx, a.Px(400), "Calibrazione non riuscita", colText, x11ui.Center)
		x11ui.Text(img, a.F.Body, cx, a.Px(460), r.Err.Error(), colWarn, x11ui.Center)
		a.drawHints(img, cx, a.Px(560), []keyHint{{"Invio", "continua"}, {"2", "riprova"}}, x11ui.Center)
		return
	}
	m := r.Model
	x11ui.Text(img, a.F.Title, cx, a.Px(170), "Pupilla e luce: modello stimato", colText, x11ui.Center)
	verdict, vc := "la luce spiega bene la pupilla: compensazione affidabile", colOK
	if m.R2 < 0.6 {
		verdict, vc = "la luce spiega poco la pupilla: compensazione incerta (rumore o movimenti)", colWarn
	}
	x11ui.Text(img, a.F.Body, cx, a.Px(220), fmt.Sprintf(
		"D = %s %s %s·log₁₀(L)  ·  ritardo %s ms  ·  costrizione τ %s s, dilatazione τ %s s  ·  R² %s  ·  residuo %s mm",
		it(m.A, 2), "−", it(-m.B, 2), it(m.DelayS*1000, 0), it(m.TauConS, 2), it(m.TauDilS, 2), it(m.R2, 2),
		it(m.NoiseMM, 3)), colDim, x11ui.Center)
	x11ui.Text(img, a.F.Body, cx, a.Px(260), verdict, vc, x11ui.Center)

	panel := image.Rect(a.Px(160), a.Px(310), a.W-a.Px(160), a.Px(860))
	x11ui.RoundRect(img, panel, a.Pf(14), colPanel)
	plot := panel.Inset(a.Px(40))
	lo, hi := math.Inf(1), math.Inf(-1)
	for i, p := range r.Points {
		if !math.IsNaN(p.D) {
			lo, hi = math.Min(lo, p.D), math.Max(hi, p.D)
		}
		lo, hi = math.Min(lo, r.Pred[i]), math.Max(hi, r.Pred[i])
	}
	if hi <= lo {
		hi = lo + 1
	}
	pad := (hi - lo) * 0.08
	lo, hi = lo-pad, hi+pad
	t0, t1 := r.Points[0].T, r.Points[len(r.Points)-1].T
	X := func(t float64) int { return plot.Min.X + int((t-t0)/(t1-t0)*float64(plot.Dx())) }
	Y := func(d float64) int { return plot.Max.Y - int((d-lo)/(hi-lo)*float64(plot.Dy())) }
	// luminanza sullo sfondo, come banda grigia
	for i := 1; i < len(r.Points); i++ {
		x := X(r.Points[i].T)
		g := gray(math.Pow(r.Points[i].L, 1/2.2) * 0.35)
		x11ui.Fill(img, image.Rect(x, plot.Max.Y+a.Px(8), x+2, plot.Max.Y+a.Px(24)), g)
	}
	for i, p := range r.Points {
		if !math.IsNaN(p.D) {
			x, y := X(p.T), Y(p.D)
			x11ui.Blend(img, image.Rect(x-1, y-1, x+2, y+2), x11ui.Alpha(colText, 0.7))
		}
		if i > 0 {
			plotLine(img, X(r.Points[i-1].T), Y(r.Pred[i-1]), X(p.T), Y(r.Pred[i]), colPred, 3)
		}
	}
	x11ui.Text(img, a.F.Small, plot.Min.X, plot.Min.Y-a.Px(6),
		fmt.Sprintf("diametro pupillare %s–%s mm: misurato (punti) e previsto dalla luce (linea arancione); in basso la luminanza",
			it(lo, 1), it(hi, 1)), colDim, x11ui.Left)
	a.drawHints(img, cx, a.Px(940), []keyHint{{"Invio", "continua"}, {"2", "ripeti"}}, x11ui.Center)
}

func (c *calib) Event(a *App, ev x11ui.Event) {
	if ev.Kind != x11ui.KeyDown {
		return
	}
	switch c.phase {
	case cIntro:
		switch ev.Sym {
		case x11ui.KeySpace, x11ui.KeyReturn, x11ui.KeyKPEnter:
			c.begin(a)
		case x11ui.KeyEscape:
			a.Home()
		}
	case cRunning:
		if ev.Sym == x11ui.KeyEscape {
			a.Home()
		}
	case cDone:
		switch ev.Sym {
		case x11ui.KeySpace, x11ui.KeyReturn, x11ui.KeyKPEnter:
			a.Next()
		case '2':
			*c = calib{}
			a.RefreshStatic()
		case x11ui.KeyEscape:
			a.Home()
		}
	}
}

// ---------------------------------------------------------------------------
// Risultati

type results struct{ scheduled bool }

func newResults() *results { return &results{} }

func (rs *results) Static(a *App, img *image.RGBA) {
	a.Disp.ShowCursor(true)
	cx := a.W / 2
	x11ui.Text(img, a.F.Title, cx, a.Px(120), "Risultati della sessione", colText, x11ui.Center)
	if len(a.Results) == 0 {
		x11ui.Text(img, a.F.Body, cx, a.Px(400), "Nessuna frase completata: prova l'esercizio di copiatura (tasto 4).", colDim, x11ui.Center)
		a.drawHints(img, cx, a.Px(500), []keyHint{{"Invio", "menu"}}, x11ui.Center)
		return
	}
	g := metrics.Combine(engine.CondHybrid, a.Results, a.KB.Chars)
	acc := it(100*g.Accuracy, 1) + "% di selezioni corrette"
	if g.Task == metrics.TaskText {
		acc = it(100*g.Accuracy, 1) + "% di testo finale corretto"
	}
	x11ui.Text(img, a.F.Big, cx, a.Px(225), it(g.CharsPerMin, 1)+" caratteri al minuto", colOK, x11ui.Center)
	x11ui.Text(img, a.F.Body, cx, a.Px(272), fmt.Sprintf("%d frasi · %s · ITR %s bit/min · %s", g.Blocks, acc,
		it(g.ITR, 0), g.Label), colDim, x11ui.Center)
	type row struct {
		name string
		v    float64
		dig  int
		unit string
	}
	rows := []row{
		{"Caratteri corretti al minuto", g.CharsPerMin, 1, ""},
		{"Parole al minuto (WPM)", g.WPM, 1, ""},
		{"Accuratezza", 100 * g.Accuracy, 1, "%"},
		{"Selezioni al minuto", g.SelPerMin, 1, ""},
		{"ITR (bit/min, Wolpaw)", g.ITR, 0, ""},
		{"Dwell medio (ms)", g.MeanDwellMs, 0, ""},
		{"Tempo per selezione (s)", g.MeanIntS, 2, ""},
		{"Pupilla al netto della luce (mm)", g.PupilResid, 2, ""},
	}
	if g.Task == metrics.TaskText {
		rows[0].name, rows[2].name, rows[4].name = "Caratteri al minuto", "Testo finale corretto (1 − CER)", "ITR effettivo (bit/min)"
		rows = append(rows[:6],
			row{"Errori commessi", float64(g.ErrorsMade), 0, ""},
			row{"Correzioni (automatiche + suggerimenti)", float64(g.AutoFixes + g.Suggestions), 0, ""},
			row{"Selezioni per carattere (KSPC)", g.KSPC, 2, ""},
		)
	}
	x0, x1 := cx-a.Px(640), cx+a.Px(600)
	y := a.Px(350)
	x11ui.Text(img, a.F.Small, x1, y, fmt.Sprintf("tutte le frasi (%d)", g.Blocks), colDim, x11ui.Right)
	y += a.Px(14)
	x11ui.Fill(img, image.Rect(x0, y, x1, y+1), colFaint)
	y += a.Px(44)
	for _, r := range rows {
		x11ui.Text(img, a.F.Body, x0, y, r.name, colText, x11ui.Left)
		x11ui.Text(img, a.F.Heading, x1, y, it(r.v, r.dig)+r.unit, colText, x11ui.Right)
		y += a.Px(50)
	}
	y += a.Px(20)
	for _, r := range a.Results {
		x11ui.Text(img, a.F.Small, x0, y, fmt.Sprintf("#%d", r.ID), colDim, x11ui.Left)
		x11ui.Text(img, a.F.Small, x0+a.Px(60), y, "«"+r.Phrase+"»", colFaint, x11ui.Left)
		x11ui.Text(img, a.F.Small, x1-a.Px(420), y, it(r.CharsPerMin, 1)+" car/min", colDim, x11ui.Right)
		x11ui.Text(img, a.F.Small, x1-a.Px(250), y, it(100*r.Accuracy, 0)+"%", colDim, x11ui.Right)
		x11ui.Text(img, a.F.Small, x1, y, it(r.ITR, 0)+" bit/min", colDim, x11ui.Right)
		y += a.Px(28)
	}
	x11ui.Text(img, a.F.Small, cx, a.H-a.Px(70), "dati grezzi e summary.json in "+a.Log.Dir, colFaint, x11ui.Center)
	a.drawHints(img, cx, a.H-a.Px(25), []keyHint{{"Invio", "menu"}}, x11ui.Center)
	if !rs.scheduled {
		rs.scheduled = true
		a.AutoAdvance(2500*time.Millisecond, a.Quit)
	}
}

func (rs *results) Frame(*App, *image.RGBA, time.Time) {}

func (rs *results) Event(a *App, ev x11ui.Event) {
	if ev.Kind == x11ui.KeyDown && (ev.Sym == x11ui.KeyReturn || ev.Sym == x11ui.KeyKPEnter || ev.Sym == x11ui.KeyEscape || ev.Sym == x11ui.KeySpace) {
		a.Home()
	}
}

// ---------------------------------------------------------------------------
// Scrittura libera

type free struct {
	debug     bool
	lastTyped string
	lastPrior uint64
	ghost     image.Rectangle // dove è disegnata la frase proposta
}

func newFree() *free { return &free{debug: true} }

func (f *free) Static(a *App, img *image.RGBA) {
	a.Disp.ShowCursor(false)
	s := a.snap
	if s.Mode != engine.ModeFree {
		a.Eng.StartFree()
		s = a.Eng.Snapshot()
		a.snap = s
	}
	f.lastTyped, f.lastPrior = a.textState(s), s.PriorVersion
	status := "Scrittura libera · " + engine.HybridLabel(a.Opt.UsePrediction, a.Opt.UsePupil, a.Opt.UseCorrector) +
		" · Invio parla · Backspace cancella · D aree di tocco · G sguardo · P predizione · U pupilla · C correttore · Esc menu"
	if a.Opt.UsePhrases {
		status = strings.Replace(status, " · Backspace", " · Tab frase · Backspace", 1)
	}
	x11ui.Text(img, a.F.Small, a.Px(40), a.Px(34), status, colDim, x11ui.Left)
	if s.Voice {
		info := "Voce " + a.VoiceInfo + " · «parla» dice il testo, poi lo cancella"
		if a.Opt.UsePhrases {
			info += " · fissa la frase grigia per accettarla"
		}
		x11ui.Text(img, a.F.Small, a.Px(40), a.Px(100), info, colDim, x11ui.Left)
	} else {
		x11ui.Text(img, a.F.Small, a.Px(40), a.Px(100), "Voce non disponibile: "+a.VoiceErr, colWarn, x11ui.Left)
	}
	f.ghost = a.drawTyped(img, s.Typed, "scrivi guardando i tasti", s.Phrase)
	if !f.ghost.Empty() {
		a.Eng.SetPhraseRect(s.PhraseVer, a.norm(f.ghost))
	}
	a.drawKeyboard(img, s.Prior, f.debug, a.Opt.UsePrediction)
}

func (f *free) Frame(a *App, img *image.RGBA, now time.Time) {
	s := a.snap
	if a.textState(s) != f.lastTyped || (f.debug && s.PriorVersion != f.lastPrior) {
		a.RefreshStatic()
	}
	dbg := a.Opt.Debug
	a.Opt.Debug = f.debug
	a.drawFeedback(img, now)
	a.drawPhraseProgress(img, f.ghost, s.PhraseProg)
	a.drawLastFix(img, now, 100) // sulla riga della voce: la riga del testo è per il testo e la frase grigia
	a.Opt.Debug = dbg
}

func (f *free) Event(a *App, ev x11ui.Event) {
	if ev.Kind != x11ui.KeyDown {
		return
	}
	switch ev.Sym {
	case x11ui.KeyBackSpace:
		a.Eng.Backspace()
	case x11ui.KeyReturn, x11ui.KeyKPEnter:
		a.Eng.Speak()
	case x11ui.KeyTab:
		a.Eng.AcceptPhrase()
	case x11ui.KeyEscape:
		a.Home()
	case 'd':
		f.debug = !f.debug
		a.RefreshStatic()
	case 'g':
		a.Opt.ShowGaze = !a.Opt.ShowGaze
	case 'p', 'u', 'c':
		a.toggle(ev.Sym)
		a.Eng.StartFree()
		a.snap = a.Eng.Snapshot()
		a.RefreshStatic()
	}
}

// ---------------------------------------------------------------------------
// Laboratorio della pupilla

type lab struct {
	auto    bool
	level   float64
	shown   float64
	load    bool
	t0      time.Time
	trace   []engine.TracePt
	entered bool
}

func newLab() *lab { return &lab{auto: true, level: 0.45, shown: -1} }

var labFix = [2]float64{0.5, 0.2}

func (l *lab) Static(a *App, img *image.RGBA) {
	if !l.entered {
		l.entered, l.t0 = true, time.Now()
		a.Eng.SetLab()
		a.Eng.SetCue(labFix, true, 0)
		a.Disp.ShowCursor(false)
	}
	v := l.shown
	x11ui.Fill(img, img.Bounds(), gray(v))
	col := contrast(v)
	cx, cy := labFix[0]*float64(a.W), labFix[1]*float64(a.H)
	x11ui.Line(img, cx-a.Pf(14), cy, cx+a.Pf(14), cy, a.Pf(3), col)
	x11ui.Line(img, cx, cy-a.Pf(14), cx, cy+a.Pf(14), a.Pf(3), col)
	x11ui.Text(img, a.F.Heading, a.Px(60), a.Px(60), "Laboratorio della pupilla", col, x11ui.Left)
	x11ui.Text(img, a.F.Small, a.Px(60), a.Px(92), "Guarda la croce. L: luce automatica/fissa · + −: luce fissa · "+
		"M: calcolo mentale sì/no · Esc: menu", col, x11ui.Left)
	if l.load {
		x11ui.Text(img, a.F.Heading, a.W/2, int(cy)+a.Px(90),
			"Calcola a mente: parti da 1000 e togli 7, poi ancora 7, poi ancora…", col, x11ui.Center)
	} else {
		x11ui.Text(img, a.F.Body, a.W/2, int(cy)+a.Px(90), "Riposo: guarda la croce senza pensare a nulla di impegnativo",
			col, x11ui.Center)
	}
}

func (l *lab) Frame(a *App, img *image.RGBA, now time.Time) {
	want := l.level
	if l.auto {
		t := now.Sub(l.t0).Seconds()
		want = 0.5 + 0.33*math.Sin(2*math.Pi*t/16)
		want = math.Round(want*40) / 40 // gradini: ridisegno a schermo intero solo ~4 volte al secondo
	}
	if want != l.shown {
		l.shown = want
		a.RefreshStatic()
	}
	l.trace = a.Eng.PupilTrace(l.trace)
	panel := image.Rect(a.Px(60), int(0.42*float64(a.H)), a.W-a.Px(60), a.H-a.Px(30))
	x11ui.RoundRect(img, panel, a.Pf(14), colPanel)
	a.Dyn(panel)
	if len(l.trace) < 2 {
		return
	}
	last := l.trace[len(l.trace)-1]
	const window = 30.0
	t0 := last.T - window
	top := image.Rect(panel.Min.X+a.Px(90), panel.Min.Y+a.Px(50), panel.Max.X-a.Px(30), panel.Min.Y+panel.Dy()/2-a.Px(10))
	bot := image.Rect(top.Min.X, panel.Min.Y+panel.Dy()/2+a.Px(40), top.Max.X, panel.Max.Y-a.Px(30))
	X := func(t float64) int { return top.Min.X + int((t-t0)/window*float64(top.Dx())) }
	// intervalli di calcolo mentale: una banda per ogni tratto continuo
	for i := 0; i < len(l.trace); i++ {
		if !l.trace[i].Load || l.trace[i].T < t0 {
			continue
		}
		j := i
		for j+1 < len(l.trace) && l.trace[j+1].Load {
			j++
		}
		x11ui.Blend(img, image.Rect(X(l.trace[i].T), top.Min.Y, X(l.trace[j].T)+1, bot.Max.Y), x11ui.Alpha(colAccent, 0.14))
		i = j
	}
	lo, hi := math.Inf(1), math.Inf(-1)
	rlo, rhi := -0.1, 0.1
	for _, p := range l.trace {
		if p.T < t0 || !p.Valid {
			continue
		}
		lo, hi = math.Min(lo, p.Meas), math.Max(hi, p.Meas)
		if p.Compensated {
			lo, hi = math.Min(lo, p.Pred), math.Max(hi, p.Pred)
		}
		rlo, rhi = math.Min(rlo, p.Resid), math.Max(rhi, p.Resid)
	}
	if !(hi > lo) {
		lo, hi = 2, 6
	}
	pad := (hi - lo) * 0.1
	lo, hi = lo-pad, hi+pad
	comp := last.Compensated
	if !comp {
		// senza modello il "residuo" è il diametro stesso: si mostra centrato
		rlo, rhi = lo-((lo+hi)/2), hi-((lo+hi)/2)
	}
	Y1 := func(v float64) int { return top.Max.Y - int((v-lo)/(hi-lo)*float64(top.Dy())) }
	Y2 := func(v float64) int { return bot.Max.Y - int((v-rlo)/(rhi-rlo)*float64(bot.Dy())) }
	plotLine(img, bot.Min.X, Y2(0), bot.Max.X, Y2(0), colFaint, 1)
	var mid float64
	if !comp {
		mid = (lo + hi) / 2
	}
	for i := 1; i < len(l.trace); i++ {
		p, q := l.trace[i-1], l.trace[i]
		if p.T < t0 || !p.Valid || !q.Valid || q.T-p.T > 0.2 {
			continue
		}
		if comp {
			plotLine(img, X(p.T), Y1(p.Pred), X(q.T), Y1(q.Pred), colPred, 2)
		}
		plotLine(img, X(p.T), Y1(p.Meas), X(q.T), Y1(q.Meas), colText, 2)
		plotLine(img, X(p.T), Y2(p.Resid-mid), X(q.T), Y2(q.Resid-mid), colOK, 2)
	}
	x11ui.Text(img, a.F.Small, top.Min.X, top.Min.Y-a.Px(12),
		"diametro misurato (bianco) e previsto dalla sola luce (arancione), ultimi 30 s", colDim, x11ui.Left)
	lbl := "residuo: pupilla al netto della luce = indice di carico (verde)"
	if !comp {
		lbl = "senza calibrazione della luminanza: il verde è il diametro grezzo, NON compensato"
	}
	x11ui.Text(img, a.F.Small, bot.Min.X, bot.Min.Y-a.Px(12), lbl, colDim, x11ui.Left)
	x11ui.Text(img, a.F.Small, top.Min.X-a.Px(12), top.Min.Y+a.Px(12), it(hi, 1), colFaint, x11ui.Right)
	x11ui.Text(img, a.F.Small, top.Min.X-a.Px(12), top.Max.Y, it(lo, 1)+" mm", colFaint, x11ui.Right)
	x11ui.Text(img, a.F.Small, bot.Min.X-a.Px(12), bot.Min.Y+a.Px(12), "+"+it(rhi, 2), colFaint, x11ui.Right)
	x11ui.Text(img, a.F.Small, bot.Min.X-a.Px(12), bot.Max.Y, it(rlo, 2), colFaint, x11ui.Right)
	if last.Valid {
		x11ui.Text(img, a.F.Body, panel.Max.X-a.Px(30), panel.Min.Y+a.Px(34), fmt.Sprintf(
			"misurato %s mm · luce %s · residuo %s mm · z %s", it(last.Meas, 2), it(last.Pred, 2), it(last.Resid, 2),
			it(last.Z, 1)), colText, x11ui.Right)
	}
}

func (l *lab) Event(a *App, ev x11ui.Event) {
	if ev.Kind != x11ui.KeyDown {
		return
	}
	switch ev.Sym {
	case 'l':
		l.auto = !l.auto
		l.level = l.shown
	case '+', '=':
		l.auto = false
		l.level = math.Min(1, l.shown+0.1)
	case '-':
		l.auto = false
		l.level = math.Max(0, l.shown-0.1)
	case 'm':
		l.load = !l.load
		ld := 0.0
		if l.load {
			ld = 1
		}
		a.Eng.SetCue(labFix, true, ld)
		a.RefreshStatic()
	case x11ui.KeyEscape:
		a.Home()
	}
}

// plotLine traccia un segmento (Bresenham) di spessore th pixel, opaco:
// molto più veloce del rasterizzatore vettoriale per tracce lunghe.
func plotLine(img *image.RGBA, x0, y0, x1, y1 int, c color.RGBA, th int) {
	dx, dy := abs(x1-x0), -abs(y1-y0)
	sx, sy := 1, 1
	if x0 > x1 {
		sx = -1
	}
	if y0 > y1 {
		sy = -1
	}
	e := dx + dy
	b := img.Bounds()
	h := th / 2
	for {
		for oy := -h; oy < th-h; oy++ {
			for ox := -h; ox < th-h; ox++ {
				x, y := x0+ox, y0+oy
				if x >= b.Min.X && x < b.Max.X && y >= b.Min.Y && y < b.Max.Y {
					i := img.PixOffset(x, y)
					img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = c.R, c.G, c.B, 255
				}
			}
		}
		if x0 == x1 && y0 == y1 {
			return
		}
		e2 := 2 * e
		if e2 >= dy {
			e += dy
			x0 += sx
		}
		if e2 <= dx {
			e += dx
			y0 += sy
		}
	}
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
