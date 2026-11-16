// SPDX-License-Identifier: AGPL-3.0-or-later

// Package engine elabora lo stream dello sguardo in una goroutine dedicata,
// campione per campione (90 Hz), indipendentemente dal disegno
// dell'interfaccia: un frame lento non ritarda mai una decisione.
//
// Per ogni campione:
//
//  1. acquisizione: posizione degli occhi, sguardo filtrato e non filtrato
//  2. stato oculare: saccadi/fissazioni (oculo) e pupilla compensata per la
//     luminanza vista dalla persona (pupil)
//  3. attuazione: il selettore ibrido con il priore del modello di lingua
//  4. compito: scrittura libera, o esercizio di copiatura con il confronto
//     con il testo da copiare e le metriche; log della sessione
package engine

import (
	"context"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/ablescia/kite/internal/correct"
	"github.com/ablescia/kite/internal/gazein"
	"github.com/ablescia/kite/internal/lm"
	"github.com/ablescia/kite/internal/metrics"
	"github.com/ablescia/kite/internal/ngram"
	"github.com/ablescia/kite/internal/oculo"
	"github.com/ablescia/kite/internal/pupil"
	"github.com/ablescia/kite/internal/selector"
	"github.com/ablescia/kite/internal/speech"
)

// Mode è ciò che l'engine sta facendo.
type Mode int

const (
	ModeIdle  Mode = iota // menu e schermate: solo monitoraggio
	ModeCalib             // calibrazione pupilla–luminanza: registra
	ModeBlock             // esercizio di copiatura: seleziona e misura
	ModeFree              // scrittura libera con il selettore ibrido
	ModeLab               // laboratorio della pupilla
)

var modeNames = [...]string{"idle", "calib", "block", "free", "lab"}

func (m Mode) String() string { return modeNames[m] }

// DefaultPriorEps è la quota uniforme predefinita mescolata al modello di lingua.
const DefaultPriorEps = 0.1

// CondHybrid è la condizione registrata nei log dei blocchi (campo "cond"):
// il selettore ibrido. Si chiama "B" come nelle sessioni del banco di prova
// da cui nasce Kite, dove "A" era il dwell fisso di confronto, così il
// replay legge allo stesso modo le sessioni vecchie e quelle nuove.
const CondHybrid = "B"

// Config configura l'engine.
type Config struct {
	Geom      gazein.Geometry
	KB        *selector.Keyboard
	LM        *lm.Model
	Oculo     oculo.Config
	Hybrid    selector.HybridParams
	SigmaDeg  float64 // dispersione dello sguardo nel decoder
	CoreFrac  float64 // nucleo ancorato dei tasti
	PriorEps  float64 // quota uniforme mescolata al modello di lingua
	LumAlpha  float64 // peso della zona foveale nella luminanza efficace
	FovealDeg float64 // raggio della zona foveale
	Log       *Logger

	// Correttore italiano (nil: nessuno). Si usa negli esercizi del compito
	// "testo" e nella scrittura libera, se acceso (SetToggles).
	Corrector *correct.Corrector
	// Compito degli esercizi: metrics.TaskText (gli errori entrano nel testo,
	// con cancella, suggerimenti e correttore) o metrics.TaskStrict (prima
	// versione: la lettera sbagliata non entra). Vuoto = rigida.
	Task string
	// Voce del tasto parla, nella scrittura libera (nil: nessuna).
	Speaker speech.Speaker
	// Frasi imparate, proposte nella scrittura libera (nil: nessuna). Un
	// messaggio detto per intero dalla voce si impara.
	Phrases *ngram.Model
	// Fissazione che accetta la frase proposta (0: DefaultPhraseDwellMs).
	PhraseDwellMs float64
}

// LumMap è la luminanza relativa (lineare, bianco = 1) dello schermo su una
// griglia grossolana, calcolata dall'interfaccia a ogni frame.
type LumMap struct {
	NX, NY int
	V      []float32
	Mean   float64
}

// TracePt è un punto della traccia pupillare (laboratorio).
type TracePt struct {
	T                        float64 // s
	Meas, Pred, Resid, Z     float64
	Lum                      float64
	Valid, Compensated, Load bool
}

// Snapshot è lo stato da disegnare, copiato sotto lock.
type Snapshot struct {
	Samples    uint64
	RateHz     float64
	LastSample time.Time
	Presence   uint8
	EyeValid   bool
	EyeMM      [3]float64
	GazeValid  bool
	Gaze       [2]float64
	Oc         oculo.State
	Dec        selector.Decision
	Mode       Mode
	Cond       string
	Label      string

	Target     string
	Pos        int
	Typed      string
	Task       string
	Suggest    []string // suggerimenti correnti (immutabile)
	LastFix    Fix      // ultima parola corretta dal correttore
	Voice      bool     // c'è una voce per il tasto parla
	Speaking   bool     // la voce sta parlando
	Said       string   // ultimo testo detto e cancellato (cancella lo ripristina)
	SaidAt     time.Time
	VoiceErr   string // ultimo errore della voce
	VoiceErrAt time.Time
	Phrase     string  // frase proposta: ciò che manca dopo il testo, in grigio ("" = nessuna)
	PhraseVer  uint64  // cambia a ogni proposta nuova o testo nuovo (vedi SetPhraseRect)
	PhraseProg float64 // avanzamento della fissazione che la accetta (0–1)
	Accepted   string  // ultima frase accettata (cancella subito dopo la annulla)
	AcceptedAt time.Time
	Errors     int
	Selections int
	LastSelKey int
	LastSelOK  bool
	LastSelAt  time.Time

	Prior        []float64 // immutabile: sostituita a ogni cambio
	PriorVersion uint64

	Pupil      pupil.Out
	PupilValid float64 // frazione recente di campioni con pupilla valida
	Model      pupil.Model
	ModelValid bool
	LumGlobal  float64
}

// EventKind è il tipo di evento per l'interfaccia.
type EventKind int

const (
	EvBlockDone EventKind = iota + 1
	EvUserWord            // parola da aggiungere al dizionario personale (annullata una correzione)
	EvPhrase              // frase imparata, da salvare (detta per intero dalla voce)
)

// Event è una notifica all'interfaccia.
type Event struct {
	Kind   EventKind
	Result metrics.Result
	Word   string // EvUserWord
	Text   string // EvPhrase
}

// Fix è una parola corretta dal correttore (per evidenziarla).
type Fix struct {
	Literal, Text string
	At            time.Time
}

// Cue è ciò che "vede" l'utente simulato: dove deve guardare.
type Cue struct {
	Mode      Mode
	HasTarget bool
	Target    [2]float64
	Seq       uint64 // cambia a ogni nuovo bersaglio o selezione
	Keys      [][2]float64
	HasFix    bool
	Fix       [2]float64
	Load      float64
}

// Engine è il motore.
type Engine struct {
	cfg Config

	mu     sync.Mutex
	det    *oculo.Detector
	pp     pupil.Processor
	model  pupil.Model
	dec    *selector.Decoder
	sel    selector.Selector
	events chan Event

	mode     Mode
	cond     string
	label    string
	block    *metrics.Block
	blockID  int
	target   []rune
	pos      int
	typed    []rune
	errors   int
	nsel     int
	selCount uint64
	lastSel  struct {
		key int
		ok  bool
		at  time.Time
	}
	usePred, usePupil, useCorr bool
	task                       string
	ed                         *editor
	obsv                       *Observer
	voice                      voiceState
	phr                        phraseState

	prior    []float64
	priorVer uint64

	lum      LumMap
	eye      [3]float64
	eyeInit  bool
	t0Dev    int64
	lastDev  int64
	snap     Snapshot
	rateN    int
	rateT0   time.Time
	pupilOK  float64
	calib    []pupil.Point
	trace    [1200]TracePt
	traceN   int
	traceH   int
	traceK   int
	cueFix   [2]float64
	cueHas   bool
	cueLoad  float64
	keyCents [][2]float64
}

// New crea l'engine.
func New(cfg Config) *Engine {
	e := &Engine{cfg: cfg, events: make(chan Event, 8), usePred: true, usePupil: true, useCorr: cfg.Corrector != nil}
	e.det = oculo.New(cfg.Oculo, cfg.Geom)
	e.dec = e.Decoder()
	e.eye = gazein.DefaultEye
	e.lum = LumMap{NX: 1, NY: 1, V: []float32{0.2}, Mean: 0.2}
	for _, k := range cfg.KB.Keys {
		e.keyCents = append(e.keyCents, k.Center)
	}
	e.snap.LastSelKey = -1
	e.recomputePrior()
	return e
}

// Events restituisce il canale delle notifiche.
func (e *Engine) Events() <-chan Event { return e.events }

// Decoder restituisce un decoder nuovo con gli stessi parametri, da usare
// in un'altra goroutine (per disegnare le aree di tocco): quello dell'engine
// ha un buffer di lavoro e non va condiviso.
func (e *Engine) Decoder() *selector.Decoder {
	return &selector.Decoder{KB: e.cfg.KB, Geom: e.cfg.Geom, SigmaDeg: e.cfg.SigmaDeg, CoreFrac: e.cfg.CoreFrac, MarginDeg: 1}
}

// Run elabora i campioni finché ctx non termina.
func (e *Engine) Run(ctx context.Context, in <-chan gazein.Sample) {
	flush := time.NewTicker(time.Second)
	defer flush.Stop()
	for {
		select {
		case <-ctx.Done():
			e.cfg.Log.Flush()
			return
		case s := <-in:
			e.Process(s)
		case <-flush.C:
			e.cfg.Log.Flush()
		}
	}
}

// Process elabora un campione.
func (e *Engine) Process(s gazein.Sample) {
	now := time.Now()
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.t0Dev == 0 {
		e.t0Dev = s.DeviceUs
		e.rateT0 = now
	}
	e.lastDev = s.DeviceUs
	tS := float64(s.DeviceUs-e.t0Dev) / 1e6

	// 1. acquisizione
	e.rateN++
	if d := now.Sub(e.rateT0); d >= time.Second {
		e.snap.RateHz = float64(e.rateN) / d.Seconds()
		e.rateN, e.rateT0 = 0, now
	}
	var eye [3]float64
	eyeOK := true
	switch {
	case s.EyeValid[0] && s.EyeValid[1]:
		for i := range eye {
			eye[i] = (s.EyeMM[0][i] + s.EyeMM[1][i]) / 2
		}
	case s.EyeValid[0]:
		eye = s.EyeMM[0]
	case s.EyeValid[1]:
		eye = s.EyeMM[1]
	default:
		eyeOK = false
	}
	if eyeOK && eye[2] > 200 {
		if !e.eyeInit {
			e.eye, e.eyeInit = eye, true
		}
		for i := range eye {
			e.eye[i] += (eye[i] - e.eye[i]) * 0.04 // ~0,3 s
		}
	}
	gp := e.snap.Gaze
	switch {
	case s.GazeValid:
		gp = s.Gaze
	case s.RawValid:
		gp = s.Raw
	}

	// 2. stato oculare
	lumEff := e.lumAt(gp)
	po := e.pp.Update(tS, s.PupilMM, s.PupilValid, lumEff)
	ok := 0.0
	if po.Valid {
		ok = 1
	}
	e.pupilOK += (ok - e.pupilOK) * 0.005
	st, evs := e.det.Update(s.DeviceUs, s.Raw, s.RawValid, e.eye)
	if e.mode == ModeBlock || e.mode == ModeFree {
		for _, ev := range evs {
			if ev.Kind == oculo.EvSaccadeEnd {
				e.cfg.Log.Event("saccade", map[string]any{"block": e.blockID, "start_us": ev.Sac.StartUs,
					"dur_ms": ev.Sac.DurationMs(), "amp_deg": round(ev.Sac.AmplitudeDeg, 3), "peak_deg_s": round(ev.Sac.PeakVel, 1)})
			}
		}
	}
	if e.mode == ModeCalib {
		d := math.NaN()
		if po.Valid {
			d = po.MeasMM
		}
		e.calib = append(e.calib, pupil.Point{T: tS, D: d, L: lumEff})
	}

	// 3. attuazione
	dec := selector.Decision{Selected: -1, Candidate: -1}
	if (e.mode == ModeBlock || e.mode == ModeFree) && e.sel != nil {
		if e.block != nil && e.block.StartUs == 0 {
			e.block.StartUs = s.DeviceUs
		}
		in := selector.Input{TUs: s.DeviceUs, Gaze: s.Gaze, GazeValid: s.GazeValid, Oc: st, Eye: e.eye,
			Prior: e.prior, PupilZ: po.Z, PupilOK: po.Valid && po.Compensated}
		dec = e.sel.Update(&in)
		if dec.Selected >= 0 {
			e.onSelect(dec, s, now, &st)
		}
		e.armVoice(gp, s.GazeValid || s.RawValid)
		if e.mode == ModeFree {
			e.phraseGaze(s.DeviceUs, s.RawValid, &st, gp, now)
		}
		if e.block != nil && po.Valid {
			e.block.PupilSum += po.ResidMM
			e.block.PupilZSum += po.Z
			e.block.PupilN++
		}
	}

	// traccia pupillare a 30 Hz
	if e.traceK++; e.traceK%3 == 0 {
		e.trace[e.traceH] = TracePt{T: tS, Meas: po.MeasMM, Pred: po.PredMM, Resid: po.ResidMM, Z: po.Z, Lum: lumEff,
			Valid: po.Valid, Compensated: po.Compensated, Load: e.cueLoad > 0}
		e.traceH = (e.traceH + 1) % len(e.trace)
		e.traceN = min(e.traceN+1, len(e.trace))
	}

	// 4. log e stato per l'interfaccia
	if e.cfg.Log != nil {
		e.cfg.Log.Samplef("%d,%d,%d,%s,%d,%.5f,%.5f,%d,%.5f,%.5f,%d,%.3f,%.3f,%d,%d,%.1f,%.1f,%.1f,%s,%.1f,%.1f,%.1f,%d,%.5f,%.5f,%.3f,%d,%d,%.3f,%.0f,%d,%.4f,%.4f,%.4f,%.3f,%d,%.4f,%.4f\n",
			s.HostNs, s.DeviceUs, s.Seq, e.mode, e.blockID, s.Gaze[0], s.Gaze[1], b2i(s.GazeValid), s.Raw[0], s.Raw[1], b2i(s.RawValid),
			s.PupilMM[0], s.PupilMM[1], b2i(s.PupilValid[0]), b2i(s.PupilValid[1]), e.eye[0], e.eye[1], e.eye[2],
			st.Phase, st.VelDeg, st.PeakThr, st.OnsetThr, st.FixID, st.FixCentroid[0], st.FixCentroid[1], st.FixRMSDeg, b2i(st.FixStable),
			dec.Candidate, dec.Progress, dec.RequiredMs, dec.Selected, po.MeasMM, nan0(po.PredMM), po.ResidMM, po.Z, b2i(po.Valid), lumEff, e.lum.Mean)
	}
	sn := &e.snap
	sn.Samples++
	sn.LastSample = now
	sn.Presence = s.Presence
	sn.EyeValid, sn.EyeMM = eyeOK, e.eye
	sn.GazeValid = s.GazeValid
	if s.GazeValid {
		sn.Gaze = s.Gaze
	}
	sn.Oc, sn.Dec, sn.Pupil, sn.PupilValid = st, dec, po, e.pupilOK
	sn.LumGlobal = e.lum.Mean
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

func nan0(v float64) float64 {
	if math.IsNaN(v) {
		return 0
	}
	return v
}

func round(v float64, d int) float64 {
	p := math.Pow(10, float64(d))
	return math.Round(v*p) / p
}

// lumAt è la luminanza efficace per uno sguardo in p: media pesata tra la
// zona foveale (raggio FovealDeg) e lo schermo intero.
func (e *Engine) lumAt(p [2]float64) float64 {
	m := &e.lum
	if len(m.V) == 0 {
		return m.Mean
	}
	r := e.cfg.FovealDeg * e.cfg.Geom.MMPerDeg(e.eye)
	rx, ry := r/e.cfg.Geom.WidthMM, r/e.cfg.Geom.HeightMM
	var s float64
	n := 0
	for j := 0; j < m.NY; j++ {
		cy := (float64(j) + 0.5) / float64(m.NY)
		dy := (cy - p[1]) / ry
		if dy*dy > 1 {
			continue
		}
		for i := 0; i < m.NX; i++ {
			cx := (float64(i) + 0.5) / float64(m.NX)
			dx := (cx - p[0]) / rx
			if dx*dx+dy*dy <= 1 {
				s += float64(m.V[j*m.NX+i])
				n++
			}
		}
	}
	if n == 0 {
		return m.Mean
	}
	a := e.cfg.LumAlpha
	return a*s/float64(n) + (1-a)*m.Mean
}

// LumAt è lumAt con il lock (per l'utente simulato).
func (e *Engine) LumAt(p [2]float64) float64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.lumAt(p)
}

// SetLuminance aggiorna la mappa di luminanza dello schermo.
func (e *Engine) SetLuminance(m LumMap) {
	e.mu.Lock()
	if len(e.lum.V) != len(m.V) {
		e.lum.V = make([]float32, len(m.V))
	}
	copy(e.lum.V, m.V)
	e.lum.NX, e.lum.NY, e.lum.Mean = m.NX, m.NY, m.Mean
	e.mu.Unlock()
}

// KeyPrior è il priore dei tasti dopo typed: il modello di lingua
// mescolato con una quota eps uniforme (ogni tasto resta raggiungibile).
// Alloca una slice nuova.
// I tasti di modifica restano spenti (0): li accende EditPrior.
func KeyPrior(m *lm.Model, kb *selector.Keyboard, typed string, eps float64) []float64 {
	p := m.Next(typed)
	n := kb.Chars
	pr := make([]float64, len(kb.Keys))
	var s float64
	for i, k := range kb.Keys[:n] {
		if c := lm.Index(k.Char); c >= 0 {
			pr[i] = p[c]
		}
		s += pr[i]
	}
	for i := range pr[:n] {
		pr[i] = (1-eps)*pr[i]/s + eps/float64(n)
	}
	return pr
}

// EditKeys sono i tasti di modifica accesi.
type EditKeys struct {
	Backspace bool
	Suggest   int // i primi Suggest suggerimenti
	Speak     bool
}

// EditPrior accende i tasti di modifica indicati da on, con il priore
// minimo (eps sul caso, quindi il tempo massimo: sono scelte deliberate,
// non lettere da indovinare).
func EditPrior(pr []float64, kb *selector.Keyboard, eps float64, on EditKeys) {
	for i := kb.Chars; i < len(kb.Keys); i++ {
		pr[i] = 0
		c := kb.Keys[i].Char
		s, isSugg := selector.SuggestSlot(c)
		if isSugg && s < on.Suggest || c == selector.KeyBackspace && on.Backspace || c == selector.KeySpeak && on.Speak {
			pr[i] = eps / float64(kb.Chars)
		}
	}
}

// recomputePrior aggiorna il priore dei tasti dal modello di lingua.
// Alloca una slice nuova: quella vecchia può essere in uso nello snapshot.
func (e *Engine) recomputePrior() {
	ctx := string(e.typed)
	if e.ed != nil {
		ctx = e.ed.context()
	}
	pr := KeyPrior(e.cfg.LM, e.cfg.KB, ctx, e.cfg.PriorEps)
	if e.ed != nil {
		EditPrior(pr, e.cfg.KB, e.cfg.PriorEps, EditKeys{Backspace: true, Suggest: len(e.ed.sugg), Speak: e.speakOn()})
		if e.voice.on {
			// mentre la voce parla la tastiera tace: resta solo stop
			for i := range pr {
				if e.cfg.KB.Keys[i].Char != selector.KeySpeak {
					pr[i] = 0
				}
			}
		}
	}
	e.prior = pr
	e.priorVer++
	e.snap.Prior, e.snap.PriorVersion = pr, e.priorVer
}

func (e *Engine) onSelect(dec selector.Decision, s gazein.Sample, now time.Time, st *oculo.State) {
	key := e.cfg.KB.Keys[dec.Selected]
	e.selCount++
	e.lastSel.key, e.lastSel.at = dec.Selected, now
	latUs := float64(now.UnixNano()-s.HostNs) / 1000
	if e.ed != nil {
		e.onEdit(key, dec, s, latUs, st, now)
		e.syncTask()
		return
	}
	switch e.mode {
	case ModeBlock:
		exp := e.target[e.pos]
		ok := key.Char == exp
		e.lastSel.ok = ok
		e.nsel++
		e.block.Selections = append(e.block.Selections, metrics.Selection{TUs: s.DeviceUs, Key: string(key.Char),
			Expected: string(exp), Correct: ok, DwellMs: round(dec.ElapsedMs, 1), RequiredMs: round(dec.RequiredMs, 1),
			Posterior: round(dec.Posterior, 4), PriorRatio: round(dec.PriorRatio, 3), LatencyUs: round(latUs, 1)})
		e.cfg.Log.Event("selection", map[string]any{"block": e.blockID, "cond": e.cond, "key": string(key.Char),
			"expected": string(exp), "correct": ok, "dev_us": s.DeviceUs, "dwell_ms": round(dec.ElapsedMs, 1),
			"required_ms": round(dec.RequiredMs, 1), "posterior": round(dec.Posterior, 4), "prior_ratio": round(dec.PriorRatio, 3),
			"aim": round(dec.Aim, 3), "decision_latency_us": round(latUs, 1)})
		if l, isL := e.sel.(selector.Learner); isL {
			l.Feedback(ok)
		}
		if ok {
			e.typed = append(e.typed, key.Char)
			e.pos++
			e.recomputePrior()
		} else {
			e.errors++
		}
		if e.pos == len(e.target) {
			e.finishBlock()
		}
	case ModeFree:
		e.lastSel.ok = true
		e.typed = append(e.typed, key.Char)
		if len(e.typed) > 120 {
			e.typed = e.typed[len(e.typed)-120:]
		}
		e.recomputePrior()
	}
	e.syncTask()
}

// UserWordCount sono le occorrenze date a una parola del dizionario
// personale: quanto una parola di media frequenza.
const UserWordCount = 2000

// edit applica un tasto all'editor e gestisce le conseguenze comuni
// (dizionario personale, registro della correzione).
func (e *Engine) edit(c rune, obs correct.Obs, now time.Time) (Edit, bool) {
	var ed Edit
	switch slot, isSugg := selector.SuggestSlot(c); {
	case c == selector.KeySpeak:
		return e.speakKey(now)
	case e.voice.on:
		return ed, false // la voce sta parlando: la tastiera tace
	case c == selector.KeyBackspace:
		ed = e.ed.backspace()
	case isSugg:
		var ok bool
		if ed, ok = e.ed.pick(slot); !ok {
			return ed, false
		}
	case c == ' ':
		ed = e.ed.space()
	default:
		ed = e.ed.letter(c, obs)
	}
	if ed.Undone != "" && e.cfg.Corrector != nil {
		e.cfg.LM.AddWord(ed.Undone, UserWordCount)
		e.cfg.Corrector.Refresh()
		e.cfg.Log.Event("user_word", map[string]any{"word": ed.Undone})
		select {
		case e.events <- Event{Kind: EvUserWord, Word: ed.Undone}:
		default:
		}
	}
	if r := ed.Commit; r != nil {
		alts := []string{}
		for _, a := range r.Alts {
			if len(alts) == 3 {
				break
			}
			alts = append(alts, a.Text)
		}
		e.cfg.Log.Event("correction", map[string]any{"block": e.blockID, "literal": r.Literal, "text": r.Text,
			"kind": string(r.Kind), "post": round(r.Post, 3), "alts": alts})
		if r.Kind == correct.Fixed {
			e.snap.LastFix = Fix{Literal: r.Literal, Text: r.Text, At: now}
		}
	}
	return ed, true
}

// observe è la selezione di una lettera vista dal correttore.
func (e *Engine) observe(c rune, st *oculo.State) correct.Obs {
	if e.obsv == nil {
		e.obsv = NewObserver(e.dec, e.cfg.Hybrid)
	}
	return e.obsv.Observe(c, st, e.eye)
}

// onEdit gestisce una selezione nel compito "testo" e nella scrittura libera.
func (e *Engine) onEdit(key selector.Key, dec selector.Decision, s gazein.Sample, latUs float64, st *oculo.State, now time.Time) {
	c := keyChar(key)
	before := e.ed.String()
	var obs correct.Obs
	if c >= 'a' && c <= 'z' {
		obs = e.observe(c, st)
	}
	ed, applied := e.edit(c, obs, now)
	if !applied {
		return
	}
	after := e.ed.String()
	name := string(c)
	switch slot, isSugg := selector.SuggestSlot(c); {
	case c == selector.KeyBackspace:
		name = "canc"
	case c == selector.KeySpeak:
		name = "parla"
	case isSugg:
		name = fmt.Sprintf("sugg%d", slot+1)
	}
	ok, exp := true, ""
	if e.mode == ModeBlock {
		target := e.goal()
		was, is := onTrack(before, target), onTrack(after, target)
		ok = !(was && !is) // la selezione ha portato il testo fuori strada?
		exp = "canc"
		if ft, fb := foldText(target), foldText(before); was && len(fb) < len(ft) {
			exp = ft[len(fb) : len(fb)+1]
		}
		b := e.block
		e.nsel++
		if !ok {
			e.errors++
			b.ErrorsMade++
		}
		switch ed.Kind {
		case "canc", "annulla":
			b.Backspaces++
			if ed.Kind == "annulla" {
				b.Undos++
			}
		case "suggerimento":
			b.Suggestions++
		}
		if ed.Commit != nil && ed.Commit.Kind == correct.Fixed {
			b.AutoFixes++
		}
		b.Selections = append(b.Selections, metrics.Selection{TUs: s.DeviceUs, Key: name, Expected: exp, Correct: ok,
			DwellMs: round(dec.ElapsedMs, 1), RequiredMs: round(dec.RequiredMs, 1), Posterior: round(dec.Posterior, 4),
			PriorRatio: round(dec.PriorRatio, 3), LatencyUs: round(latUs, 1), Kind: ed.Kind, Text: after})
	}
	e.lastSel.ok = ok
	e.cfg.Log.Event("selection", map[string]any{"block": e.blockID, "cond": e.cond, "key": name, "kind": ed.Kind,
		"expected": exp, "correct": ok, "text": after, "dev_us": s.DeviceUs, "dwell_ms": round(dec.ElapsedMs, 1),
		"required_ms": round(dec.RequiredMs, 1), "posterior": round(dec.Posterior, 4), "prior_ratio": round(dec.PriorRatio, 3),
		"aim": round(dec.Aim, 3), "decision_latency_us": round(latUs, 1)})
	if l, isL := e.sel.(selector.Learner); isL {
		if e.mode == ModeBlock {
			l.Feedback(ok)
		} else if c == selector.KeyBackspace {
			l.Feedback(false) // nella scrittura libera cancellare segnala un errore
		}
	}
	e.recomputePrior()
	if e.mode == ModeBlock && ed.Word && len(strings.Fields(after)) >= len(strings.Fields(string(e.target))) {
		e.finishBlock()
	}
}

func (e *Engine) finishBlock() {
	b := e.block
	b.Completed = true
	if e.ed != nil {
		b.Text = e.ed.String()
	}
	r := b.Result()
	e.cfg.Log.Event("block_end", map[string]any{"block": b.ID, "cond": b.Condition, "label": b.Label,
		"chars_per_min": round(r.CharsPerMin, 2), "accuracy": round(r.Accuracy, 4), "itr": round(r.ITR, 2)})
	e.cfg.Log.WriteJSON(fmt.Sprintf("block-%02d.json", b.ID), map[string]any{"block": b, "result": r})
	e.mode, e.sel, e.block, e.ed = ModeIdle, nil, nil, nil
	select {
	case e.events <- Event{Kind: EvBlockDone, Result: r}:
	default:
	}
}

func (e *Engine) syncTask() {
	e.updatePhrase()
	sn := &e.snap
	sn.Mode, sn.Cond, sn.Label = e.mode, e.cond, e.label
	sn.Target, sn.Pos, sn.Typed, sn.Task, sn.Suggest = string(e.target), e.pos, string(e.typed), e.task, nil
	if e.ed != nil {
		sn.Typed, sn.Suggest = e.ed.String(), e.ed.suggestions()
	}
	v := &e.voice
	sn.Voice, sn.Speaking = e.cfg.Speaker != nil, v.on
	sn.Said, sn.SaidAt, sn.VoiceErr, sn.VoiceErrAt = v.said, v.saidAt, v.err, v.errAt
	ph := &e.phr
	sn.Phrase, sn.PhraseVer, sn.PhraseProg = ph.ghost, ph.ver, ph.prog
	sn.Accepted, sn.AcceptedAt = "", ph.acceptedAt
	if e.ed != nil && e.ed.phUndo != nil {
		sn.Accepted = ph.accepted // solo finché cancella la può annullare
	}
	sn.Errors, sn.Selections = e.errors, e.nsel
	sn.LastSelKey, sn.LastSelOK, sn.LastSelAt = e.lastSel.key, e.lastSel.ok, e.lastSel.at
	sn.Model, sn.ModelValid = e.model, e.model.Valid()
}

// SetToggles accende o spegne le componenti del selettore ibrido (dal blocco
// o dalla scrittura libera successivi).
func (e *Engine) SetToggles(prediction, pupilGov, corrector bool) {
	e.mu.Lock()
	e.usePred, e.usePupil, e.useCorr = prediction, pupilGov, corrector && e.cfg.Corrector != nil
	e.mu.Unlock()
}

// HybridLabel descrive il selettore ibrido con le componenti attive.
func HybridLabel(prediction, pupilGov, corrector bool) string {
	l := "Ibrido: saccadi"
	if prediction {
		l += " + predizione"
	}
	if pupilGov {
		l += " + pupilla"
	}
	if corrector {
		l += " + correttore"
	}
	return l
}

// goal è il testo da scrivere nel compito "testo": la frase e lo spazio che
// chiude l'ultima parola.
func (e *Engine) goal() string { return string(e.target) + " " }

// corrector è il correttore da usare adesso (nil se spento).
func (e *Engine) corrector() *correct.Corrector {
	if e.useCorr {
		return e.cfg.Corrector
	}
	return nil
}

func (e *Engine) newHybrid() selector.Selector {
	p := e.cfg.Hybrid
	p.UsePrediction = e.usePred
	if !e.usePupil {
		p.PupilGain = 0
	}
	return selector.NewHybrid(p, e.dec)
}

// StartBlock avvia un blocco dell'esercizio di copiatura e ne restituisce
// l'id.
func (e *Engine) StartBlock(phrase string) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.stopVoice("interrotta")
	e.blockID++
	e.target, e.pos, e.typed, e.errors, e.nsel = []rune(phrase), 0, nil, 0, 0
	e.cond = CondHybrid
	e.task = e.cfg.Task
	if e.task == "" {
		e.task = metrics.TaskStrict
	}
	e.ed = nil
	var corr *correct.Corrector
	e.sel = e.newHybrid()
	if e.task == metrics.TaskText {
		corr = e.corrector()
		e.ed = newEditor(corr, selector.NumSuggest, nil)
	}
	e.label = HybridLabel(e.usePred, e.usePupil, corr != nil)
	e.block = &metrics.Block{ID: e.blockID, Condition: e.cond, Label: e.label, Phrase: phrase,
		NKeys: e.cfg.KB.Chars, StartedAt: time.Now(), PupilCompensated: e.model.Valid(), Task: e.task}
	e.mode = ModeBlock
	e.lastSel.key = -1
	e.recomputePrior()
	e.cfg.Log.Event("block_start", map[string]any{"block": e.blockID, "cond": e.cond, "label": e.label, "phrase": phrase,
		"pupil_compensated": e.model.Valid(), "task": e.task, "corrector": corr != nil})
	e.syncTask()
	return e.blockID
}

// AbortBlock interrompe il blocco in corso senza registrarne i risultati.
func (e *Engine) AbortBlock() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.mode == ModeBlock {
		e.cfg.Log.Event("block_abort", map[string]any{"block": e.blockID})
	}
	e.stopVoice("interrotta")
	e.mode, e.sel, e.block, e.ed = ModeIdle, nil, nil, nil
	e.syncTask()
}

// StartFree avvia la scrittura libera con il selettore ibrido.
func (e *Engine) StartFree() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.stopVoice("interrotta")
	e.voice.said = ""
	e.mode, e.cond = ModeFree, CondHybrid
	e.sel = e.newHybrid()
	e.label = HybridLabel(e.usePred, e.usePupil, e.corrector() != nil)
	e.ed = newEditor(e.corrector(), selector.NumSuggest, e.cfg.Phrases)
	e.task = ""
	e.target, e.typed, e.pos, e.errors, e.nsel = nil, nil, 0, 0, 0
	e.lastSel.key = -1
	e.recomputePrior()
	e.syncTask()
}

// Backspace cancella l'ultimo carattere della scrittura libera.
func (e *Engine) Backspace() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.mode == ModeFree && e.ed != nil && !e.voice.on {
		// la selezione cancellata era sbagliata: prudenza sulle prossime
		if l, ok := e.sel.(selector.Learner); ok {
			l.Feedback(false)
		}
		e.edit(selector.KeyBackspace, correct.Obs{}, time.Now())
		e.recomputePrior()
		e.syncTask()
	}
}

// SetIdle torna al solo monitoraggio.
func (e *Engine) SetIdle() {
	e.mu.Lock()
	e.stopVoice("interrotta")
	e.mode, e.sel, e.block, e.ed = ModeIdle, nil, nil, nil
	e.cueHas, e.cueLoad = false, 0
	e.typed, e.target = nil, nil
	e.recomputePrior()
	e.syncTask()
	e.mu.Unlock()
}

// SetLab entra nel laboratorio della pupilla.
func (e *Engine) SetLab() {
	e.mu.Lock()
	e.mode = ModeLab
	e.syncTask()
	e.mu.Unlock()
}

// SetCue indica il punto di fissazione e il carico richiesto (calibrazione e
// laboratorio): serve all'utente simulato e alla traccia.
func (e *Engine) SetCue(fix [2]float64, has bool, load float64) {
	e.mu.Lock()
	e.cueFix, e.cueHas, e.cueLoad = fix, has, load
	e.mu.Unlock()
}

// StartCalib inizia a registrare la calibrazione pupilla–luminanza.
func (e *Engine) StartCalib() {
	e.mu.Lock()
	e.mode = ModeCalib
	e.calib = e.calib[:0]
	e.cfg.Log.Event("calib_start", nil)
	e.syncTask()
	e.mu.Unlock()
}

// CalibResult è l'esito della calibrazione.
type CalibResult struct {
	Model  pupil.Model
	Points []pupil.Point
	Pred   []float64
	Err    error
}

// FinishCalib stima il modello dai dati registrati e lo adotta se valido.
func (e *Engine) FinishCalib() CalibResult {
	e.mu.Lock()
	pts := append([]pupil.Point(nil), e.calib...)
	e.mode = ModeIdle
	e.mu.Unlock()

	m, err := pupil.Fit(pts) // fuori dal lock: ~50 ms
	res := CalibResult{Model: m, Points: pts, Err: err}
	if err == nil {
		res.Pred = m.Predict(pts)
		e.SetModel(m)
	}
	ev := map[string]any{"points": len(pts)}
	if err != nil {
		ev["error"] = err.Error()
	} else {
		ev["model"] = m
	}
	e.cfg.Log.Event("calib_end", ev)
	e.cfg.Log.WriteJSON("pupil-calibration.json", map[string]any{"model": m, "error": fmt.Sprint(err), "points": len(pts)})
	return res
}

// SetModel adotta un modello pupilla–luminanza.
func (e *Engine) SetModel(m pupil.Model) {
	e.mu.Lock()
	e.model = m
	e.pp.Model = &e.model
	e.pp.Reset()
	e.syncTask()
	e.mu.Unlock()
}

// Snapshot restituisce una copia dello stato.
func (e *Engine) Snapshot() Snapshot {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.snap
}

// PupilTrace copia in dst la traccia pupillare in ordine cronologico.
func (e *Engine) PupilTrace(dst []TracePt) []TracePt {
	e.mu.Lock()
	defer e.mu.Unlock()
	dst = dst[:0]
	start := (e.traceH - e.traceN + len(e.trace)) % len(e.trace)
	for i := 0; i < e.traceN; i++ {
		dst = append(dst, e.trace[(start+i)%len(e.trace)])
	}
	return dst
}

// Cue restituisce ciò che l'utente simulato deve guardare.
func (e *Engine) Cue() Cue {
	e.mu.Lock()
	defer e.mu.Unlock()
	c := Cue{Mode: e.mode, Keys: e.keyCents, HasFix: e.cueHas, Fix: e.cueFix, Load: e.cueLoad,
		Seq: uint64(e.blockID)<<32 | e.selCount}
	switch {
	case e.mode == ModeBlock && e.ed != nil:
		// il tasto che serve adesso: la lettera successiva, o cancella se il
		// testo è uscito di strada
		before, target := e.ed.String(), e.goal()
		next := selector.KeyBackspace
		if ft, fb := foldText(target), foldText(before); onTrack(before, target) && len(fb) < len(ft) {
			next = rune(ft[len(fb)])
		}
		if k := e.cfg.KB.IndexOf(next); k >= 0 {
			c.HasTarget, c.Target = true, e.cfg.KB.Keys[k].Center
		}
	case e.mode == ModeBlock && e.pos < len(e.target):
		if k := e.cfg.KB.IndexOf(e.target[e.pos]); k >= 0 {
			c.HasTarget, c.Target = true, e.cfg.KB.Keys[k].Center
		}
	}
	return c
}
