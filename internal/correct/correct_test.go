// SPDX-License-Identifier: AGPL-3.0-or-later

package correct

import (
	"strings"
	"testing"
	"time"

	"github.com/ablescia/kite/internal/lm"
)

var model = lm.New()

// typed costruisce selezioni nette: lo sguardo era sul tasto selezionato.
func typed(s string) []Obs {
	var out []Obs
	for i := 0; i < len(s); i++ {
		o := Obs{Char: s[i]}
		for k := range o.Like {
			o.Like[k] = 0.02 / 25
		}
		o.Like[s[i]-'a'] = 0.98
		out = append(out, o)
	}
	return out
}

// between rende ambigua la selezione i tra il tasto scritto e un altro
// (atterraggio corto, sguardo sul bordo).
func between(obs []Obs, i int, other byte, share float64) []Obs {
	o := &obs[i]
	o.Like[other-'a'] += share
	o.Like[o.Char-'a'] -= share
	return obs
}

func TestCommit(t *testing.T) {
	c := New(DefaultParams(), model)
	cases := []struct {
		name string
		obs  []Obs
		text string
		kind Kind
	}{
		{"parola giusta", typed("sangue"), "sangue", Literal},
		{"accento", typed("perche"), "perché", Accents},
		{"accento", typed("citta"), "città", Accents},
		{"e resta e", typed("e"), "e", Literal},
		{"elisione", typed("l"), "l'", Accents},
		{"selezione spuria", typed("sangkue"), "sangue", Fixed},
		{"atterraggio corto b→n", between(typed("sabgue"), 2, 'n', 0.4), "sangue", Fixed},
		{"doppia saltata", typed("pasare"), "passare", Fixed},
		{"doppia saltata", typed("tera"), "terra", Fixed},
		{"nome fuori lessico", typed("blescia"), "blescia", Unknown},
		{"parola fuori lessico", typed("fisiatra"), "fisiatra", Unknown},
	}
	for _, tc := range cases {
		r := c.Commit(tc.obs)
		if r.Text != tc.text || r.Kind != tc.kind {
			alts := []string{}
			for _, a := range r.Alts {
				alts = append(alts, a.Text)
			}
			t.Errorf("%s: %q → %q (%s, %.2f), atteso %q (%s); alternative %v", tc.name, r.Literal, r.Text, r.Kind,
				r.Post, tc.text, tc.kind, alts)
		}
	}
	// e/è: la seconda forma resta tra le alternative
	r := c.Commit(typed("e"))
	found := false
	for _, a := range r.Alts {
		found = found || a.Text == "è"
	}
	if !found {
		t.Error("\"è\" deve essere tra le alternative di \"e\"")
	}
	if r := c.Commit(typed("l")); !r.Attach {
		t.Error("\"l'\" si unisce alla parola seguente")
	}
}

func TestSuggest(t *testing.T) {
	c := New(DefaultParams(), model)
	for _, tc := range []struct{ typed, want string }{{"fami", "famiglia"}, {"grazi", "grazie"}, {"perc", "perché"}} {
		s := c.Suggest(typed(tc.typed), 3)
		var got []string
		for _, x := range s {
			got = append(got, x.Text)
		}
		if !strings.Contains(" "+strings.Join(got, " ")+" ", " "+tc.want+" ") {
			t.Errorf("%q: suggerimenti %v, atteso tra questi %q", tc.typed, got, tc.want)
		}
	}
	// con un errore nel mezzo il completamento lo scavalca
	s := c.Suggest(typed("famkig"), 3)
	if len(s) == 0 || s[0].Text != "famiglia" {
		t.Errorf("\"famkig\": %v", s)
	}
	// un refuso che "sembra italiano" non si corregge da solo, ma la
	// correzione resta tra i suggerimenti
	for _, tc := range []struct{ typed, want string }{{"fiorse", "forse"}, {"caspelli", "capelli"}, {"famigla", "famiglia"}} {
		if r := c.Commit(typed(tc.typed)); r.Kind == Fixed {
			t.Errorf("%q non doveva essere corretto da solo (%q)", tc.typed, r.Text)
		}
		found := false
		for _, x := range c.Suggest(typed(tc.typed), 3) {
			found = found || x.Text == tc.want
		}
		if !found {
			t.Errorf("%q: %q deve essere tra i suggerimenti", tc.typed, tc.want)
		}
	}
}

func TestDictionary(t *testing.T) {
	m := lm.New()
	c := New(DefaultParams(), m)
	before := c.Commit(typed("blescia"))
	m.AddWord("blescia", 50)
	c.Refresh()
	after := c.Commit(typed("blescia"))
	if after.Text != "blescia" || after.Kind != Literal {
		t.Errorf("parola del dizionario personale: %q (%s), prima %q (%s)", after.Text, after.Kind, before.Text, before.Kind)
	}
}

func TestSpeed(t *testing.T) {
	if raceOn {
		t.Skip("con il rilevatore di race i tempi non sono indicativi")
	}
	c := New(DefaultParams(), model)
	obs := typed("lavorare")
	t0 := time.Now()
	for i := 0; i < 20; i++ {
		c.Commit(obs)
		c.Suggest(obs[:4], 3)
	}
	per := time.Since(t0) / 20
	t.Logf("fine parola + suggerimenti: %v", per)
	if per > 40*time.Millisecond {
		t.Errorf("troppo lento per il ciclo a 90 Hz: %v", per)
	}
}
