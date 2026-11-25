// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Antonio Blescia

package lm

import (
	"math"
	"math/rand/v2"
	"strings"
	"testing"
)

func argmax(p [N]float64) int {
	k := 0
	for i := range p {
		if p[i] > p[k] {
			k = i
		}
	}
	return k
}

func TestNextIsDistribution(t *testing.T) {
	m := New()
	if m.Words() < 400 {
		t.Fatalf("lessico di %d parole", m.Words())
	}
	for _, ctx := range []string{"", "c", "cas", "casa", "casa ", "xqz", "buongiorn"} {
		p := m.Next(ctx)
		var s float64
		for _, v := range p {
			if v < 0 {
				t.Fatalf("%q: probabilità negativa", ctx)
			}
			s += v
		}
		if math.Abs(s-1) > 1e-9 {
			t.Errorf("%q: somma %v", ctx, s)
		}
	}
}

func TestPrediction(t *testing.T) {
	m := New()
	cases := map[string]rune{"buongiorn": 'o', "grazi": 'e', "settiman": 'a', "mangiar": 'e'}
	for ctx, want := range cases {
		p := m.Next(ctx)
		if got := rune(Alphabet[argmax(p)]); got != want {
			t.Errorf("dopo %q atteso %q, ottenuto %q (%.2f)", ctx, want, got, p[argmax(p)])
		}
	}
	// A fine parola lo spazio è probabile.
	if p := m.Next("grazie"); p[Space] < 0.5 {
		t.Errorf("P(spazio | grazie) = %.2f", p[Space])
	}
	// Prevedibilità: a metà parola l'entropia è molto più bassa che all'inizio.
	h0, h1 := Entropy(sl(m.Next(""))), Entropy(sl(m.Next("settim")))
	t.Logf("entropia: inizio parola %.2f bit, dopo \"settim\" %.2f bit (uniforme %.2f)", h0, h1, math.Log2(N))
	if h1 > h0/2 {
		t.Errorf("entropia a metà parola %.2f non abbastanza più bassa di %.2f", h1, h0)
	}
}

func sl(p [N]float64) []float64 { return p[:] }

func TestPhrase(t *testing.T) {
	m := New()
	r := rand.New(rand.NewPCG(1, 2))
	used := map[string]bool{}
	a := m.Phrase(r, 5, used)
	b := m.Phrase(r, 5, used)
	if len(strings.Fields(a)) != 5 || len(strings.Fields(b)) != 5 {
		t.Fatalf("frasi: %q, %q", a, b)
	}
	for _, w := range strings.Fields(b) {
		if strings.Contains(" "+a+" ", " "+w+" ") {
			t.Errorf("parola ripetuta tra blocchi: %q", w)
		}
	}
	t.Logf("%q / %q", a, b)
}

// Il modello di base deve dare esattamente le probabilità della prima
// versione (scorrere tutto il lessico): le sessioni registrate con quel
// modello si rigiocano con questo.
func TestBasicMatchesFirstVersion(t *testing.T) {
	m := NewBasic()
	ws, fs := curated()
	ref := func(text string) [N]float64 {
		prefix := text[strings.LastIndexByte(text, ' ')+1:]
		prev := Space
		if prefix != "" {
			prev = Index(rune(prefix[len(prefix)-1]))
		}
		var pp [N]float64
		var total float64
		n := 0
		for i, w := range ws {
			if !strings.HasPrefix(w, prefix) {
				continue
			}
			n++
			total += fs[i]
			if len(w) > len(prefix) {
				pp[Index(rune(w[len(prefix)]))] += fs[i]
			} else {
				pp[Space] += fs[i]
			}
		}
		lambda := 0.0
		if n > 0 {
			lambda = float64(n) / (float64(n) + 0.5)
		}
		var out [N]float64
		var s float64
		for c := 0; c < N; c++ {
			v := (1 - lambda) * m.bigram[prev][c]
			if total > 0 {
				v += lambda * pp[c] / total
			}
			out[c] = v
			s += v
		}
		for c := range out {
			out[c] /= s
		}
		return out
	}
	if m.Words() != 544 {
		t.Errorf("modello di base: %d parole, attese 544", m.Words())
	}
	for _, ctx := range []string{"", "c", "ca", "cas", "casa ", "settim", "xqz", "arte sa", "voce ragazzo fa"} {
		got, want := m.Next(ctx), ref(ctx)
		for c := range got {
			if math.Abs(got[c]-want[c]) > 1e-12 {
				t.Errorf("%q: simbolo %q %.15f, prima versione %.15f", ctx, Alphabet[c], got[c], want[c])
				break
			}
		}
	}
}

func TestItalianLexicon(t *testing.T) {
	l := Italian()
	if len(l.Entries) < 40000 {
		t.Fatalf("lessico di %d voci", len(l.Entries))
	}
	forms := func(key string) []string {
		e := l.Lookup(key)
		if e == nil {
			return nil
		}
		var out []string
		for _, f := range e.Forms {
			s := f.Text
			if f.Attach {
				s += "+"
			}
			out = append(out, s)
		}
		return out
	}
	cases := map[string]string{
		"perche": "perché", "citta": "città", "piu": "più", "cosi": "così", "gia": "già", "caffe": "caffè",
		"e": "e è", "si": "si sì", "la": "la là", "qui": "qui", "su": "su", "po": "po'",
		"l": "l'+ l", "un": "un un'+", "dell": "dell'+", "sangue": "sangue",
		"alla": "alla", "prima": "prima", "subito": "subito", "papa": "papà", "pero": "però",
	}
	for key, want := range cases {
		if got := strings.Join(forms(key), " "); !strings.HasPrefix(got, want) {
			t.Errorf("%q: forme %q, attese all'inizio %q", key, got, want)
		}
	}
	for _, bad := range []string{"grae", "didn", "mr"} {
		if e := l.Lookup(bad); e != nil {
			for _, f := range e.Forms {
				if strings.Contains(f.Text, "'") || strings.Contains(f.Text, ".") {
					t.Errorf("forma spuria %q", f.Text)
				}
			}
		}
	}
	if Fold("Perché L'acqua è là") != "perche lacqua e la" {
		t.Errorf("Fold: %q", Fold("Perché L'acqua è là"))
	}
}

func TestAddWord(t *testing.T) {
	m := New()
	if m.Freq("blescia") != 0 {
		t.Fatal("parola già presente")
	}
	m.AddWord("Blescia", 200)
	if m.Freq("blescia") == 0 || m.Lex.Lookup("blescia") == nil {
		t.Fatal("parola personale non aggiunta")
	}
	if p := m.Next("blesci"); argmax(p) != Index('a') {
		t.Errorf("dopo \"blesci\" la A deve essere la più probabile")
	}
	if New().Freq("blescia") != 0 {
		t.Error("il dizionario personale non deve toccare il lessico condiviso")
	}
}

func TestPrefixMass(t *testing.T) {
	m := New()
	if m.PrefixMass("") != 1 {
		t.Error("prefisso vuoto: 1")
	}
	h, ho, hx := m.PrefixMass("h"), m.PrefixMass("ho"), m.PrefixMass("hx")
	if !(h > ho && ho > hx && hx >= 0) {
		t.Errorf("h %.4f, ho %.4f, hx %.6f: deve scendere allungando il prefisso", h, ho, hx)
	}
	var s float64
	for c := 'a'; c <= 'z'; c++ {
		s += m.PrefixMass(string(c))
	}
	if math.Abs(s-1) > 1e-9 {
		t.Errorf("le iniziali sommano a %v", s)
	}
}
