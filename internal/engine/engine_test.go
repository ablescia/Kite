// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Antonio Blescia

package engine_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/ablescia/kite/internal/correct"
	"github.com/ablescia/kite/internal/engine"
	"github.com/ablescia/kite/internal/gazein"
	"github.com/ablescia/kite/internal/lm"
	"github.com/ablescia/kite/internal/metrics"
	"github.com/ablescia/kite/internal/oculo"
	"github.com/ablescia/kite/internal/selector"
	"github.com/ablescia/kite/internal/sim"
)

var geom = gazein.Geometry{WidthMM: 700, HeightMM: 390, WidthPx: 1920, HeightPx: 1080}

func newEngine(t *testing.T) *engine.Engine {
	kb, err := selector.NewKeyboard("qwerty", [4]float64{0.02, 0.25, 0.98, 0.97}, 0.08)
	if err != nil {
		t.Fatal(err)
	}
	e := engine.New(engine.Config{Geom: geom, KB: kb, LM: lm.New(), Oculo: oculo.DefaultConfig(),
		Hybrid: selector.DefaultHybrid(), SigmaDeg: 1.6, CoreFrac: 0.6, PriorEps: 0.1,
		LumAlpha: 0.3, FovealDeg: 5})
	e.SetLuminance(engine.LumMap{NX: 2, NY: 1, V: []float32{0.12, 0.12}, Mean: 0.12})
	return e
}

// runBlock esegue un blocco con l'utente simulato, senza attese reali.
func runBlock(t *testing.T, e *engine.Engine, u *sim.User, phrase string) metrics.Result {
	e.StartBlock(phrase)
	for i := 0; i < 90*240; i++ {
		e.Process(u.Next())
		select {
		case ev := <-e.Events():
			return ev.Result
		default:
		}
	}
	t.Fatalf("frase «%s» non conclusa in 4 minuti simulati", phrase)
	return metrics.Result{}
}

// maxFixedDwellCPM è la velocità massima di qualunque tastiera a dwell fisso
// di 1 s: un carattere al secondo anche con saccadi istantanee.
const maxFixedDwellCPM = 60

// L'esercizio nel compito "rigida": quattro frasi con l'utente simulato.
func TestSimulatedExercise(t *testing.T) {
	e := newEngine(t)
	u := sim.New(sim.DefaultParams(), geom, e, 42)
	for i := 0; i < 90*3; i++ { // assestamento
		e.Process(u.Next())
	}
	var rs []metrics.Result
	for _, phrase := range []string{"casa bella sole", "acqua fresca mare", "treno lungo notte", "libro nuovo festa"} {
		r := runBlock(t, e, u, phrase)
		t.Logf("%-38s %5.1f car/min  acc %3.0f%%  ITR %5.1f bit/min  dwell %4.0f ms  latenza decisione %.0f µs",
			r.Label, r.CharsPerMin, 100*r.Accuracy, r.ITR, r.MeanDwellMs, r.MeanLatencyUs)
		if r.Condition != engine.CondHybrid {
			t.Errorf("condizione registrata %q", r.Condition)
		}
		rs = append(rs, r)
	}
	g := metrics.Combine(engine.CondHybrid, rs, 27)
	t.Logf("simulato: %.1f car/min (%.0f%%), ITR %.1f bit/min, dwell medio %.0f ms", g.CharsPerMin, 100*g.Accuracy, g.ITR, g.MeanDwellMs)
	if g.Blocks != 4 || g.Accuracy < 0.85 {
		t.Errorf("accuratezza %.0f%% su %d frasi", 100*g.Accuracy, g.Blocks)
	}
	if !(g.CharsPerMin > maxFixedDwellCPM) {
		t.Errorf("il selettore ibrido deve superare il limite di un dwell fisso di 1 s (%.1f car/min)", g.CharsPerMin)
	}
	if g.MeanDwellMs > 600 {
		t.Errorf("dwell medio %.0f ms", g.MeanDwellMs)
	}
}

// Il compito "testo": gli errori entrano nel testo, l'utente simulato li
// cancella, il correttore chiude le parole. Il blocco finisce con l'ultimo
// spazio e il testo finale coincide con quello da copiare.
func TestSimulatedTextTask(t *testing.T) {
	kb, err := selector.NewKeyboard("qwerty", selector.DefaultArea, selector.DefaultGap)
	if err != nil {
		t.Fatal(err)
	}
	kb.AddEditBar(selector.DefaultBar, selector.DefaultGap, true)
	model := lm.New()
	e := engine.New(engine.Config{Geom: geom, KB: kb, LM: model, Oculo: oculo.DefaultConfig(),
		Hybrid: selector.DefaultHybrid(), SigmaDeg: 1.6, CoreFrac: 0.6, PriorEps: 0.1,
		LumAlpha: 0.3, FovealDeg: 5, Corrector: correct.New(correct.DefaultParams(), model), Task: metrics.TaskText})
	e.SetLuminance(engine.LumMap{NX: 2, NY: 1, V: []float32{0.12, 0.12}, Mean: 0.12})
	u := sim.New(sim.DefaultParams(), geom, e, 42)
	for i := 0; i < 90*3; i++ {
		e.Process(u.Next())
	}
	var rs []metrics.Result
	for _, phrase := range []string{"casa bella sole", "acqua fresca mare", "treno lungo notte", "libro nuovo festa"} {
		r := runBlock(t, e, u, phrase)
		t.Logf("%-50s %5.1f car/min  testo %3.0f%% «%s»  errori commessi %d, canc %d, correzioni %d, KSPC %.2f",
			r.Label, r.CharsPerMin, 100*r.Accuracy, r.Text, r.ErrorsMade, r.Backspaces, r.AutoFixes+r.Suggestions, r.KSPC)
		if r.Task != metrics.TaskText || r.Text == "" {
			t.Fatalf("blocco senza testo: %+v", r)
		}
		if r.CER > 0.15 {
			t.Errorf("testo finale troppo lontano da quello da copiare: «%s» (CER %.2f)", r.Text, r.CER)
		}
		rs = append(rs, r)
	}
	g := metrics.Combine(engine.CondHybrid, rs, kb.Chars)
	t.Logf("simulato, compito testo: %.1f car/min (CER %.2f), KSPC %.2f", g.CharsPerMin, g.CER, g.KSPC)
	if !(g.CharsPerMin > maxFixedDwellCPM) {
		t.Errorf("il selettore ibrido deve superare il limite di un dwell fisso di 1 s (%.1f car/min)", g.CharsPerMin)
	}
	if !strings.Contains(g.Label, "correttore") {
		t.Errorf("etichetta senza correttore: %q", g.Label)
	}
}

// gazeSpeaker è una voce finta per i test con lo sguardo.
type gazeSpeaker struct {
	said   chan string
	finish chan error
}

func (g *gazeSpeaker) Say(ctx context.Context, text string) error {
	g.said <- text
	select {
	case err := <-g.finish:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// gazer muove uno sguardo sintetico, senza rumore: una saccade di 33 ms
// verso il punto, poi la fissazione per il tempo indicato.
type gazer struct {
	e   *engine.Engine
	seq uint64
	at  [2]float64
}

func (g *gazer) look(p [2]float64, hold time.Duration) {
	from := g.at
	n := 3 + int(hold/(11111*time.Microsecond))
	for i := 1; i <= n; i++ {
		q := p
		if i <= 3 {
			f := float64(i) / 3
			q = [2]float64{from[0] + (p[0]-from[0])*f, from[1] + (p[1]-from[1])*f}
		}
		g.seq++
		g.e.Process(gazein.Sample{Seq: g.seq, HostNs: time.Now().UnixNano(), DeviceUs: int64(g.seq) * 11111,
			Gaze: q, GazeValid: true, Raw: q, RawValid: true, PupilMM: [2]float64{3.5, 3.5}, PupilValid: [2]bool{true, true},
			EyeMM: [2][3]float64{{-31, 0, 620}, {31, 0, 620}}, EyeValid: [2]bool{true, true}, Presence: 2})
	}
	g.at = p
}

// La scrittura libera con lo sguardo, fino alla voce: si scrive "ciao", si
// guarda parla e la voce dice il testo. Mentre parla, guardare le lettere non
// scrive e restare su parla non la ferma; uscire e tornare su stop la ferma,
// e il testo resta. Detto fino in fondo, il testo si cancella.
func TestFreeWritingVoiceByGaze(t *testing.T) {
	kb, err := selector.NewKeyboard("qwerty", selector.DefaultArea, selector.DefaultGap)
	if err != nil {
		t.Fatal(err)
	}
	kb.AddEditBar(selector.DefaultBar, selector.DefaultGap, true)
	model := lm.New()
	sp := &gazeSpeaker{said: make(chan string, 4), finish: make(chan error, 1)}
	e := engine.New(engine.Config{Geom: geom, KB: kb, LM: model, Oculo: oculo.DefaultConfig(),
		Hybrid: selector.DefaultHybrid(), SigmaDeg: 1.6, CoreFrac: 0.6, PriorEps: 0.1,
		LumAlpha: 0.3, FovealDeg: 5, Corrector: correct.New(correct.DefaultParams(), model), Speaker: sp})
	e.StartFree()
	key := func(c rune) [2]float64 { return kb.Keys[kb.IndexOf(c)].Center }
	strip := [2]float64{0.5, 0.12} // sul testo scritto: nessun tasto
	g := &gazer{e: e, at: strip}
	g.look(strip, time.Second)
	for _, c := range "ciao" {
		g.look(key(c), 750*time.Millisecond)
	}
	if s := e.Snapshot(); s.Typed != "ciao" {
		t.Fatalf("scritto con lo sguardo: %q", s.Typed)
	}
	said := func() string { // la voce parte in una sua goroutine
		select {
		case s := <-sp.said:
			return s
		case <-time.After(time.Second):
			return ""
		}
	}

	// parla chiede SpeakMs (1 s): lo sguardo resta 1,3 s
	g.look(key(selector.KeySpeak), 1300*time.Millisecond)
	if got := said(); got != "ciao" {
		t.Fatalf("parla con lo sguardo: la voce ha ricevuto %q", got)
	}
	g.look(key(selector.KeySpeak), 2500*time.Millisecond) // lo sguardo resta sul tasto
	g.look(key('a'), 750*time.Millisecond)                // e poi va su una lettera
	if s := e.Snapshot(); !s.Speaking || s.Typed != "ciao " {
		t.Fatalf("mentre parla: voce %v, testo %q", s.Speaking, s.Typed)
	}
	g.look(key(selector.KeySpeak), 1300*time.Millisecond) // stop
	if s := e.Snapshot(); s.Speaking || s.Typed != "ciao " {
		t.Fatalf("dopo stop: voce %v, testo %q", s.Speaking, s.Typed)
	}

	g.look(strip, 500*time.Millisecond)
	g.look(key(selector.KeySpeak), 1300*time.Millisecond)
	if got := said(); got != "ciao" {
		t.Fatalf("di nuovo parla: %q", got)
	}
	sp.finish <- nil
	for i := 0; i < 200 && e.Snapshot().Speaking; i++ {
		time.Sleep(5 * time.Millisecond)
	}
	if s := e.Snapshot(); s.Speaking || s.Typed != "" || s.Said != "ciao" {
		t.Errorf("a voce finita: voce %v, testo %q, detto %q", s.Speaking, s.Typed, s.Said)
	}
}

// A voce finita la tastiera si riarma: lo sguardo fermo su una lettera
// mentre la voce parlava non la scrive appena la voce tace, ma solo dopo la
// prudenza dell'armamento (prima scattava subito: la fissazione era già
// lunga).
func TestVoiceEndRearms(t *testing.T) {
	kb, err := selector.NewKeyboard("qwerty", selector.DefaultArea, selector.DefaultGap)
	if err != nil {
		t.Fatal(err)
	}
	kb.AddEditBar(selector.DefaultBar, selector.DefaultGap, true)
	model := lm.New()
	sp := &gazeSpeaker{said: make(chan string, 4), finish: make(chan error, 1)}
	hp := selector.DefaultHybrid()
	e := engine.New(engine.Config{Geom: geom, KB: kb, LM: model, Oculo: oculo.DefaultConfig(),
		Hybrid: hp, SigmaDeg: 1.6, CoreFrac: 0.6, PriorEps: 0.1,
		LumAlpha: 0.3, FovealDeg: 5, Corrector: correct.New(correct.DefaultParams(), model), Speaker: sp})
	e.StartFree()
	key := func(c rune) [2]float64 { return kb.Keys[kb.IndexOf(c)].Center }
	strip := [2]float64{0.5, 0.12}
	g := &gazer{e: e, at: strip}
	g.look(strip, time.Second)
	for _, c := range "ciao" {
		g.look(key(c), 750*time.Millisecond)
	}
	g.look(key(selector.KeySpeak), 1300*time.Millisecond)
	select {
	case <-sp.said:
	case <-time.After(time.Second):
		t.Fatal("la voce non è partita")
	}
	g.look(key('e'), 1500*time.Millisecond) // mentre la voce parla, lo sguardo resta sulla E
	sp.finish <- nil
	for i := 0; i < 200 && e.Snapshot().Speaking; i++ {
		time.Sleep(5 * time.Millisecond)
	}
	if s := e.Snapshot(); s.Speaking || s.Typed != "" {
		t.Fatalf("a voce finita: voce %v, testo %q", s.Speaking, s.Typed)
	}
	g.look(key('e'), 300*time.Millisecond) // stessa fissazione: nessuna saccade
	if s := e.Snapshot(); s.Typed != "" {
		t.Errorf("300 ms dopo la fine della voce la E non deve essere scritta: %q", s.Typed)
	}
	g.look(key('e'), 700*time.Millisecond)
	if s := e.Snapshot(); s.Typed != "e" {
		t.Errorf("dopo la prudenza dell'armamento la E si scrive: %q", s.Typed)
	}
}
