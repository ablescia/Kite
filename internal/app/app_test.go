// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Antonio Blescia

package app

import (
	"context"
	"encoding/json"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ablescia/kite/internal/correct"
	"github.com/ablescia/kite/internal/driver"
	"github.com/ablescia/kite/internal/engine"
	"github.com/ablescia/kite/internal/gazein"
	"github.com/ablescia/kite/internal/lm"
	"github.com/ablescia/kite/internal/metrics"
	"github.com/ablescia/kite/internal/oculo"
	"github.com/ablescia/kite/internal/selector"
	"github.com/ablescia/kite/internal/sim"
	"github.com/ablescia/kite/internal/x11ui"
)

// testApp prepara l'applicazione su un display headless, con l'utente
// simulato. step(n) intercala in modo deterministico campioni (90 Hz) e
// frame (60 Hz), senza attese reali.
func testApp(t *testing.T) (*App, func(n int)) {
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
	t.Cleanup(func() { log.Close() })
	model := lm.New()
	eng := engine.New(engine.Config{Geom: geom, KB: kb, LM: model, Oculo: oculo.DefaultConfig(),
		Hybrid: selector.DefaultHybrid(), SigmaDeg: 1.6, CoreFrac: 0.6, PriorEps: 0.1,
		LumAlpha: 0.3, FovealDeg: 5, Log: log, Corrector: correct.New(correct.DefaultParams(), model),
		Task: metrics.TaskText})
	user := sim.New(sim.DefaultParams(), geom, eng, 1)

	a, err := New(x11ui.NewHeadless(1920, 1080))
	if err != nil {
		t.Fatal(err)
	}
	a.Eng, a.Geom, a.KB, a.LM, a.Log, a.Source = eng, geom, kb, model, log, "simulazione"
	a.Task = metrics.TaskText
	a.Opt = Options{Words: 2, UsePrediction: true, UsePupil: true, UseCorrector: true, Debug: true, ShowGaze: true}
	a.ctx = context.Background()
	a.rng = rand.New(rand.NewPCG(1, 2))

	now := time.Now()
	step := func(n int) {
		for i := 0; i < n; i++ {
			// 60 Hz di interfaccia, 90 Hz di campioni: 3 campioni ogni 2 frame.
			for k := 0; k < 3; k++ {
				eng.Process(user.Next())
			}
			for k := 0; k < 2; k++ {
				now = now.Add(time.Second / 60)
				if err := a.frame(now); err != nil {
					t.Fatal(err)
				}
			}
			select {
			case ev := <-eng.Events():
				if l, ok := a.screen.(engineListener); ok {
					l.EngineEvent(a, ev)
				}
			case fn := <-a.async:
				fn()
			default:
			}
		}
	}
	return a, step
}

// Ogni schermata si disegna senza errori, e una frase dell'esercizio
// pilotata dall'utente simulato arriva fino al risultato.
func TestScreensRenderAndBlockCompletes(t *testing.T) {
	a, step := testApp(t)
	a.Inspect = func(string) driver.State { return driver.State{} }
	for _, s := range []Screen{newHome(), newFree(), newLab(), newResults(), newCalib(), newGazeCal()} {
		a.SetScreen(s)
		step(20)
	}

	b := newBlock()
	a.SetScreen(b)
	step(2)
	b.Event(a, x11ui.Event{Kind: x11ui.KeyDown, Sym: x11ui.KeySpace})
	for i := 0; i < 2000 && b.phase != bDone; i++ {
		step(1)
	}
	if b.phase != bDone || len(a.Results) != 1 {
		t.Fatalf("blocco non concluso: fase %d, risultati %d", b.phase, len(a.Results))
	}
	r := a.Results[0]
	t.Logf("«%s» → «%s»: %.1f car/min, testo %.0f%%", r.Phrase, r.Text, r.CharsPerMin, 100*r.Accuracy)
	if r.Task != metrics.TaskText || r.Text == "" {
		t.Errorf("risultato del compito testo: %+v", r)
	}
	a.SetScreen(newResults())
	step(2)
}

// suspDisplay è un display headless che registra Suspend e Resume.
type suspDisplay struct {
	*x11ui.Headless
	calls []string
}

func (d *suspDisplay) Suspend()      { d.calls = append(d.calls, "suspend") }
func (d *suspDisplay) Resume() error { d.calls = append(d.calls, "resume"); return nil }

// La calibrazione dello sguardo dal menu: con il tracker tenuto da un
// processo avviato a mano non parte; quando è libero, Kite lascia lo schermo
// al driver, lo riprende e mostra la verifica. Se il driver ha misurato uno
// schermo diverso, Invio chiede il riavvio.
func TestGazeCalibrationFlow(t *testing.T) {
	a, step := testApp(t)
	disp := &suspDisplay{Headless: x11ui.NewHeadless(1920, 1080)}
	a.Disp = disp
	a.Source, a.Monitor, a.GeomDriver = "UDP 127.0.0.1:5005", "HDMI-A-1", true
	busy := driver.Holder{PID: 4242, Comm: "tobii4c", Args: []string{"/home/u/.local/bin/tobii4c", "run", "-headpose", "off"}}
	st := driver.State{Exe: "/usr/bin/tobii4c", Connected: true, Manual: []driver.Holder{busy},
		Display: driver.Display{WidthMM: 700, HeightMM: 390}}
	a.Inspect = func(string) driver.State { return st }
	var got driver.Options
	a.GazeCal = func(_ context.Context, o driver.Options) driver.Result {
		got = o
		o.Before()
		o.After()
		return driver.Result{Finished: true, Stopped: []string{"tobii4c.service"}, Restarted: []string{"tobii4c.service"},
			Points: []driver.Point{{ErrorDeg: 0.6, PrecisionDeg: 0.2}, {ErrorDeg: 0.8, PrecisionDeg: 0.2}},
			Before: st.Display, After: driver.Display{WidthMM: 698, HeightMM: 393}}
	}
	h := newHome()
	a.SetScreen(h)
	step(2)
	h.Event(a, x11ui.Event{Kind: x11ui.KeyDown, Sym: '1'})
	g, ok := a.screen.(*gazeCal)
	if !ok {
		t.Fatalf("il tasto 1 apre %T", a.screen)
	}
	for i := 0; i < 20 && !g.probed; i++ {
		step(1)
	}
	if !g.probed || g.ready(a) {
		t.Fatalf("con il tracker tenuto a mano la calibrazione non deve partire (probed %v)", g.probed)
	}
	if lines := g.statusLines(a); !strings.Contains(lines[0].text, "tobii4c run -headpose off") || !strings.Contains(lines[0].text, "4242") {
		t.Errorf("stato: %q", lines[0].text)
	}
	g.Event(a, x11ui.Event{Kind: x11ui.KeyDown, Sym: x11ui.KeySpace})
	if g.phase != gcIntro {
		t.Fatal("partita con il tracker occupato")
	}
	// il driver a mano viene chiuso: Kite se ne accorge al controllo successivo
	st.Manual, st.Units = nil, []string{"tobii4c.service"}
	g.nextProbe = time.Time{}
	for i := 0; i < 20 && !g.ready(a); i++ {
		step(1)
	}
	if !g.ready(a) {
		t.Fatal("tracker libero, ma la calibrazione non è pronta")
	}
	g.Event(a, x11ui.Event{Kind: x11ui.KeyDown, Sym: x11ui.KeySpace})
	for i := 0; i < 20 && g.phase != gcDone; i++ {
		step(1)
	}
	if g.phase != gcDone || got.Monitor != "HDMI-A-1" || filepath.Dir(got.LogPath) != a.Log.Dir {
		t.Fatalf("fase %d, opzioni %+v", g.phase, got)
	}
	if strings.Join(disp.calls, ",") != "suspend,resume" {
		t.Errorf("finestra: %v", disp.calls)
	}
	if e, _, _ := g.res.Mean(); e != 0.7 || !g.needRestart(a) {
		t.Errorf("accuratezza %.2f, riavvio %v", e, g.needRestart(a))
	}
	step(5)
	g.Event(a, x11ui.Event{Kind: x11ui.KeyDown, Sym: x11ui.KeyReturn})
	if !a.Restart || !a.done {
		t.Errorf("geometria nuova: Restart %v, uscita %v", a.Restart, a.done)
	}
	// con -display-mm la geometria non cambia: niente riavvio
	a.GeomDriver = false
	if g.needRestart(a) {
		t.Error("riavvio chiesto anche con -display-mm")
	}
}

// Il tasto V del menu passa da un preset di velocità al successivo, lo
// ricorda per le sessioni successive e lo applica alla scrittura seguente.
func TestSpeedPresetMenu(t *testing.T) {
	a, _ := testApp(t)
	a.Inspect = func(string) driver.State { return driver.State{} }
	a.SettingsPath = filepath.Join(t.TempDir(), "kite", "impostazioni.json")
	n, _ := selector.SpeedByName(selector.DefaultSpeed)
	a.Opt.Speed, a.Opt.TMinMs, a.Opt.TMaxMs = n.Name, n.TMinMs, n.TMaxMs
	a.SetScreen(newHome())
	press := func() { a.screen.Event(a, x11ui.Event{Kind: x11ui.KeyDown, Sym: 'v'}) }
	for range selector.Speeds {
		i := 0
		for j, s := range selector.Speeds {
			if s.Name == a.Opt.Speed {
				i = j
			}
		}
		want := selector.Speeds[(i+1)%len(selector.Speeds)]
		press()
		if a.Opt.Speed != want.Name || a.Opt.TMinMs != want.TMinMs || a.Opt.TMaxMs != want.TMaxMs {
			t.Fatalf("dopo V: %q %v–%v ms, atteso %+v", a.Opt.Speed, a.Opt.TMinMs, a.Opt.TMaxMs, want)
		}
		if got := LoadSettings(a.SettingsPath).Speed; got != want.Name {
			t.Errorf("impostazioni salvate: %q, atteso %q", got, want.Name)
		}
		if !strings.Contains(a.speedLabel(), want.Name) {
			t.Errorf("etichetta del menu: %q", a.speedLabel())
		}
	}
	// i tempi scelti valgono dalla scrittura libera successiva
	press()
	a.Eng.StartFree()
	a.Log.Flush()
	b, err := os.ReadFile(filepath.Join(a.Log.Dir, "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var got selector.HybridParams
	for _, l := range strings.Split(string(b), "\n") {
		var ev struct {
			Event  string                `json:"event"`
			Hybrid selector.HybridParams `json:"hybrid"`
		}
		if json.Unmarshal([]byte(l), &ev) == nil && ev.Event == "free_start" {
			got = ev.Hybrid
		}
	}
	if got.TMinMs != a.Opt.TMinMs || got.TMaxMs != a.Opt.TMaxMs {
		t.Errorf("scrittura libera con %v–%v ms, scelti %v–%v ms", got.TMinMs, got.TMaxMs, a.Opt.TMinMs, a.Opt.TMaxMs)
	}
	// tempi personalizzati da riga di comando: V porta al primo preset
	a.Opt.Speed = ""
	if !strings.Contains(a.speedLabel(), "personalizzata") {
		t.Errorf("etichetta con tempi personalizzati: %q", a.speedLabel())
	}
	press()
	if a.Opt.Speed != selector.Speeds[0].Name {
		t.Errorf("da personalizzata, V porta a %q", a.Opt.Speed)
	}
}
