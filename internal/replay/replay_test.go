// SPDX-License-Identifier: AGPL-3.0-or-later

package replay_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ablescia/kite/internal/engine"
	"github.com/ablescia/kite/internal/gazein"
	"github.com/ablescia/kite/internal/lm"
	"github.com/ablescia/kite/internal/oculo"
	"github.com/ablescia/kite/internal/replay"
	"github.com/ablescia/kite/internal/selector"
	"github.com/ablescia/kite/internal/sim"
)

// record registra con il motore e l'utente simulato una sessione di tre
// frasi, con la prima versione dei parametri: il simulatore fa errori, così
// il controllo copre anche le selezioni sbagliate.
func record(t *testing.T) string {
	dir := t.TempDir()
	geom := gazein.Geometry{WidthMM: 700, HeightMM: 390, WidthPx: 1920, HeightPx: 1080}
	kb, err := selector.NewKeyboard("qwerty", selector.DefaultArea, selector.DefaultGap)
	if err != nil {
		t.Fatal(err)
	}
	log, err := engine.NewLogger(dir)
	if err != nil {
		t.Fatal(err)
	}
	hp := selector.DefaultHybrid()
	hp.LockFixation, hp.LandMs, hp.CautionMs, hp.RepeatMs, hp.RepeatMinMs = false, 0, 0, 700, 0
	model := lm.New()
	e := engine.New(engine.Config{Geom: geom, KB: kb, LM: model, Oculo: oculo.DefaultConfig(), Hybrid: hp,
		SigmaDeg: selector.DefaultSigmaDeg, CoreFrac: selector.DefaultCoreFrac,
		PriorEps: engine.DefaultPriorEps, LumAlpha: 0.3, FovealDeg: 5, Log: log})
	if err := log.WriteJSON("session.json", map[string]any{"layout": "qwerty", "geometry": geom, "hybrid": hp,
		"sigma_deg": selector.DefaultSigmaDeg, "core": selector.DefaultCoreFrac, "eps": engine.DefaultPriorEps,
		"lm": model.Name(), "area": kb.Area, "gap": selector.DefaultGap}); err != nil {
		t.Fatal(err)
	}
	u := sim.New(sim.DefaultParams(), geom, e, 7)
	for i := 0; i < 90*2; i++ {
		e.Process(u.Next())
	}
	for _, phrase := range []string{"casa bella sole", "mare", "treno lungo notte"} {
		e.StartBlock(phrase)
		done := false
		for i := 0; i < 90*240 && !done; i++ {
			e.Process(u.Next())
			select {
			case <-e.Events():
				done = true
			default:
			}
		}
		if !done {
			t.Fatalf("frase «%s» non conclusa", phrase)
		}
		for i := 0; i < 90; i++ { // pausa tra i blocchi
			e.Process(u.Next())
		}
	}
	log.Close()
	return dir
}

// Una sessione registrata dal motore e rigiocata con i suoi parametri deve
// dare le stesse selezioni del log.
func TestReplayReproducesLog(t *testing.T) {
	s, err := replay.Load(record(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Blocks) != 3 {
		t.Fatalf("blocchi caricati: %d", len(s.Blocks))
	}
	res, err := s.Run(s.Recorded())
	if err != nil {
		t.Fatal(err)
	}
	b := res[replay.CondHybrid]
	if b == nil || b.Logged == 0 || b.Same != b.Logged || b.Errors != b.LogError || b.Late+b.Missed != 0 {
		t.Fatalf("blocchi ibridi: %+v", b)
	}
	t.Logf("%d selezioni (%d errori) riprodotte tutte", b.Logged, b.LogError)
}

// Le sessioni del banco di prova hanno anche blocchi "A", scritti con il
// dwell fisso di 1 s: il replay ne rigioca lo sguardo con il selettore
// ibrido, e la persona, vista la selezione registrata, passa oltre.
func TestReplayBenchDwellBlocks(t *testing.T) {
	dir := record(t)
	p := filepath.Join(dir, "events.jsonl")
	ev, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(ev), "\n")
	n := 0
	for i, l := range lines {
		if strings.Contains(l, `"block":2,`) && strings.Contains(l, `"cond":"B"`) {
			lines[i] = strings.Replace(l, `"cond":"B"`, `"cond":"A"`, 1)
			n++
		}
	}
	if n == 0 {
		t.Fatal("nessun evento del blocco 2 da rietichettare")
	}
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := replay.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	res, err := s.Run(s.Recorded())
	if err != nil {
		t.Fatal(err)
	}
	a, b := res[replay.CondDwell], res[replay.CondHybrid]
	if a == nil || b == nil || a.Correct+a.Errors == 0 {
		t.Fatalf("risultati: A %+v, B %+v", a, b)
	}
	if b.Same != b.Logged || b.Logged == 0 {
		t.Errorf("i blocchi ibridi restano riprodotti: %d su %d", b.Same, b.Logged)
	}
	t.Logf("sguardo del blocco A con il selettore ibrido: %d giuste, %d errori", a.Correct, a.Errors)
}
