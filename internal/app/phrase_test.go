// SPDX-License-Identifier: AGPL-3.0-or-later

package app

import (
	"context"
	"image/png"
	"math/rand/v2"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ablescia/kite/internal/correct"
	"github.com/ablescia/kite/internal/engine"
	"github.com/ablescia/kite/internal/gazein"
	"github.com/ablescia/kite/internal/lm"
	"github.com/ablescia/kite/internal/ngram"
	"github.com/ablescia/kite/internal/oculo"
	"github.com/ablescia/kite/internal/selector"
	"github.com/ablescia/kite/internal/x11ui"
)

// La catena intera nella scrittura libera: lo sguardo scrive «ho s»
// guardando i tasti, l'interfaccia disegna la frase imparata in grigio e ne
// comunica l'area, e fissarla la scrive. Con KITE_SHOTS=cartella salva
// le schermate (per guardarle).
func TestFreePhraseEndToEnd(t *testing.T) {
	geom := gazein.Geometry{WidthMM: 700, HeightMM: 390, WidthPx: 1920, HeightPx: 1080}
	kb, err := selector.NewKeyboard("qwerty", selector.DefaultArea, selector.DefaultGap)
	if err != nil {
		t.Fatal(err)
	}
	kb.AddEditBar(selector.DefaultBar, selector.DefaultGap, true)
	log, err := engine.NewLogger(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	model := lm.New()
	phrases := ngram.New(ngram.DefaultParams())
	phrases.PrefixMass = model.PrefixMass
	phrases.Learn("ho sete, vorrei dell'acqua fresca")
	phrases.Learn("ho sete, vorrei dell'acqua fresca")
	eng := engine.New(engine.Config{Geom: geom, KB: kb, LM: model, Oculo: oculo.DefaultConfig(),
		Hybrid: selector.DefaultHybrid(), SigmaDeg: 1.6, CoreFrac: 0.6, PriorEps: 0.1,
		LumAlpha: 0.3, FovealDeg: 5, Log: log, Corrector: correct.New(correct.DefaultParams(), model),
		Phrases: phrases})
	a, err := New(x11ui.NewHeadless(1920, 1080))
	if err != nil {
		t.Fatal(err)
	}
	a.Eng, a.Geom, a.KB, a.LM, a.Log, a.Source = eng, geom, kb, model, log, "simulazione"
	a.Opt = Options{UsePrediction: true, UsePupil: true, UseCorrector: true, UsePhrases: true}
	a.ctx = context.Background()
	a.rng = rand.New(rand.NewPCG(1, 2))
	eng.SetToggles(true, true, true)
	f := newFree()
	f.debug = false
	a.SetScreen(f)

	now := time.Now()
	dev := int64(1_000_000)
	r := rand.New(rand.NewPCG(5, 6))
	shots := os.Getenv("KITE_SHOTS")
	shot := func(name string) {
		if shots == "" {
			return
		}
		fh, err := os.Create(filepath.Join(shots, name))
		if err != nil {
			t.Fatal(err)
		}
		png.Encode(fh, a.canvas)
		fh.Close()
	}
	// look guarda p per ms millisecondi: campioni a 90 Hz, frame a 60 Hz
	look := func(p [2]float64, ms int) {
		for n := 0; n < ms*90/1000; n++ {
			dev += 11_111
			q := [2]float64{p[0] + r.NormFloat64()*0.0006, p[1] + r.NormFloat64()*0.001}
			eye := [3]float64{0, 0, 620}
			eng.Process(gazein.Sample{HostNs: time.Now().UnixNano(), DeviceUs: dev, Gaze: q, GazeValid: true,
				Raw: q, RawValid: true, PupilMM: [2]float64{3.5, 3.5}, PupilValid: [2]bool{true, true},
				EyeMM: [2][3]float64{eye, eye}, EyeValid: [2]bool{true, true}, Presence: 2})
			if n%3 != 2 {
				now = now.Add(time.Second / 60)
				if err := a.frame(now); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	rest := [2]float64{0.5, 0.05} // in alto, lontano dai tasti
	look(rest, 300)
	for _, c := range "ho s" {
		look(kb.Keys[kb.IndexOf(c)].Center, 800)
		look(rest, 300)
	}
	s := eng.Snapshot()
	if s.Typed != "ho s" || s.Phrase != "ete, vorrei dell'acqua fresca" && s.Phrase != "ete vorrei dell'acqua fresca" {
		t.Fatalf("scritto «%s», frase grigia %q", s.Typed, s.Phrase)
	}
	if f.ghost.Empty() {
		t.Fatal("la frase grigia non è stata disegnata")
	}
	shot("frase-proposta.png")
	g := a.norm(f.ghost)
	center := [2]float64{(g[0] + g[2]) / 2, (g[1] + g[3]) / 2}
	look(center, 600)
	if p := eng.Snapshot().PhraseProg; p < 0.3 || p >= 1 {
		t.Fatalf("avanzamento a metà fissazione: %.2f", p)
	}
	shot("frase-fissata.png")
	look(center, 600)
	if s := eng.Snapshot(); s.Typed != "ho sete vorrei dell'acqua fresca " {
		t.Fatalf("dopo la fissazione: «%s» (grigio %q)", s.Typed, s.Phrase)
	}
	// l'engine data l'accettazione con l'orologio vero: il messaggio dura 4 s da lì
	now = time.Now()
	look(rest, 200)
	if s := eng.Snapshot(); s.Accepted != "sete vorrei dell'acqua fresca" {
		t.Errorf("frase accettata annullabile: %q", s.Accepted)
	}
	shot("frase-accettata.png")
}
