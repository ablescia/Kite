// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Antonio Blescia

// Package oculo separa in tempo reale le saccadi dalle fissazioni sullo
// stream a 90 Hz (un campione ogni ~11 ms).
//
// Algoritmo: I-VT (soglia di velocità angolare) con soglia adattiva, sul
// modello di Nyström & Holmqvist (2010). La velocità si misura in gradi di
// angolo visivo al secondo, usando la posizione reale degli occhi (centro
// corneale nel sistema dello schermo). La soglia di picco è media + 6·DS e
// quella di inizio/fine media + 3·DS della velocità durante le fissazioni,
// stimate di continuo: si adatta al rumore del tracker e della persona.
//
// Una saccade di 30–80 ms copre 3–7 campioni. La fine viene riconosciuta
// quando la velocità su due intervalli (22 ms) scende sotto la soglia di
// fine: da quell'istante parte la nuova fissazione, che è "stabile" dopo
// StableSamples campioni con dispersione contenuta.
//
// Centroide e dispersione si misurano su tutta la fissazione. Se l'occhio
// scivola lentamente, senza una saccade, la dispersione supera la soglia e la
// fissazione resta instabile fino alla saccade successiva (Drift). Per questi
// casi lo stato riporta anche centroide e dispersione degli ultimi
// WindowSamples campioni: dicono dove lo sguardo si è fermato adesso.
package oculo

import (
	"math"

	"github.com/ablescia/kite/internal/gazein"
)

// Phase è lo stato oculomotore corrente.
type Phase uint8

const (
	Lost     Phase = iota // nessun campione valido (ammiccamento, occhi persi)
	Saccade               // movimento balistico in corso
	Fixation              // sguardo fermo su un punto
)

func (p Phase) String() string {
	switch p {
	case Saccade:
		return "saccade"
	case Fixation:
		return "fissazione"
	}
	return "perso"
}

// Config sono i parametri del rilevatore.
type Config struct {
	PeakMin, PeakMax float64 // limiti della soglia di picco adattiva (°/s)
	PeakK, OnsetK    float64 // moltiplicatori della DS del rumore (6 e 3)
	NoiseTauS        float64 // costante di tempo della stima del rumore
	MinAmplitudeDeg  float64 // saccadi più piccole (microsaccadi, rumore) non aprono una nuova fissazione
	MaxSaccadeMs     float64 // oltre, la "saccade" è un artefatto: si chiude
	GapMaxMs         float64 // buco di campioni oltre il quale lo sguardo è perso
	StableSamples    int     // campioni minimi per una fissazione stabile
	StableRMSDeg     float64 // dispersione RMS massima di una fissazione stabile
	WindowSamples    int     // campioni della finestra recente (0 = nessuna; al più 32)
}

// DefaultConfig: valori tipici per 90 Hz.
func DefaultConfig() Config {
	return Config{
		PeakMin: 60, PeakMax: 300, PeakK: 6, OnsetK: 3, NoiseTauS: 2,
		MinAmplitudeDeg: 1.0, MaxSaccadeMs: 160, GapMaxMs: 100,
		StableSamples: 3, StableRMSDeg: 1.0, WindowSamples: 8,
	}
}

// SaccadeInfo descrive una saccade conclusa.
type SaccadeInfo struct {
	StartUs, EndUs int64
	AmplitudeDeg   float64
	PeakVel        float64 // °/s
	From, To       [2]float64
}

// DurationMs della saccade.
func (s SaccadeInfo) DurationMs() float64 { return float64(s.EndUs-s.StartUs) / 1000 }

// State è lo stato dopo l'ultimo campione.
type State struct {
	Phase             Phase
	VelDeg            float64 // velocità angolare corrente, °/s
	PeakThr, OnsetThr float64 // soglie adattive correnti, °/s

	FixID        uint64     // cambia a ogni nuova fissazione
	FixStartUs   int64      // inizio della fissazione (= fine della saccade)
	FixN         int        // campioni nella fissazione
	FixCentroid  [2]float64 // media dei campioni, normalizzata
	FixRMSDeg    float64    // dispersione RMS attorno al centroide
	FixStable    bool
	AfterSaccade bool // la fissazione segue una saccade (non un ammiccamento o una perdita)

	// Drift: la fissazione ha abbastanza campioni ma la dispersione supera
	// StableRMSDeg (lo sguardo è scivolato senza una saccade). Resta così
	// fino alla saccade successiva.
	Drift bool
	// Finestra recente: gli ultimi WindowSamples campioni della fissazione.
	WinCentroid [2]float64
	WinRMSDeg   float64
	WinStable   bool // almeno StableSamples campioni e dispersione entro StableRMSDeg

	Last SaccadeInfo // ultima saccade conclusa
}

// EventKind classifica gli eventi oculomotori.
type EventKind uint8

const (
	EvSaccadeStart EventKind = iota + 1
	EvSaccadeEnd
	EvFixationStart
	EvLost
)

// Event è un evento oculomotore.
type Event struct {
	Kind  EventKind
	TUs   int64
	Sac   SaccadeInfo // per EvSaccadeEnd
	FixID uint64
	Pos   [2]float64
}

type pt struct {
	t int64
	p [2]float64
}

// Detector è il rilevatore. Non è sicuro per l'uso concorrente.
type Detector struct {
	cfg  Config
	geom gazein.Geometry
	st   State

	hist        [3]pt // ultimi campioni validi, il più recente in fondo
	nh          int
	lastValidUs int64
	seen        bool // almeno un campione valido ricevuto

	mu, m2 float64 // media e momento secondo della velocità in fissazione

	// accumulatori della fissazione (mm sul piano dello schermo)
	sx, sy, sxx, syy float64
	su, sv           float64
	n                int

	sacStartUs int64
	sacFrom    [2]float64
	sacPeak    float64

	win    [32][2]float64 // finestra recente della fissazione (normalizzata), circolare
	nw, hw int

	events []Event
}

// New crea un rilevatore.
func New(cfg Config, geom gazein.Geometry) *Detector {
	d := &Detector{cfg: cfg, geom: geom}
	// Rumore iniziale ipotetico: soglia di picco 100 °/s finché non si misura.
	d.mu, d.m2 = 25, 25*25+12.5*12.5
	d.updateThresholds()
	return d
}

// State restituisce lo stato corrente.
func (d *Detector) State() State { return d.st }

func (d *Detector) updateThresholds() {
	sd := math.Sqrt(math.Max(d.m2-d.mu*d.mu, 0))
	d.st.PeakThr = clamp(d.mu+d.cfg.PeakK*sd, d.cfg.PeakMin, d.cfg.PeakMax)
	d.st.OnsetThr = clamp(d.mu+d.cfg.OnsetK*sd, 15, d.st.PeakThr*0.8)
}

func clamp(v, lo, hi float64) float64 { return math.Max(lo, math.Min(hi, v)) }

func (d *Detector) emit(e Event) { d.events = append(d.events, e) }

func (d *Detector) push(t int64, p [2]float64) {
	if d.nh < len(d.hist) {
		d.hist[d.nh] = pt{t, p}
		d.nh++
		return
	}
	copy(d.hist[:], d.hist[1:])
	d.hist[len(d.hist)-1] = pt{t, p}
}

func (d *Detector) resetFix() {
	d.sx, d.sy, d.sxx, d.syy, d.su, d.sv, d.n = 0, 0, 0, 0, 0, 0, 0
	d.nw, d.hw = 0, 0
}

func (d *Detector) addFix(p [2]float64, eye [3]float64) {
	m := d.geom.ToMM(p)
	d.sx += m[0]
	d.sy += m[1]
	d.sxx += m[0] * m[0]
	d.syy += m[1] * m[1]
	d.su += p[0]
	d.sv += p[1]
	d.n++
	n := float64(d.n)
	d.st.FixN = d.n
	d.st.FixCentroid = [2]float64{d.su / n, d.sv / n}
	vx := d.sxx/n - (d.sx/n)*(d.sx/n)
	vy := d.syy/n - (d.sy/n)*(d.sy/n)
	d.st.FixRMSDeg = math.Sqrt(math.Max(vx+vy, 0)) / d.geom.MMPerDeg(eye)
	d.addWindow(p, eye)
}

// addWindow aggiunge p alla finestra recente e ne aggiorna centroide e
// dispersione.
func (d *Detector) addWindow(p [2]float64, eye [3]float64) {
	w := min(d.cfg.WindowSamples, len(d.win))
	if w <= 0 {
		return
	}
	d.win[d.hw] = p
	d.hw = (d.hw + 1) % w
	d.nw = min(d.nw+1, w)
	var su, sv, sx, sy, sxx, syy float64
	for i := 0; i < d.nw; i++ {
		q := d.win[i]
		m := d.geom.ToMM(q)
		su += q[0]
		sv += q[1]
		sx += m[0]
		sy += m[1]
		sxx += m[0] * m[0]
		syy += m[1] * m[1]
	}
	n := float64(d.nw)
	d.st.WinCentroid = [2]float64{su / n, sv / n}
	vx := sxx/n - (sx/n)*(sx/n)
	vy := syy/n - (sy/n)*(sy/n)
	d.st.WinRMSDeg = math.Sqrt(math.Max(vx+vy, 0)) / d.geom.MMPerDeg(eye)
}

// settle aggiorna gli indicatori di stabilità dopo un campione.
func (d *Detector) settle() {
	fix := d.st.Phase == Fixation && d.n >= d.cfg.StableSamples
	d.st.FixStable = fix && d.st.FixRMSDeg <= d.cfg.StableRMSDeg
	d.st.Drift = fix && d.st.FixRMSDeg > d.cfg.StableRMSDeg
	d.st.WinStable = d.st.Phase == Fixation && d.nw >= d.cfg.StableSamples && d.st.WinRMSDeg <= d.cfg.StableRMSDeg
}

// startFixation apre una nuova fissazione con i campioni di atterraggio.
func (d *Detector) startFixation(t int64, samples []pt, eye [3]float64, afterSaccade bool) {
	d.st.Phase = Fixation
	d.st.FixID++
	d.st.FixStartUs = t
	d.st.AfterSaccade = afterSaccade
	d.resetFix()
	for _, s := range samples {
		d.addFix(s.p, eye)
	}
	d.emit(Event{Kind: EvFixationStart, TUs: t, FixID: d.st.FixID, Pos: d.st.FixCentroid})
}

// Update elabora un campione e restituisce lo stato e gli eventi generati
// (la slice è riusata alla chiamata successiva).
func (d *Detector) Update(tUs int64, p [2]float64, valid bool, eye [3]float64) (State, []Event) {
	d.events = d.events[:0]
	gapMax := int64(d.cfg.GapMaxMs * 1000)
	if !valid {
		if d.st.Phase != Lost && d.seen && tUs-d.lastValidUs > gapMax {
			d.st.Phase, d.st.VelDeg = Lost, 0
			d.settle()
			d.nh = 0
			d.emit(Event{Kind: EvLost, TUs: tUs})
		}
		return d.st, d.events
	}
	if !d.seen || tUs-d.lastValidUs > gapMax || d.st.Phase == Lost {
		// Ripresa dopo una perdita: nuova fissazione, senza saccade nota.
		d.seen = true
		d.lastValidUs = tUs
		d.nh = 0
		d.push(tUs, p)
		d.st.VelDeg = 0
		d.startFixation(tUs, d.hist[:1], eye, false)
		d.settle()
		return d.st, d.events
	}
	d.lastValidUs = tUs
	d.push(tUs, p)

	// Velocità su due intervalli (22 ms): metà del rumore di quella su uno,
	// e una saccade di 30 ms supera comunque la soglia.
	a := d.hist[0]
	if d.nh < 3 {
		a = d.hist[d.nh-2]
	}
	dt := float64(tUs-a.t) / 1e6
	v := 0.0
	if dt > 0 {
		v = d.geom.AngleDeg(eye, a.p, p) / dt
	}
	d.st.VelDeg = v

	switch d.st.Phase {
	case Fixation:
		// La saccade si cerca solo con la velocità su due intervalli: quella
		// su uno, subito dopo una ripresa, è troppo rumorosa.
		if d.nh >= 3 && v > d.st.PeakThr {
			d.st.Phase = Saccade
			d.st.FixStable = false
			prev := d.hist[d.nh-2]
			d.sacStartUs = prev.t
			d.sacFrom = d.st.FixCentroid
			if d.n == 0 {
				d.sacFrom = prev.p
			}
			d.sacPeak = v
			d.emit(Event{Kind: EvSaccadeStart, TUs: prev.t, Pos: d.sacFrom})
			break
		}
		if v < d.st.OnsetThr {
			alpha := math.Min(1, dt/2/d.cfg.NoiseTauS) // dt copre due intervalli
			d.mu += alpha * (v - d.mu)
			d.m2 += alpha * (v*v - d.m2)
			d.updateThresholds()
		}
		d.addFix(p, eye)

	case Saccade:
		if v > d.sacPeak {
			d.sacPeak = v
		}
		tooLong := float64(tUs-d.sacStartUs)/1000 > d.cfg.MaxSaccadeMs
		if v >= d.st.OnsetThr && !tooLong {
			break
		}
		// Atterraggio: con la velocità su due intervalli sotto soglia, i
		// campioni da hist[0] in poi sono già fermi (escluso quello di
		// partenza, se la saccade è durata un solo campione).
		landing := d.hist[:d.nh]
		for len(landing) > 1 && landing[0].t <= d.sacStartUs {
			landing = landing[1:]
		}
		var c [2]float64
		for _, s := range landing {
			c[0] += s.p[0]
			c[1] += s.p[1]
		}
		c[0] /= float64(len(landing))
		c[1] /= float64(len(landing))
		amp := d.geom.AngleDeg(eye, d.sacFrom, c)
		if amp < d.cfg.MinAmplitudeDeg && d.st.FixID > 0 && !tooLong {
			// Microsaccade o rumore: la fissazione continua (stesso id e
			// stesso inizio), il centroide riparte dai campioni nuovi.
			d.st.Phase = Fixation
			d.resetFix()
			for _, s := range landing {
				d.addFix(s.p, eye)
			}
			break
		}
		sac := SaccadeInfo{StartUs: d.sacStartUs, EndUs: landing[0].t, AmplitudeDeg: amp,
			PeakVel: d.sacPeak, From: d.sacFrom, To: c}
		d.st.Last = sac
		d.emit(Event{Kind: EvSaccadeEnd, TUs: sac.EndUs, Sac: sac})
		d.startFixation(sac.EndUs, landing, eye, true)
	}
	d.settle()
	return d.st, d.events
}
