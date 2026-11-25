// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Antonio Blescia

// Kite: tastiera a sguardo ibrida per la comunicazione aumentativa e
// alternativa, con il Tobii Eye Tracker 4C su Linux. Il tasto si attiva
// quando lo sguardo si ferma dopo la saccade, in un tempo che dipende dalla
// probabilità della lettera; le aree di tocco dei tasti probabili si
// allargano in modo invisibile; la pupilla, compensata per la luminanza,
// stima il carico cognitivo. Con correttore italiano, voce e frasi imparate.
//
// Legge i campioni del driver tobii4c (github.com/ablescia/tobii4c-linux,
// UDP T4CG v2, 127.0.0.1:5005):
//
//	tobii4c run -headpose off          # terminale 1 (o il servizio tobii4c)
//	kite                               # terminale 2
//
// Senza tracker: kite -simulate (utente simulato). Senza grafica:
// kite -simulate -headless -auto -shots DIR (esercizio automatico).
// Prova della voce: kite -say "ciao, come stai?".
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/ablescia/kite/internal/app"
	"github.com/ablescia/kite/internal/correct"
	"github.com/ablescia/kite/internal/driver"
	"github.com/ablescia/kite/internal/engine"
	"github.com/ablescia/kite/internal/gazein"
	"github.com/ablescia/kite/internal/lm"
	"github.com/ablescia/kite/internal/metrics"
	"github.com/ablescia/kite/internal/ngram"
	"github.com/ablescia/kite/internal/oculo"
	"github.com/ablescia/kite/internal/pupil"
	"github.com/ablescia/kite/internal/selector"
	"github.com/ablescia/kite/internal/sim"
	"github.com/ablescia/kite/internal/speech"
	"github.com/ablescia/kite/internal/x11ui"
)

func main() {
	err := run()
	if errors.Is(err, errRestart) {
		// La calibrazione dello sguardo ha cambiato la geometria dello
		// schermo: Kite riparte con gli stessi argomenti e una sessione nuova.
		exe, xerr := os.Executable()
		if xerr == nil {
			fmt.Fprintln(os.Stderr, "kite: nuova geometria dello schermo, riavvio")
			xerr = syscall.Exec(exe, os.Args, os.Environ())
		}
		err = fmt.Errorf("riavvio non riuscito (%v): riavvia Kite a mano", xerr)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "errore:", err)
		os.Exit(1)
	}
}

// errRestart chiede a main di riavviare Kite.
var errRestart = errors.New("riavvio")

// appName è il nome delle cartelle di Kite in ~/.config e ~/.local/share.
const appName = "kite"

func run() error {
	udp := flag.String("udp", "127.0.0.1:5005", "indirizzo su cui ricevere i campioni del driver (T4CG v2)")
	simulate := flag.Bool("simulate", false, "utente simulato al posto del tracker")
	headless := flag.Bool("headless", false, "nessuna finestra (con -simulate e -auto per i test)")
	auto := flag.Bool("auto", false, "esegue l'esercizio da solo e poi esce (con -simulate)")
	exercise := flag.Bool("exercise", false, "parte subito con l'esercizio di copiatura")
	shots := flag.String("shots", "", "cartella in cui salvare uno screenshot a ogni cambio di schermata")
	monitor := flag.String("monitor", "", "monitor RandR (default: quello di ~/.config/tobii4c/display.json, poi il primario)")
	dispMM := flag.String("display-mm", "", "dimensioni della display area LxA in mm (default: ~/.config/tobii4c/display.json)")
	outDir := flag.String("out", "", "cartella della sessione (default ~/.local/share/kite/sessions/<data>)")
	words := flag.Int("words", 5, "parole per frase dell'esercizio")
	blocks := flag.Int("blocks", 3, "frasi da copiare nell'esercizio")
	layout := flag.String("layout", "qwerty", "disposizione dei tasti: qwerty (righe sfalsate e barra spaziatrice), abc (griglia alfabetica)")
	dh := selector.DefaultHybrid()
	speedNames := make([]string, len(selector.Speeds))
	for i, s := range selector.Speeds {
		speedNames[i] = fmt.Sprintf("%s (%.0f–%.0f ms)", s.Name, s.TMinMs, s.TMaxMs)
	}
	speed := flag.String("speed", "", "tempi delle lettere: "+strings.Join(speedNames, ", ")+
		"; default quello scelto nel menu (tasto V), poi "+selector.DefaultSpeed)
	tmin := flag.Duration("tmin", dur(dh.TMinMs), "fissazione minima dopo la saccade (lettera molto probabile); se indicato sostituisce il preset")
	tmax := flag.Duration("tmax", dur(dh.TMaxMs), "fissazione massima (lettera improbabile o sguardo ambiguo); se indicato sostituisce il preset")
	editDwell := flag.Duration("edit", dur(dh.EditMs), "fissazione per cancella e i suggerimenti")
	speakDwell := flag.Duration("speak", dur(dh.SpeakMs), "fissazione per parla (e stop)")
	editMargin := flag.Float64("edit-margin", 0, "tolleranza in gradi dello sguardo appena fuori dai tasti di modifica (0 = solo dentro; da provare: 0.4)")
	repeat := flag.Duration("repeat", dur(dh.RepeatMs), "ripetere la lettera tenendo il tasto, doppia improbabile")
	repeatMin := flag.Duration("repeat-min", dur(dh.RepeatMinMs), "ripetere la lettera tenendo il tasto, doppia probabile (0 = sempre -repeat)")
	land := flag.Duration("land", dur(dh.LandMs), "attesa minima dopo un atterraggio forse corto, per la saccade correttiva (0 = spento)")
	caution := flag.Duration("caution", dur(dh.CautionMs), "tempo minimo della selezione dopo un errore (0 = spento)")
	sigma := flag.Float64("sigma", selector.DefaultSigmaDeg, "dispersione dello sguardo attorno al centro del tasto (gradi)")
	core := flag.Float64("core", selector.DefaultCoreFrac, "frazione centrale del tasto che non può essere ceduta ai vicini")
	eps := flag.Float64("eps", engine.DefaultPriorEps, "quota uniforme mescolata al modello di lingua (ogni tasto resta raggiungibile)")
	pupilGain := flag.Float64("pupil-gain", 0.25, "allungamento dei tempi per unità di carico pupillare (max 2)")
	noPred := flag.Bool("no-prediction", false, "senza predizione: aree di tocco e tempi senza modello di lingua (ablazione)")
	noPupil := flag.Bool("no-pupil", false, "senza modulazione pupillare dei tempi (ablazione)")
	noCorrect := flag.Bool("no-correct", false, "senza correttore né suggerimenti")
	noPhrases := flag.Bool("no-phrases", false, "scrittura libera senza frasi proposte (e senza impararne)")
	phraseDwell := flag.Duration("phrase-dwell", dur(engine.DefaultPhraseDwellMs), "fissazione sulla frase grigia che la accetta")
	phraseMin := flag.Float64("phrase-min", ngram.DefaultParams().MinProb, "probabilità minima di una frase per proporla (0–1)")
	task := flag.String("task", metrics.TaskText, "compito dell'esercizio: testo (gli errori entrano nel testo e si correggono), rigida (la lettera sbagliata non entra)")
	lumAlpha := flag.Float64("lum-fovea", 0.3, "peso della zona guardata (raggio 5°) nella luminanza efficace")
	seed := flag.Uint64("seed", 0, "seme per i testi da copiare (0 = dall'orologio)")
	debug := flag.Bool("debug", false, "mostra subito la diagnostica")
	script := flag.String("script", "", "solo con -headless: tasti da premere, es. \"5@1,d@4,esc@8\" (tasto@secondi)")
	voiceName := flag.String("voice", "", "voce di Piper per il tasto parla: nome (it_IT-paola-medium) o file .onnx; default la più naturale installata (it_IT-serena-high, poi it_IT-paola-medium…); no = senza voce")
	voiceSpeed := flag.Float64("voice-speed", 1, "velocità della voce (1 = naturale, 0.8 = più lenta)")
	piperCmd := flag.String("piper", "", "eseguibile di Piper (default: piper-tts, ~/.local/bin/piper o piper nel PATH)")
	say := flag.String("say", "", "dice il testo con la voce ed esce (prova dell'audio)")
	driverExe := flag.String("tobii4c", "", "eseguibile del driver per la calibrazione dello sguardo (default: tobii4c nel PATH, poi ~/.local/bin/tobii4c)")
	flag.Parse()

	if *auto && !*simulate {
		return fmt.Errorf("-auto ha senso solo con -simulate: con una persona i tempi li decide lei")
	}
	if *seed == 0 {
		*seed = uint64(time.Now().UnixNano())
	}
	given := map[string]bool{}
	flag.Visit(func(f *flag.Flag) { given[f.Name] = true })
	if given["say"] && strings.TrimSpace(*say) == "" {
		return fmt.Errorf("-say: manca il testo da dire")
	}
	if *voiceSpeed <= 0 {
		return fmt.Errorf("-voice-speed: %g (deve essere > 0)", *voiceSpeed)
	}
	if *phraseMin <= 0 || *phraseMin >= 1 {
		return fmt.Errorf("-phrase-min: %g (deve essere tra 0 e 1)", *phraseMin)
	}
	if *phraseDwell < 300*time.Millisecond {
		return fmt.Errorf("-phrase-dwell: %s (almeno 300ms: si accetterebbe leggendo)", *phraseDwell)
	}
	if *blocks < 1 || *words < 1 {
		return fmt.Errorf("-blocks e -words: almeno 1")
	}
	if *editMargin < 0 || *editMargin > 1 {
		return fmt.Errorf("-edit-margin: %g (tra 0 e 1 grado)", *editMargin)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// Voce: sintesi locale con Piper. Il processo con la voce caricata parte
	// subito, così il primo «parla» non aspetta il caricamento. Senza
	// finestra (test, demo) non serve.
	var voice *speech.Piper
	voiceErr := ""
	switch {
	case *voiceName == "no":
		voiceErr = "spenta con -voice no"
	case *headless && *say == "":
		voiceErr = "senza finestra non serve (-headless)"
	default:
		var err error
		voice, err = speech.NewPiper(speech.Config{Cmd: *piperCmd, Voice: *voiceName, Speed: *voiceSpeed})
		if err != nil {
			voiceErr = err.Error()
			if *say == "" {
				fmt.Fprintln(os.Stderr, "voce:", err)
			}
		} else {
			defer voice.Close()
		}
	}
	if *say != "" {
		if voice == nil {
			return fmt.Errorf("voce: %s", voiceErr)
		}
		t0 := time.Now()
		err := voice.Say(ctx, *say)
		fmt.Fprintf(os.Stderr, "%s: detto in %.1f s (avvio della voce compreso)\n", voice.Name(), time.Since(t0).Seconds())
		return err
	}

	// Display: finestra X11 a schermo intero o headless.
	var disp x11ui.Display
	var monName string
	saved := driver.LoadDisplay()
	if *headless {
		disp = x11ui.NewHeadless(1920, 1080)
		monName = "headless"
	} else {
		conn, err := x11ui.Connect()
		if err != nil {
			return err
		}
		defer conn.Close()
		mons, err := x11ui.Monitors(conn)
		if err != nil {
			return err
		}
		want := *monitor
		if want == "" {
			want = saved.Monitor
		}
		m := mons[0]
		found := false
		for _, mm := range mons {
			if (want != "" && mm.Name == want) || (want == "" && mm.Primary) {
				m, found = mm, true
				break
			}
		}
		if !found && *monitor != "" {
			return fmt.Errorf("monitor %q non trovato", *monitor)
		}
		w, err := x11ui.Open(conn, m, 0x45494f)
		if err != nil {
			return err
		}
		defer w.Close()
		disp, monName = w, m.Name
		if saved.WidthMM == 0 && m.WidthMM > 0 {
			saved.WidthMM, saved.HeightMM = m.WidthMM, m.HeightMM
		}
	}
	wPx, hPx := disp.Size()

	geom := gazein.Geometry{WidthMM: saved.WidthMM, HeightMM: saved.HeightMM, WidthPx: wPx, HeightPx: hPx}
	if *dispMM != "" {
		var err error
		if geom.WidthMM, geom.HeightMM, err = parseSize(*dispMM); err != nil {
			return fmt.Errorf("-display-mm: %w", err)
		}
	}
	if geom.WidthMM <= 0 || geom.HeightMM <= 0 {
		geom.WidthMM, geom.HeightMM = 700, 390
	}

	if *task != metrics.TaskText && *task != metrics.TaskStrict {
		return fmt.Errorf("-task: %q (testo o rigida)", *task)
	}
	kb, err := selector.NewKeyboard(*layout, selector.DefaultArea, selector.DefaultGap)
	if err != nil {
		return err
	}
	kb.AddEditBar(selector.DefaultBar, selector.DefaultGap, true)
	if *outDir == "" {
		base, _ := os.UserHomeDir()
		*outDir = filepath.Join(base, ".local", "share", appName, "sessions", time.Now().Format("20060102-150405"))
	}
	log, err := engine.NewLogger(*outDir)
	if err != nil {
		return err
	}
	defer log.Close()

	cfgDir, _ := os.UserConfigDir()
	// Tempi delle lettere: il preset di -speed, o quello scelto nel menu e
	// salvato; -tmin e -tmax, se indicati, lo sostituiscono.
	settingsPath := filepath.Join(cfgDir, appName, "impostazioni.json")
	if *simulate {
		settingsPath = "" // le scelte di un utente simulato non vanno salvate
	}
	speedName := *speed
	if speedName == "" {
		speedName = app.LoadSettings(settingsPath).Speed
	}
	sp, ok := selector.SpeedByName(speedName)
	if !ok {
		if *speed != "" {
			return fmt.Errorf("-speed: %q (%s)", *speed, strings.Join(speedNames, ", "))
		}
		if speedName != "" {
			fmt.Fprintf(os.Stderr, "impostazioni: velocità %q sconosciuta, uso %s\n", speedName, selector.DefaultSpeed)
		}
		sp, _ = selector.SpeedByName(selector.DefaultSpeed)
	}
	hp := dh
	hp.TMinMs, hp.TMaxMs = sp.TMinMs, sp.TMaxMs
	if given["tmin"] || given["tmax"] {
		if given["tmin"] {
			hp.TMinMs = ms(*tmin)
		}
		if given["tmax"] {
			hp.TMaxMs = ms(*tmax)
		}
		sp.Name = ""
	}
	if hp.TMinMs <= 0 || hp.TMaxMs < hp.TMinMs {
		return fmt.Errorf("-tmin %s, -tmax %s: serve 0 < tmin ≤ tmax", dur(hp.TMinMs), dur(hp.TMaxMs))
	}
	hp.RepeatMs, hp.RepeatMinMs = ms(*repeat), ms(*repeatMin)
	hp.LandMs, hp.CautionMs = ms(*land), ms(*caution)
	hp.EditMs, hp.SpeakMs = ms(*editDwell), ms(*speakDwell)
	hp.PupilGain = *pupilGain
	model := lm.New()
	wordsPath := filepath.Join(cfgDir, appName, "parole.txt")
	phrasesPath := filepath.Join(cfgDir, appName, "frasi.txt")
	if !*simulate {
		createEmpty(wordsPath, phrasesPath)
	}
	userWords := 0
	if !*simulate {
		userWords = loadWords(model, wordsPath)
	}
	cp := correct.DefaultParams()
	corr := correct.New(cp, model)
	// Frasi imparate: ogni riga è un messaggio detto, le ripetizioni contano.
	var phrases *ngram.Model
	if !*noPhrases {
		pp := ngram.DefaultParams()
		pp.MinProb = *phraseMin
		phrases = ngram.New(pp)
		phrases.PrefixMass = model.PrefixMass
		if !*simulate {
			loadPhrases(phrases, phrasesPath)
		}
	}
	var speaker speech.Speaker // un *Piper nil non deve diventare un'interfaccia non nil
	voiceInfo := ""
	if voice != nil {
		speaker, voiceInfo = voice, voice.Name()
	}
	eng := engine.New(engine.Config{Geom: geom, KB: kb, LM: model, Oculo: oculo.DefaultConfig(), Hybrid: hp,
		SigmaDeg: *sigma, CoreFrac: *core, EditMarginDeg: *editMargin, PriorEps: *eps, LumAlpha: *lumAlpha, FovealDeg: 5,
		Log: log, Corrector: corr, Task: *task, Speaker: speaker, Phrases: phrases, PhraseDwellMs: ms(*phraseDwell)})

	modelPath := filepath.Join(cfgDir, appName, "pupil-model.json")
	if !*simulate {
		if m, ok := loadModel(modelPath); ok {
			eng.SetModel(m)
		}
	}

	samples := make(chan gazein.Sample, 512)
	var stats gazein.Stats
	source := "UDP " + *udp
	if *simulate {
		source = "simulazione"
		u := sim.New(sim.DefaultParams(), geom, eng, *seed)
		go u.Run(ctx, samples)
	} else {
		errc := make(chan error, 1)
		go func() { errc <- gazein.Listen(ctx, *udp, samples, &stats) }()
		select {
		case err := <-errc:
			if err != nil {
				return err
			}
		case <-time.After(100 * time.Millisecond):
		}
	}
	go eng.Run(ctx, samples)

	a, err := app.New(disp)
	if err != nil {
		return err
	}
	a.Eng, a.Geom, a.KB, a.LM, a.Log = eng, geom, kb, model, log
	a.ModelPath, a.Source, a.UDPStats, a.ShotDir = modelPath, source, &stats, *shots
	a.Task, a.WordsPath, a.PhrasesPath, a.SettingsPath = *task, wordsPath, phrasesPath, settingsPath
	a.VoiceInfo, a.VoiceErr = voiceInfo, voiceErr
	a.Driver, a.Monitor, a.GeomDriver = *driverExe, monName, *dispMM == ""
	if *headless {
		a.Monitor = ""
	}
	if *simulate {
		a.ModelPath, a.WordsPath, a.PhrasesPath = "", "", "" // modello, parole e frasi di un utente simulato non vanno salvati
	}
	a.Opt = app.Options{Words: *words, Blocks: *blocks, UsePrediction: !*noPred, UsePupil: !*noPupil, UseCorrector: !*noCorrect,
		UsePhrases: phrases != nil, Speed: sp.Name, TMinMs: hp.TMinMs, TMaxMs: hp.TMaxMs, Debug: *debug, Auto: *auto, Seed: *seed}
	if *shots != "" {
		if err := os.MkdirAll(*shots, 0o755); err != nil {
			return err
		}
	}
	log.WriteJSON("session.json", map[string]any{
		"program": appName, "started": time.Now(), "source": source, "monitor": monName, "geometry": geom, "layout": *layout,
		"keys": len(kb.Keys), "hybrid": hp, "speed": sp.Name, "sigma_deg": *sigma, "core": *core, "eps": *eps,
		"edit_margin_deg": *editMargin, "window_samples": oculo.DefaultConfig().WindowSamples,
		"lum_fovea": *lumAlpha, "words": *words, "blocks": *blocks, "seed": *seed, "lexicon_words": model.Words(), "lm": model.Name(),
		"task": *task, "area": kb.Area, "bar": kb.Bar, "gap": selector.DefaultGap, "edit_bar": kb.HasEditBar(),
		"corrector": cp, "user_words": userWords, "speak_key": true, "voice": voiceInfo, "voice_speed": *voiceSpeed,
		"phrases": phrasesInfo(phrases, ms(*phraseDwell)),
	})
	fmt.Fprintf(os.Stderr, "kite: %s, schermo %s %d×%d px = %.0f×%.0f mm, sessione in %s\n",
		source, monName, wPx, hPx, geom.WidthMM, geom.HeightMM, log.Dir)
	if voice != nil {
		fmt.Fprintln(os.Stderr, "voce:", voiceInfo)
	}

	if *script != "" {
		h, ok := disp.(*x11ui.Headless)
		if !ok {
			return fmt.Errorf("-script funziona solo con -headless")
		}
		if err := runScript(ctx, h, *script); err != nil {
			return err
		}
	}
	var start func()
	if *auto || *exercise {
		start = a.Exercise
	}
	err = a.Run(ctx, start)
	if len(a.Results) > 0 {
		printSummary(a)
	}
	if err == nil && a.Restart {
		return errRestart
	}
	return err
}

// createEmpty crea vuoti i file indicati che non esistono ancora (il
// dizionario personale e le frasi imparate): al primo avvio Kite parte senza
// parole né frasi. Un file che esiste già non viene toccato.
func createEmpty(paths ...string) {
	for _, p := range paths {
		err := os.MkdirAll(filepath.Dir(p), 0o755)
		if err == nil {
			var f *os.File
			if f, err = os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644); err == nil {
				err = f.Close()
			}
		}
		if err != nil && !errors.Is(err, os.ErrExist) {
			fmt.Fprintf(os.Stderr, "kite: %s: %v\n", p, err)
		}
	}
}

func ms(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) }

// loadWords carica il dizionario personale (una parola per riga) nel modello
// di lingua e restituisce quante parole ha letto.
func loadWords(m *lm.Model, path string) int {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	ws := strings.Fields(string(b))
	m.AddWords(ws, engine.UserWordCount)
	return len(ws)
}

// loadPhrases impara le frasi salvate (una per riga, le ripetizioni contano).
func loadPhrases(m *ngram.Model, path string) {
	b, err := os.ReadFile(path)
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(b), "\n") {
		m.Learn(line)
	}
}

// phrasesInfo descrive la predizione delle frasi per session.json.
func phrasesInfo(m *ngram.Model, dwellMs float64) any {
	if m == nil {
		return false
	}
	return map[string]any{"learned": m.Phrases(), "words": m.Words(), "params": m.P, "dwell_ms": dwellMs}
}

func dur(ms float64) time.Duration { return time.Duration(ms * float64(time.Millisecond)) }

func parseSize(s string) (float64, float64, error) {
	p := strings.Split(strings.ToLower(s), "x")
	if len(p) != 2 {
		return 0, 0, fmt.Errorf("formato LxA, es. 700x390")
	}
	w, err1 := strconv.ParseFloat(p[0], 64)
	h, err2 := strconv.ParseFloat(p[1], 64)
	if err1 != nil || err2 != nil || w <= 0 || h <= 0 {
		return 0, 0, fmt.Errorf("dimensioni non valide: %q", s)
	}
	return w, h, nil
}

// loadModel carica il modello pupilla–luminanza se è di meno di 12 ore: la
// luce della stanza cambia nel corso della giornata.
func loadModel(path string) (pupil.Model, bool) {
	var m pupil.Model
	b, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(b, &m) != nil || !m.Valid() {
		return m, false
	}
	if time.Since(m.FittedAt) > 12*time.Hour {
		fmt.Fprintf(os.Stderr, "modello pupilla–luminanza del %s ignorato: troppo vecchio, rifai la calibrazione (tasto 2)\n",
			m.FittedAt.Format("2/1 15:04"))
		return m, false
	}
	return m, true
}

func printSummary(a *app.App) {
	fmt.Println("\nfrasi completate:")
	for _, r := range a.Results {
		fmt.Printf("  #%d %-42s %6.1f car/min  acc %5.1f%%  ITR %6.1f bit/min  dwell %4.0f ms  «%s»\n",
			r.ID, r.Label, r.CharsPerMin, 100*r.Accuracy, r.ITR, r.MeanDwellMs, r.Phrase)
	}
	fmt.Println("riepilogo:", filepath.Join(a.Log.Dir, "summary.json"))
}

// runScript preme i tasti indicati agli istanti indicati (test headless).
func runScript(ctx context.Context, h *x11ui.Headless, script string) error {
	names := map[string]uint32{"esc": x11ui.KeyEscape, "space": x11ui.KeySpace, "enter": x11ui.KeyReturn,
		"bs": x11ui.KeyBackSpace, "tab": x11ui.KeyTab, "plus": '+', "minus": '-'}
	type step struct {
		at  time.Duration
		sym uint32
	}
	var steps []step
	for _, item := range strings.Split(script, ",") {
		k, t, ok := strings.Cut(strings.TrimSpace(item), "@")
		sec, err := strconv.ParseFloat(t, 64)
		if !ok || err != nil {
			return fmt.Errorf("-script: %q non è tasto@secondi", item)
		}
		sym, found := names[k]
		if !found {
			if len(k) != 1 {
				return fmt.Errorf("-script: tasto %q sconosciuto", k)
			}
			sym = uint32(k[0])
		}
		steps = append(steps, step{time.Duration(sec * float64(time.Second)), sym})
	}
	start := time.Now()
	go func() {
		for _, s := range steps {
			select {
			case <-time.After(time.Until(start.Add(s.at))):
				h.Send(x11ui.Event{Kind: x11ui.KeyDown, Sym: s.sym})
			case <-ctx.Done():
				return
			}
		}
	}()
	return nil
}
