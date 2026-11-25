// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Antonio Blescia

// Package correct è il correttore automatico italiano: a fine parola sceglie
// la parola più probabile date le lettere selezionate con lo sguardo, come
// la correzione delle tastiere dei telefoni, e mentre si scrive propone i
// completamenti.
//
// È un modello a canale rumoroso (noisy channel):
//
//	P(parola | selezioni) ∝ P(parola) · P(selezioni | parola)
//
// P(parola) viene dal lessico italiano (frequenze dei sottotitoli, più il
// dizionario personale). P(selezioni | parola) descrive gli errori di una
// tastiera a sguardo, misurati sulle sessioni reali:
//   - ogni lettera voluta produce una selezione, con la probabilità che lo
//     sguardo indichi davvero quel tasto (Obs.Like: il decoder spaziale e,
//     dopo una saccade lunga, il modello dell'atterraggio corto, per cui una
//     "b" raggiunta da sinistra può essere una "n");
//   - una selezione può essere spuria (PIns): un'occhiata di ricerca o una
//     doppia involontaria, la prima causa di errore misurata;
//   - una lettera voluta può mancare (PDel), più spesso la seconda di una
//     doppia (PDelDouble), che con lo sguardo costa un gesto in più.
//
// Gli accenti e gli apostrofi non si digitano: "perche" diventa "perché",
// "citta" "città", "l" + spazio "l'" unito alla parola dopo.
package correct

import (
	"math"
	"sort"

	"github.com/ablescia/kite/internal/lm"
)

// Obs è una lettera selezionata.
type Obs struct {
	Char byte        // 'a'..'z'
	Like [26]float64 // quanto lo sguardo indica ciascuna lettera (somma 1)
}

// Certain è un'osservazione senza incertezza (per esempio ricostruita dal
// testo dopo una cancellazione).
func Certain(c byte) Obs {
	o := Obs{Char: c}
	if c >= 'a' && c <= 'z' {
		o.Like[c-'a'] = 1
	}
	return o
}

// Params sono i parametri del modello degli errori.
type Params struct {
	PIns       float64 // selezione spuria
	PDel       float64 // lettera voluta non selezionata
	PDelDouble float64 // seconda lettera di una doppia non selezionata
	Floor      float64 // quota uniforme nelle Like: errori che lo sguardo non spiega
	POOV       float64 // probabilità a priori di una parola fuori lessico
	MinPost    float64 // la correzione automatica richiede almeno questa probabilità
	Band       int     // differenza massima tra selezioni e lettere della parola
	// KeepKnown: una parola del lessico con almeno questa frequenza
	// relativa non viene sostituita (solo accenti e apostrofi): cambiare una
	// parola valida è peggio di lasciare un refuso. Le alternative restano
	// nella barra dei suggerimenti.
	KeepKnown float64
	// Plausible: una parola fuori lessico che "sembra italiana" (nessun
	// trigramma di lettere sotto questo logaritmo di probabilità) non viene
	// sostituita: è più probabilmente una parola rara o un nome che un
	// errore. Le selezioni spurie dello sguardo creano sequenze che in
	// italiano non ci sono ("sabngue", "dpensiero"). 0: nessun filtro.
	Plausible float64
}

// DefaultParams sono i valori di partenza.
func DefaultParams() Params {
	return Params{PIns: 0.12, PDel: 0.02, PDelDouble: 0.25, Floor: 0.02, POOV: 0.03, MinPost: 0.5, Band: 3,
		KeepKnown: 1e-6, Plausible: -5}
}

// Cand è una parola candidata.
type Cand struct {
	Key    string  // digitazione: "perche"
	Text   string  // forma scritta: "perché"
	Attach bool    // elisione da unire alla parola seguente
	LogP   float64 // log P(parola) + log P(selezioni | parola)
	Post   float64 // probabilità tra i candidati considerati
	Next   int     // completamento: lettere di Key già coperte dalle selezioni
}

// Corrector è il correttore.
type Corrector struct {
	P Params
	M *lm.Model // modello con il lessico italiano (Lex non nil)

	order  []int     // voci in ordine di frequenza decrescente
	logF   []float64 // log P(voce), nello stesso ordine
	orderN int
	d      []float64
	em     [][26]float64
	plaus  [26]int // selezioni per cui la lettera è plausibile (vedi rankEntries)
	delta  float64 // costo minimo di un errore
}

// New crea un correttore sul modello m.
func New(p Params, m *lm.Model) *Corrector {
	return &Corrector{P: p, M: m}
}

func (c *Corrector) sortEntries() {
	es := c.M.Lex.Entries
	if c.orderN == len(es) && c.order != nil {
		return
	}
	c.order = make([]int, len(es))
	for i := range c.order {
		c.order[i] = i
	}
	sort.SliceStable(c.order, func(a, b int) bool { return es[c.order[a]].Count > es[c.order[b]].Count })
	c.logF = make([]float64, len(es))
	for i, idx := range c.order {
		c.logF[i] = math.Log(1-c.P.POOV) + math.Log(es[idx].Count/c.M.Lex.Total)
	}
	c.orderN = len(es)
}

// Refresh va chiamato dopo aver aggiunto parole al lessico.
func (c *Corrector) Refresh() { c.orderN = -1 }

// emissions prepara i costi delle selezioni.
func (c *Corrector) emissions(obs []Obs) (ins float64, best float64) {
	p := c.P
	if cap(c.em) < len(obs) {
		c.em = make([][26]float64, len(obs))
	}
	c.em = c.em[:len(obs)]
	ins = math.Log(p.PIns / 26)
	match := math.Log((1 - p.PIns) * (1 - p.PDel))
	for i, o := range obs {
		var s float64
		for k := 0; k < 26; k++ {
			s += o.Like[k]
		}
		m := math.Inf(-1)
		for k := 0; k < 26; k++ {
			l := p.Floor / 26
			if s > 0 {
				l += (1 - p.Floor) * o.Like[k] / s
			}
			c.em[i][k] = match + math.Log(l)
			m = math.Max(m, c.em[i][k])
		}
		best += math.Max(m, ins)
	}
	// Costo minimo di un errore rispetto al miglior caso: una lettera
	// saltata (non doppia), una selezione spuria o una sostituzione con una
	// lettera non plausibile (oltre delta dalla migliore). Le doppie saltate
	// costano meno e si contano a parte.
	c.delta = -math.Log(p.PDel)
	c.plaus = [26]int{}
	for i := range obs {
		bi := math.Inf(-1)
		for k := 0; k < 26; k++ {
			bi = math.Max(bi, c.em[i][k])
		}
		c.delta = math.Min(c.delta, bi-ins)
	}
	for i := range obs {
		bi := math.Inf(-1)
		for k := 0; k < 26; k++ {
			bi = math.Max(bi, c.em[i][k])
		}
		for k := 0; k < 26; k++ {
			if bi-c.em[i][k] < c.delta {
				c.plaus[k]++
			}
		}
	}
	return ins, best
}

// align calcola il miglior allineamento delle selezioni con la parola w.
// complete: la parola deve essere coperta tutta; altrimenti le selezioni
// coprono un suo inizio e restituisce anche quante lettere.
func (c *Corrector) align(w string, n int, ins float64, complete bool) (float64, int) {
	band := c.P.Band
	m := len(w)
	if !complete {
		m = min(m, n+band) // le lettere oltre non possono essere allineate
	}
	cols := m + 1
	if need := (n + 1) * cols; cap(c.d) < need {
		c.d = make([]float64, need)
	}
	d := c.d[:(n+1)*cols]
	for i := range d {
		d[i] = math.Inf(-1)
	}
	lDel, lDelDouble := math.Log(c.P.PDel), math.Log(c.P.PDelDouble)
	d[0] = 0
	for i := 0; i <= n; i++ {
		jLo, jHi := max(0, i-band), min(m, i+band)
		for j := jLo; j <= jHi; j++ {
			v := d[i*cols+j]
			if math.IsInf(v, -1) {
				continue
			}
			if j < m {
				// lettera saltata
				del := lDel
				if j > 0 && w[j] == w[j-1] {
					del = lDelDouble
				}
				if x := v + del; x > d[i*cols+j+1] {
					d[i*cols+j+1] = x
				}
			}
			if i < n {
				// selezione spuria
				if x := v + ins; x > d[(i+1)*cols+j] {
					d[(i+1)*cols+j] = x
				}
				// selezione della lettera j
				if j < m {
					if x := v + c.em[i][w[j]-'a']; x > d[(i+1)*cols+j+1] {
						d[(i+1)*cols+j+1] = x
					}
				}
			}
		}
	}
	if complete {
		return d[n*cols+m], m
	}
	bestV, bestJ := math.Inf(-1), 0
	for j := max(0, n-band); j <= min(m, n+band); j++ {
		if d[n*cols+j] > bestV {
			bestV, bestJ = d[n*cols+j], j
		}
	}
	return bestV, bestJ
}

// rankEntries restituisce le migliori k voci (una per digitazione).
func (c *Corrector) rankEntries(obs []Obs, complete bool, k int) []Cand {
	c.sortEntries()
	n := len(obs)
	if n == 0 {
		return nil
	}
	ins, best := c.emissions(obs)
	lex := c.M.Lex
	var top []Cand // ordinati per LogP decrescente, al più k
	for oi, idx := range c.order {
		e := &lex.Entries[idx]
		lw := c.logF[oi]
		if len(top) == k && lw+best < top[k-1].LogP {
			break // le voci successive sono meno frequenti: nessuna può entrare
		}
		m := len(e.Key)
		if complete && (m < n-c.P.Band || m > n+c.P.Band) || !complete && m < n-c.P.Band {
			continue
		}
		if len(top) == k {
			// Limite rapido: le selezioni che non trovano nella parola una
			// lettera plausibile, e (a parola finita) le lettere che non
			// trovano una selezione, costano almeno delta ciascuna.
			mm := m
			if !complete {
				mm = min(m, n+c.P.Band)
			}
			var cw [26]int
			dbl := 0
			for j := 0; j < mm; j++ {
				cw[e.Key[j]-'a']++
				if j > 0 && e.Key[j] == e.Key[j-1] {
					dbl++
				}
			}
			free := 0
			for k := 0; k < 26; k++ {
				free += min(cw[k], c.plaus[k])
			}
			free = min(free, n, mm)
			errs := n - free // selezioni senza una lettera plausibile
			if complete {
				errs = max(errs, mm-free-dbl) // lettere senza una selezione (le doppie a parte)
			}
			if lw+best-float64(errs)*c.delta < top[k-1].LogP {
				continue
			}
		}
		s, next := c.align(e.Key, n, ins, complete)
		if math.IsInf(s, -1) {
			continue
		}
		cand := Cand{Key: e.Key, LogP: lw + s, Next: next}
		if len(top) < k {
			top = append(top, cand)
		} else if cand.LogP > top[k-1].LogP {
			top[k-1] = cand
		} else {
			continue
		}
		for i := len(top) - 1; i > 0 && top[i].LogP > top[i-1].LogP; i-- {
			top[i], top[i-1] = top[i-1], top[i]
		}
	}
	return top
}

// literal è la digitazione delle selezioni.
func literal(obs []Obs) string {
	b := make([]byte, len(obs))
	for i, o := range obs {
		b[i] = o.Char
	}
	return string(b)
}

// oovLogP è la probabilità che le selezioni siano esattamente una parola
// fuori lessico.
func (c *Corrector) oovLogP(obs []Obs) float64 {
	c.emissions(obs)
	lp := math.Log(c.P.POOV) + c.M.LetterLogP(literal(obs))
	for i, o := range obs {
		if o.Char >= 'a' && o.Char <= 'z' {
			lp += c.em[i][o.Char-'a']
		}
	}
	return lp
}

// normalize assegna Post sui candidati (più un'eventuale alternativa con
// logaritmo extra, −Inf se assente) e restituisce la probabilità di questa.
func normalize(cs []Cand, extra float64) float64 {
	m := extra
	for _, c := range cs {
		m = math.Max(m, c.LogP)
	}
	s := math.Exp(extra - m)
	for _, c := range cs {
		s += math.Exp(c.LogP - m)
	}
	for i := range cs {
		cs[i].Post = math.Exp(cs[i].LogP-m) / s
	}
	return math.Exp(extra-m) / s
}

// forms espande le voci nelle loro forme scritte.
func (c *Corrector) forms(es []Cand) []Cand {
	var out []Cand
	for _, e := range es {
		ent := c.M.Lex.Lookup(e.Key)
		if ent == nil {
			continue
		}
		for _, f := range ent.Forms {
			x := e
			x.Text, x.Attach = f.Text, f.Attach
			share := f.Count / ent.Count
			x.LogP += math.Log(share)
			x.Post *= share
			out = append(out, x)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].LogP > out[j].LogP })
	return out
}

// Suggest restituisce fino a n parole per completare quella in corso (forme
// scritte distinte, dalla più probabile).
func (c *Corrector) Suggest(obs []Obs, n int) []Cand {
	es := c.rankEntries(obs, false, 4*n)
	normalize(es, math.Inf(-1))
	fs := c.forms(es)
	if len(fs) > n {
		fs = fs[:n]
	}
	return fs
}

// undoubled riduce le lettere doppie a una: "passare" → "pasare".
func undoubled(w string) string {
	b := make([]byte, 0, len(w))
	for i := 0; i < len(w); i++ {
		if i == 0 || w[i] != w[i-1] {
			b = append(b, w[i])
		}
	}
	return string(b)
}

// Kind dice che cosa ha fatto il correttore a fine parola.
type Kind string

const (
	Literal Kind = "letterale" // scritta come selezionata
	Accents Kind = "accenti"   // stesse lettere, forma con accenti o apostrofo
	Fixed   Kind = "corretta"  // sostituita con un'altra parola
	Unknown Kind = "sconosciuta"
)

// Result è la decisione a fine parola.
type Result struct {
	Literal string
	Text    string
	Attach  bool
	Kind    Kind
	Post    float64 // probabilità della parola scelta
	Alts    []Cand  // le migliori forme, per la barra dei suggerimenti
}

// Commit decide come scrivere la parola finita.
func (c *Corrector) Commit(obs []Obs) Result {
	lit := literal(obs)
	r := Result{Literal: lit, Text: lit, Kind: Unknown}
	if len(obs) == 0 {
		r.Kind = Literal
		return r
	}
	es := c.rankEntries(obs, true, 6)
	pOOV := normalize(es, c.oovLogP(obs))
	r.Alts = c.forms(es)
	if len(r.Alts) > 6 {
		r.Alts = r.Alts[:6]
	}
	litPost := pOOV
	known := false
	if e := c.M.Lex.Lookup(lit); e != nil {
		known = e.Count/c.M.Lex.Total >= c.P.KeepKnown
		r.Text, r.Attach, r.Kind = e.Forms[0].Text, e.Forms[0].Attach, Literal
		if r.Text != lit {
			r.Kind = Accents
		}
		litPost = 0
		for _, x := range es {
			if x.Key == lit {
				litPost = x.Post
			}
		}
	}
	r.Post = litPost
	if len(es) == 0 || es[0].Key == lit {
		if len(es) > 0 {
			r.Post = es[0].Post
		}
		return r
	}
	plausible := c.P.Plausible < 0 && c.M.MinLetterLogP(lit) >= c.P.Plausible
	if b := es[0]; plausible && undoubled(b.Key) == undoubled(lit) {
		plausible = false // doppia saltata ("pasare"): l'errore più atteso con lo sguardo
	}
	if b := es[0]; !known && !plausible && b.Post >= c.P.MinPost && b.Post > litPost {
		ent := c.M.Lex.Lookup(b.Key)
		r.Text, r.Attach, r.Kind, r.Post = ent.Forms[0].Text, ent.Forms[0].Attach, Fixed, b.Post
	}
	return r
}
