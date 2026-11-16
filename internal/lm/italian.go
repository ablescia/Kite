// SPDX-License-Identifier: AGPL-3.0-or-later

package lm

import (
	"bytes"
	"compress/gzip"
	_ "embed"
	"io"
	"sort"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"
)

// Lessico italiano grande: le 150.000 parole più frequenti nei sottotitoli
// (OpenSubtitles 2018, FrequencyWords, CC BY-SA 4.0: vedi data/LEGGIMI.md).
// I sottotitoli sono lingua parlata, la più vicina a quella di chi comunica
// con una tastiera assistiva, ma sono trascritti in fretta: accenti mancanti
// ("perche"), accenti scritti con l'apostrofo ("e'", "piu'"), abbreviazioni.
// Il caricamento li riporta alla forma corretta.

//go:embed data/it_150k.txt.gz
var itGz []byte

// Form è un modo di scrivere una parola.
type Form struct {
	Text   string  // "perché", "l'"
	Count  float64 // occorrenze nel corpus
	Attach bool    // elisione: si unisce alla parola seguente senza spazio ("l'acqua")
}

// Entry raggruppa le forme che sulla tastiera di 26 lettere si digitano allo
// stesso modo (senza accenti né apostrofi), dalla più frequente.
type Entry struct {
	Key   string // "perche"
	Count float64
	Forms []Form
}

// Lexicon è un lessico con le forme raggruppate per digitazione.
type Lexicon struct {
	Entries []Entry // ordinate per Key
	Total   float64
	index   map[string]int
}

// Lookup restituisce la voce di una digitazione, nil se manca.
func (l *Lexicon) Lookup(key string) *Entry {
	if i, ok := l.index[key]; ok {
		return &l.Entries[i]
	}
	return nil
}

var (
	italianOnce sync.Once
	italian     *Lexicon
)

// Italian è il lessico italiano incorporato (costruito una volta sola).
func Italian() *Lexicon {
	italianOnce.Do(func() {
		zr, err := gzip.NewReader(bytes.NewReader(itGz))
		if err != nil {
			panic("lessico italiano: " + err.Error())
		}
		data, err := io.ReadAll(zr)
		if err != nil {
			panic("lessico italiano: " + err.Error())
		}
		italian = parseItalian(string(data))
	})
	return italian
}

// Fold riduce un testo a quello che si digita: minuscole, vocali senza
// accento, niente apostrofi.
func Fold(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if f, ok := foldRune[r]; ok {
			r = f
		}
		if r == '\'' || r == '’' {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

var foldRune = map[rune]rune{'à': 'a', 'á': 'a', 'è': 'e', 'é': 'e', 'ì': 'i', 'í': 'i', 'ò': 'o', 'ó': 'o', 'ù': 'u', 'ú': 'u'}

// Monosillabi (e parole di tre lettere) che si scrivono con l'accento: le
// loro forme senza accento sono errori di trascrizione, tranne quelle che
// sono parole a loro volta (e, da, la, si...). Tutti gli altri brevi con
// l'accento ("quì", "sù", "fà") sono errori: vince la forma senza.
var (
	shortAccented = map[string]bool{"è": true, "dà": true, "là": true, "lì": true, "sì": true, "né": true,
		"sé": true, "tè": true, "dì": true, "ciò": true, "più": true, "può": true, "già": true, "giù": true}
	shortPlainValid = map[string]bool{"e": true, "da": true, "la": true, "li": true, "si": true, "ne": true,
		"se": true, "te": true, "di": true, "che": true}
	// apocopi: finiscono con l'apostrofo ma non si uniscono alla parola dopo
	apocope = map[string]bool{"po'": true, "mo'": true, "be'": true, "to'": true, "va'": true, "fa'": true,
		"sta'": true, "da'": true, "di'": true}
	// forme senza apostrofo che sono trascrizioni sbagliate di un'apocope
	apocopePlain = map[string]string{"po": "po'"}
	// Elisioni italiane: una classe chiusa (articoli, preposizioni, pronomi,
	// pochi aggettivi e avverbi). Le altre forme con l'apostrofo nei
	// sottotitoli sono rumore ("grae'" da "grazie", "didn'").
	elisions = setOf("l' c' un' d' dell' all' nell' dall' sull' coll' quell' quest' cos' dov' com' anch' n' dev' " +
		"gliel' m' qualcos' quand' nient' s' bell' t' mezz' quant' brav' vent' neanch' tutt' senz' nessun' " +
		"qualcun' sott' ch' v' sant' grand' trent' quarant' cinquant' sessant' settant' ottant' novant' cent' " +
		"buon' ultim' prim' null' tant' gl' quattr' diciott' ventiquattr' pover'")
)

func setOf(s string) map[string]bool {
	m := map[string]bool{}
	for _, w := range strings.Fields(s) {
		m[w] = true
	}
	return m
}

func hasAccent(s string) bool {
	for _, r := range s {
		if _, ok := foldRune[r]; ok {
			return true
		}
	}
	return false
}

// finalAccent dice se l'unico accento è sull'ultima lettera: l'accento
// grafico dell'italiano. Gli accenti in mezzo alla parola ("príma",
// "subìto") nei sottotitoli sono refusi o rare distinzioni.
func finalAccent(s string) bool {
	s = strings.TrimSuffix(s, "'")
	r, size := utf8.DecodeLastRuneInString(s)
	_, last := foldRune[r]
	return last && !hasAccent(s[:len(s)-size])
}

func validWord(w string) bool {
	if w == "" || w == "'" || strings.Count(w, "'") > 1 || (strings.Contains(w, "'") && !strings.HasSuffix(w, "'")) {
		return false
	}
	letters := 0
	for _, r := range w {
		switch {
		case r >= 'a' && r <= 'z':
			letters++
		case r == '\'':
		default:
			if _, ok := foldRune[r]; !ok {
				return false
			}
			letters++
		}
	}
	return letters > 0
}

func parseItalian(data string) *Lexicon {
	counts := map[string]float64{}
	for _, line := range strings.Split(data, "\n") {
		f := strings.Fields(line)
		if len(f) != 2 {
			continue
		}
		w := strings.ToLower(f[0])
		n, err := strconv.ParseFloat(f[1], 64)
		if err != nil || !validWord(w) {
			continue
		}
		counts[w] += n
	}
	return buildLexicon(counts)
}

// buildLexicon normalizza e raggruppa le forme.
func buildLexicon(counts map[string]float64) *Lexicon {
	groups := map[string]map[string]float64{}
	add := func(w string, n float64) {
		k := Fold(w)
		if groups[k] == nil {
			groups[k] = map[string]float64{}
		}
		groups[k][w] += n
	}
	for w, n := range counts {
		add(w, n)
	}
	l := &Lexicon{index: map[string]int{}}
	for key, forms := range groups {
		// 0. Accenti in mezzo alla parola: refusi, confluiscono nella forma
		// senza accento se c'è.
		for w, n := range forms {
			if hasAccent(w) && !finalAccent(w) {
				if _, ok := forms[key]; ok {
					forms[key] += n
					delete(forms, w)
				}
			}
		}
		// 1. Accenti scritti con l'apostrofo ("e'", "piu'"): se esiste la
		// forma accentata ci confluiscono. Le altre forme con l'apostrofo
		// sono elisioni ("l'", "dell'") o apocopi ("po'").
		mainAcc, accN := "", -1.0
		for w, n := range forms {
			if hasAccent(w) && n > accN {
				mainAcc, accN = w, n
			}
		}
		for w, n := range forms {
			if !strings.HasSuffix(w, "'") || apocope[w] || elisions[w] {
				continue
			}
			r, _ := utf8.DecodeLastRuneInString(strings.TrimSuffix(w, "'"))
			if mainAcc != "" && strings.ContainsRune("aeiou", r) {
				forms[mainAcc] += n
			}
			delete(forms, w)
		}
		if a, ok := apocopePlain[key]; ok && forms[a] > 0 {
			forms[a] += forms[key]
			delete(forms, key)
		}
		// 2. Tra le forme accentate vince la più frequente ("perché" e
		// "perchè" → la prima, se più usata).
		if mainAcc != "" {
			for w, n := range forms {
				if w != mainAcc && hasAccent(w) && !strings.HasSuffix(w, "'") {
					forms[mainAcc] += n
					delete(forms, w)
				}
			}
		}
		// 3. Forma senza accento contro forma accentata. Nelle parole lunghe
		// l'accento finale è obbligatorio ("perche" è "perché"), purché la
		// forma accentata sia usata davvero: "allá" (133) non è "alla"
		// (469.810). Nelle brevi decide l'elenco dei monosillabi accentati.
		if n, ok := forms[key]; ok && mainAcc != "" {
			long := utf8.RuneCountInString(key) >= 4
			switch {
			case long && forms[mainAcc] >= 0.2*n || !long && shortAccented[mainAcc] && !shortPlainValid[key]:
				forms[mainAcc] += n // "perche" → "perché", "piu" → "più"
				delete(forms, key)
			case long || !shortAccented[mainAcc]:
				forms[key] += forms[mainAcc] // "allá" → "alla", "quì" → "qui"
				delete(forms, mainAcc)
			}
		}
		if len(forms) == 0 {
			continue
		}
		e := Entry{Key: key}
		for w, n := range forms {
			e.Forms = append(e.Forms, Form{Text: w, Count: n, Attach: strings.HasSuffix(w, "'") && !apocope[w]})
			e.Count += n
		}
		sort.Slice(e.Forms, func(i, j int) bool {
			if e.Forms[i].Count != e.Forms[j].Count {
				return e.Forms[i].Count > e.Forms[j].Count
			}
			return e.Forms[i].Text < e.Forms[j].Text
		})
		l.Entries = append(l.Entries, e)
		l.Total += e.Count
	}
	sort.Slice(l.Entries, func(i, j int) bool { return l.Entries[i].Key < l.Entries[j].Key })
	for i, e := range l.Entries {
		l.index[e.Key] = i
	}
	return l
}

// Add aggiunge (o rinforza) una parola, per esempio dal dizionario personale.
func (l *Lexicon) Add(w string, n float64) {
	w = strings.ToLower(strings.TrimSpace(w))
	if !validWord(w) {
		return
	}
	key := Fold(w)
	if key == "" {
		return
	}
	l.Total += n
	if i, ok := l.index[key]; ok {
		e := &l.Entries[i]
		e.Count += n
		for k := range e.Forms {
			if e.Forms[k].Text == w {
				e.Forms[k].Count += n
				sort.SliceStable(e.Forms, func(a, b int) bool { return e.Forms[a].Count > e.Forms[b].Count })
				return
			}
		}
		e.Forms = append(e.Forms, Form{Text: w, Count: n, Attach: strings.HasSuffix(w, "'") && !apocope[w]})
		sort.SliceStable(e.Forms, func(a, b int) bool { return e.Forms[a].Count > e.Forms[b].Count })
		return
	}
	i := sort.Search(len(l.Entries), func(i int) bool { return l.Entries[i].Key >= key })
	l.Entries = append(l.Entries, Entry{})
	copy(l.Entries[i+1:], l.Entries[i:])
	l.Entries[i] = Entry{Key: key, Count: n, Forms: []Form{{Text: w, Count: n, Attach: strings.HasSuffix(w, "'") && !apocope[w]}}}
	for j := i; j < len(l.Entries); j++ {
		l.index[l.Entries[j].Key] = j
	}
}
