// SPDX-License-Identifier: AGPL-3.0-or-later

// Package selector decide quale bersaglio viene attivato e quando.
//
// Il selettore ibrido (Hybrid) fa partire il conteggio dalla fine della
// saccade che porta sul tasto, non da quando lo sguardo ci entra. Il tempo
// richiesto si adatta alla probabilità del tasto, e le aree di tocco si
// espandono in modo invisibile sui tasti probabili (decoder bayesiano), con
// l'eventuale modulazione dal carico cognitivo pupillare.
package selector

import (
	"fmt"
	"strings"
)

// Key è un bersaglio. Coordinate normalizzate sullo schermo (0,0 in alto a sinistra).
type Key struct {
	Char   rune
	Label  string
	Rect   [4]float64 // x0, y0, x1, y1
	Center [2]float64
	// Strict: si attiva solo guardandolo dentro, senza margine né
	// espansione (tasti di modifica, vicini al testo che si rilegge).
	Strict bool
}

// Tasti di modifica: non sono lettere e non hanno un simbolo nel modello di
// lingua.
const (
	KeyBackspace rune = '\b'
	KeySuggest   rune = '\x01' // primo dei tre suggerimenti: KeySuggest+0..2
	NumSuggest        = 3
	KeySpeak     rune = '\x07' // parla: la voce dice il testo, poi il testo si cancella
)

// SuggestSlot dice se r è un tasto suggerimento e quale (0..2).
func SuggestSlot(r rune) (int, bool) {
	if r >= KeySuggest && r < KeySuggest+NumSuggest {
		return int(r - KeySuggest), true
	}
	return 0, false
}

// IsEdit dice se r è un tasto di modifica.
func IsEdit(r rune) bool {
	_, s := SuggestSlot(r)
	return s || r == KeyBackspace || r == KeySpeak
}

// Contains dice se p cade nel tasto.
func (k *Key) Contains(p [2]float64) bool {
	return p[0] >= k.Rect[0] && p[0] < k.Rect[2] && p[1] >= k.Rect[1] && p[1] < k.Rect[3]
}

// Keyboard è un insieme di tasti disposti su righe.
type Keyboard struct {
	Keys  []Key
	Rows  int
	Units float64 // larghezza della riga più lunga, in tasti
	Area  [4]float64
	Chars int // tasti di lettere e spazio (i primi); dopo vengono i tasti di modifica
	Bar   [4]float64
	index map[rune]int
}

// Row è una riga del layout: scostamento dal bordo sinistro e larghezza dei
// tasti in unità di tasto; ' ' è lo spazio.
type Row struct {
	Offset float64
	Keys   string
	Widths []float64 // per tasto; vuoto = tutti larghi 1
}

// Layouts disponibili.
//
// qwerty è la disposizione classica delle tastiere su schermo: righe
// sfalsate di mezzo tasto e di un tasto e mezzo, centrate, e la barra
// spaziatrice larga cinque tasti sotto X…N.
var Layouts = map[string][]Row{
	"qwerty": {
		{Offset: 0, Keys: "qwertyuiop"},
		{Offset: 0.5, Keys: "asdfghjkl"},
		{Offset: 1.5, Keys: "zxcvbnm"},
		{Offset: 2.5, Keys: " ", Widths: []float64{5}},
	},
	"abc": {
		{Offset: 0, Keys: "abcdefghi"},
		{Offset: 0, Keys: "jklmnopqr"},
		{Offset: 0, Keys: "stuvwxyz "},
	},
}

// Area (normalizzata) e spaziatura della tastiera nell'applicazione. Con la
// barra di modifica la tastiera scende un poco (LegacyArea è quella delle
// sessioni registrate senza barra).
var (
	DefaultArea = [4]float64{0.02, 0.31, 0.98, 0.97}
	DefaultBar  = [4]float64{0.02, 0.205, 0.98, 0.295}
	LegacyArea  = [4]float64{0.02, 0.25, 0.98, 0.97}
	DefaultGap  = 0.08
)

// NewKeyboard dispone il layout nell'area (normalizzata) con uno spazio tra
// i tasti pari a gap (frazione del tasto).
func NewKeyboard(layout string, area [4]float64, gap float64) (*Keyboard, error) {
	rows, ok := Layouts[layout]
	if !ok {
		return nil, fmt.Errorf("layout %q sconosciuto (qwerty, abc)", layout)
	}
	kb := &Keyboard{Rows: len(rows), Area: area, index: map[rune]int{}}
	for _, r := range rows {
		if r.Widths != nil && len(r.Widths) != len([]rune(r.Keys)) {
			return nil, fmt.Errorf("layout %q: larghezze e tasti non corrispondono", layout)
		}
		w := r.Offset
		for i := range []rune(r.Keys) {
			w += r.width(i)
		}
		kb.Units = max(kb.Units, w)
	}
	cw := (area[2] - area[0]) / kb.Units
	ch := (area[3] - area[1]) / float64(kb.Rows)
	gx, gy := cw*gap/2, ch*gap/2
	for ri, r := range rows {
		u := r.Offset
		for i, c := range []rune(r.Keys) {
			x0 := area[0] + u*cw + gx
			u += r.width(i)
			x1 := area[0] + u*cw - gx
			y0 := area[1] + float64(ri)*ch + gy
			y1 := area[1] + float64(ri+1)*ch - gy
			label := strings.ToUpper(string(c))
			if c == ' ' {
				label = "spazio"
			}
			if _, dup := kb.index[c]; dup {
				return nil, fmt.Errorf("layout %q: tasto %q ripetuto", layout, c)
			}
			kb.index[c] = len(kb.Keys)
			kb.Keys = append(kb.Keys, Key{Char: c, Label: label, Rect: [4]float64{x0, y0, x1, y1},
				Center: [2]float64{(x0 + x1) / 2, (y0 + y1) / 2}})
		}
	}
	kb.Chars = len(kb.Keys)
	return kb, nil
}

// AddEditBar aggiunge la barra di modifica nell'area bar, larga come la
// tastiera: i tre suggerimenti, cancella e, con speak, parla. Senza speak è
// la barra delle sessioni registrate prima della voce (per il replay).
func (kb *Keyboard) AddEditBar(bar [4]float64, gap float64, speak bool) {
	widths := []float64{2.8, 2.8, 2.8, 1.6} // su 10 unità
	runes := []rune{KeySuggest, KeySuggest + 1, KeySuggest + 2, KeyBackspace}
	labels := []string{"", "", "", "canc"}
	if speak {
		widths = []float64{2.4, 2.4, 2.4, 1.4, 1.4}
		runes = append(runes, KeySpeak)
		labels = append(labels, "parla")
	}
	cw := (bar[2] - bar[0]) / 10
	gx, gy := cw*gap/2, (bar[3]-bar[1])*gap/2
	u := 0.0
	for i, w := range widths {
		x0 := bar[0] + u*cw + gx
		u += w
		x1 := bar[0] + u*cw - gx
		r := [4]float64{x0, bar[1] + gy, x1, bar[3] - gy}
		kb.index[runes[i]] = len(kb.Keys)
		kb.Keys = append(kb.Keys, Key{Char: runes[i], Label: labels[i], Rect: r,
			Center: [2]float64{(r[0] + r[2]) / 2, (r[1] + r[3]) / 2}, Strict: true})
	}
	kb.Bar = bar
}

// HasEditBar dice se la tastiera ha la barra di modifica.
func (kb *Keyboard) HasEditBar() bool { return len(kb.Keys) > kb.Chars }

func (r *Row) width(i int) float64 {
	if r.Widths == nil {
		return 1
	}
	return r.Widths[i]
}

// IndexOf restituisce il tasto del carattere, −1 se manca.
func (kb *Keyboard) IndexOf(r rune) int {
	if i, ok := kb.index[r]; ok {
		return i
	}
	return -1
}

// Hit è il tasto che contiene p (geometria pura), −1 se nessuno.
func (kb *Keyboard) Hit(p [2]float64) int {
	for i := range kb.Keys {
		if kb.Keys[i].Contains(p) {
			return i
		}
	}
	return -1
}
