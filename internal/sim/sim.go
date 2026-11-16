// SPDX-License-Identifier: AGPL-3.0-or-later

// Package sim è un utente simulato che genera campioni come il driver
// (90 Hz) guardando ciò che Kite gli chiede. Serve a provare
// l'intera catena senza tracker e senza una persona davanti (test
// automatici, interfaccia headless).
//
// Il modello oculomotore è volutamente realistico, perché i problemi che
// il selettore ibrido deve risolvere nascono proprio da qui:
//   - latenza saccadica ~200 ms, durata dalla sequenza principale
//     (21 ms + 2,2 ms/°), profilo di velocità minimum-jerk;
//   - atterraggio corto (guadagno ~0,95) con dispersione, e saccade
//     correttiva dopo ~150 ms se l'errore supera 1°;
//   - ricerca visiva: a volte fissa uno o due tasti sbagliati prima di
//     quello giusto (è il "Midas touch" che il dwell lungo evitava);
//   - rumore del tracker, errore sistematico di calibrazione, filtro del
//     firmware, ammiccamenti;
//   - pupilla con riflesso alla luce (ritardo, costrizione veloce,
//     dilatazione lenta) e dilatazione da carico cognitivo.
//
// I risultati di un blocco simulato dipendono da questi parametri: provano
// che il software funziona, non quanto è veloce una persona reale.
package sim

import (
	"context"
	"math"
	"math/rand/v2"
	"time"

	"github.com/ablescia/kite/internal/engine"
	"github.com/ablescia/kite/internal/gazein"
	"github.com/ablescia/kite/internal/pupil"
)

// Source è ciò che l'utente simulato osserva (l'engine).
type Source interface {
	Cue() engine.Cue
	LumAt(p [2]float64) float64
}

// Params sono i parametri dell'utente simulato.
type Params struct {
	RawNoiseDeg  float64    // rumore campione per campione dello sguardo non filtrato
	BiasDeg      [2]float64 // errore sistematico residuo della calibrazione
	FilterTauS   float64    // filtro del firmware sullo sguardo "filtrato"
	LatencyS     float64    // latenza saccadica media
	LatencySDS   float64
	SearchProb   float64 // probabilità di cercare il tasto prima di trovarlo
	BlinkPerS    float64
	Gain, GainSD float64 // guadagno della saccade primaria
	LandSDDeg    float64 // dispersione dell'atterraggio (più 3% dell'ampiezza)
	Pupil        pupil.Model
	LoadMM       float64 // dilatazione a carico massimo
}

// DefaultParams: un utente esperto ma non perfetto, con un 4C ben calibrato.
// Rumore e filtro sono misurati su un 4C reale (2026-11-06, persona a 70 cm):
// precisione RMS campione-campione 0,27° sullo sguardo non filtrato (= 0,14°
// per asse), sguardo filtrato in ritardo di ~30 ms.
func DefaultParams() Params {
	return Params{
		RawNoiseDeg: 0.14, BiasDeg: [2]float64{0.35, -0.3}, FilterTauS: 0.03,
		LatencyS: 0.21, LatencySDS: 0.04, SearchProb: 0.2, BlinkPerS: 0.25,
		Gain: 0.95, GainSD: 0.03, LandSDDeg: 0.35,
		Pupil:  pupil.Model{A: 3.4, B: -1.0, L0: pupil.L0, DelayS: 0.28, TauConS: 0.22, TauDilS: 1.1},
		LoadMM: 0.3,
	}
}

type moveKind int

const (
	moveDistractor moveKind = iota
	moveMain
	moveCorrective
	moveFix
	moveWander
)

type move struct {
	at   float64
	goal [2]float64
	kind moveKind
}

type saccade struct {
	from, to [2]float64
	t0, dur  float64
	kind     moveKind
	goal     [2]float64
}

// User è l'utente simulato.
type User struct {
	P    Params
	Geom gazein.Geometry
	src  Source
	r    *rand.Rand

	seq      uint64
	t        float64
	pos      [2]float64
	plan     []move
	sac      *saccade
	cue      engine.Cue
	cueInit  bool
	goal     [2]float64 // bersaglio corrente (per i tentativi successivi)
	hasGoal  bool
	goalAt   float64 // da quando si fissa il bersaglio senza esito
	filt     [2]float64
	filtInit bool

	blinkUntil, nextBlink float64
	searching             bool
	targetT               float64

	lumX  [128]float64 // storia del log della luminanza, un valore per campione
	lumN  int
	plr   float64
	plrOK bool
	load  float64
}

// New crea l'utente simulato.
func New(p Params, geom gazein.Geometry, src Source, seed uint64) *User {
	u := &User{P: p, Geom: geom, src: src, r: rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15)),
		pos: [2]float64{0.5, 0.5}}
	u.nextBlink = u.expo(1 / p.BlinkPerS)
	return u
}

func (u *User) expo(mean float64) float64 { return -mean * math.Log(1-u.r.Float64()) }

func (u *User) norm(mu, sd, lo, hi float64) float64 {
	return math.Max(lo, math.Min(hi, mu+sd*u.r.NormFloat64()))
}

// degToNorm converte uno spostamento in gradi in unità normalizzate (x, y).
func (u *User) degToNorm(dx, dy float64) [2]float64 {
	mm := u.Geom.MMPerDeg(gazein.DefaultEye)
	return [2]float64{dx * mm / u.Geom.WidthMM, dy * mm / u.Geom.HeightMM}
}

func (u *User) jitter(p [2]float64, sdDeg float64) [2]float64 {
	d := u.degToNorm(sdDeg*u.r.NormFloat64(), sdDeg*u.r.NormFloat64())
	return [2]float64{p[0] + d[0], p[1] + d[1]}
}

// Run genera campioni a 90 Hz su out finché ctx non termina.
func (u *User) Run(ctx context.Context, out chan<- gazein.Sample) {
	tk := time.NewTicker(time.Second / 90)
	defer tk.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tk.C:
		}
		select {
		case out <- u.Next():
		case <-ctx.Done():
			return
		}
	}
}

// Next avanza di un campione (1/90 s di tempo simulato) e lo restituisce.
// I test lo chiamano direttamente, più veloci del tempo reale.
func (u *User) Next() gazein.Sample {
	const dev0 = 50_000_000_000 // orologio del dispositivo simulato, µs
	u.seq++
	u.t = float64(u.seq) / 90
	s := u.step()
	s.Seq, s.HostNs, s.DeviceUs = u.seq, time.Now().UnixNano(), dev0+int64(u.seq)*11111
	return s
}

func (u *User) replan(c engine.Cue) {
	u.plan = u.plan[:0]
	at := u.t + u.norm(u.P.LatencyS, u.P.LatencySDS, 0.12, 0.45)
	u.searching = false
	switch {
	case c.HasTarget:
		u.goal, u.hasGoal, u.targetT = c.Target, true, u.t
		if len(c.Keys) > 1 && u.r.Float64() < u.P.SearchProb {
			n := 1 + u.r.IntN(2)
			for i := 0; i < n; i++ {
				k := c.Keys[u.r.IntN(len(c.Keys))]
				if k == c.Target {
					continue
				}
				u.plan = append(u.plan, move{at: at, goal: k, kind: moveDistractor})
				at += u.norm(0.26, 0.05, 0.15, 0.4) // fissazione di ricerca
			}
		}
		u.plan = append(u.plan, move{at: at, goal: c.Target, kind: moveMain})
	case c.HasFix:
		u.goal, u.hasGoal = c.Fix, true
		u.plan = append(u.plan, move{at: at, goal: c.Fix, kind: moveFix})
	default:
		u.hasGoal = false
	}
	u.goalAt = math.Inf(1)
}

func (u *User) startSaccade(m move) {
	from := u.pos
	amp := u.Geom.AngleDeg(gazein.DefaultEye, from, m.goal)
	to := m.goal
	switch m.kind {
	case moveMain, moveDistractor, moveFix, moveWander:
		g := u.norm(u.P.Gain, u.P.GainSD, 0.8, 1.05)
		to = [2]float64{from[0] + g*(m.goal[0]-from[0]), from[1] + g*(m.goal[1]-from[1])}
		to = u.jitter(to, u.P.LandSDDeg+0.03*amp)
	case moveCorrective:
		to = u.jitter(m.goal, 0.25)
	}
	amp = math.Max(0.3, u.Geom.AngleDeg(gazein.DefaultEye, from, to))
	u.sac = &saccade{from: from, to: to, t0: u.t, dur: 0.021 + 0.0022*amp, kind: m.kind, goal: m.goal}
	u.searching = m.kind == moveDistractor
}

func (u *User) step() gazein.Sample {
	c := u.src.Cue()
	if !u.cueInit || c.Seq != u.cue.Seq || c.HasTarget != u.cue.HasTarget || c.HasFix != u.cue.HasFix ||
		c.Fix != u.cue.Fix || c.Target != u.cue.Target {
		u.cue, u.cueInit = c, true
		u.replan(c)
	}
	u.cue.Load = c.Load

	// Movimento.
	if u.sac == nil && len(u.plan) > 0 && u.t >= u.plan[0].at {
		u.startSaccade(u.plan[0])
		u.plan = u.plan[1:]
	}
	if u.sac != nil {
		s := u.sac
		tau := (u.t - s.t0) / s.dur
		if tau >= 1 {
			u.pos = s.to
			u.sac = nil
			if s.kind == moveMain || s.kind == moveFix || s.kind == moveCorrective {
				u.goalAt = u.t
				if s.kind != moveCorrective && u.Geom.AngleDeg(gazein.DefaultEye, s.to, s.goal) > 1.0 {
					u.plan = append(u.plan, move{at: u.t + u.norm(0.15, 0.03, 0.1, 0.25), goal: s.goal, kind: moveCorrective})
				}
			}
		} else {
			k := 10*math.Pow(tau, 3) - 15*math.Pow(tau, 4) + 6*math.Pow(tau, 5)
			u.pos = [2]float64{s.from[0] + k*(s.to[0]-s.from[0]), s.from[1] + k*(s.to[1]-s.from[1])}
		}
	} else if len(u.plan) == 0 {
		switch {
		case u.hasGoal && u.t-u.goalAt > 2.5:
			// Fermo sul bersaglio da troppo senza esito: si riposiziona.
			u.plan = append(u.plan, move{at: u.t + 0.1, goal: u.goal, kind: moveCorrective})
			u.goalAt = math.Inf(1)
		case !u.hasGoal && u.r.Float64() < 1.0/90:
			// Senza compiti lo sguardo vaga nella parte centrale dello schermo.
			u.plan = append(u.plan, move{at: u.t, goal: [2]float64{0.3 + 0.4*u.r.Float64(), 0.3 + 0.4*u.r.Float64()}, kind: moveWander})
		}
		// Deriva fissazionale lenta.
		d := u.degToNorm(0.02*u.r.NormFloat64(), 0.02*u.r.NormFloat64())
		u.pos[0] += d[0]
		u.pos[1] += d[1]
	}

	// Ammiccamenti (mai durante una saccade).
	if u.t >= u.nextBlink && u.sac == nil {
		u.blinkUntil = u.t + u.norm(0.15, 0.03, 0.08, 0.3)
		u.nextBlink = u.blinkUntil + u.expo(1/u.P.BlinkPerS)
	}
	blink := u.t < u.blinkUntil

	// Pupilla: luce vista + carico (ricerca, decisione dopo un nuovo
	// bersaglio, compito mentale del laboratorio).
	lum := u.src.LumAt(u.pos)
	m := u.P.Pupil
	u.lumX[u.lumN%len(u.lumX)] = math.Log10(lum + m.L0)
	u.lumN++
	lag := min(int(m.DelayS*90), u.lumN-1)
	x := u.lumX[(u.lumN-1-lag)%len(u.lumX)]
	if !u.plrOK {
		u.plr, u.plrOK = x, true
	}
	tau := m.TauDilS
	if x > u.plr {
		tau = m.TauConS
	}
	u.plr += (x - u.plr) * (1 - math.Exp(-1.0/90/tau))
	want := c.Load
	if u.cue.HasTarget {
		want += 0.5 * math.Exp(-(u.t-u.targetT)/1.5)
	}
	if u.searching {
		want += 0.4
	}
	u.load += (math.Min(want, 1.5) - u.load) * (1 - math.Exp(-1.0/90/0.8))
	d := m.A + m.B*u.plr + u.P.LoadMM*u.load

	s := gazein.Sample{Presence: 2}
	sway := 15 * math.Sin(u.t*0.3)
	s.EyeMM = [2][3]float64{{-31 + sway, 25, 620 + 10*math.Sin(u.t*0.17)}, {31 + sway, 25, 620 + 10*math.Sin(u.t*0.17)}}
	s.EyeValid = [2]bool{true, true}
	if blink {
		s.Gaze, s.Raw = [2]float64{-1, -1}, [2]float64{-1, -1}
		s.PupilMM = [2]float64{-1, -1}
		return s
	}
	b := u.degToNorm(u.P.BiasDeg[0], u.P.BiasDeg[1])
	raw := u.jitter([2]float64{u.pos[0] + b[0], u.pos[1] + b[1]}, u.P.RawNoiseDeg)
	if !u.filtInit {
		u.filt, u.filtInit = raw, true
	}
	a := 1 - math.Exp(-1.0/90/u.P.FilterTauS)
	u.filt[0] += (raw[0] - u.filt[0]) * a
	u.filt[1] += (raw[1] - u.filt[1]) * a
	s.Raw, s.RawValid = raw, true
	s.Gaze, s.GazeValid = u.filt, true
	s.PupilMM = [2]float64{d + 0.06 + 0.015*u.r.NormFloat64(), d - 0.06 + 0.015*u.r.NormFloat64()}
	s.PupilValid = [2]bool{true, true}
	return s
}
