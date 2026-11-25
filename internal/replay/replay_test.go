// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Antonio Blescia

package replay_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ablescia/kite/internal/engine"
	"github.com/ablescia/kite/internal/gazein"
	"github.com/ablescia/kite/internal/lm"
	"github.com/ablescia/kite/internal/metrics"
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
	// la prima versione: i campi aggiunti dopo valgono zero (spenti)
	d := selector.DefaultHybrid()
	hp := selector.HybridParams{TMinMs: 120, TMaxMs: 700, RHi: d.RHi, RLo: d.RLo, SHi: d.SHi, RepeatMs: 700,
		PupilGain: d.PupilGain, UsePrediction: true}
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

// recordText registra tre frasi del compito "testo" con i parametri attuali
// (armamento, tempi dei tasti di modifica, ecc.): gli errori entrano nel
// testo e l'utente simulato li cancella.
func recordText(t *testing.T) string {
	dir := t.TempDir()
	geom := gazein.Geometry{WidthMM: 700, HeightMM: 390, WidthPx: 1920, HeightPx: 1080}
	kb, err := selector.NewKeyboard("qwerty", selector.DefaultArea, selector.DefaultGap)
	if err != nil {
		t.Fatal(err)
	}
	kb.AddEditBar(selector.DefaultBar, selector.DefaultGap, true)
	log, err := engine.NewLogger(dir)
	if err != nil {
		t.Fatal(err)
	}
	hp := selector.DefaultHybrid()
	model := lm.New()
	e := engine.New(engine.Config{Geom: geom, KB: kb, LM: model, Oculo: oculo.DefaultConfig(), Hybrid: hp,
		SigmaDeg: selector.DefaultSigmaDeg, CoreFrac: selector.DefaultCoreFrac,
		PriorEps: engine.DefaultPriorEps, LumAlpha: 0.3, FovealDeg: 5, Log: log, Task: metrics.TaskText})
	if err := log.WriteJSON("session.json", map[string]any{"layout": "qwerty", "geometry": geom, "hybrid": hp,
		"sigma_deg": selector.DefaultSigmaDeg, "core": selector.DefaultCoreFrac, "eps": engine.DefaultPriorEps,
		"lm": model.Name(), "area": kb.Area, "bar": kb.Bar, "gap": selector.DefaultGap, "edit_bar": true,
		"speak_key": true, "task": metrics.TaskText}); err != nil {
		t.Fatal(err)
	}
	u := sim.New(sim.DefaultParams(), geom, e, 11)
	for i := 0; i < 90*2; i++ {
		e.Process(u.Next())
	}
	for _, phrase := range []string{"casa bella", "mare blu", "treno lungo"} {
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
		for i := 0; i < 90; i++ {
			e.Process(u.Next())
		}
	}
	log.Close()
	return dir
}

// La scrittura libera si rigioca come un blocco "L". Qui il terzo blocco di
// una registrazione diventa scrittura libera, con le selezioni che portano
// l'id del blocco 2 come faceva il motore: devono finire nel blocco libero e
// non contaminare il blocco 2.
func TestReplayFreeWriting(t *testing.T) {
	dir := recordText(t)
	orig, err := replay.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(orig.Blocks) != 3 {
		t.Fatalf("blocchi registrati: %d", len(orig.Blocks))
	}
	n2, n3 := len(orig.Blocks[1].Selections), len(orig.Blocks[2].Selections)

	sp := filepath.Join(dir, "samples.csv")
	b, err := os.ReadFile(sp)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(b), "\n")
	for i, l := range lines[1:] {
		f := strings.Split(l, ",")
		if len(f) > 4 && f[3] == "block" && f[4] == "3" {
			f[3] = "free"
			lines[i+1] = strings.Join(f, ",")
		}
	}
	if err := os.WriteFile(sp, []byte(strings.Join(lines, "\n")), 0o644); err != nil {
		t.Fatal(err)
	}
	ep := filepath.Join(dir, "events.jsonl")
	if b, err = os.ReadFile(ep); err != nil {
		t.Fatal(err)
	}
	var kept []string
	for _, l := range strings.Split(string(b), "\n") {
		if strings.Contains(l, `"block":3,`) {
			if strings.Contains(l, `"block_start"`) {
				continue
			}
			l = strings.Replace(l, `"block":3,`, `"block":2,`, 1)
		}
		kept = append(kept, l)
	}
	if err := os.WriteFile(ep, []byte(strings.Join(kept, "\n")), 0o644); err != nil {
		t.Fatal(err)
	}

	s, err := replay.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Blocks) != 3 || s.Blocks[2].Cond != replay.CondFree {
		t.Fatalf("blocchi dopo la conversione: %d, ultimo %q", len(s.Blocks), s.Blocks[len(s.Blocks)-1].Cond)
	}
	if got := len(s.Blocks[1].Selections); got != n2 {
		t.Errorf("blocco 2: %d selezioni, erano %d (contaminato dalla scrittura libera)", got, n2)
	}
	free := s.Blocks[2]
	if len(free.Selections) != n3 {
		t.Errorf("blocco libero: %d selezioni, il terzo blocco ne aveva %d", len(free.Selections), n3)
	}
	letters, deleted := 0, 0
	for _, sel := range free.Selections {
		if sel.Kind == "lettera" || sel.Kind == "spazio" {
			letters++
			if !sel.Correct {
				deleted++
			}
		}
	}
	if len(free.Targets) != letters-deleted {
		t.Errorf("bersagli %d, lettere tenute %d", len(free.Targets), letters-deleted)
	}
	res, err := s.Run(s.Recorded())
	if err != nil {
		t.Fatal(err)
	}
	l := res[replay.CondFree]
	if l == nil || l.Correct == 0 || l.Same < letters*3/4 {
		t.Fatalf("replay della scrittura libera: %+v (lettere %d)", l, letters)
	}
	t.Logf("scrittura libera: %d lettere (%d cancellate), riprodotte identiche %d", letters, deleted, l.Same)
}
