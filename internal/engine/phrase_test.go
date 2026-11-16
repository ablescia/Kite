// SPDX-License-Identifier: AGPL-3.0-or-later

package engine

import (
	"math/rand/v2"
	"testing"
	"time"

	"github.com/ablescia/kite/internal/gazein"
	"github.com/ablescia/kite/internal/ngram"
)

func newPhraseEngine(t *testing.T, phrases ...string) *Engine {
	t.Helper()
	e := newTextEngine(t)
	m := ngram.New(ngram.DefaultParams())
	m.PrefixMass = e.cfg.LM.PrefixMass
	for _, p := range phrases {
		m.Learn(p)
	}
	e.cfg.Phrases = m
	e.StartFree()
	return e
}

// typeFree scrive come typeText e aggiorna lo stato come dopo una selezione.
func typeFree(e *Engine, s string) {
	locked(e, func() {
		typeText(e, s)
		e.syncTask()
	})
}

// gazer genera campioni a 90 Hz con un rumore di fissazione piccolo.
type gazer struct {
	e   *Engine
	t   int64
	seq uint64
	r   *rand.Rand
}

func newGazer(e *Engine) *gazer { return &gazer{e: e, t: 1_000_000, r: rand.New(rand.NewPCG(3, 4))} }

func (g *gazer) look(p [2]float64, ms int, valid bool) {
	for n := 0; n < ms*90/1000; n++ {
		g.t += 11_111
		g.seq++
		q := [2]float64{p[0] + g.r.NormFloat64()*0.0006, p[1] + g.r.NormFloat64()*0.001}
		eye := [3]float64{0, 0, 620}
		g.e.Process(gazein.Sample{Seq: g.seq, HostNs: time.Now().UnixNano(), DeviceUs: g.t,
			Gaze: q, GazeValid: valid, Raw: q, RawValid: valid,
			PupilMM: [2]float64{3.5, 3.5}, PupilValid: [2]bool{valid, valid},
			EyeMM: [2][3]float64{eye, eye}, EyeValid: [2]bool{valid, valid}, Presence: 2})
	}
}

// la frase grigia sulla riga del testo, come la disegna l'interfaccia
var ghostRect = [4]float64{0.30, 0.12, 0.60, 0.185}

func showGhost(t *testing.T, e *Engine) Snapshot {
	t.Helper()
	s := e.Snapshot()
	if s.Phrase == "" {
		t.Fatalf("nessuna frase proposta dopo «%s»", s.Typed)
	}
	e.SetPhraseRect(s.PhraseVer, ghostRect)
	return s
}

func TestPhraseAcceptedByFixation(t *testing.T) {
	e := newPhraseEngine(t, "ho bisogno di aiuto", "ho bisogno di aiuto")
	typeFree(e, "ho bisogno d")
	if s := showGhost(t, e); s.Phrase != "i aiuto" {
		t.Fatalf("frase grigia %q", s.Phrase)
	}
	g := newGazer(e)
	g.look([2]float64{0.45, 0.15}, 600, true)
	if s := e.Snapshot(); s.Typed != "ho bisogno d" || s.PhraseProg < 0.3 || s.PhraseProg >= 1 {
		t.Fatalf("a metà fissazione: «%s», avanzamento %.2f", s.Typed, s.PhraseProg)
	}
	g.look([2]float64{0.45, 0.15}, 600, true)
	s := e.Snapshot()
	if s.Typed != "ho bisogno di aiuto " || s.Phrase != "" || s.Accepted != "di aiuto" {
		t.Fatalf("dopo 1,2 s di fissazione: «%s», grigio %q, accettata %q", s.Typed, s.Phrase, s.Accepted)
	}
	// cancella subito dopo: torna il testo di prima, senza riproporre la frase
	typeFree(e, "<")
	if s := e.Snapshot(); s.Typed != "ho bisogno d" || s.Phrase != "" {
		t.Fatalf("annullata: «%s», grigio %q", s.Typed, s.Phrase)
	}
	// il testo cambia: la proposta torna
	typeFree(e, "i")
	if s := e.Snapshot(); s.Phrase != " aiuto" {
		t.Fatalf("dopo un'altra lettera: grigio %q", s.Phrase)
	}
}

// Leggere la frase (fissazioni brevi separate da saccadi) non la accetta;
// un ammiccamento durante una fissazione lunga non la interrompe.
func TestPhraseReadingDoesNotAccept(t *testing.T) {
	e := newPhraseEngine(t, "per favore chiamate mia figlia", "per favore chiamate mia figlia")
	typeFree(e, "per f")
	showGhost(t, e)
	g := newGazer(e)
	for k := 0; k < 4; k++ {
		for _, x := range []float64{0.33, 0.38, 0.43, 0.48, 0.53, 0.58} {
			g.look([2]float64{x, 0.15}, 300, true)
		}
	}
	if s := e.Snapshot(); s.Typed != "per f" {
		t.Fatalf("la lettura ha accettato la frase: «%s»", s.Typed)
	}
	g.look([2]float64{0.40, 0.15}, 500, true)
	g.look([2]float64{0.40, 0.15}, 150, false) // ammiccamento
	g.look([2]float64{0.40, 0.15}, 600, true)
	if s := e.Snapshot(); s.Typed != "per favore chiamate mia figlia " {
		t.Fatalf("fissazione con ammiccamento: «%s»", s.Typed)
	}
}

// Lo sguardo fuori dalla frase, o una frase non ancora disegnata, non
// accettano nulla.
func TestPhraseNeedsRectAndGaze(t *testing.T) {
	e := newPhraseEngine(t, "ho sete", "ho sete")
	typeFree(e, "ho s")
	g := newGazer(e)
	g.look([2]float64{0.45, 0.15}, 1500, true) // area non ancora comunicata
	if s := e.Snapshot(); s.Typed != "ho s" {
		t.Fatalf("accettata senza area: «%s»", s.Typed)
	}
	showGhost(t, e)
	g.look([2]float64{0.80, 0.15}, 1500, true) // sulla riga, ma fuori dalla frase
	if s := e.Snapshot(); s.Typed != "ho s" {
		t.Fatalf("accettata guardando altrove: «%s»", s.Typed)
	}
	// l'area di una proposta vecchia non vale per quella nuova
	old := e.Snapshot().PhraseVer
	typeFree(e, "<")
	e.SetPhraseRect(old, ghostRect)
	g.look([2]float64{0.45, 0.15}, 1500, true)
	if s := e.Snapshot(); s.Typed != "ho " {
		t.Fatalf("accettata con l'area vecchia: «%s»", s.Typed)
	}
}

func TestPhraseTabAndElision(t *testing.T) {
	e := newPhraseEngine(t, "vorrei dell'acqua fresca", "vorrei dell'acqua fresca")
	typeFree(e, "vorrei del")
	if s := e.Snapshot(); s.Phrase != "l'acqua fresca" {
		t.Fatalf("frase grigia %q", s.Phrase)
	}
	e.AcceptPhrase()
	if s := e.Snapshot(); s.Typed != "vorrei dell'acqua fresca " {
		t.Fatalf("Tab: «%s»", s.Typed)
	}
	// la scrittura continua normalmente dopo la frase
	typeFree(e, "per ")
	if s := e.Snapshot(); s.Typed != "vorrei dell'acqua fresca per " {
		t.Fatalf("dopo la frase: «%s»", s.Typed)
	}
}

// Nell'esercizio di copiatura non si propone nulla, e sul testo vuoto
// nemmeno.
func TestPhraseOnlyInFreeWriting(t *testing.T) {
	e := newPhraseEngine(t, "ho sete", "ho sete", "ho sete")
	if s := e.Snapshot(); s.Phrase != "" {
		t.Fatalf("testo vuoto: grigio %q", s.Phrase)
	}
	e.cfg.Task = "testo"
	e.StartBlock("casa bella")
	typeFree(e, "ho s")
	if s := e.Snapshot(); s.Phrase != "" {
		t.Fatalf("blocco: grigio %q", s.Phrase)
	}
}

// Un messaggio detto per intero si impara e torna come proposta; uno
// fermato prima della fine no.
func TestPhraseLearnedWhenSpoken(t *testing.T) {
	e, f := newVoiceEngine(t)
	m := ngram.New(ngram.DefaultParams())
	e.cfg.Phrases = m
	e.StartFree()
	typeFree(e, "mi giri sul fianco ")
	pressSpeak(e)
	said(t, f)
	f.finish <- nil
	waitSnap(t, e, func(s Snapshot) bool { return !s.Speaking }, "fine della voce")
	if m.Phrases() != 1 {
		t.Fatalf("frasi imparate: %d", m.Phrases())
	}
	select {
	case ev := <-e.Events():
		if ev.Kind != EvPhrase || ev.Text != "mi giri sul fianco" {
			t.Fatalf("evento %+v", ev)
		}
	default:
		t.Fatal("nessun evento per salvare la frase")
	}
	typeFree(e, "mi g")
	if s := e.Snapshot(); s.Phrase != "iri sul fianco" {
		t.Fatalf("frase imparata non proposta: %q", s.Phrase)
	}
	// fermata a metà: non si impara
	typeFree(e, "<<<<")
	typeFree(e, "chiamate qualcuno ")
	pressSpeak(e)
	said(t, f)
	pressSpeak(e) // stop
	if m.Phrases() != 1 {
		t.Fatalf("frase fermata imparata: %d frasi", m.Phrases())
	}
}
