// SPDX-License-Identifier: AGPL-3.0-or-later

package pupil

import (
	"math"
	"math/rand/v2"
	"testing"
)

// truth è una pupilla sintetica con parametri noti.
var truth = Model{A: 3.3, B: -1.1, L0: L0, DelayS: 0.25, TauConS: 0.2, TauDilS: 1.2}

// synth genera 90 Hz di diametri per una sequenza di luminanze a gradini,
// con rumore e un carico cognitivo opzionale (mm aggiunti).
func synth(r *rand.Rand, levels []float64, stepS float64, load func(t float64) float64) []Point {
	var pts []Point
	n := int(float64(len(levels)) * stepS * 90)
	for i := 0; i < n; i++ {
		t := float64(i) / 90
		L := levels[int(t/stepS)]
		pts = append(pts, Point{T: t, L: L})
	}
	pred := truth.Predict(pts)
	for i := range pts {
		pts[i].D = pred[i] + r.NormFloat64()*0.03
		if load != nil {
			pts[i].D += load(pts[i].T)
		}
		if math.Mod(pts[i].T, 4.1) < 0.15 { // ammiccamenti
			pts[i].D = math.NaN()
		}
	}
	return pts
}

var levels = []float64{0.2, 0.003, 0.65, 0.05, 1, 0, 0.3, 0.02, 0.2}

func TestFitRecoversModel(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 1))
	m, err := Fit(synth(r, levels, 3.3, nil))
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("A=%.2f B=%.2f ritardo=%.2f τc=%.2f τd=%.2f R²=%.3f rumore=%.3f", m.A, m.B, m.DelayS, m.TauConS, m.TauDilS, m.R2, m.NoiseMM)
	if math.Abs(m.B-truth.B) > 0.08 || math.Abs(m.A-truth.A) > 0.08 {
		t.Errorf("A, B = %.2f, %.2f; attesi %.2f, %.2f", m.A, m.B, truth.A, truth.B)
	}
	if math.Abs(m.DelayS-truth.DelayS) > 0.051 {
		t.Errorf("ritardo %.2f, atteso %.2f", m.DelayS, truth.DelayS)
	}
	if m.R2 < 0.95 {
		t.Errorf("R² %.3f", m.R2)
	}
}

func TestFitRejectsFlatPupil(t *testing.T) {
	r := rand.New(rand.NewPCG(2, 2))
	pts := synth(r, levels, 3.3, nil)
	for i := range pts {
		if !math.IsNaN(pts[i].D) {
			pts[i].D = 4 + r.NormFloat64()*0.03 // la pupilla non risponde alla luce
		}
	}
	if m, err := Fit(pts); err == nil && m.R2 > 0.2 {
		t.Errorf("una pupilla piatta non deve dare un modello credibile: %+v", m)
	}
}

// Con il modello, il residuo resta piatto mentre la luce cambia e il carico
// cognitivo emerge nell'indice Z.
func TestProcessorSeparatesLoadFromLight(t *testing.T) {
	r := rand.New(rand.NewPCG(3, 3))
	m, err := Fit(synth(r, levels, 3.3, nil))
	if err != nil {
		t.Fatal(err)
	}
	// 60 s di luminanza che oscilla lentamente; carico +0,25 mm tra 40 e 50 s.
	load := func(t float64) float64 {
		if t > 40 && t < 50 {
			return 0.25 * (1 - math.Exp(-(t-40)/0.8))
		}
		return 0
	}
	var pts []Point
	for i := 0; i < 60*90; i++ {
		tt := float64(i) / 90
		v := 0.5 + 0.35*math.Sin(2*math.Pi*tt/16)
		pts = append(pts, Point{T: tt, L: math.Pow(v, 2.2)})
	}
	pred := truth.Predict(pts)
	p := &Processor{Model: &m}
	var maxRestResid, minMeas, maxMeas, zLoad float64
	minMeas = 99
	for i, pt := range pts {
		d := pred[i] + load(pt.T) + r.NormFloat64()*0.03
		o := p.Update(pt.T+1, [2]float64{d + 0.05, d - 0.05}, [2]bool{true, true}, pt.L)
		if !o.Valid || pt.T < 10 {
			continue
		}
		minMeas, maxMeas = math.Min(minMeas, o.MeasMM), math.Max(maxMeas, o.MeasMM)
		if pt.T < 40 {
			maxRestResid = math.Max(maxRestResid, math.Abs(o.ResidMM-m.A+truth.A))
		}
		if pt.T > 43 && pt.T < 50 {
			zLoad = math.Max(zLoad, o.Z)
		}
	}
	t.Logf("diametro misurato %.2f–%.2f mm, |residuo| a riposo ≤ %.3f mm, Z sotto carico %.1f", minMeas, maxMeas, maxRestResid, zLoad)
	if maxMeas-minMeas < 0.5 {
		t.Fatalf("la luce deve muovere la pupilla di almeno 0,5 mm (%.2f)", maxMeas-minMeas)
	}
	if maxRestResid > 0.12 {
		t.Errorf("residuo a riposo fino a %.3f mm: la luce non è compensata", maxRestResid)
	}
	if zLoad < 2 {
		t.Errorf("il carico di 0,25 mm deve dare Z > 2 (%.2f)", zLoad)
	}
}

func TestLumHistDelay(t *testing.T) {
	var h lumHist
	for i := 0; i < 300; i++ {
		h.push(float64(i)*0.01, float64(i))
	}
	if v := h.at(2.0); v != 200 {
		t.Errorf("at(2.0) = %v", v)
	}
	if v := h.at(-5); v != 44 { // il più vecchio rimasto (300 − 256)
		t.Errorf("at(-5) = %v", v)
	}
}
