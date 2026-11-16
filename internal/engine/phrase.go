// SPDX-License-Identifier: AGPL-3.0-or-later

package engine

import (
	"time"

	"github.com/ablescia/kite/internal/oculo"
)

// La predizione delle frasi, nella scrittura libera: mentre si scrive, il
// modello delle frasi imparate (ngram) propone il resto della frase, che
// l'interfaccia disegna in grigio chiaro dopo il testo.
//
// Si accetta fissandola: una sola fissazione di PhraseDwellMs (1 s) dentro
// la frase grigia. Leggere non basta: chi legge salta di parola in parola
// con saccadi di qualche grado, e ogni saccade riparte da zero. Le
// microsaccadi (sotto 1°) e gli ammiccamenti brevi non azzerano il
// conteggio. Tab sulla tastiera fisica la accetta subito. Cancella subito
// dopo annulla l'accettazione, e la stessa proposta non torna finché il
// testo non cambia.
//
// Le frasi si imparano quando la voce le ha dette per intero: una frase
// accettata e detta si rinforza, una cambiata prima di dirla si impara come
// è stata detta.

// DefaultPhraseDwellMs è la fissazione che accetta la frase proposta.
const DefaultPhraseDwellMs = 1000

const (
	phrasePadDeg  = 0.8 // margine attorno alla frase grigia (errore dello sguardo)
	phraseBlinkUs = 300_000
)

type phraseState struct {
	key     string // testo e frase mostrati: cambiano posizione e area
	full    string // messaggio proposto per intero (per il registro)
	ghost   string
	ver     uint64
	rect    [4]float64 // area che accetta, normalizzata e con il margine
	rectVer uint64     // proposta a cui si riferisce rect
	dwell   ghostDwell
	prog    float64

	accepted   string // ultima frase accettata
	acceptedAt time.Time
}

// ghostDwell conta la fissazione sulla frase grigia.
type ghostDwell struct {
	fix     uint64
	elapsed int64 // µs
	prevUs  int64
	lostUs  int64 // inizio della perdita dello sguardo (0 = visto)
}

func (d *ghostDwell) update(tUs int64, valid bool, st *oculo.State, in bool) {
	dt := int64(0)
	if d.prevUs != 0 {
		dt = min(max(tUs-d.prevUs, 0), 50_000)
	}
	d.prevUs = tUs
	if !valid || st.Phase == oculo.Lost {
		// ammiccamento: il conteggio aspetta, fino a 300 ms
		if d.lostUs == 0 {
			d.lostUs = tUs
		}
		if tUs-d.lostUs > phraseBlinkUs {
			d.elapsed = 0
		}
		return
	}
	d.lostUs = 0
	if st.Phase == oculo.Saccade {
		return // in volo: se è una microsaccade la fissazione continua
	}
	if st.FixID != d.fix {
		if st.AfterSaccade {
			d.elapsed = 0 // una saccade vera: si legge o si guarda altrove
		}
		d.fix = st.FixID
	}
	if !in {
		d.elapsed = 0
		return
	}
	d.elapsed += dt
}

// updatePhrase allinea la proposta mostrata con quella dell'editor: a ogni
// cambio di testo o di frase la proposta ha una versione nuova, e il
// conteggio riparte quando l'interfaccia ne ha comunicato l'area.
func (e *Engine) updatePhrase() {
	ph := &e.phr
	key, full, ghost := "", "", ""
	prob := 0.0
	if e.mode == ModeFree && e.ed != nil && e.ed.phrase != nil && !e.voice.on {
		p := e.ed.phrase
		typed := e.ed.String()
		key, ghost, prob = typed+"\x00"+p.Ghost, p.Ghost, p.Prob
		full = string([]rune(typed)[:wordStart([]rune(typed))]) + p.Text
	}
	if key == ph.key {
		return
	}
	if full != "" && full != ph.full {
		e.cfg.Log.Event("phrase_shown", map[string]any{"text": e.ed.String(), "phrase": full, "ghost": ghost, "p": round(prob, 3)})
	}
	ph.key, ph.full, ph.ghost = key, full, ghost
	ph.ver++
	ph.dwell, ph.prog = ghostDwell{}, 0
}

// SetPhraseRect comunica dove l'interfaccia ha disegnato la frase grigia
// della versione ver (coordinate normalizzate): da lì lo sguardo la accetta.
func (e *Engine) SetPhraseRect(ver uint64, r [4]float64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	ph := &e.phr
	if ver != ph.ver || ph.ghost == "" {
		return
	}
	pad := phrasePadDeg * e.cfg.Geom.MMPerDeg(e.eye)
	px, py := pad/e.cfg.Geom.WidthMM, pad/e.cfg.Geom.HeightMM
	r = [4]float64{max(r[0]-px, 0), max(r[1]-py, 0), min(r[2]+px, 1), r[3] + py}
	// mai sopra i tasti: la barra (o la tastiera) resta dei tasti
	limit := e.cfg.KB.Area[1]
	if e.cfg.KB.HasEditBar() {
		limit = e.cfg.KB.Bar[1]
	}
	r[3] = min(r[3], limit)
	if ph.rectVer == ver && ph.rect == r {
		return // ridisegnata uguale: il conteggio continua
	}
	ph.rect, ph.rectVer = r, ver
	ph.dwell, ph.prog = ghostDwell{}, 0
}

// phraseGaze conta la fissazione sulla frase grigia e la accetta.
func (e *Engine) phraseGaze(tUs int64, valid bool, st *oculo.State, gaze [2]float64, now time.Time) {
	ph := &e.phr
	if ph.ghost == "" || ph.rectVer != ph.ver || e.voice.on {
		ph.dwell, ph.prog = ghostDwell{}, 0
		e.snap.PhraseProg = 0
		return
	}
	p := gaze
	if st.Phase == oculo.Fixation && st.FixN >= 3 {
		p = st.FixCentroid // media della fissazione: più ferma del campione
	}
	r := ph.rect
	in := p[0] >= r[0] && p[0] < r[2] && p[1] >= r[1] && p[1] < r[3]
	ph.dwell.update(tUs, valid, st, in)
	need := e.phraseDwellMs()
	ph.prog = min(float64(ph.dwell.elapsed)/1000/need, 1)
	e.snap.PhraseProg = ph.prog
	if ph.prog >= 1 {
		dwellMs := float64(ph.dwell.elapsed) / 1000
		if e.acceptPhrase("sguardo", dwellMs, now) {
			e.recomputePrior()
			e.syncTask()
		}
	}
}

func (e *Engine) phraseDwellMs() float64 {
	if e.cfg.PhraseDwellMs > 0 {
		return e.cfg.PhraseDwellMs
	}
	return DefaultPhraseDwellMs
}

// acceptPhrase scrive la frase proposta (via: "sguardo" o "tab").
func (e *Engine) acceptPhrase(via string, dwellMs float64, now time.Time) bool {
	if e.mode != ModeFree || e.ed == nil || e.ed.phrase == nil || e.voice.on {
		return false
	}
	p := *e.ed.phrase
	before := e.ed.String()
	ed, ok := e.ed.acceptPhrase()
	if !ok {
		return false
	}
	e.phr.accepted, e.phr.acceptedAt = ed.Phrase, now
	ev := map[string]any{"before": before, "text": e.ed.String(), "phrase": p.Text, "ghost": p.Ghost,
		"p": round(p.Prob, 3), "via": via}
	if via == "sguardo" {
		ev["dwell_ms"] = round(dwellMs, 1)
	}
	e.cfg.Log.Event("phrase_accept", ev)
	return true
}

// AcceptPhrase accetta la frase proposta dalla tastiera fisica (Tab).
func (e *Engine) AcceptPhrase() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.acceptPhrase("tab", 0, time.Now()) {
		e.recomputePrior()
		e.syncTask()
	}
}

// learnPhrase impara un messaggio detto dalla voce e chiede all'interfaccia
// di salvarlo.
func (e *Engine) learnPhrase(text string) {
	if e.cfg.Phrases == nil || !e.cfg.Phrases.Learn(text) {
		return
	}
	e.cfg.Log.Event("phrase_learn", map[string]any{"text": text, "phrases": e.cfg.Phrases.Phrases()})
	select {
	case e.events <- Event{Kind: EvPhrase, Text: text}:
	default:
	}
}
