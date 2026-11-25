// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Antonio Blescia

// Package pupil stima il carico cognitivo dal diametro pupillare,
// separandolo dalla risposta alla luce.
//
// Il diametro risponde soprattutto alla luminanza (riflesso fotomotore,
// PLR): uno schermo chiaro restringe la pupilla, uno scuro la dilata. Il
// carico cognitivo aggiunge dilatazioni di qualche decimo di mm, più lente
// (picco dopo 0,5–1,5 s). Per vederle bisogna quindi togliere la parte
// spiegata dalla luce.
//
// Modello della luce (per persona, monitor e illuminazione ambiente):
//
//	x(t)    = log10(Leff(t − ritardo) + L0)        luminanza efficace, in log
//	xf      = filtro del primo ordine di x, costante di tempo τc quando la
//	          luce aumenta (costrizione, veloce) e τd quando diminuisce
//	          (dilatazione, lenta)
//	D_luce  = A + B·xf                              B < 0
//
// A, B, ritardo, τc e τd si stimano con una calibrazione di ~30 s su livelli
// di grigio a tutto schermo (Fit). Il residuo D − D_luce è l'indice di
// carico; Z ne è la componente fasica (media su 1 s meno la linea di base
// degli ultimi 20 s) in unità della sua deviazione tipica.
package pupil

import (
	"errors"
	"math"
	"time"
)

// L0 evita il logaritmo di zero: il nero del monitor e la luce ambiente non
// sono mai buio assoluto (luminanza relativa, bianco = 1).
const L0 = 0.01

// Model è la risposta della pupilla alla luminanza.
type Model struct {
	A, B     float64 // mm, mm per decade di luminanza
	L0       float64
	DelayS   float64 // latenza del riflesso
	TauConS  float64 // costante di tempo della costrizione
	TauDilS  float64 // costante di tempo della dilatazione
	NoiseMM  float64 // deviazione tipica del residuo in calibrazione
	R2       float64
	N        int
	FittedAt time.Time
}

// Valid dice se il modello è utilizzabile.
func (m *Model) Valid() bool { return m != nil && m.N > 0 && m.B < 0 }

// plrFilter è il filtro asimmetrico sulla luminanza in log.
type plrFilter struct {
	x    float64
	init bool
}

func (f *plrFilter) step(target, dt, tauC, tauD float64) float64 {
	if !f.init {
		f.x, f.init = target, true
		return f.x
	}
	tau := tauD
	if target > f.x { // più luce: costrizione
		tau = tauC
	}
	f.x += (target - f.x) * (1 - math.Exp(-dt/tau))
	return f.x
}

// lumHist è la storia recente della luminanza, per applicare il ritardo.
type lumHist struct {
	t, v [256]float64
	head int // prossima posizione da scrivere
	n    int
}

func (h *lumHist) push(t, v float64) {
	h.t[h.head], h.v[h.head] = t, v
	h.head = (h.head + 1) % len(h.t)
	if h.n < len(h.t) {
		h.n++
	}
}

// at restituisce l'ultimo valore registrato a un istante ≤ t (o il più vecchio).
func (h *lumHist) at(t float64) float64 {
	if h.n == 0 {
		return 0
	}
	i := (h.head - 1 + len(h.t)) % len(h.t)
	for k := 0; k < h.n; k++ {
		if h.t[i] <= t {
			return h.v[i]
		}
		if k < h.n-1 {
			i = (i - 1 + len(h.t)) % len(h.t)
		}
	}
	return h.v[i]
}

// Point è un campione di calibrazione: istante (s), diametro pulito (NaN se
// non valido) e luminanza efficace relativa.
type Point struct {
	T, D, L float64
}

// Fit stima il modello dai campioni di calibrazione: ricerca a griglia su
// ritardo, τc e τd, minimi quadrati per A e B.
func Fit(pts []Point) (Model, error) {
	valid := 0
	for _, p := range pts {
		if !math.IsNaN(p.D) {
			valid++
		}
	}
	if valid < 200 {
		return Model{}, errors.New("troppo pochi campioni di pupilla validi (servono gli occhi tracciati)")
	}
	best := Model{}
	bestSSE := math.Inf(1)
	xs := make([]float64, len(pts))
	for _, delay := range []float64{0.1, 0.15, 0.2, 0.25, 0.3, 0.35, 0.4, 0.5} {
		for _, tc := range []float64{0.1, 0.15, 0.2, 0.3, 0.4, 0.6, 0.8} {
			for _, td := range []float64{0.3, 0.5, 0.8, 1.2, 1.8, 2.5, 3.5} {
				if td < tc {
					continue
				}
				simulate(pts, delay, tc, td, xs)
				a, b, sse, sst, n := regress(pts, xs)
				if n > 0 && b < 0 && sse < bestSSE {
					bestSSE = sse
					best = Model{A: a, B: b, L0: L0, DelayS: delay, TauConS: tc, TauDilS: td,
						NoiseMM: math.Sqrt(sse / float64(n)), R2: 1 - sse/sst, N: n}
				}
			}
		}
	}
	if best.N == 0 {
		return best, errors.New("la pupilla non si restringe con la luce: compensazione non stimabile")
	}
	best.FittedAt = time.Now()
	return best, nil
}

// simulate calcola xf per ogni punto con i parametri dati.
func simulate(pts []Point, delay, tc, td float64, xs []float64) {
	var f plrFilter
	j := 0 // indice dell'ultimo punto con T ≤ t − delay
	for i, p := range pts {
		for j+1 < len(pts) && pts[j+1].T <= p.T-delay {
			j++
		}
		dt := 0.0
		if i > 0 {
			dt = p.T - pts[i-1].T
		}
		xs[i] = f.step(math.Log10(pts[j].L+L0), dt, tc, td)
	}
}

func regress(pts []Point, xs []float64) (a, b, sse, sst float64, n int) {
	var sx, sy, sxx, sxy, syy float64
	for i, p := range pts {
		if math.IsNaN(p.D) {
			continue
		}
		x := xs[i]
		sx += x
		sy += p.D
		sxx += x * x
		sxy += x * p.D
		syy += p.D * p.D
		n++
	}
	if n < 3 {
		return 0, 0, 0, 0, 0
	}
	fn := float64(n)
	den := fn*sxx - sx*sx
	if den <= 1e-12 {
		return 0, 0, 0, 0, 0
	}
	b = (fn*sxy - sx*sy) / den
	a = (sy - b*sx) / fn
	sst = syy - sy*sy/fn
	for i, p := range pts {
		if math.IsNaN(p.D) {
			continue
		}
		r := p.D - (a + b*xs[i])
		sse += r * r
	}
	if sst <= 0 {
		sst = 1e-9
	}
	return a, b, sse, sst, n
}

// Predict restituisce la curva del modello sui punti di calibrazione (per il grafico).
func (m *Model) Predict(pts []Point) []float64 {
	xs := make([]float64, len(pts))
	simulate(pts, m.DelayS, m.TauConS, m.TauDilS, xs)
	for i := range xs {
		xs[i] = m.A + m.B*xs[i]
	}
	return xs
}

// Out è il risultato di un campione.
type Out struct {
	Valid       bool    // diametro misurato disponibile
	MeasMM      float64 // diametro pulito e levigato (media dei due occhi)
	PredMM      float64 // previsto dalla luminanza (NaN senza modello)
	ResidMM     float64 // MeasMM − PredMM: carico al netto della luce
	Z           float64 // componente fasica del residuo, in deviazioni tipiche
	LumEff      float64
	Compensated bool // c'è un modello: il residuo è al netto della luce
}

// Processor pulisce il segnale e calcola residuo e indice di carico.
type Processor struct {
	Model *Model

	offLR      float64 // differenza sinistra − destra, per un occhio solo
	offInit    bool
	lastBad    float64 // istante dell'ultimo campione non valido
	smooth     float64
	smoothInit bool

	lum lumHist
	plr plrFilter

	fast, slow, dev2 float64
	emaInit          bool
	lastT            float64
	out              Out
}

// Parametri di pulizia e dell'indice.
const (
	blinkMarginS = 0.12 // dopo un ammiccamento il diametro è falsato
	spikeMM      = 0.6
	smoothTauS   = 0.12
	fastTauS     = 1.0
	slowTauS     = 20.0
	devTauS      = 60.0
	minDevMM     = 0.02
)

// Reset azzera lo stato (non il modello).
func (p *Processor) Reset() {
	m := p.Model
	*p = Processor{Model: m}
}

// Update elabora un campione: t in secondi, diametri sinistro/destro con
// validità, luminanza efficace relativa (bianco = 1) vista dalla persona.
func (p *Processor) Update(t float64, d [2]float64, v [2]bool, lumEff float64) Out {
	dt := 0.0
	if p.lastT > 0 {
		dt = math.Max(0, math.Min(t-p.lastT, 0.2))
	}
	p.lastT = t
	out := Out{LumEff: lumEff, PredMM: math.NaN()}

	m := p.Model
	if m.Valid() {
		p.lum.push(t, math.Log10(lumEff+m.L0))
		xf := p.plr.step(p.lum.at(t-m.DelayS), dt, m.TauConS, m.TauDilS)
		out.PredMM = m.A + m.B*xf
		out.Compensated = true
	}

	// Diametro: media dei due occhi; con uno solo si corregge con la
	// differenza media tra i due.
	var meas float64
	switch {
	case v[0] && v[1] && d[0] > 0 && d[1] > 0:
		meas = (d[0] + d[1]) / 2
		diff := d[0] - d[1]
		if !p.offInit {
			p.offLR, p.offInit = diff, true
		} else {
			p.offLR += (diff - p.offLR) * math.Min(1, dt/10)
		}
	case v[0] && d[0] > 0:
		meas = d[0] - p.offLR/2
	case v[1] && d[1] > 0:
		meas = d[1] + p.offLR/2
	default:
		p.lastBad = t
		return p.hold(out)
	}
	if t-p.lastBad < blinkMarginS {
		return p.hold(out)
	}
	if p.smoothInit && math.Abs(meas-p.smooth) > spikeMM {
		return p.hold(out)
	}
	if !p.smoothInit {
		p.smooth, p.smoothInit = meas, true
	} else {
		p.smooth += (meas - p.smooth) * (1 - math.Exp(-dt/smoothTauS))
	}
	out.Valid = true
	out.MeasMM = p.smooth
	resid := p.smooth
	if out.Compensated {
		resid = p.smooth - out.PredMM
	}
	out.ResidMM = resid
	if !p.emaInit {
		p.fast, p.slow, p.dev2, p.emaInit = resid, resid, minDevMM*minDevMM, true
	} else {
		p.fast += (resid - p.fast) * (1 - math.Exp(-dt/fastTauS))
		p.slow += (resid - p.slow) * (1 - math.Exp(-dt/slowTauS))
		dev := p.fast - p.slow
		p.dev2 += (dev*dev - p.dev2) * (1 - math.Exp(-dt/devTauS))
	}
	out.Z = (p.fast - p.slow) / math.Max(math.Sqrt(p.dev2), minDevMM)
	p.out = out
	return out
}

// hold: campione non valido, si mantengono gli ultimi indici ma Valid=false.
func (p *Processor) hold(out Out) Out {
	out.MeasMM = p.out.MeasMM
	out.ResidMM = p.out.ResidMM
	out.Z = p.out.Z
	return out
}
