// SPDX-License-Identifier: AGPL-3.0-or-later

// kite-replay rigioca le sessioni registrate (di Kite e del banco di prova da
// cui Kite nasce) attraverso il selettore ibrido, con i parametri registrati
// e con quelli attuali (o con una serie di varianti, -suite), e confronta
// errori, lettere mancate e tempi.
//
//	kite-replay                    # tutte le sessioni registrate (scrittura libera e copiatura)
//	kite-replay -suite             # contributo di ogni correzione
//	kite-replay -v DIR             # una sessione, con l'elenco degli errori
//	kite-replay -correttore        # il correttore sulle parole copiate
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ablescia/kite/internal/correct"
	"github.com/ablescia/kite/internal/engine"
	"github.com/ablescia/kite/internal/lm"
	"github.com/ablescia/kite/internal/replay"
	"github.com/ablescia/kite/internal/selector"
)

// entry è una variante da provare; i parametri possono dipendere dalla sessione.
type entry struct {
	name string
	v    func(*replay.Session) replay.Variant
}

func main() {
	home, _ := os.UserHomeDir()
	defRoots := filepath.Join(home, ".local/share/tobii4c-hybridbci/sessions") + "," +
		filepath.Join(home, ".local/share/kite/sessions")
	root := flag.String("sessions", defRoots,
		"cartelle delle sessioni, separate da virgole: senza argomenti si rigiocano tutte (prima quelle del banco di prova, poi quelle di Kite)")
	suite := flag.Bool("suite", false, "prova le correzioni una alla volta e alcune varianti dei parametri")
	verbose := flag.Bool("v", false, "elenca le selezioni sbagliate (registrato e attuale)")
	corrMode := flag.Bool("correttore", false, "valuta il correttore sulle parole copiate, con gli errori come sarebbero entrati nel testo")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "uso: kite-replay [opzioni] [cartella-sessione ...]\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	dirs := flag.Args()
	if len(dirs) == 0 {
		for _, r := range strings.Split(*root, ",") {
			ds, err := replay.Sessions(r)
			if err != nil && !os.IsNotExist(err) {
				fail(err)
			}
			dirs = append(dirs, ds...)
		}
	}
	if len(dirs) == 0 {
		fail(fmt.Errorf("nessuna sessione in %s", *root))
	}
	var sessions []*replay.Session
	for _, d := range dirs {
		s, err := replay.Load(d)
		if err != nil {
			fail(fmt.Errorf("%s: %w", d, err))
		}
		nb := map[string]int{}
		for _, b := range s.Blocks {
			nb[b.Cond]++
		}
		fmt.Printf("%s: layout %s, blocchi A %d, B %d\n", filepath.Base(d), s.Info.Layout, nb[replay.CondDwell], nb[replay.CondHybrid])
		sessions = append(sessions, s)
	}
	current := func(*replay.Session) replay.Variant {
		return replay.Variant{Name: "attuale", Hybrid: selector.DefaultHybrid(), Sigma: selector.DefaultSigmaDeg,
			Core: selector.DefaultCoreFrac, Eps: engine.DefaultPriorEps, LM: "italiano"}
	}
	variants := []entry{
		{"registrato", func(s *replay.Session) replay.Variant { return s.Recorded() }},
		{"attuale", current},
	}
	if *suite {
		variants = suiteVariants(current)
	}

	if *corrMode {
		evalCorrector(sessions, current(nil), *verbose)
		return
	}

	fmt.Println()
	fmt.Println("L: scrittura libera (le lettere poi cancellate contano come errori). B: copiatura con il selettore")
	fmt.Println("ibrido. A (solo nelle sessioni del banco di prova): lo sguardo naturale, con il dwell fisso di 1 s,")
	fmt.Println("rigiocato con il selettore ibrido. Ritardo = lettere lasciate prima dell'attivazione, che dal vivo")
	fmt.Println("si sarebbero attese guardandole ancora; Δ = anticipo o ritardo medio delle lettere giuste rispetto")
	fmt.Println("al log (escluse quelle anticipate di oltre 1 s, dove il replay ha perso la sincronia con la persona);")
	fmt.Printf("netto = Δ × lettere + errori in più o in meno × %.1f s (L) o %.1f s (B).\n\n", replay.ErrorCostFreeS, replay.ErrorCostS)
	conds := []string{replay.CondFree, replay.CondHybrid, replay.CondDwell}
	fmt.Printf("%-44s │ %-37s │ %-37s │ %s\n", "", "L: scrittura libera", "B: copiatura", "A: sguardo naturale")
	head := fmt.Sprintf("%4s %5s %4s %6s %5s %6s", "err", "giuste", "rit", "acc", "Δ ms", "netto")
	fmt.Printf("%-44s │ %s │ %s │ %4s %5s %4s %6s\n", "variante", head, head, "err", "giuste", "rit", "acc")
	for _, e := range variants {
		tot := map[string]*replay.Result{}
		for _, c := range conds {
			tot[c] = &replay.Result{Cond: c}
		}
		var picks []replay.Pick
		for _, s := range sessions {
			res, err := s.Run(e.v(s))
			if err != nil {
				fail(err)
			}
			for c, r := range res {
				t := tot[c]
				if t == nil {
					continue
				}
				t.Targets += r.Targets
				t.Correct += r.Correct
				t.Errors += r.Errors
				t.Missed += r.Missed
				t.Late += r.Late
				t.Deltas = append(t.Deltas, r.Deltas...)
				t.Same += r.Same
				t.Logged += r.Logged
				t.LogError += r.LogError
				picks = append(picks, r.Picks...)
			}
		}
		for _, t := range tot {
			t.Summarize()
		}
		fmt.Printf("%-44s", e.name)
		for _, c := range conds[:2] {
			t := tot[c]
			fmt.Printf(" │ %4d %6d %4d %5.1f%% %+5.0f %+6.1f", t.Errors, t.Correct, t.Late, 100*t.Accuracy(), t.DeltaMs, t.NetS())
		}
		a := tot[replay.CondDwell]
		fmt.Printf(" │ %4d %5d %4d %5.1f%%\n", a.Errors, a.Correct, a.Late, 100*a.Accuracy())
		var missed []string
		for _, c := range conds {
			if m := tot[c].Missed; m > 0 {
				missed = append(missed, fmt.Sprintf("%s %d", c, m))
			}
		}
		if len(missed) > 0 {
			fmt.Printf("%-44s │ lettere mai candidate (ritardo sconosciuto): %s\n", "", strings.Join(missed, ", "))
		}
		if e.name == "registrato" {
			fmt.Printf("%-44s │ log: L %d selezioni, %d cancellate; B %d selezioni, %d errori; identiche nel replay: L %d, B %d\n",
				"  (controllo di fedeltà)", tot[replay.CondFree].Logged, tot[replay.CondFree].LogError,
				tot[replay.CondHybrid].Logged, tot[replay.CondHybrid].LogError, tot[replay.CondFree].Same, tot[replay.CondHybrid].Same)
		}
		if *verbose && (e.name == "registrato" || e.name == "attuale") {
			printErrors(picks)
		}
	}
}

// suiteVariants aggiunge le correzioni una alla volta a partire dai
// parametri della prima versione, poi prova alcune varianti dei valori.
func suiteVariants(current func(*replay.Session) replay.Variant) []entry {
	old := func() replay.Variant {
		d := selector.DefaultHybrid()
		h := selector.HybridParams{TMinMs: 120, TMaxMs: 700, RHi: d.RHi, RLo: d.RLo, SHi: d.SHi, RepeatMs: 700,
			PupilGain: d.PupilGain, UsePrediction: true}
		return replay.Variant{Hybrid: h, Sigma: 1.6, Core: 0.6, Eps: engine.DefaultPriorEps}
	}
	d := selector.DefaultHybrid()
	steps := []struct {
		name string
		f    func(*replay.Variant)
	}{
		{"prima versione", func(v *replay.Variant) {}},
		{"+ una selezione per fissazione", func(v *replay.Variant) { v.Hybrid.LockFixation = true }},
		{"+ attesa della correttiva (atterraggio)", func(v *replay.Variant) {
			v.Hybrid.LandMs, v.Hybrid.LandAmpDeg, v.Hybrid.LandConf, v.Hybrid.Landing = d.LandMs, d.LandAmpDeg, d.LandConf, d.Landing
		}},
		{"+ ripetizione secondo la predizione", func(v *replay.Variant) {
			v.Hybrid.RepeatMs, v.Hybrid.RepeatMinMs = d.RepeatMs, d.RepeatMinMs
		}},
		{"+ prudenza dopo un errore", func(v *replay.Variant) {
			v.Hybrid.CautionMs, v.Hybrid.CautionSel = d.CautionMs, d.CautionSel
		}},
		{"+ lessico italiano grande", func(v *replay.Variant) { v.LM = "italiano" }},
		{"+ armamento della tastiera", func(v *replay.Variant) { v.Hybrid.Arm = true }},
		{"+ doppie improbabili solo uscendo", func(v *replay.Variant) { v.Hybrid.RepeatHoldMinR = d.RepeatHoldMinR }},
		{"+ veto della mira", func(v *replay.Variant) { v.Hybrid.AimVeto = d.AimVeto }},
		{"+ da capo se la fissazione si disperde", func(v *replay.Variant) {
			v.Hybrid.RestartUnstable, v.Hybrid.EditRecover = true, true
		}},
		{"+ tasti di modifica a tempo proprio", func(v *replay.Variant) {
			v.Hybrid.EditMs, v.Hybrid.SpeakMs, v.Hybrid.CancRepeatMs = d.EditMs, d.SpeakMs, d.CancRepeatMs
		}},
		{"+ tempi 200–1000 ms (= attuale)", func(v *replay.Variant) { v.Hybrid = selector.DefaultHybrid() }},
	}
	var out []entry
	cur := old()
	for _, st := range steps {
		st.f(&cur)
		v := cur
		v.Name = st.name
		out = append(out, entry{st.name, func(*replay.Session) replay.Variant { return v }})
	}
	tweak := func(name string, f func(*replay.Variant)) {
		v := current(nil)
		f(&v)
		v.Name = name
		out = append(out, entry{name, func(*replay.Session) replay.Variant { return v }})
	}
	for _, sp := range selector.Speeds {
		tweak(fmt.Sprintf("attuale, preset %s (%.0f–%.0f ms)", sp.Name, sp.TMinMs, sp.TMaxMs), func(v *replay.Variant) {
			v.Hybrid.TMinMs, v.Hybrid.TMaxMs = sp.TMinMs, sp.TMaxMs
		})
	}
	tweak("attuale, tempi di prima (120–700 ms)", func(v *replay.Variant) { v.Hybrid.TMinMs, v.Hybrid.TMaxMs = 120, 700 })
	tweak("attuale, senza armamento", func(v *replay.Variant) { v.Hybrid.Arm = false })
	tweak("attuale, senza veto della mira", func(v *replay.Variant) { v.Hybrid.AimVeto = 0 })
	tweak("attuale, doppie improbabili anche tenendo", func(v *replay.Variant) { v.Hybrid.RepeatHoldMinR = 0 })
	tweak("attuale, atterraggio sempre (senza modello)", func(v *replay.Variant) { v.Hybrid.LandConf = 2 })
	tweak("attuale, senza atterraggio", func(v *replay.Variant) { v.Hybrid.LandMs = 0 })
	for _, sg := range []float64{1.2, 2.0} {
		tweak(fmt.Sprintf("attuale, σ %.1f°", sg), func(v *replay.Variant) { v.Sigma = sg })
	}
	for _, c := range []float64{0.4, 0.8} {
		tweak(fmt.Sprintf("attuale, nucleo %.0f%%", 100*c), func(v *replay.Variant) { v.Core = c })
	}
	for _, c := range []float64{0, 300, 700} {
		tweak(fmt.Sprintf("attuale, prudenza %.0f ms", c), func(v *replay.Variant) { v.Hybrid.CautionMs = c })
	}
	for _, rh := range []float64{2, 8} {
		tweak(fmt.Sprintf("attuale, RHi %.0f", rh), func(v *replay.Variant) { v.Hybrid.RHi = rh })
	}
	return out
}

func printErrors(picks []replay.Pick) {
	sort.SliceStable(picks, func(i, j int) bool { return picks[i].TUs < picks[j].TUs })
	var b strings.Builder
	for _, p := range picks {
		switch {
		case p.Correct:
			continue
		case p.Late:
			fmt.Fprintf(&b, "      blocco %d (%s): %q in ritardo       al massimo %4.0f/%4.0f ms  post %.2f  r %.2f  mira %.2f\n",
				p.Block, p.Cond, p.Expected, p.Dec.ElapsedMs, p.Dec.RequiredMs, p.Dec.Posterior, p.Dec.PriorRatio, p.Dec.Aim)
		case p.Missed:
			fmt.Fprintf(&b, "      blocco %d (%s): %q mai candidata\n", p.Block, p.Cond, p.Expected)
		default:
			fmt.Fprintf(&b, "      blocco %d (%s): %q invece di %q  fissazione %4.0f/%4.0f ms  post %.2f  r %.2f  mira %.2f\n",
				p.Block, p.Cond, p.Key, p.Expected, p.Dec.ElapsedMs, p.Dec.RequiredMs, p.Dec.Posterior, p.Dec.PriorRatio, p.Dec.Aim)
		}
	}
	fmt.Print(b.String())
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "kite-replay:", err)
	os.Exit(1)
}

// evalCorrector confronta, sulle parole copiate, il testo selezionato con
// quello che avrebbe scritto il correttore, con e senza lo sguardo.
func evalCorrector(sessions []*replay.Session, v replay.Variant, verbose bool) {
	model := lm.New()
	type variant struct {
		name string
		p    correct.Params
		gaze bool
	}
	base := correct.DefaultParams()
	vs := []variant{{"correttore con lo sguardo", base, true}, {"correttore con le sole lettere", base, false}}
	for _, pi := range []float64{0.04, 0.08, 0.12, 0.15} {
		for _, mp := range []float64{0.4, 0.5, 0.6} {
			p := base
			p.PIns, p.MinPost = pi, mp
			if p == base {
				continue
			}
			vs = append(vs, variant{fmt.Sprintf("  con lo sguardo, PIns %.2f, soglia %.1f", pi, mp), p, true})
		}
	}
	var words []replay.Word
	for _, s := range sessions {
		ws, err := s.Words(v)
		if err != nil {
			fail(err)
		}
		words = append(words, ws...)
	}
	nErr := 0
	for _, w := range words {
		if w.Typed != w.Target {
			nErr++
		}
	}
	fmt.Printf("\nParole copiate: %d, di cui %d con errori nelle selezioni (come sarebbero entrate nel testo).\n",
		len(words), nErr)
	fmt.Println("Corrette = parole con errori che lo spazio rende giuste; nei suggerimenti = parole con errori")
	fmt.Println("non corrette da sole ma tra i tre suggerimenti della barra (un'occhiata); guastate = parole")
	fmt.Println("giuste che diventano sbagliate.")
	fmt.Println()
	fmt.Printf("%-44s %9s %13s %9s %14s\n", "", "corrette", "suggerimenti", "guastate", "parole giuste")
	fmt.Printf("%-44s %9s %13s %9s %8d/%d\n", "testo selezionato", "—", "—", "—", len(words)-nErr, len(words))
	for _, vv := range vs {
		c := correct.New(vv.p, model)
		fixed, inSugg, broken, right := 0, 0, 0, 0
		var log []string
		for _, w := range words {
			obs := w.Obs
			if !vv.gaze {
				obs = make([]correct.Obs, len(w.Obs))
				for i, o := range w.Obs {
					obs[i] = correct.Certain(o.Char)
				}
			}
			r := c.Commit(obs)
			got := lm.Fold(r.Text)
			if got == w.Target {
				right++
			}
			switch {
			case w.Typed != w.Target && got == w.Target:
				fixed++
				log = append(log, fmt.Sprintf("      ✓ %-12s → %-12s", w.Typed, r.Text))
			case w.Typed != w.Target:
				mark := "✗"
				for _, sg := range c.Suggest(obs, 3) {
					if lm.Fold(sg.Text) == w.Target {
						inSugg++
						mark = "≈"
					}
				}
				log = append(log, fmt.Sprintf("      %s %-12s → %-12s (era %s)", mark, w.Typed, r.Text, w.Target))
			case got != w.Target:
				broken++
				log = append(log, fmt.Sprintf("      ! %-12s → %-12s (era giusta)", w.Typed, r.Text))
			}
		}
		fmt.Printf("%-44s %6d/%-2d %10d/%-2d %9d %8d/%d\n", vv.name, fixed, nErr, inSugg, nErr, broken, right, len(words))
		if verbose && vv.name[0] != ' ' {
			for _, l := range log {
				fmt.Println(l)
			}
		}
	}
}
