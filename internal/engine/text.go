// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Antonio Blescia

package engine

import (
	"strings"
	"unicode"

	"github.com/ablescia/kite/internal/correct"
	"github.com/ablescia/kite/internal/lm"
	"github.com/ablescia/kite/internal/ngram"
	"github.com/ablescia/kite/internal/selector"
)

// editor è il testo scritto con lo sguardo: lettere, spazi, cancellazioni e
// suggerimenti, con il correttore a fine parola (se c'è) e la frase proposta
// dal modello delle frasi (se c'è).
type editor struct {
	corr  *correct.Corrector // nil: le parole restano come selezionate
	text  []rune
	start int           // inizio della parola in corso in text
	obs   []correct.Obs // selezioni della parola in corso
	undo  *undoInfo     // ultima correzione automatica, annullabile con cancella
	said  []rune        // testo detto dalla voce e cancellato, ripristinabile con cancella
	sugg  []correct.Cand
	nsugg int

	ph     *ngram.Model      // nil: nessuna frase proposta
	phrase *ngram.Prediction // frase proposta adesso
	phUndo *phraseUndo       // ultima frase accettata, annullabile con cancella
	reject string            // frase appena annullata: non si ripropone finché il testo non cambia
}

type phraseUndo struct {
	text   []rune
	start  int
	obs    []correct.Obs
	phrase string
}

type undoInfo struct {
	start  int
	before []rune
	obs    []correct.Obs
	res    correct.Result
}

// Edit descrive l'effetto di una selezione sul testo.
type Edit struct {
	Kind   string          // "lettera", "spazio", "canc", "annulla", "suggerimento", "parla", "stop", "ripristina", "frase", "annulla frase"
	Commit *correct.Result // parola chiusa dallo spazio (con il correttore)
	Undone string          // parola ripristinata annullando una correzione
	Sugg   string          // suggerimento scelto
	Phrase string          // frase accettata o annullata
	Word   bool            // una parola è stata chiusa (spazio, suggerimento o frase)
}

func newEditor(corr *correct.Corrector, nsugg int, ph *ngram.Model) *editor {
	return &editor{corr: corr, nsugg: nsugg, ph: ph}
}

// changed azzera ciò che vale solo subito dopo l'ultima modifica.
func (ed *editor) changed() { ed.undo, ed.said, ed.phUndo, ed.reject = nil, nil, nil, "" }

func (ed *editor) String() string { return string(ed.text) }

// context è la parola in corso come la vede il modello di lingua.
func (ed *editor) context() string { return " " + lm.Fold(string(ed.text[ed.start:])) }

func (ed *editor) letter(c rune, o correct.Obs) Edit {
	ed.changed()
	ed.text = append(ed.text, c)
	ed.obs = append(ed.obs, o)
	ed.suggest()
	ed.predict()
	return Edit{Kind: "lettera"}
}

// syncObs ricostruisce le osservazioni della parola in corso dal testo quando
// non corrispondono più (dopo una cancellazione dentro una parola già chiusa).
func (ed *editor) syncObs() {
	w := lm.Fold(string(ed.text[ed.start:]))
	if len(ed.obs) == len(w) {
		return
	}
	ed.obs = ed.obs[:0]
	for i := 0; i < len(w); i++ {
		ed.obs = append(ed.obs, correct.Certain(w[i]))
	}
}

func (ed *editor) space() Edit {
	ed.said, ed.phUndo, ed.reject = nil, nil, ""
	e := Edit{Kind: "spazio", Word: true}
	attach := false
	if ed.corr != nil && len(ed.text) > ed.start {
		ed.syncObs()
		r := ed.corr.Commit(ed.obs)
		before := append([]rune(nil), ed.text[ed.start:]...)
		if r.Kind == correct.Fixed {
			ed.undo = &undoInfo{start: ed.start, before: before, obs: append([]correct.Obs(nil), ed.obs...), res: r}
		} else {
			ed.undo = nil
		}
		if r.Kind != correct.Unknown {
			ed.text = append(ed.text[:ed.start], []rune(r.Text)...)
		}
		attach = r.Attach
		e.Commit = &r
	} else {
		ed.undo = nil
	}
	if !attach {
		ed.text = append(ed.text, ' ')
	}
	ed.start, ed.obs, ed.sugg = len(ed.text), ed.obs[:0], nil
	ed.predict()
	return e
}

func (ed *editor) backspace() Edit {
	if ed.said != nil {
		// subito dopo la voce: torna il testo detto, per correggerlo o ridirlo
		ed.text, ed.said = ed.said, nil
		ed.start, ed.obs = wordStart(ed.text), ed.obs[:0]
		ed.syncObs()
		ed.suggest()
		ed.predict()
		return Edit{Kind: "ripristina"}
	}
	if u := ed.phUndo; u != nil {
		// annulla la frase accettata: torna il testo di prima, e la stessa
		// frase non si ripropone finché il testo non cambia
		ed.text, ed.start = u.text, u.start
		ed.obs = append(ed.obs[:0], u.obs...)
		ed.phUndo, ed.undo = nil, nil
		ed.suggest()
		ed.reject = u.phrase
		ed.predict()
		return Edit{Kind: "annulla frase", Phrase: u.phrase}
	}
	if u := ed.undo; u != nil {
		// annulla la correzione automatica: torna la parola selezionata,
		// ancora aperta
		ed.text = append(ed.text[:u.start], u.before...)
		ed.start = u.start
		ed.obs = append(ed.obs[:0], u.obs...)
		ed.undo = nil
		ed.suggest()
		ed.predict()
		return Edit{Kind: "annulla", Undone: u.res.Literal}
	}
	ed.changed()
	if len(ed.text) == 0 {
		return Edit{Kind: "canc"}
	}
	ed.text = ed.text[:len(ed.text)-1]
	if len(ed.text) < ed.start || ed.start > 0 && len(ed.text) == ed.start && !isSep(ed.text[ed.start-1]) {
		// rientro nella parola precedente
		ed.start = wordStart(ed.text)
		ed.obs = ed.obs[:0]
	} else if len(ed.obs) > 0 {
		ed.obs = ed.obs[:len(ed.obs)-1]
	}
	ed.syncObs()
	ed.suggest()
	ed.predict()
	return Edit{Kind: "canc"}
}

func isSep(r rune) bool { return unicode.IsSpace(r) || r == '\'' }

// wordStart è l'inizio dell'ultima parola di t (dopo uno spazio o un apostrofo).
func wordStart(t []rune) int {
	for i := len(t) - 1; i >= 0; i-- {
		if isSep(t[i]) {
			return i + 1
		}
	}
	return 0
}

func (ed *editor) pick(i int) (Edit, bool) {
	if i < 0 || i >= len(ed.sugg) {
		return Edit{}, false
	}
	c := ed.sugg[i]
	ed.changed()
	ed.text = append(ed.text[:ed.start], []rune(c.Text)...)
	if !c.Attach {
		ed.text = append(ed.text, ' ')
	}
	ed.start, ed.obs, ed.sugg = len(ed.text), ed.obs[:0], nil
	ed.predict()
	return Edit{Kind: "suggerimento", Sugg: c.Text, Word: true}, true
}

// acceptPhrase scrive la frase proposta al posto della parola in corso.
// Cancella subito dopo la annulla.
func (ed *editor) acceptPhrase() (Edit, bool) {
	p := ed.phrase
	if p == nil {
		return Edit{}, false
	}
	u := &phraseUndo{text: append([]rune(nil), ed.text...), start: ed.start,
		obs: append([]correct.Obs(nil), ed.obs...), phrase: p.Text}
	ed.changed()
	ed.text = append(ed.text[:wordStart(ed.text)], []rune(p.Text)...)
	if last := p.Words[len(p.Words)-1]; !last.Attach {
		ed.text = append(ed.text, ' ')
	}
	ed.start, ed.obs, ed.sugg = len(ed.text), ed.obs[:0], nil
	ed.phUndo = u
	ed.predict()
	return Edit{Kind: "frase", Phrase: p.Text, Word: true}, true
}

// open dice se l'ultima parola è ancora aperta (non chiusa da uno spazio).
func (ed *editor) open() bool { return len(ed.text) > ed.start }

// clearSaid cancella il testo appena detto dalla voce; cancella subito dopo
// lo ripristina.
func (ed *editor) clearSaid() {
	text := ed.text
	ed.changed()
	ed.said, ed.text = text, nil
	ed.start, ed.obs, ed.sugg, ed.phrase = 0, ed.obs[:0], nil, nil
}

func (ed *editor) suggest() {
	ed.sugg = nil
	if ed.corr == nil || ed.nsugg == 0 || len(ed.obs) == 0 {
		return
	}
	ed.syncObs()
	ed.sugg = ed.corr.Suggest(ed.obs, ed.nsugg)
}

// predict aggiorna la frase proposta: serve almeno una lettera del messaggio
// (una frase che compare sul testo vuoto si accetterebbe guardando nel
// vuoto mentre si pensa).
func (ed *editor) predict() {
	ed.phrase = nil
	if ed.ph == nil || strings.TrimSpace(string(ed.text)) == "" {
		return
	}
	if p, ok := ed.ph.Predict(string(ed.text)); ok && p.Text != ed.reject {
		ed.phrase = &p
	}
}

// Suggestions restituisce i testi dei suggerimenti correnti.
func (ed *editor) suggestions() []string {
	out := make([]string, len(ed.sugg))
	for i, c := range ed.sugg {
		out[i] = c.Text
	}
	return out
}

// onTrack dice se il testo è un inizio corretto del bersaglio (a meno di
// accenti, apostrofi e maiuscole).
func onTrack(text, target string) bool {
	return strings.HasPrefix(foldText(target), foldText(text))
}

// foldText riduce un testo alla digitazione, con gli spazi normalizzati e
// l'apostrofo di elisione come confine di parola.
func foldText(s string) string {
	s = strings.ReplaceAll(s, "'", "' ")
	f := strings.Fields(lm.Fold(s))
	out := strings.Join(f, " ")
	if strings.HasSuffix(s, " ") && out != "" {
		out += " "
	}
	return out
}

// keyChar è il simbolo di un tasto per l'editor ('a'..'z', ' ', o un tasto
// di modifica).
func keyChar(k selector.Key) rune { return unicode.ToLower(k.Char) }
