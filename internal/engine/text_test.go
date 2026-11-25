// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Antonio Blescia

package engine

import (
	"testing"
	"time"

	"github.com/ablescia/kite/internal/correct"
	"github.com/ablescia/kite/internal/gazein"
	"github.com/ablescia/kite/internal/lm"
	"github.com/ablescia/kite/internal/oculo"
	"github.com/ablescia/kite/internal/selector"
)

func newTextEngine(t *testing.T) *Engine {
	t.Helper()
	kb, err := selector.NewKeyboard("qwerty", selector.DefaultArea, selector.DefaultGap)
	if err != nil {
		t.Fatal(err)
	}
	kb.AddEditBar(selector.DefaultBar, selector.DefaultGap, true)
	m := lm.New()
	e := New(Config{Geom: gazein.Geometry{WidthMM: 700, HeightMM: 390, WidthPx: 1920, HeightPx: 1080}, KB: kb, LM: m,
		Oculo: oculo.DefaultConfig(), Hybrid: selector.DefaultHybrid(), SigmaDeg: 1.6, CoreFrac: 0.6,
		PriorEps: 0.1, Corrector: correct.New(correct.DefaultParams(), m)})
	e.StartFree()
	return e
}

// typeText scrive con selezioni nette; '<' è cancella, '1'..'3' i suggerimenti.
func typeText(e *Engine, s string) []Edit {
	var out []Edit
	for _, r := range s {
		c := r
		switch r {
		case '<':
			c = selector.KeyBackspace
		case '1', '2', '3':
			c = selector.KeySuggest + (r - '1')
		}
		ed, _ := e.edit(c, correct.Certain(byte(r)), time.Now())
		e.recomputePrior()
		out = append(out, ed)
	}
	return out
}

func TestEditorCorrectsAndUndoes(t *testing.T) {
	e := newTextEngine(t)
	typeText(e, "sabngue ")
	if got := e.ed.String(); got != "sangue " {
		t.Fatalf("correzione allo spazio: %q", got)
	}
	// cancella subito dopo: torna la parola selezionata, ancora aperta, e
	// finisce nel dizionario personale
	eds := typeText(e, "<")
	if got := e.ed.String(); got != "sabngue" || eds[0].Kind != "annulla" {
		t.Fatalf("annullamento: %q (%s)", got, eds[0].Kind)
	}
	select {
	case ev := <-e.Events():
		if ev.Kind != EvUserWord || ev.Word != "sabngue" {
			t.Errorf("evento: %+v", ev)
		}
	default:
		t.Error("manca l'evento della parola personale")
	}
	typeText(e, " ")
	if got := e.ed.String(); got != "sabngue " {
		t.Errorf("dopo l'annullamento la parola resta com'è: %q", got)
	}
	// un secondo cancella non annulla più: toglie lo spazio
	typeText(e, "<<")
	if got := e.ed.String(); got != "sabngu" {
		t.Errorf("cancellazione normale: %q", got)
	}
}

func TestEditorAccentsElisionsSuggestions(t *testing.T) {
	e := newTextEngine(t)
	typeText(e, "perche l acqua ")
	if got := e.ed.String(); got != "perché l'acqua " {
		t.Fatalf("accenti ed elisione: %q", got)
	}
	typeText(e, "fami")
	sugg := e.ed.suggestions()
	k := -1
	for i, s := range sugg {
		if s == "famiglia" {
			k = i
		}
	}
	if k < 0 {
		t.Fatalf("suggerimenti per \"fami\": %v", sugg)
	}
	// il suggerimento è un tasto acceso, gli spazi vuoti sono spenti
	kb := e.cfg.KB
	for i := kb.Chars; i < len(kb.Keys); i++ {
		slot, isSugg := selector.SuggestSlot(kb.Keys[i].Char)
		on := e.prior[i] > 0
		if isSugg && on != (slot < len(sugg)) {
			t.Errorf("tasto %d: acceso %v con %d suggerimenti", slot, on, len(sugg))
		}
		if kb.Keys[i].Char == selector.KeyBackspace && !on {
			t.Error("cancella deve essere acceso")
		}
	}
	typeText(e, string(rune('1'+k)))
	if got := e.ed.String(); got != "perché l'acqua famiglia " {
		t.Errorf("dopo il suggerimento: %q", got)
	}
	// cancellare dentro la parola precedente la riapre
	typeText(e, "<<")
	if got := e.ed.String(); got != "perché l'acqua famigli" || e.ed.context() != " famigli" {
		t.Errorf("rientro nella parola: %q, contesto %q", got, e.ed.context())
	}
}

func TestOnTrack(t *testing.T) {
	cases := []struct {
		text, target string
		ok           bool
	}{
		{"", "casa bella ", true},
		{"cas", "casa bella ", true},
		{"casa ", "casa bella ", true},
		{"casa b", "casa bella ", true},
		{"cosa ", "casa bella ", false},
		{"perché ", "perche no ", true},
		{"casa bella ", "casa bella ", true},
		{"casa bella s", "casa bella ", false},
	}
	for _, c := range cases {
		if got := onTrack(c.text, c.target); got != c.ok {
			t.Errorf("onTrack(%q, %q) = %v", c.text, c.target, got)
		}
	}
}
