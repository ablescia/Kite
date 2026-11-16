// SPDX-License-Identifier: AGPL-3.0-or-later

// kite-replay rigioca le sessioni registrate (di Kite e del banco di prova da
// cui Kite nasce) attraverso il selettore ibrido, con i parametri registrati
// e con quelli attuali (o con una serie di varianti, -suite), e confronta
// errori, lettere mancate e tempi.
//
//	kite-replay                    # tutte le sessioni registrate
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
	fmt.Println("Blocchi B: lo sguardo di chi usava il selettore ibrido. Blocchi A (solo nelle sessioni del")
	fmt.Println("banco di prova): lo sguardo naturale, con il dwell fisso di 1 s, rigiocato con il selettore")
	fmt.Println("ibrido. Ritardo = lettere lasciate prima dell'attivazione, che dal vivo si sarebbero attese")
	fmt.Println("guardandole ancora; Δ = anticipo o ritardo medio delle lettere giuste rispetto al log")
	fmt.Println("(escluse quelle anticipate di oltre 1 s, dove il replay ha perso la sincronia con la")
	fmt.Printf("persona); netto = Δ × lettere + errori in più o in meno × %.1f s.\n\n", replay.ErrorCostS)
	fmt.Printf("%-44s │ %-44s │ %s\n", "", "blocchi B", "blocchi A (sguardo naturale)")
	fmt.Printf("%-44s │ %4s %6s %7s %6s %6s %7s │ %4s %6s %7s %6s\n", "variante",
		"err", "giuste", "ritardo", "acc", "Δ ms", "netto s", "err", "giuste", "ritardo", "acc")
	for _, e := range variants {
		tot := map[string]*replay.Result{replay.CondDwell: {}, replay.CondHybrid: {}}
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
		a, b := tot[replay.CondDwell], tot[replay.CondHybrid]
		a.Summarize()
		b.Summarize()
		fmt.Printf("%-44s │ %4d %6d %7d %5.1f%% %+6.0f %+7.1f │ %4d %6d %7d %5.1f%%\n", e.name,
			b.Errors, b.Correct, b.Late, 100*b.Accuracy(), b.DeltaMs, b.NetS(),
			a.Errors, a.Correct, a.Late, 100*a.Accuracy())
		if b.Missed+a.Missed > 0 {
			fmt.Printf("%-44s │ lettere mai candidate (ritardo sconosciuto): B %d, A %d\n", "", b.Missed, a.Missed)
		}
		if e.name == "registrato" {
			fmt.Printf("%-44s │ log: %d selezioni, %d errori; identiche nel replay: %d\n", "  (controllo di fedeltà)",
				b.Logged, b.LogError, b.Same)
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
		h := selector.DefaultHybrid()
		h.LockFixation, h.LandMs, h.CautionMs, h.RepeatMs, h.RepeatMinMs = false, 0, 0, 700, 0
		return replay.Variant{Hybrid: h, Sigma: 1.6, Core: 0.6, Eps: engine.DefaultPriorEps}
	}
	steps := []struct {
		name string
		f    func(*replay.Variant)
	}{
		{"prima versione", func(v *replay.Variant) {}},
		{"+ una selezione per fissazione", func(v *replay.Variant) { v.Hybrid.LockFixation = true }},
		{"+ attesa della correttiva (atterraggio)", func(v *replay.Variant) {
			d := selector.DefaultHybrid()
			v.Hybrid.LandMs, v.Hybrid.LandAmpDeg, v.Hybrid.LandConf, v.Hybrid.Landing = d.LandMs, d.LandAmpDeg, d.LandConf, d.Landing
		}},
		{"+ ripetizione secondo la predizione", func(v *replay.Variant) {
			d := selector.DefaultHybrid()
			v.Hybrid.RepeatMs, v.Hybrid.RepeatMinMs = d.RepeatMs, d.RepeatMinMs
		}},
		{"+ prudenza dopo un errore", func(v *replay.Variant) {
			d := selector.DefaultHybrid()
			v.Hybrid.CautionMs, v.Hybrid.CautionSel = d.CautionMs, d.CautionSel
		}},
		{"+ lessico italiano grande (= attuale)", func(v *replay.Variant) { v.LM = "italiano" }},
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
	tweak("attuale, atterraggio sempre (senza modello)", func(v *replay.Variant) { v.Hybrid.LandConf = 2 })
	tweak("attuale, senza atterraggio", func(v *replay.Variant) { v.Hybrid.LandMs = 0 })
	for _, ms := range []float64{220, 350} {
		tweak(fmt.Sprintf("attuale, atterraggio %.0f ms", ms), func(v *replay.Variant) { v.Hybrid.LandMs = ms })
	}
	for _, sg := range []float64{0.8, 1.2, 2.0} {
		tweak(fmt.Sprintf("attuale, σ %.1f°", sg), func(v *replay.Variant) { v.Sigma = sg })
	}
	for _, c := range []float64{0.4, 0.8} {
		tweak(fmt.Sprintf("attuale, nucleo %.0f%%", 100*c), func(v *replay.Variant) { v.Core = c })
	}
	tweak("attuale, ripetizione fissa 1500 ms", func(v *replay.Variant) { v.Hybrid.RepeatMinMs = 0 })
	tweak("attuale, ripetizione 500…1500 ms", func(v *replay.Variant) { v.Hybrid.RepeatMinMs = 500 })
	tweak("attuale, ripetizione 700…2000 ms", func(v *replay.Variant) { v.Hybrid.RepeatMs = 2000 })
	for _, c := range []float64{0, 300, 500} {
		tweak(fmt.Sprintf("attuale, prudenza %.0f ms", c), func(v *replay.Variant) { v.Hybrid.CautionMs = c })
	}
	tweak("attuale, prudenza per 2 selezioni", func(v *replay.Variant) { v.Hybrid.CautionSel = 2 })
	for _, t := range []float64{150} {
		tweak(fmt.Sprintf("attuale, tmin %.0f ms", t), func(v *replay.Variant) { v.Hybrid.TMinMs = t })
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
