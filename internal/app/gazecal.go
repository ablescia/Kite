// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Antonio Blescia

package app

import (
	"fmt"
	"image"
	"image/color"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ablescia/kite/internal/driver"
	"github.com/ablescia/kite/internal/x11ui"
)

// ---------------------------------------------------------------------------
// Calibrazione dello sguardo
//
// La esegue il driver tobii4c nella sua finestra a schermo intero: Kite
// controlla chi usa il tracker, nasconde la propria finestra, lancia
// `tobii4c calibrate` sullo stesso monitor e, alla fine, mostra la verifica
// misurata dal driver e riprende lo schermo.

// suspender è un display che può lasciare lo schermo e la tastiera a
// un'altra applicazione (la finestra X11; quello headless no).
type suspender interface {
	Suspend()
	Resume() error
}

// gazeAccOK è l'accuratezza media della verifica oltre la quale conviene
// ripetere la calibrazione. Sulla QWERTY di uno schermo di 700 × 390 mm visto
// da 62 cm, con σ = 1,6° e priori uguali, la posteriore del tasto guardato
// scende sotto 0,95 a 1,85–2,15° dal centro verso i vicini, e lì il selettore
// allunga i tempi per prudenza. Lo sguardo finale si disperde già di ~0,65°
// attorno al punto mirato (misura al banco): con uno scarto sistematico
// oltre 1,85 − 0,65 ≈ 1,2° molte fissazioni finiscono oltre quel limite.
const gazeAccOK = 1.2

type gcPhase int

const (
	gcIntro gcPhase = iota
	gcRunning
	gcDone
)

type gazeCal struct {
	phase     gcPhase
	st        driver.State
	probed    bool
	probing   bool
	nextProbe time.Time
	shown     string // stato disegnato, per ridisegnare solo quando cambia
	res       driver.Result
}

func newGazeCal() *gazeCal { return &gazeCal{} }

func (a *App) simulated() bool { return a.Source == "simulazione" }

// gazeCalStatus è la riga del menu sulla calibrazione dello sguardo.
func (a *App) gazeCalStatus() (string, color.RGBA) {
	if a.simulated() {
		return "Sguardo simulato: la calibrazione dello sguardo serve con il tracker vero", colFaint
	}
	t := driver.LastCalibration()
	if t.IsZero() {
		return "Sguardo: il driver non ha una calibrazione salvata: falla prima di scrivere (tasto 1)", colWarn
	}
	return fmt.Sprintf("Sguardo: ultima calibrazione salvata dal driver il %s alle %s (tasto 1 per rifarla)",
		t.Format("2/1/2006"), t.Format("15:04")), colDim
}

type statusLine struct {
	text string
	col  color.RGBA
}

// statusLines descrive il driver e il tracker prima di calibrare.
func (g *gazeCal) statusLines(a *App) []statusLine {
	var out []statusLine
	add := func(c color.RGBA, f string, v ...any) { out = append(out, statusLine{fmt.Sprintf(f, v...), c}) }
	if a.simulated() {
		add(colWarn, "Con l'utente simulato non c'è un tracker da calibrare.")
		add(colFaint, "La calibrazione dello sguardo si fa con il tracker vero: avvia Kite senza -simulate.")
		return out
	}
	if !g.probed {
		add(colFaint, "Controllo del driver e del tracker…")
		return out
	}
	st := g.st
	if st.ExeErr != nil {
		add(colBad, "%s", st.ExeErr)
		return out
	}
	if !st.Connected {
		add(colWarn, "Tracker non trovato sulla USB: collega il Tobii 4C.")
	}
	switch {
	case st.Busy():
		h := st.Manual[0]
		add(colBad, "Il tracker è aperto da «%s» (PID %d), avviato a mano: chiudilo (Ctrl+C nel suo terminale).", h.Command(), h.PID)
		add(colFaint, "Kite se ne accorge da solo. Dopo la calibrazione il driver va riavviato.")
	case len(st.Units) > 0:
		add(colOK, "Il driver gira come servizio (%s): Kite lo ferma durante la calibrazione e poi lo riavvia.",
			strings.Join(st.Units, ", "))
	case st.SystemUnit:
		add(colWarn, "È attivo un %s di sistema: Kite non può fermarlo («sudo systemctl stop tobii4c»).", driver.ServiceUnit)
	case a.receiving():
		add(colWarn, "Kite riceve i dati ma non trova il programma che tiene il tracker: se il driver dice che è occupato, chiudilo e riprova.")
	default:
		add(colDim, "Nessun driver in esecuzione: dopo la calibrazione avvialo («tobii4c run -headpose off» o il servizio).")
	}
	var info []string
	if !st.LastCal.IsZero() {
		info = append(info, "ultima calibrazione "+st.LastCal.Format("2/1/2006 15:04"))
	}
	if d := st.Display; d.WidthMM > 0 {
		info = append(info, fmt.Sprintf("schermo %s × %s mm", it(d.WidthMM, 0), it(d.HeightMM, 0)))
	}
	info = append(info, "driver "+st.Exe)
	add(colFaint, "%s", strings.Join(info, " · "))
	return out
}

// receiving dice se i campioni del driver stanno arrivando.
func (a *App) receiving() bool {
	return a.snap.Samples > 0 && time.Since(a.snap.LastSample) < 700*time.Millisecond
}

// ready dice se si può avviare la calibrazione.
func (g *gazeCal) ready(a *App) bool {
	return !a.simulated() && g.probed && g.st.ExeErr == nil && !g.st.Busy()
}

func (g *gazeCal) Static(a *App, img *image.RGBA) {
	cx := a.W / 2
	switch g.phase {
	case gcIntro:
		a.Disp.ShowCursor(true)
		x11ui.Text(img, a.F.Title, cx, a.Px(190), "Calibrazione dello sguardo", colText, x11ui.Center)
		y := a.paragraph(img, a.F.Body, cx, a.Px(260), a.Px(1250),
			"La fa chi userà Kite, con i propri occhi, seduto a 50–80 cm dallo schermo come quando scrive. Va rifatta "+
				"se cambiano la persona, il monitor o la posizione del sensore, e con occhiali o lenti a contatto diversi.",
			colDim, x11ui.Center)
		y = a.paragraph(img, a.F.Body, cx, y+a.Px(14), a.Px(1250),
			"La esegue il driver tobii4c nella sua finestra: posizione del sensore, posizione della persona, 7 punti "+
				"da fissare e una verifica su 5 punti. Basta guardare i punti; i tasti (Spazio, Invio) li può premere "+
				"chi assiste. Alla fine si torna qui.", colDim, x11ui.Center)
		y += a.Px(50)
		lines := g.statusLines(a)
		for _, l := range lines {
			x11ui.Text(img, a.F.Body, cx, y, l.text, l.col, x11ui.Center)
			y += a.Px(46)
		}
		g.shown = g.key(a)
		hints := []keyHint{{"Esc", "menu"}}
		if g.ready(a) {
			hints = append([]keyHint{{"Spazio", "calibra"}}, hints...)
		}
		a.drawHints(img, cx, a.Px(900), hints, x11ui.Center)
	case gcRunning:
		a.Disp.ShowCursor(true)
		x11ui.Text(img, a.F.Title, cx, a.H/2, "Calibrazione in corso nella finestra del driver…", colText, x11ui.Center)
	case gcDone:
		g.drawResult(a, img)
	}
}

// key riassume ciò che lo schermo iniziale mostra.
func (g *gazeCal) key(a *App) string {
	var b strings.Builder
	for _, l := range g.statusLines(a) {
		b.WriteString(l.text)
		b.WriteByte(0)
	}
	if g.ready(a) {
		b.WriteByte('r')
	}
	return b.String()
}

func (g *gazeCal) Frame(a *App, img *image.RGBA, now time.Time) {
	switch g.phase {
	case gcIntro:
		if !a.simulated() && !g.probing && !now.Before(g.nextProbe) {
			g.probing = true
			exe := a.Driver
			Go(a, func() driver.State { return a.Inspect(exe) }, func(st driver.State) {
				g.st, g.probed, g.probing = st, true, false
				g.nextProbe = time.Now().Add(time.Second)
				if g.phase == gcIntro && a.screen == g && g.key(a) != g.shown {
					a.RefreshStatic()
				}
			})
		}
	case gcDone:
		if msg, col := a.trackerStatus(); col != colOK || len(g.res.Restarted) > 0 {
			a.Dyn(x11ui.Text(img, a.F.Body, a.W/2, a.H-a.Px(60), msg, col, x11ui.Center))
		}
	}
}

func (g *gazeCal) start(a *App) {
	g.phase = gcRunning
	a.Eng.SetIdle()
	a.RefreshStatic()
	sus, _ := a.Disp.(suspender)
	logPath := ""
	if a.Log != nil && a.Log.Dir != "" {
		logPath = filepath.Join(a.Log.Dir, "gaze-calibration-"+time.Now().Format("150405")+".log")
	}
	a.Log.Event("gaze_calib_start", map[string]any{"driver": g.st.Exe, "units": g.st.Units, "monitor": a.Monitor})
	o := driver.Options{Exe: a.Driver, Monitor: a.Monitor, LogPath: logPath}
	if sus != nil {
		o.Before = sus.Suspend
		o.After = func() {
			if err := sus.Resume(); err != nil {
				fmt.Fprintln(os.Stderr, "kite:", err)
			}
		}
	}
	ctx := a.ctx
	Go(a, func() driver.Result { return a.GazeCal(ctx, o) }, func(r driver.Result) { g.finish(a, r) })
}

func (g *gazeCal) finish(a *App, r driver.Result) {
	g.res, g.phase = r, gcDone
	ev := map[string]any{"finished": r.Finished, "aborted": r.Aborted, "restored_previous": r.Restored,
		"points": r.Points, "stopped": r.Stopped, "restarted": r.Restarted, "took_s": round1(r.Took.Seconds()), "log": r.Log}
	if e, p, ok := r.Mean(); ok {
		ev["error_deg"], ev["precision_deg"] = e, p
	}
	if r.Err != nil {
		ev["error"] = r.Err.Error()
	}
	if r.Restart != nil {
		ev["restart_error"] = r.Restart.Error()
	}
	if r.DisplayChanged() {
		ev["display_before"], ev["display_after"] = r.Before, r.After
	}
	a.Log.Event("gaze_calib_end", ev)
	if a.screen == g {
		a.RefreshStatic()
	}
}

func round1(v float64) float64 { return float64(int(v*10+0.5)) / 10 }

// newGeom: il driver ha salvato una geometria dello schermo diversa da
// quella che Kite sta usando.
func (g *gazeCal) newGeom(a *App) bool {
	d := g.res.After
	return d.WidthMM > 0 && d.HeightMM > 0 && g.res.Err == nil &&
		!d.Same(driver.Display{WidthMM: a.Geom.WidthMM, HeightMM: a.Geom.HeightMM, OffsetXMM: d.OffsetXMM})
}

// needRestart: Kite deve ripartire per usare la geometria nuova (se non è
// stata imposta con -display-mm).
func (g *gazeCal) needRestart(a *App) bool { return a.GeomDriver && g.newGeom(a) }

func (g *gazeCal) drawResult(a *App, img *image.RGBA) {
	a.Disp.ShowCursor(true)
	cx := a.W / 2
	r := g.res
	title, sub, subCol := "Finestra del driver chiusa", "La calibrazione non è stata eseguita: resta quella di prima.", colDim
	switch {
	case r.Err != nil:
		title, sub, subCol = "Calibrazione non riuscita", r.Err.Error(), colBad
	case r.Finished:
		title, sub, subCol = "Calibrazione dello sguardo salvata", "È attiva sul tracker e salvata dal driver.", colOK
	case r.Aborted:
		title, sub = "Calibrazione interrotta", "Resta la calibrazione di prima."
		if r.Restored {
			sub = "Il driver ha rimesso sul tracker la calibrazione di prima."
		}
	}
	x11ui.Text(img, a.F.Title, cx, a.Px(240), title, colText, x11ui.Center)
	y := a.paragraph(img, a.F.Body, cx, a.Px(300), a.Px(1300), sub, subCol, x11ui.Center)
	y += a.Px(30)
	if e, p, ok := r.Mean(); ok {
		x11ui.Text(img, a.F.Big, cx, y+a.Px(30), fmt.Sprintf("%s° di accuratezza", it(e, 2)), colText, x11ui.Center)
		y += a.Px(90)
		verdict, vc := "Accuratezza adatta alla tastiera.", colOK
		if e > gazeAccOK {
			verdict, vc = "Oltre "+it(gazeAccOK, 1)+"°: lo sguardo cadrà spesso vicino ai bordi dei tasti, dove la tastiera rallenta per prudenza. Conviene ripeterla.", colWarn
		}
		x11ui.Text(img, a.F.Body, cx, y, fmt.Sprintf("verifica su %d punti · precisione %s°", len(r.Points), it(p, 2)),
			colDim, x11ui.Center)
		y += a.Px(44)
		x11ui.Text(img, a.F.Body, cx, y, verdict, vc, x11ui.Center)
		y += a.Px(56)
	}
	switch {
	case r.Restart != nil:
		y = a.paragraph(img, a.F.Body, cx, y, a.Px(1300), "Riavvio del driver non riuscito: "+r.Restart.Error(), colBad, x11ui.Center)
	case len(r.Restarted) > 0:
		x11ui.Text(img, a.F.Body, cx, y, "Servizio "+strings.Join(r.Restarted, ", ")+" riavviato.", colOK, x11ui.Center)
		y += a.Px(46)
	case r.Err == nil:
		x11ui.Text(img, a.F.Body, cx, y, "Se il driver non è in esecuzione, avvialo: «tobii4c run -headpose off».", colDim, x11ui.Center)
		y += a.Px(46)
	}
	if g.newGeom(a) {
		msg := fmt.Sprintf("Lo schermo misurato dal driver (%s × %s mm) è diverso da quello che Kite usa (%s × %s mm).",
			it(r.After.WidthMM, 0), it(r.After.HeightMM, 0), it(a.Geom.WidthMM, 0), it(a.Geom.HeightMM, 0))
		if g.needRestart(a) {
			msg += " Kite si riavvia per usarlo."
		} else {
			msg += " Kite usa le dimensioni date con -display-mm."
		}
		x11ui.Text(img, a.F.Body, cx, y, msg, colWarn, x11ui.Center)
		y += a.Px(46)
	}
	if r.Log != "" {
		x11ui.Text(img, a.F.Small, cx, y+a.Px(10), "output del driver: "+r.Log, colFaint, x11ui.Center)
	}
	hints := []keyHint{{"Invio", "continua"}, {"1", "ripeti"}, {"Esc", "menu"}}
	if g.needRestart(a) {
		hints = []keyHint{{"Invio", "riavvia Kite"}, {"Esc", "menu, senza riavviare"}}
	}
	a.drawHints(img, cx, a.Px(940), hints, x11ui.Center)
}

func (g *gazeCal) Event(a *App, ev x11ui.Event) {
	if ev.Kind != x11ui.KeyDown {
		return
	}
	switch g.phase {
	case gcIntro:
		switch ev.Sym {
		case x11ui.KeySpace, x11ui.KeyReturn, x11ui.KeyKPEnter:
			if g.ready(a) {
				g.start(a)
			}
		case x11ui.KeyEscape:
			a.Home()
		}
	case gcDone:
		switch ev.Sym {
		case x11ui.KeySpace, x11ui.KeyReturn, x11ui.KeyKPEnter:
			if g.needRestart(a) {
				a.Restart = true
				a.Quit()
				return
			}
			a.Next()
		case '1':
			if !g.needRestart(a) {
				*g = gazeCal{}
				a.RefreshStatic()
			}
		case x11ui.KeyEscape:
			a.Home()
		}
	}
}
