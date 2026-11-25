// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Antonio Blescia

// Package app è l'interfaccia a schermo intero di Kite.
//
// Disegno a due livelli, come l'app di calibrazione del driver: lo statico
// (sfondo, tasti, testo da copiare) si ridisegna solo quando cambia; il
// dinamico (avanzamento, sguardo, pannelli dal vivo) a ogni frame, e al
// server X vanno solo le regioni toccate. Le decisioni non dipendono dal
// disegno: le prende l'engine nella sua goroutine.
//
// A ogni frame l'interfaccia calcola la luminanza dello schermo su una
// griglia grossolana e la passa all'engine, che ne ricava la luce vista
// dalla persona per compensare la pupilla.
package app

import (
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ablescia/kite/internal/driver"
	"github.com/ablescia/kite/internal/engine"
	"github.com/ablescia/kite/internal/gazein"
	"github.com/ablescia/kite/internal/lm"
	"github.com/ablescia/kite/internal/metrics"
	"github.com/ablescia/kite/internal/pupil"
	"github.com/ablescia/kite/internal/selector"
	"github.com/ablescia/kite/internal/x11ui"
)

// Palette: sfondo grigio a luminanza media (pupilla stabile), come la
// calibrazione del driver.
var (
	colBg     = x11ui.Hex(0x45494f)
	colPanel  = x11ui.Hex(0x34373c)
	colKey    = x11ui.Hex(0x5b6067)
	colKeyTxt = x11ui.Hex(0xf2f2ef)
	colText   = x11ui.Hex(0xf2f2ef)
	colDim    = x11ui.Hex(0xb3b8bf)
	colFaint  = x11ui.Hex(0x868c94)
	colAccent = x11ui.Hex(0x7fb7ff)
	colOK     = x11ui.Hex(0x93d3a2)
	colWarn   = x11ui.Hex(0xf1c56b)
	colBad    = x11ui.Hex(0xff8a80)
	colPred   = x11ui.Hex(0xffb067)
	colGhost  = x11ui.Hex(0xa0a6ae) // frase proposta: grigio chiaro, più tenue del testo
)

// Options sono le scelte dell'utente.
type Options struct {
	Words         int  // parole per frase dell'esercizio
	Blocks        int  // frasi dell'esercizio
	UsePrediction bool // espansione e dwell guidati dal modello di lingua
	UsePupil      bool // modulazione dal carico pupillare
	UseCorrector  bool // correttore italiano e suggerimenti (scrittura libera ed esercizio "testo")
	UsePhrases    bool // scrittura libera: frase proposta dalle frasi imparate
	// Tempi delle lettere: il preset (selector.Speeds; "" = personalizzati
	// da riga di comando) e i suoi valori.
	Speed          string
	TMinMs, TMaxMs float64
	Debug          bool // aree di tocco effettive e pannello diagnostico
	ShowGaze       bool
	Auto           bool // avanza da solo nelle attese (simulazione, test)
	Seed           uint64
}

// Screen è una schermata.
type Screen interface {
	Static(a *App, img *image.RGBA)
	Frame(a *App, img *image.RGBA, now time.Time)
	Event(a *App, ev x11ui.Event)
}

// engineListener è una schermata interessata alle notifiche dell'engine.
type engineListener interface {
	EngineEvent(a *App, ev engine.Event)
}

// App è l'applicazione.
type App struct {
	Eng          *engine.Engine
	Disp         x11ui.Display
	Geom         gazein.Geometry
	KB           *selector.Keyboard
	LM           *lm.Model
	Log          *engine.Logger
	Opt          Options
	ModelPath    string
	WordsPath    string // dizionario personale ("" = non si salva)
	PhrasesPath  string // frasi imparate, una per riga ("" = non si salvano)
	SettingsPath string // impostazioni che restano tra le sessioni ("" = non si salvano)
	Task         string // compito dell'esercizio (metrics.TaskText o TaskStrict)
	Source       string // "UDP 127.0.0.1:5005" o "simulazione"
	VoiceInfo    string // la voce del tasto parla, per esempio "it_IT-serena-high (Piper, 22050 Hz, pw-play)"
	VoiceErr     string // perché non c'è una voce
	UDPStats     *gazein.Stats
	ShotDir      string

	// Calibrazione dello sguardo, eseguita dal driver tobii4c.
	Driver     string // eseguibile del driver ("" = cercato)
	Monitor    string // monitor della finestra, su cui calibrare
	GeomDriver bool   // la geometria viene da display.json del driver (non da -display-mm)
	// GazeCal esegue la calibrazione e Inspect descrive il driver: di solito
	// driver.Calibrate e driver.Inspect, sostituibili nei test.
	GazeCal func(context.Context, driver.Options) driver.Result
	Inspect func(exe string) driver.State
	// Restart chiede a chi ha avviato Kite di riavviarlo (la calibrazione ha
	// cambiato la geometria dello schermo).
	Restart bool

	F       *x11ui.Fonts
	KeyFont *x11ui.Font
	W, H    int
	S       float64

	static, canvas  *image.RGBA
	prevDyn, curDyn []image.Rectangle
	full            bool
	screen          Screen
	async           chan func()
	ctx             context.Context
	flow            []func() Screen
	done            bool
	snap            engine.Snapshot
	rng             *rand.Rand
	used            map[string]bool
	Results         []metrics.Result
	lastShot        time.Time
	shotN           int
	srgb            [256]float32
	lum             engine.LumMap
}

// New prepara l'applicazione sul display.
func New(d x11ui.Display) (*App, error) {
	w, h := d.Size()
	f, err := x11ui.LoadFonts(h)
	if err != nil {
		return nil, err
	}
	a := &App{Disp: d, F: f, W: w, H: h, S: float64(h) / 1080, async: make(chan func(), 16), used: map[string]bool{},
		GazeCal: driver.Calibrate, Inspect: driver.Inspect}
	if a.KeyFont, err = x11ui.NewFont(true, 74*a.S); err != nil {
		return nil, err
	}
	b := image.Rect(0, 0, w, h)
	a.static, a.canvas = image.NewRGBA(b), image.NewRGBA(b)
	for i := range a.srgb {
		v := float64(i) / 255
		if v <= 0.04045 {
			v /= 12.92
		} else {
			v = math.Pow((v+0.055)/1.055, 2.4)
		}
		a.srgb[i] = float32(v)
	}
	a.lum = engine.LumMap{NX: 48, NY: 27, V: make([]float32, 48*27)}
	return a, nil
}

// Px scala una misura pensata per 1080p.
func (a *App) Px(v float64) int { return int(math.Round(v * a.S)) }

// Pf come Px, in virgola mobile.
func (a *App) Pf(v float64) float64 { return v * a.S }

// Dyn registra una regione del livello dinamico.
func (a *App) Dyn(r image.Rectangle) {
	if !r.Empty() {
		a.curDyn = append(a.curDyn, r)
	}
}

// SetScreen mostra una schermata.
func (a *App) SetScreen(s Screen) {
	a.screen = s
	a.RefreshStatic()
}

// RefreshStatic ridisegna il livello statico.
func (a *App) RefreshStatic() {
	x11ui.Fill(a.static, a.static.Bounds(), colBg)
	a.screen.Static(a, a.static)
	a.full = true
}

// Go esegue fn fuori dal loop e consegna il risultato nel loop.
func Go[T any](a *App, fn func() T, done func(T)) {
	ctx := a.ctx
	go func() {
		r := fn()
		select {
		case a.async <- func() { done(r) }:
		case <-ctx.Done():
		}
	}()
}

// After esegue fn nel loop dopo d.
func (a *App) After(d time.Duration, fn func()) {
	ctx := a.ctx
	go func() {
		select {
		case <-time.After(d):
			select {
			case a.async <- fn:
			case <-ctx.Done():
			}
		case <-ctx.Done():
		}
	}()
}

// AutoAdvance: in modalità automatica esegue fn dopo d (le attese dei tasti).
func (a *App) AutoAdvance(d time.Duration, fn func()) {
	if a.Opt.Auto {
		a.After(d, fn)
	}
}

// Flow imposta i passi successivi e avvia il primo.
func (a *App) Flow(steps ...func() Screen) {
	a.flow = steps
	a.Next()
}

// Next passa al passo successivo del flusso, o al menu.
func (a *App) Next() {
	if len(a.flow) == 0 {
		a.Home()
		return
	}
	next := a.flow[0]
	a.flow = a.flow[1:]
	a.SetScreen(next())
}

// Home torna al menu abbandonando il flusso.
func (a *App) Home() {
	a.flow = nil
	a.Eng.SetIdle()
	if a.Opt.Auto {
		a.done = true // in automatico l'esercizio finisce qui
		return
	}
	a.SetScreen(newHome())
}

// Quit chiude l'applicazione.
func (a *App) Quit() { a.done = true }

// Run esegue il loop a 60 Hz. start, se non nil, avvia la prima schermata
// (es. l'esercizio); altrimenti si parte dal menu.
func (a *App) Run(ctx context.Context, start func()) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	a.ctx = ctx
	a.rng = rand.New(rand.NewPCG(a.Opt.Seed, 0x5eed))
	a.Eng.SetToggles(a.Opt.UsePrediction, a.Opt.UsePupil, a.Opt.UseCorrector)
	a.snap = a.Eng.Snapshot()
	if start != nil {
		start()
	} else {
		a.SetScreen(newHome())
	}
	tk := time.NewTicker(time.Second / 60)
	defer tk.Stop()
	for !a.done {
		select {
		case <-ctx.Done():
			return nil
		case ev := <-a.Disp.EventChan():
			switch ev.Kind {
			case x11ui.Closed:
				return nil
			case x11ui.Expose:
				a.full = true
			default:
				a.screen.Event(a, ev)
			}
		case ev := <-a.Eng.Events():
			switch l, ok := a.screen.(engineListener); {
			case ev.Kind == engine.EvUserWord:
				a.SaveUserWord(ev.Word)
			case ev.Kind == engine.EvPhrase:
				a.SavePhrase(ev.Text)
			case ok:
				l.EngineEvent(a, ev)
			}
		case fn := <-a.async:
			fn()
		case now := <-tk.C:
			if err := a.frame(now); err != nil {
				return err
			}
		}
	}
	a.saveShot(true)
	return nil
}

func (a *App) frame(now time.Time) error {
	a.snap = a.Eng.Snapshot()
	full := a.full
	a.full = false
	if full {
		copy(a.canvas.Pix, a.static.Pix)
	} else {
		for _, r := range a.prevDyn {
			draw.Draw(a.canvas, r, a.static, r.Min, draw.Src)
		}
	}
	a.curDyn = a.curDyn[:0]
	a.screen.Frame(a, a.canvas, now)
	a.measureLuminance()
	if a.ShotDir != "" && (full || now.Sub(a.lastShot) > 2*time.Second) {
		a.lastShot = now
		a.saveShot(false)
	}
	var err error
	if full {
		err = a.Disp.Present(a.canvas, a.canvas.Bounds())
	} else {
		err = a.Disp.Present(a.canvas, mergeRects(append(a.prevDyn, a.curDyn...))...)
	}
	a.prevDyn = append(a.prevDyn[:0], a.curDyn...)
	return err
}

func (a *App) saveShot(final bool) {
	if a.ShotDir == "" {
		return
	}
	a.shotN++
	name := fmt.Sprintf("screen-%03d.png", a.shotN)
	if final {
		name = "screen-final.png"
	}
	if fh, err := os.Create(filepath.Join(a.ShotDir, name)); err == nil {
		png.Encode(fh, a.canvas)
		fh.Close()
	}
}

// measureLuminance calcola la luminanza relativa (lineare, bianco = 1) del
// frame su una griglia 48×27, campionando un pixel ogni 8.
func (a *App) measureLuminance() {
	m := &a.lum
	cw, ch := float64(a.W)/float64(m.NX), float64(a.H)/float64(m.NY)
	var tot float64
	for j := 0; j < m.NY; j++ {
		y0, y1 := int(float64(j)*ch), int(float64(j+1)*ch)
		for i := 0; i < m.NX; i++ {
			x0, x1 := int(float64(i)*cw), int(float64(i+1)*cw)
			var s float32
			n := 0
			for y := y0 + 3; y < y1; y += 8 {
				row := a.canvas.Pix[y*a.canvas.Stride:]
				for x := x0 + 3; x < x1; x += 8 {
					p := row[x*4:]
					s += 0.2126*a.srgb[p[0]] + 0.7152*a.srgb[p[1]] + 0.0722*a.srgb[p[2]]
					n++
				}
			}
			if n > 0 {
				s /= float32(n)
			}
			m.V[j*m.NX+i] = s
			tot += float64(s)
		}
	}
	m.Mean = tot / float64(m.NX*m.NY)
	a.Eng.SetLuminance(*m)
}

// mergeRects unisce i rettangoli che si sovrappongono.
func mergeRects(rs []image.Rectangle) []image.Rectangle {
	out := make([]image.Rectangle, 0, len(rs))
	for _, r := range rs {
		if r.Empty() {
			continue
		}
		merged := true
		for merged {
			merged = false
			for i := 0; i < len(out); i++ {
				if out[i].Inset(-4).Overlaps(r) {
					r = r.Union(out[i])
					out = append(out[:i], out[i+1:]...)
					merged = true
					break
				}
			}
		}
		out = append(out, r)
	}
	return out
}

// ---------------------------------------------------------------------------
// Esercizio e risultati

// NewPhrase estrae il testo di un blocco.
func (a *App) NewPhrase() string { return a.LM.Phrase(a.rng, a.Opt.Words, a.used) }

// Exercise avvia l'esercizio di copiatura: Opt.Blocks frasi da copiare con il
// selettore ibrido, poi i risultati. Se la pupilla non è ancora compensata
// per la luce (e sempre in automatico) comincia con la calibrazione
// pupilla–luminanza, così il carico pupillare si misura e modula i tempi.
func (a *App) Exercise() {
	var steps []func() Screen
	if !a.snap.ModelValid || a.Opt.Auto {
		steps = append(steps, func() Screen { return newCalib() })
	}
	for i := 0; i < max(1, a.Opt.Blocks); i++ {
		steps = append(steps, func() Screen { return newBlock() })
	}
	a.Flow(append(steps, func() Screen { return newResults() })...)
}

// AddResult registra un blocco concluso e aggiorna summary.json.
func (a *App) AddResult(r metrics.Result) {
	a.Results = append(a.Results, r)
	n := a.KB.Chars
	sum := map[string]any{
		"blocks": a.Results, "hybrid": metrics.Combine(engine.CondHybrid, a.Results, n),
		"source": a.Source, "keys": n, "options": a.Opt,
	}
	if err := a.Log.WriteJSON("summary.json", sum); err != nil {
		fmt.Fprintln(os.Stderr, "summary.json:", err)
	}
}

// toggle accende o spegne una componente del selettore ibrido (P, U, C).
func (a *App) toggle(k uint32) {
	switch k {
	case 'p':
		a.Opt.UsePrediction = !a.Opt.UsePrediction
	case 'u':
		a.Opt.UsePupil = !a.Opt.UsePupil
	case 'c':
		a.Opt.UseCorrector = !a.Opt.UseCorrector
	}
	a.Eng.SetToggles(a.Opt.UsePrediction, a.Opt.UsePupil, a.Opt.UseCorrector)
}

// Settings sono le scelte dell'utente che restano da una sessione all'altra.
type Settings struct {
	Speed string `json:"velocita"` // preset dei tempi delle lettere (selector.Speeds)
}

// LoadSettings legge le impostazioni salvate (vuote se il file manca).
func LoadSettings(path string) Settings {
	var s Settings
	if path == "" {
		return s
	}
	if b, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(b, &s); err != nil {
			fmt.Fprintln(os.Stderr, "impostazioni:", err)
		}
	}
	return s
}

func (a *App) saveSettings() {
	if a.SettingsPath == "" {
		return
	}
	b, _ := json.MarshalIndent(Settings{Speed: a.Opt.Speed}, "", "  ")
	err := os.MkdirAll(filepath.Dir(a.SettingsPath), 0o755)
	if err == nil {
		err = os.WriteFile(a.SettingsPath, append(b, '\n'), 0o644)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "impostazioni:", err)
	}
}

// cycleSpeed passa al preset successivo dei tempi delle lettere (dai tempi
// personalizzati al primo) e lo ricorda per le sessioni successive. Vale
// dalla scrittura libera o dall'esercizio successivi.
func (a *App) cycleSpeed() {
	i := -1
	for j, s := range selector.Speeds {
		if s.Name == a.Opt.Speed {
			i = j
		}
	}
	s := selector.Speeds[(i+1)%len(selector.Speeds)]
	a.Opt.Speed, a.Opt.TMinMs, a.Opt.TMaxMs = s.Name, s.TMinMs, s.TMaxMs
	a.Eng.SetTiming(s.TMinMs, s.TMaxMs)
	a.Log.Event("speed", map[string]any{"speed": s.Name, "tmin_ms": s.TMinMs, "tmax_ms": s.TMaxMs})
	a.saveSettings()
}

// speedLabel descrive i tempi delle lettere per il menu.
func (a *App) speedLabel() string {
	name := a.Opt.Speed
	if name == "" {
		name = "personalizzata"
	}
	return fmt.Sprintf("velocità delle lettere: %s (%s–%s ms)", name, it(a.Opt.TMinMs, 0), it(a.Opt.TMaxMs, 0))
}

// SaveUserWord aggiunge una parola al dizionario personale (una per riga).
func (a *App) SaveUserWord(w string) { appendLine(a.WordsPath, w, "dizionario personale") }

// SavePhrase aggiunge una frase imparata (una per riga: le ripetizioni
// contano come usi).
func (a *App) SavePhrase(p string) { appendLine(a.PhrasesPath, p, "frasi imparate") }

func appendLine(path, line, what string) {
	line = strings.TrimSpace(strings.ReplaceAll(line, "\n", " "))
	if path == "" || line == "" {
		return
	}
	os.MkdirAll(filepath.Dir(path), 0o755)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		fmt.Fprintln(os.Stderr, what+":", err)
		return
	}
	fmt.Fprintln(f, line)
	if err := f.Close(); err != nil {
		fmt.Fprintln(os.Stderr, what+":", err)
	}
}

// SaveModel salva il modello pupilla–luminanza per le sessioni successive.
func (a *App) SaveModel(m pupil.Model) {
	if a.ModelPath == "" {
		return
	}
	os.MkdirAll(filepath.Dir(a.ModelPath), 0o755)
	b, _ := json.MarshalIndent(m, "", "  ")
	os.WriteFile(a.ModelPath, b, 0o644)
}

// ---------------------------------------------------------------------------
// Elementi comuni

type keyHint struct{ key, label string }

// drawHints disegna una riga di tasti con la loro azione.
func (a *App) drawHints(img *image.RGBA, x, baseline int, keys []keyHint, align x11ui.Align) image.Rectangle {
	gapKey, gapItem := a.Px(10), a.Px(34)
	padX, padY := a.Px(9), a.Px(6)
	total := 0
	for i, k := range keys {
		total += a.F.Key.Measure(k.key) + 2*padX + gapKey + a.F.Small.Measure(k.label)
		if i > 0 {
			total += gapItem
		}
	}
	switch align {
	case x11ui.Center:
		x -= total / 2
	case x11ui.Right:
		x -= total
	}
	box := image.Rect(x, baseline-a.F.Small.Ascent-padY*2, x+total, baseline+a.F.Small.Desc+padY)
	for _, k := range keys {
		kw := a.F.Key.Measure(k.key) + 2*padX
		cp := image.Rect(x, baseline-a.F.Key.Ascent-padY, x+kw, baseline+a.F.Key.Desc+padY)
		x11ui.RoundRect(img, cp, a.Pf(6), colKey)
		x11ui.Text(img, a.F.Key, x+padX, baseline, k.key, colText, x11ui.Left)
		x += kw + gapKey
		r := x11ui.Text(img, a.F.Small, x, baseline, k.label, colDim, x11ui.Left)
		x = r.Max.X + gapItem
	}
	return box
}

// paragraph disegna un testo a capo automatico; restituisce la y successiva.
func (a *App) paragraph(img *image.RGBA, f *x11ui.Font, x, y, width int, s string, c color.RGBA, align x11ui.Align) int {
	lh := int(float64(f.Height) * 1.35)
	for _, line := range x11ui.Wrap(f, s, width) {
		x11ui.Text(img, f, x, y, line, c, align)
		y += lh
	}
	return y
}

// rect converte un rettangolo normalizzato in pixel.
func (a *App) rect(r [4]float64) image.Rectangle {
	return image.Rect(int(r[0]*float64(a.W)), int(r[1]*float64(a.H)), int(r[2]*float64(a.W)), int(r[3]*float64(a.H)))
}

// it formatta un decimale all'italiana.
func it(v float64, digits int) string {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return "–"
	}
	s := fmt.Sprintf("%.*f", digits, v)
	if strings.Trim(s, "-0.") == "" {
		s = strings.TrimPrefix(s, "-")
	}
	return strings.Replace(s, ".", ",", 1)
}

// trackerStatus riassume lo stato del flusso dati per la riga di stato.
func (a *App) trackerStatus() (string, color.RGBA) {
	s := a.snap
	if s.Samples == 0 || time.Since(s.LastSample) > 700*time.Millisecond {
		if a.Source == "simulazione" {
			return "simulazione in avvio…", colWarn
		}
		return "nessun dato dal driver: avvia il servizio tobii4c, o «tobii4c run -headpose off -display-mm saved -calibration saved»", colBad
	}
	if s.Presence == 1 || !s.EyeValid {
		return fmt.Sprintf("%s Hz · occhi non tracciati: siediti davanti al sensore", it(s.RateHz, 0)), colWarn
	}
	return fmt.Sprintf("%s Hz · distanza %s cm · pupilla valida %s%%", it(s.RateHz, 0), it(s.EyeMM[2]/10, 0),
		it(100*s.PupilValid, 0)), colOK
}
