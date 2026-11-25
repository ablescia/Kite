// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Antonio Blescia

// Package ngram predice il resto della frase che si sta scrivendo, con un
// modello a n-grammi di parole imparato dalle frasi che l'utente dice.
//
// Chi comunica con una tastiera assistiva ripete spesso gli stessi messaggi
// ("ho sete", "mi giri sul fianco", "chiamate mia figlia"): dopo poche
// lettere il modello riconosce la frase e la propone intera, da accettare
// con un solo gesto invece di scriverla lettera per lettera.
//
// Il modello conta gli n-grammi di parole delle frasi imparate (fino a
// Order: con 4, tre parole di contesto), con l'inizio <s> e la fine </s>
// della frase. La probabilità della parola successiva interpola gli ordini
// con il metodo di Witten-Bell:
//
//	P(w | h) = (c(h w) + T(h) · P(w | h')) / (c(h) + T(h))
//
// dove h' è il contesto h senza la parola più lontana e T(h) il numero di
// parole diverse viste dopo h. Il primo ordine riserva T/(N+T) alle parole
// mai viste: con poche frasi imparate il modello sa di sapere poco, e la
// probabilità che la persona scriva qualcosa di nuovo resta nel conto.
//
// La parola in corso vincola la prima parola della proposta: conta tra le
// parole che iniziano con le lettere scritte, comprese quelle mai viste che
// iniziano così (stimate dal lessico italiano, PrefixMass). Una ricerca a
// fascio cerca il completamento più probabile fino alla fine della frase e
// lo propone se la sua probabilità, parola per parola, supera MinProb.
package ngram

import (
	"math"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/ablescia/kite/internal/lm"
)

const (
	bos = "<s>"  // inizio della frase (solo come contesto)
	eos = "</s>" // fine della frase
)

// Params sono i parametri del modello e della ricerca.
type Params struct {
	Order    int     // lunghezza massima degli n-grammi (contesto = Order−1 parole)
	Beam     int     // ipotesi tenute a ogni passo della ricerca
	MaxWords int     // parole massime di una proposta
	MinProb  float64 // probabilità minima del completamento per proporlo
}

// DefaultParams sono i valori di partenza.
func DefaultParams() Params { return Params{Order: 4, Beam: 8, MaxWords: 25, MinProb: 0.3} }

// Token è una parola di una frase.
type Token struct {
	Key    string // digitazione sulla tastiera di 26 lettere: "perche", "dell"
	Text   string // forma scritta: "perché", "dell'"
	Attach bool   // elisione: si unisce alla parola seguente senza spazio
}

// Tokenize divide un testo in parole minuscole. Le elisioni si staccano
// dalla parola che segue ("dell'acqua" → "dell'", "acqua"); tutto ciò che
// non è una lettera o un apostrofo separa le parole.
func Tokenize(s string) []Token {
	var out []Token
	var cur []rune
	flush := func(attach bool) {
		if len(cur) == 0 {
			return
		}
		text := string(cur)
		if key := lm.Fold(text); key != "" {
			out = append(out, Token{Key: key, Text: text, Attach: attach})
		}
		cur = cur[:0]
	}
	rs := []rune(strings.ToLower(s))
	for i, r := range rs {
		switch {
		case r == '\'' || r == '’':
			if len(cur) == 0 {
				continue
			}
			cur = append(cur, '\'')
			if i+1 < len(rs) && unicode.IsLetter(rs[i+1]) {
				flush(true)
			}
		case unicode.IsLetter(r):
			cur = append(cur, r)
		default:
			flush(false)
		}
	}
	flush(false)
	return out
}

// split separa il testo già chiuso dalla parola in corso (le lettere dopo
// l'ultimo spazio o apostrofo).
func split(text string) (committed, partial string) {
	i := strings.LastIndexFunc(text, func(r rune) bool { return !unicode.IsLetter(r) })
	if i < 0 {
		return "", text
	}
	_, w := utf8.DecodeRuneInString(text[i:])
	return text[:i+w], text[i+w:]
}

type node struct {
	total float64
	next  map[string]float64
}

// form è un modo di scrivere una parola.
type form struct {
	text   string
	attach bool
}

// Model è il modello delle frasi imparate. Non è sicuro per l'uso
// concorrente: chi lo usa da più goroutine lo protegge con un lock.
type Model struct {
	P Params
	// PrefixMass è la frazione delle parole italiane (pesate per frequenza)
	// che iniziano con il prefisso: stima quanto è probabile che la parola in
	// corso sia una parola mai usata nelle frasi. nil: 0,07 per lettera.
	PrefixMass func(prefix string) float64

	nodes   map[string]*node // contesto (digitazioni separate da spazi; "" = primo ordine) → parole seguenti
	forms   map[string]map[form]float64
	vocab   []string // digitazioni ordinate (senza </s>); nil = da ricostruire
	top     []string // le più frequenti al primo ordine (con </s>), per i contesti mai visti
	phrases int
}

// New crea un modello vuoto.
func New(p Params) *Model {
	return &Model{P: p, nodes: map[string]*node{}, forms: map[string]map[form]float64{}}
}

// Phrases è il numero di frasi imparate (con le ripetizioni).
func (m *Model) Phrases() int { return m.phrases }

// Words è il numero di parole diverse nelle frasi imparate.
func (m *Model) Words() int { return len(m.forms) }

// Learn impara una frase (un messaggio). Restituisce false se il testo non
// contiene parole.
func (m *Model) Learn(text string) bool {
	toks := Tokenize(text)
	if len(toks) == 0 {
		return false
	}
	seq := make([]string, 0, len(toks)+2)
	seq = append(seq, bos)
	for _, t := range toks {
		seq = append(seq, t.Key)
		fs := m.forms[t.Key]
		if fs == nil {
			fs = map[form]float64{}
			m.forms[t.Key] = fs
		}
		fs[form{t.Text, t.Attach}]++
	}
	seq = append(seq, eos)
	for i := 1; i < len(seq); i++ {
		for k := 0; k < m.P.Order && k <= i; k++ {
			ctx := strings.Join(seq[i-k:i], " ")
			n := m.nodes[ctx]
			if n == nil {
				n = &node{next: map[string]float64{}}
				m.nodes[ctx] = n
			}
			n.total++
			n.next[seq[i]]++
		}
	}
	m.vocab, m.top = nil, nil
	m.phrases++
	return true
}

func (m *Model) index() {
	if m.vocab != nil {
		return
	}
	m.vocab = make([]string, 0, len(m.forms))
	for k := range m.forms {
		m.vocab = append(m.vocab, k)
	}
	sort.Strings(m.vocab)
	u := m.nodes[""]
	m.top = make([]string, 0, len(u.next))
	for w := range u.next {
		m.top = append(m.top, w)
	}
	sort.Slice(m.top, func(i, j int) bool {
		a, b := u.next[m.top[i]], u.next[m.top[j]]
		return a > b || a == b && m.top[i] < m.top[j]
	})
	if len(m.top) > m.P.Beam {
		m.top = m.top[:m.P.Beam]
	}
}

// chain restituisce i nodi dei contesti di h, dal primo ordine al più
// lungo che esiste (al massimo Order−1 parole).
func (m *Model) chain(h []string, dst []*node) []*node {
	dst = append(dst[:0], m.nodes[""])
	for k := 1; k < m.P.Order && k <= len(h); k++ {
		n := m.nodes[strings.Join(h[len(h)-k:], " ")]
		if n == nil {
			break // un contesto più lungo lo contiene: non c'è nemmeno lui
		}
		dst = append(dst, n)
	}
	return dst
}

// prob è P(w | contesto) interpolata con Witten-Bell lungo la catena; w = ""
// è una qualsiasi parola mai vista.
func prob(chain []*node, w string) float64 {
	u := chain[0]
	t := float64(len(u.next))
	var p float64
	if w == "" {
		p = t / (u.total + t)
	} else {
		p = u.next[w] / (u.total + t)
	}
	for _, n := range chain[1:] {
		t = float64(len(n.next))
		c := 0.0
		if w != "" {
			c = n.next[w]
		}
		p = (c + t*p) / (n.total + t)
	}
	return p
}

// candidates sono le parole da provare dopo il contesto: quelle che
// iniziano con prefix se la parola è già iniziata, altrimenti quelle viste
// dopo l'ultima parola (ogni continuazione di un contesto più lungo lo è
// anche dell'ultima parola), o le più frequenti se l'ultima parola è nuova.
func (m *Model) candidates(chain []*node, prefix string, dst []string) []string {
	dst = dst[:0]
	switch {
	case prefix != "":
		i := sort.SearchStrings(m.vocab, prefix)
		for ; i < len(m.vocab) && strings.HasPrefix(m.vocab[i], prefix); i++ {
			dst = append(dst, m.vocab[i])
		}
	case len(chain) > 1:
		for w := range chain[1].next {
			dst = append(dst, w)
		}
		sort.Strings(dst) // ordine stabile: a parità di probabilità vince sempre la stessa
	default:
		dst = append(dst, m.top...)
	}
	return dst
}

func (m *Model) prefixMass(p string) float64 {
	if p == "" {
		return 1
	}
	if m.PrefixMass != nil {
		return m.PrefixMass(p)
	}
	return math.Pow(0.07, float64(len(p)))
}

// Prediction è una frase proposta.
type Prediction struct {
	Words []Token // parole proposte: la prima completa quella in corso
	Text  string  // testo che sostituisce la parola in corso: "di aiuto"
	Ghost string  // ciò che manca dopo il testo scritto: "i aiuto"
	Prob  float64 // probabilità del completamento
}

type hyp struct {
	keys []string
	logp float64
}

// Predict propone il resto della frase dato il testo scritto finora (dal
// suo inizio): false se nessun completamento arriva a MinProb.
func (m *Model) Predict(text string) (Prediction, bool) {
	if m.nodes[""] == nil {
		return Prediction{}, false
	}
	m.index()
	committed, partial := split(strings.ToLower(text))
	hist := []string{bos}
	for _, t := range Tokenize(committed) {
		hist = append(hist, t.Key)
	}
	pkey := lm.Fold(partial)
	minLog := math.Log(m.P.MinProb)
	bestLog := math.Inf(-1)
	var best []string

	beam := []hyp{{}}
	var chain []*node
	var cands []string
	ctx := make([]string, 0, m.P.Order)
	for step := 0; step <= m.P.MaxWords && len(beam) > 0; step++ {
		var next []hyp
		for _, h := range beam {
			ctx = ctx[:0]
			ctx = append(ctx, hist...)
			ctx = append(ctx, h.keys...)
			if n := m.P.Order - 1; len(ctx) > n {
				ctx = ctx[len(ctx)-n:]
			}
			chain = m.chain(ctx, chain)
			prefix := ""
			if step == 0 {
				prefix = pkey
			}
			cands = m.candidates(chain, prefix, cands)
			z := 1.0
			if prefix != "" {
				// condizionata alle lettere scritte: tra le parole che iniziano
				// così, viste o no
				z = prob(chain, "") * m.prefixMass(prefix)
				for _, w := range cands {
					z += prob(chain, w)
				}
			}
			for _, w := range cands {
				lp := h.logp + math.Log(prob(chain, w)/z)
				if lp < minLog || lp <= bestLog {
					continue // le probabilità possono solo scendere
				}
				if w == eos {
					if len(h.keys) > 1 || len(h.keys) == 1 && len(h.keys[0]) > len(pkey) {
						best, bestLog = h.keys, lp
					}
					continue
				}
				if step == m.P.MaxWords {
					continue
				}
				keys := make([]string, len(h.keys)+1)
				copy(keys, h.keys)
				keys[len(h.keys)] = w
				next = append(next, hyp{keys: keys, logp: lp})
			}
		}
		sort.SliceStable(next, func(i, j int) bool { return next[i].logp > next[j].logp })
		if len(next) > m.P.Beam {
			next = next[:m.P.Beam]
		}
		beam = next
	}
	if best == nil {
		return Prediction{}, false
	}
	return m.build(best, pkey, math.Exp(bestLog)), true
}

// build scrive le parole proposte con la loro forma più usata.
func (m *Model) build(keys []string, pkey string, p float64) Prediction {
	pr := Prediction{Prob: p, Words: make([]Token, len(keys))}
	var b strings.Builder
	for i, k := range keys {
		f := m.bestForm(k)
		pr.Words[i] = Token{Key: k, Text: f.text, Attach: f.attach}
		b.WriteString(f.text)
		if i < len(keys)-1 && !f.attach {
			b.WriteByte(' ')
		}
	}
	pr.Text = b.String()
	first := pr.Words[0].Text
	pr.Ghost = afterLetters(first, len(pkey)) + pr.Text[len(first):]
	return pr
}

func (m *Model) bestForm(key string) form {
	var best form
	bc := -1.0
	for f, c := range m.forms[key] {
		if c > bc || c == bc && f.text < best.text {
			best, bc = f, c
		}
	}
	return best
}

// afterLetters è ciò che resta di s dopo le prime n lettere (gli apostrofi
// non si digitano e non contano).
func afterLetters(s string, n int) string {
	for i, r := range s {
		if n == 0 {
			return s[i:]
		}
		if r != '\'' && r != '’' {
			n--
		}
	}
	return ""
}
