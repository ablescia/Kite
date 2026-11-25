// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Antonio Blescia

package ngram

import (
	"fmt"
	"math/rand/v2"
	"strings"
	"testing"
)

func learnAll(m *Model, phrases ...string) {
	for _, p := range phrases {
		m.Learn(p)
	}
}

func TestTokenize(t *testing.T) {
	cases := map[string]string{
		"vorrei dell'acqua":   "vorrei|dell'+|acqua",
		"un po' di pane":      "un|po'|di|pane",
		"Ciao, come stai?":    "ciao|come|stai",
		"perché NON vieni":    "perché|non|vieni",
		"  l’altro  giorno  ": "l'+|altro|giorno",
		"'ciao'":              "ciao'",
	}
	for in, want := range cases {
		var parts []string
		for _, tk := range Tokenize(in) {
			s := tk.Text
			if tk.Attach {
				s += "+"
			}
			parts = append(parts, s)
		}
		if got := strings.Join(parts, "|"); got != want {
			t.Errorf("Tokenize(%q) = %q, atteso %q", in, got, want)
		}
	}
	if tk := Tokenize("dell'acqua")[0]; tk.Key != "dell" {
		t.Errorf("digitazione di dell': %q", tk.Key)
	}
}

func TestPredictCompletesLearnedPhrase(t *testing.T) {
	m := New(DefaultParams())
	learnAll(m, "ho bisogno di aiuto", "ho bisogno di aiuto", "ho sete")
	cases := []struct{ text, ghost, full string }{
		{"ho bisogno d", "i aiuto", "di aiuto"},
		{"ho bisogno ", "di aiuto", "di aiuto"},
		{"ho bis", "ogno di aiuto", "bisogno di aiuto"},
		{"ho se", "te", "sete"},
		{"HO BISOGNO D", "i aiuto", "di aiuto"}, // maiuscole
	}
	for _, c := range cases {
		p, ok := m.Predict(c.text)
		if !ok {
			t.Errorf("%q: nessuna proposta", c.text)
			continue
		}
		if p.Ghost != c.ghost || p.Text != c.full {
			t.Errorf("%q: fantasma %q, testo %q (attesi %q, %q), p %.2f", c.text, p.Ghost, p.Text, c.ghost, c.full, p.Prob)
		}
		if p.Prob < m.P.MinProb || p.Prob > 1 {
			t.Errorf("%q: probabilità %.3f", c.text, p.Prob)
		}
	}
	// frase finita, parola sconosciuta, lettere che nessuna frase ha: niente
	for _, text := range []string{"ho bisogno di aiuto ", "ho bisogno di aiuto", "ho bisogno dx", "ciao ", "zz"} {
		if p, ok := m.Predict(text); ok {
			t.Errorf("%q: proposta inattesa %q (p %.2f)", text, p.Ghost, p.Prob)
		}
	}
}

// Le forme scritte tornano come sono state imparate: accenti ed elisioni.
func TestPredictForms(t *testing.T) {
	m := New(DefaultParams())
	learnAll(m, "vorrei dell'acqua fresca", "perché non vieni a trovarmi")
	cases := []struct{ text, ghost, full string }{
		{"vorrei del", "l'acqua fresca", "dell'acqua fresca"},
		{"vorrei dell", "'acqua fresca", "dell'acqua fresca"},
		{"vorrei dell'ac", "qua fresca", "acqua fresca"},
		{"perch", "é non vieni a trovarmi", "perché non vieni a trovarmi"},
		{"perche ", "non vieni a trovarmi", "non vieni a trovarmi"}, // senza accento, come si digita
	}
	for _, c := range cases {
		p, ok := m.Predict(c.text)
		if !ok || p.Ghost != c.ghost || p.Text != c.full {
			t.Errorf("%q: %v fantasma %q, testo %q (attesi %q, %q)", c.text, ok, p.Ghost, p.Text, c.ghost, c.full)
		}
	}
}

// La frase più usata vince; quando le frasi si dividono senza che nessuna
// prevalga, il modello tace.
func TestPredictRanksAndAbstains(t *testing.T) {
	m := New(DefaultParams())
	learnAll(m, "ho sete", "ho sete", "ho sete", "ho fame")
	if p, ok := m.Predict("ho "); !ok || p.Text != "sete" {
		t.Errorf("«ho »: %v %q", ok, p.Text)
	}
	if p, ok := m.Predict("ho f"); !ok || p.Text != "fame" {
		t.Errorf("«ho f»: %v %q (la lettera scritta conta più della frequenza)", ok, p.Text)
	}
	m2 := New(DefaultParams())
	learnAll(m2, "mi fa male la testa", "mi fa male la gamba", "mi fa male la schiena", "mi fa male il braccio")
	if p, ok := m2.Predict("mi fa male la "); ok {
		t.Errorf("tre continuazioni alla pari: proposta %q (p %.2f)", p.Text, p.Prob)
	}
	if p, ok := m2.Predict("mi fa male la g"); !ok || p.Text != "gamba" {
		t.Errorf("«mi fa male la g»: %v %q", ok, p.Text)
	}
}

// Il contesto di tre parole guida anche dentro un messaggio iniziato in
// modo diverso da quelli imparati.
func TestPredictMidSentence(t *testing.T) {
	m := New(DefaultParams())
	learnAll(m, "per favore chiamate mia figlia", "per favore chiamate mia figlia")
	if p, ok := m.Predict("allora per favore chiamate m"); !ok || p.Text != "mia figlia" {
		t.Errorf("a metà frase: %v %q", ok, p.Text)
	}
}

// Con una sola frase imparata la proposta c'è, ma la probabilità tiene
// conto che la persona può scrivere altro.
func TestPredictSingleExample(t *testing.T) {
	m := New(DefaultParams())
	m.Learn("ho sete")
	p, ok := m.Predict("h")
	if !ok || p.Text != "ho sete" {
		t.Fatalf("«h»: %v %q", ok, p.Text)
	}
	if p.Prob > 0.9 {
		t.Errorf("una frase sola non dà certezza: p %.2f", p.Prob)
	}
	if New(DefaultParams()).Learn("  , ? ") {
		t.Error("un testo senza parole non è una frase")
	}
	if _, ok := New(DefaultParams()).Predict("ho"); ok {
		t.Error("modello vuoto: nessuna proposta")
	}
}

func TestAfterLetters(t *testing.T) {
	cases := []struct {
		s    string
		n    int
		want string
	}{{"dell'", 3, "l'"}, {"dell'", 4, "'"}, {"perché", 5, "é"}, {"perché", 6, ""}, {"ciao", 0, "ciao"}}
	for _, c := range cases {
		if got := afterLetters(c.s, c.n); got != c.want {
			t.Errorf("afterLetters(%q, %d) = %q, atteso %q", c.s, c.n, got, c.want)
		}
	}
}

// Molte frasi imparate: la proposta si calcola a ogni lettera, dentro il
// lock dell'engine, e deve restare nell'ordine del millisecondo.
func BenchmarkPredict(b *testing.B) {
	m := New(DefaultParams())
	r := rand.New(rand.NewPCG(1, 2))
	words := strings.Fields("io tu ho sono vorrei mi ci per favore acqua sete fame bagno letto luce chiamate " +
		"figlia figlio dottore aiuto male testa gamba braccio schiena caldo freddo girare aprire finestra porta " +
		"televisione radio musica grazie ciao bene stanco dormire mangiare bere adesso dopo domani oggi casa")
	for i := 0; i < 3000; i++ {
		n := 2 + r.IntN(7)
		var ws []string
		for k := 0; k < n; k++ {
			ws = append(ws, words[r.IntN(len(words))])
		}
		m.Learn(strings.Join(ws, " "))
	}
	texts := []string{"ho ", "mi fa m", "vorrei ", "per favore ch", "a"}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.Predict(texts[i%len(texts)])
	}
}

func Example() {
	m := New(DefaultParams())
	m.Learn("ho bisogno di aiuto")
	m.Learn("ho bisogno di aiuto")
	p, _ := m.Predict("ho bisogno d")
	fmt.Printf("%q %q\n", p.Ghost, p.Text)
	// Output: "i aiuto" "di aiuto"
}
