// SPDX-License-Identifier: AGPL-3.0-or-later

package oculo

import (
	"math"
	"math/rand/v2"
	"testing"

	"github.com/ablescia/kite/internal/gazein"
)

var geom = gazein.Geometry{WidthMM: 700, HeightMM: 390, WidthPx: 1920, HeightPx: 1080}
var eye = gazein.DefaultEye

// degToNorm converte uno spostamento orizzontale in gradi in unità normalizzate.
func degToNorm(deg float64) float64 { return eye[2] * math.Tan(deg*math.Pi/180) / geom.WidthMM }

type trace struct {
	t []int64
	p [][2]float64
}

// fixation aggiunge n campioni a 90 Hz attorno a c con rumore gaussiano sigma (gradi).
func (tr *trace) fixation(r *rand.Rand, c [2]float64, n int, sigma float64) {
	for i := 0; i < n; i++ {
		tr.add([2]float64{c[0] + degToNorm(sigma*r.NormFloat64()), c[1] + degToNorm(sigma*r.NormFloat64())*geom.WidthMM/geom.HeightMM})
	}
}

// saccade aggiunge i campioni di una saccade a profilo minimum-jerk.
func (tr *trace) saccade(from, to [2]float64, durMs float64) {
	start := tr.next()
	for {
		t := tr.next()
		tau := float64(t-start) / 1000 / durMs
		if tau >= 1 {
			return
		}
		s := 10*math.Pow(tau, 3) - 15*math.Pow(tau, 4) + 6*math.Pow(tau, 5)
		tr.add([2]float64{from[0] + s*(to[0]-from[0]), from[1] + s*(to[1]-from[1])})
	}
}

func (tr *trace) next() int64 { return int64(len(tr.t)) * 11111 }
func (tr *trace) add(p [2]float64) {
	tr.t = append(tr.t, tr.next())
	tr.p = append(tr.p, p)
}

func TestSaccadeDetection(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	var tr trace
	a := [2]float64{0.3, 0.5}
	b := [2]float64{0.3 + degToNorm(10), 0.5}
	tr.fixation(r, a, 300, 0.25) // 3,3 s per stimare il rumore
	tr.saccade(a, b, 43)         // 10° → ~43 ms (sequenza principale)
	landUs := tr.next()
	tr.fixation(r, b, 60, 0.25)

	d := New(DefaultConfig(), geom)
	var ends []SaccadeInfo
	var fixStartUs int64
	var stableAtUs int64
	var last State
	for i := range tr.t {
		st, evs := d.Update(tr.t[i], tr.p[i], true, eye)
		for _, e := range evs {
			if e.Kind == EvSaccadeEnd {
				ends = append(ends, e.Sac)
				fixStartUs = st.FixStartUs
			}
		}
		if len(ends) == 1 && st.FixStable && stableAtUs == 0 {
			stableAtUs = tr.t[i]
		}
		last = st
	}
	if len(ends) != 1 {
		t.Fatalf("saccadi rilevate: %d, attesa 1 (%+v)", len(ends), ends)
	}
	s := ends[0]
	if math.Abs(s.AmplitudeDeg-10) > 1 {
		t.Errorf("ampiezza %.2f°, attesa ~10°", s.AmplitudeDeg)
	}
	if s.PeakVel < 150 {
		t.Errorf("picco di velocità %.0f °/s troppo basso", s.PeakVel)
	}
	if dt := float64(fixStartUs-landUs) / 1000; math.Abs(dt) > 23 {
		t.Errorf("inizio fissazione a %+.0f ms dall'atterraggio reale", dt)
	}
	if lat := float64(stableAtUs-landUs) / 1000; lat > 60 {
		t.Errorf("fissazione stabile dopo %.0f ms dall'atterraggio (max 60)", lat)
	}
	if e := geom.AngleDeg(eye, last.FixCentroid, b); e > 0.2 {
		t.Errorf("centroide a %.2f° dal bersaglio", e)
	}
	if !last.AfterSaccade {
		t.Error("la fissazione dopo la saccade deve avere AfterSaccade")
	}
	t.Logf("soglie: picco %.0f °/s, fine %.0f °/s; saccade %.0f ms, picco %.0f °/s",
		last.PeakThr, last.OnsetThr, s.DurationMs(), s.PeakVel)
}

func TestNoFalseSaccadesInNoise(t *testing.T) {
	for _, sigma := range []float64{0.15, 0.3, 0.5} {
		r := rand.New(rand.NewPCG(3, uint64(sigma*100)))
		var tr trace
		tr.fixation(r, [2]float64{0.5, 0.5}, 900, sigma) // 10 s
		d := New(DefaultConfig(), geom)
		n := 0
		var st State
		for i := range tr.t {
			var evs []Event
			st, evs = d.Update(tr.t[i], tr.p[i], true, eye)
			for _, e := range evs {
				if e.Kind == EvSaccadeEnd && tr.t[i] > 2_000_000 {
					n++
				}
			}
		}
		if n > 0 {
			t.Errorf("rumore %.2f°: %d false saccadi dopo l'adattamento (soglia %.0f °/s)", sigma, n, st.PeakThr)
		}
		if st.FixID != 1 {
			t.Errorf("rumore %.2f°: la fissazione è stata spezzata (id %d)", sigma, st.FixID)
		}
	}
}

func TestBlinkStartsNewFixationWithoutSaccade(t *testing.T) {
	r := rand.New(rand.NewPCG(5, 6))
	var tr trace
	c := [2]float64{0.5, 0.5}
	tr.fixation(r, c, 50, 0.2)
	d := New(DefaultConfig(), geom)
	for i := range tr.t {
		d.Update(tr.t[i], tr.p[i], true, eye)
	}
	t0 := tr.next()
	// 150 ms senza campioni validi
	lost := false
	for k := int64(0); k < 14; k++ {
		_, evs := d.Update(t0+k*11111, [2]float64{}, false, eye)
		for _, e := range evs {
			lost = lost || e.Kind == EvLost
		}
	}
	if !lost {
		t.Fatal("ammiccamento di 150 ms non segnalato come perdita")
	}
	before := d.State().FixID
	st, _ := d.Update(t0+15*11111, c, true, eye)
	if st.FixID == before || st.AfterSaccade {
		t.Errorf("dopo la perdita serve una nuova fissazione senza saccade: %+v", st)
	}
}

func TestMicrosaccadeKeepsFixation(t *testing.T) {
	r := rand.New(rand.NewPCG(7, 8))
	var tr trace
	a := [2]float64{0.5, 0.5}
	b := [2]float64{0.5 + degToNorm(0.6), 0.5}
	tr.fixation(r, a, 200, 0.05)
	tr.saccade(a, b, 15)
	tr.fixation(r, b, 30, 0.05)
	d := New(DefaultConfig(), geom)
	var st State
	for i := range tr.t {
		st, _ = d.Update(tr.t[i], tr.p[i], true, eye)
	}
	if st.FixID != 1 {
		t.Errorf("una microsaccade di 0,6° non deve aprire una nuova fissazione (id %d)", st.FixID)
	}
}

// Uno sguardo che scivola lentamente (senza saccade) per 3° rende la
// fissazione dispersa per sempre (Drift), ma la finestra recente torna
// stabile dove lo sguardo si ferma: è ciò che serve per non lasciare un tasto
// guardato a lungo in una fissazione che non può più attivare nulla.
func TestDriftAndRecentWindow(t *testing.T) {
	r := rand.New(rand.NewPCG(9, 10))
	var tr trace
	a := [2]float64{0.5, 0.5}
	b := [2]float64{0.5 + degToNorm(3), 0.5}
	tr.fixation(r, a, 60, 0.05)
	for i := 1; i <= 40; i++ { // 3° in 440 ms: ~7°/s, sotto ogni soglia di saccade
		f := float64(i) / 40
		tr.add([2]float64{a[0] + f*(b[0]-a[0]), a[1]})
	}
	tr.fixation(r, b, 30, 0.05)
	d := New(DefaultConfig(), geom)
	var st State
	sawStableDrift := false
	for i := range tr.t {
		st, _ = d.Update(tr.t[i], tr.p[i], true, eye)
		if i == 59 && (!st.FixStable || st.Drift || !st.WinStable) {
			t.Fatalf("fissazione ferma: stabile %v, deriva %v, finestra stabile %v", st.FixStable, st.Drift, st.WinStable)
		}
		if st.Drift && st.WinStable {
			sawStableDrift = true
		}
	}
	if st.FixID != 1 {
		t.Fatalf("lo scivolamento lento non è una saccade (id %d)", st.FixID)
	}
	if st.FixStable || !st.Drift {
		t.Errorf("dopo 3° di scivolamento la fissazione è dispersa: stabile %v, deriva %v (disp. %.2f°)", st.FixStable, st.Drift, st.FixRMSDeg)
	}
	if !st.WinStable || !sawStableDrift {
		t.Errorf("la finestra recente torna stabile dove lo sguardo si ferma (disp. %.2f°)", st.WinRMSDeg)
	}
	if dx := math.Abs(st.WinCentroid[0] - b[0]); dx > degToNorm(0.2) {
		t.Errorf("centroide recente a %.2f° dal punto d'arrivo", dx/degToNorm(1))
	}
	if dx := math.Abs(st.FixCentroid[0] - b[0]); dx < degToNorm(0.5) {
		t.Errorf("il centroide dell'intera fissazione resta indietro (%.2f°)", dx/degToNorm(1))
	}

	cfg := DefaultConfig()
	cfg.WindowSamples = 0
	d = New(cfg, geom)
	for i := range tr.t {
		st, _ = d.Update(tr.t[i], tr.p[i], true, eye)
	}
	if st.WinStable {
		t.Error("senza finestra non c'è una finestra stabile")
	}
}
