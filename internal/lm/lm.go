// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Antonio Blescia

// Package lm è il modello probabilistico che guida l'espansione invisibile
// dei bersagli: dato il testo già scritto, restituisce la probabilità di
// ogni simbolo successivo (26 lettere + spazio).
//
// Due livelli, interpolati:
//   - prefisso: tra le parole del lessico che iniziano con la parola in
//     corso, quanto pesa ciascuna lettera successiva (o la fine della parola,
//     cioè lo spazio), con le frequenze delle parole;
//   - bigramma di lettere: P(lettera | lettera precedente), che copre le
//     parole fuori dal lessico.
//
// Il peso del prefisso cresce con il numero di parole che lo condividono:
// con un prefisso sconosciuto resta solo il bigramma.
package lm

import (
	"math"
	"math/rand/v2"
	"sort"
	"strings"
)

// Alphabet sono i simboli, nell'ordine degli indici; Space è lo spazio.
const (
	Alphabet = "abcdefghijklmnopqrstuvwxyz "
	N        = 27
	Space    = 26
)

// Index restituisce l'indice del simbolo, −1 se non è nell'alfabeto.
func Index(r rune) int {
	switch {
	case r == ' ':
		return Space
	case r >= 'a' && r <= 'z':
		return int(r - 'a')
	case r >= 'A' && r <= 'Z':
		return int(r - 'A')
	}
	return -1
}

// Model è il modello di lingua.
type Model struct {
	name   string
	keys   []string  // digitazioni (a–z), ordinate
	freq   []float64 // frequenza relativa di ciascuna
	cum    []float64 // cum[i] = somma di freq[:i]
	bigram [N][N]float64
	oov    *[N][N][N]float64 // log P(lettera | due precedenti) sui tipi (parole fuori lessico)
	Lex    *Lexicon          // forme scritte (nil per il modello di base)

	// parole per i testi da copiare (lessico curato, in ordine di frequenza)
	phrase     []string
	phraseFreq []float64
}

// NewBasic costruisce il modello della prima versione: 544 parole comuni,
// frequenza stimata dal rango (Zipf). Serve a rigiocare le sessioni
// registrate con quel modello.
func NewBasic() *Model {
	m := &Model{name: "base"}
	ws, fs := curated()
	m.phrase, m.phraseFreq = ws, fs
	m.build(ws, fs)
	return m
}

// New costruisce il modello dal lessico italiano grande (~48.000 parole con
// le frequenze dei sottotitoli): copre la lingua scritta davvero, non solo le
// parole dei testi da copiare.
func New() *Model {
	src := Italian()
	lex := &Lexicon{Total: src.Total, index: make(map[string]int, len(src.Entries))}
	lex.Entries = make([]Entry, len(src.Entries))
	for i, e := range src.Entries {
		e.Forms = append([]Form(nil), e.Forms...)
		lex.Entries[i] = e
		lex.index[e.Key] = i
	}
	m := &Model{name: "italiano", Lex: lex}
	m.phrase, m.phraseFreq = curated()
	m.rebuild()
	return m
}

func curated() ([]string, []float64) {
	var ws []string
	var fs []float64
	seen := map[string]bool{}
	for _, w := range strings.Fields(lexicon) {
		if seen[w] || !isWord(w) {
			continue
		}
		seen[w] = true
		ws = append(ws, w)
		fs = append(fs, 1/float64(len(ws)+2)) // Zipf sul rango
	}
	return ws, fs
}

// rebuild ricalcola le tabelle dal lessico.
func (m *Model) rebuild() {
	ws := make([]string, len(m.Lex.Entries))
	fs := make([]float64, len(m.Lex.Entries))
	for i, e := range m.Lex.Entries {
		ws[i], fs[i] = e.Key, e.Count/m.Lex.Total
	}
	m.build(ws, fs)
}

func (m *Model) build(words []string, freq []float64) {
	type wf struct {
		w string
		f float64
	}
	all := make([]wf, len(words))
	for i := range words {
		all[i] = wf{words[i], freq[i]}
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].w < all[j].w })
	m.keys, m.freq, m.cum = make([]string, len(all)), make([]float64, len(all)), make([]float64, len(all)+1)
	for i, x := range all {
		m.keys[i], m.freq[i] = x.w, x.f
		m.cum[i+1] = m.cum[i] + x.f
	}
	// Bigrammi pesati con la frequenza delle parole, con un minimo per non
	// avere transizioni impossibili.
	m.bigram = [N][N]float64{}
	for i, w := range m.keys {
		prev := Space
		for _, r := range w {
			c := Index(r)
			m.bigram[prev][c] += m.freq[i]
			prev = c
		}
		m.bigram[prev][Space] += m.freq[i]
	}
	for a := 0; a < N; a++ {
		var s float64
		for b := 0; b < N; b++ {
			m.bigram[a][b] += 1e-4
			s += m.bigram[a][b]
		}
		for b := 0; b < N; b++ {
			m.bigram[a][b] /= s
		}
	}
	m.buildOOV()
}

// buildOOV stima la forma delle parole fuori lessico: trigrammi di lettere
// contati sui tipi (ogni parola del lessico vale uno, non la sua frequenza).
// Le parole che mancano dal lessico sono rare, lunghe, nomi: somigliano
// alle parole rare, non a "di", "che", "e" che dominano le frequenze.
func (m *Model) buildOOV() {
	var tri [N][N][N]float64
	var bi [N][N]float64
	for _, w := range m.keys {
		a, b := Space, Space
		for i := 0; i <= len(w); i++ {
			c := Space
			if i < len(w) {
				c = Index(rune(w[i]))
				if c < 0 {
					continue
				}
			}
			tri[a][b][c]++
			bi[b][c]++
			a, b = b, c
		}
	}
	const k = 1.0 // ritorno ai bigrammi (sui tipi) per i trigrammi rari
	var biP [N][N]float64
	for b := 0; b < N; b++ {
		var s float64
		for c := 0; c < N; c++ {
			s += bi[b][c]
		}
		for c := 0; c < N; c++ {
			biP[b][c] = (bi[b][c] + 0.5/N) / (s + 0.5)
		}
	}
	m.oov = new([N][N][N]float64)
	for a := 0; a < N; a++ {
		for b := 0; b < N; b++ {
			var s float64
			for c := 0; c < N; c++ {
				s += tri[a][b][c]
			}
			for c := 0; c < N; c++ {
				m.oov[a][b][c] = math.Log((tri[a][b][c] + k*biP[b][c]) / (s + k))
			}
		}
	}
}

// AddWord aggiunge una parola (dizionario personale) con n occorrenze.
// Solo per il modello italiano.
func (m *Model) AddWord(w string, n float64) { m.AddWords([]string{w}, n) }

// AddWords aggiunge più parole ricostruendo le tabelle una volta sola.
func (m *Model) AddWords(ws []string, n float64) {
	if m.Lex == nil || len(ws) == 0 {
		return
	}
	for _, w := range ws {
		m.Lex.Add(w, n)
	}
	m.rebuild()
}

func isWord(w string) bool {
	for _, r := range w {
		if r < 'a' || r > 'z' {
			return false
		}
	}
	return w != ""
}

// Name è "base" (544 parole) o "italiano" (lessico grande).
func (m *Model) Name() string { return m.name }

// Words è la dimensione del lessico.
func (m *Model) Words() int { return len(m.keys) }

// Freq è la frequenza relativa di una digitazione (0 se manca).
func (m *Model) Freq(key string) float64 {
	i := sort.SearchStrings(m.keys, key)
	if i < len(m.keys) && m.keys[i] == key {
		return m.freq[i]
	}
	return 0
}

// PrefixMass è la frazione della frequenza del lessico che spetta alle
// parole che iniziano con p (1 con p vuoto): quanto è probabile che una
// parola qualsiasi inizi così.
func (m *Model) PrefixMass(p string) float64 {
	total := m.cum[len(m.keys)]
	if p == "" || total <= 0 {
		return 1
	}
	lo, hi := m.prefixRange(p)
	return (m.cum[hi] - m.cum[lo]) / total
}

// prefixRange restituisce l'intervallo delle digitazioni che iniziano con p.
func (m *Model) prefixRange(p string) (int, int) {
	lo := sort.SearchStrings(m.keys, p)
	hi := lo + sort.Search(len(m.keys)-lo, func(i int) bool { return !strings.HasPrefix(m.keys[lo+i], p) })
	return lo, hi
}

// Next restituisce P(simbolo successivo | testo scritto), normalizzata.
func (m *Model) Next(text string) [N]float64 {
	text = strings.ToLower(text)
	prefix := text[strings.LastIndexByte(text, ' ')+1:]
	prev := Space
	if prefix != "" {
		prev = Index(rune(prefix[len(prefix)-1]))
		if prev < 0 {
			prev = Space
		}
	}
	var pp [N]float64
	lo, hi := m.prefixRange(prefix)
	n := hi - lo
	total := m.cum[hi] - m.cum[lo]
	if n > 0 {
		if m.keys[lo] == prefix {
			pp[Space] = m.freq[lo]
		}
		for c := 0; c < 26; c++ {
			a, b := m.prefixRange(prefix + string(rune('a'+c)))
			pp[c] = m.cum[b] - m.cum[a]
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

// MinLetterLogP è il trigramma meno probabile della digitazione come parola
// fuori lessico (fine parola compresa): molto basso se contiene una sequenza
// che in italiano non c'è ("bng", "dp" all'inizio, "qq"), tipica di una
// lettera selezionata per sbaglio.
func (m *Model) MinLetterLogP(w string) float64 {
	a, b := Space, Space
	lp := 0.0
	for _, r := range w {
		c := Index(r)
		if c < 0 || c == Space {
			continue
		}
		lp = math.Min(lp, m.oov[a][b][c])
		a, b = b, c
	}
	return math.Min(lp, m.oov[a][b][Space])
}

// LetterLogP è il logaritmo della probabilità di una digitazione come parola
// fuori lessico (trigrammi di lettere sui tipi), fine parola compresa.
func (m *Model) LetterLogP(w string) float64 {
	a, b := Space, Space
	var lp float64
	for _, r := range w {
		c := Index(r)
		if c < 0 || c == Space {
			continue
		}
		lp += m.oov[a][b][c]
		a, b = b, c
	}
	return lp + m.oov[a][b][Space]
}

// Entropy in bit di una distribuzione (diagnostica: quanto è prevedibile il
// simbolo successivo).
func Entropy(p []float64) float64 {
	var h float64
	for _, v := range p {
		if v > 0 {
			h -= v * math.Log2(v)
		}
	}
	return h
}

// Phrase estrae nWords parole distinte (4–8 lettere) da copiare, con
// probabilità proporzionale alla radice della frequenza: parole comuni ma
// varie. used evita di riproporre le stesse parole nella sessione.
func (m *Model) Phrase(r *rand.Rand, nWords int, used map[string]bool) string {
	type cand struct {
		w string
		f float64
	}
	var cs []cand
	for i, w := range m.phrase {
		if len(w) < 4 || len(w) > 8 || excluded[w] || used[w] {
			continue
		}
		cs = append(cs, cand{w, math.Sqrt(m.phraseFreq[i])})
	}
	sort.SliceStable(cs, func(i, j int) bool { return cs[i].f > cs[j].f })
	var out []string
	for len(out) < nWords && len(cs) > 0 {
		var tot float64
		for _, c := range cs {
			tot += c.f
		}
		x := r.Float64() * tot
		k := 0
		for k < len(cs)-1 && x > cs[k].f {
			x -= cs[k].f
			k++
		}
		out = append(out, cs[k].w)
		if used != nil {
			used[cs[k].w] = true
		}
		cs = append(cs[:k], cs[k+1:]...)
	}
	return strings.Join(out, " ")
}
