// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Antonio Blescia

// Package metrics calcola le prestazioni di un blocco dell'esercizio di
// copiatura e il loro aggregato.
//
// Due compiti di copiatura:
//   - "rigida" (prima versione): una lettera sbagliata non entra nel testo,
//     si conta e si seleziona quella giusta. Caratteri corretti al minuto e
//     WPM (parole di 5 caratteri) dalla prima all'ultima selezione corretta,
//     (caratteri − 1) al numeratore; accuratezza = selezioni corrette /
//     selezioni; ITR di Wolpaw per selezione;
//   - "testo": come una tastiera vera, gli errori entrano nel testo e si
//     correggono con cancella, i suggerimenti o il correttore automatico.
//     Si misura il testo finale: caratteri al minuto dalla prima all'ultima
//     selezione, CER (distanza di modifica dal testo da copiare / sua
//     lunghezza), accuratezza = 1 − CER, KSPC (selezioni per carattere) e
//     ITR effettivo = bit per carattere (Wolpaw con p = 1 − CER) × caratteri
//     al minuto.
package metrics

import (
	"math"
	"sort"
	"strings"
	"time"

	"github.com/ablescia/kite/internal/lm"
)

// Compiti di copiatura.
const (
	TaskStrict = "rigida"
	TaskText   = "testo"
)

// CharsKeys è il numero di simboli (26 lettere e spazio) per l'ITR del
// compito "testo".
const CharsKeys = 27

// EditDistance è la distanza di Levenshtein tra due testi, sulle digitazioni
// (accenti e apostrofi non contano: non si digitano).
func EditDistance(a, b string) int {
	ra, rb := []rune(lm.Fold(strings.TrimSpace(a))), []rune(lm.Fold(strings.TrimSpace(b)))
	prev := make([]int, len(rb)+1)
	cur := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			c := prev[j-1]
			if ra[i-1] != rb[j-1] {
				c++
			}
			cur[j] = min(c, prev[j]+1, cur[j-1]+1)
		}
		prev, cur = cur, prev
	}
	return prev[len(rb)]
}

// WolpawBits è l'informazione per selezione con n bersagli e accuratezza p:
// B = log2 n + p log2 p + (1−p) log2((1−p)/(n−1)).
func WolpawBits(n int, p float64) float64 {
	if n < 2 || p <= 1/float64(n) {
		return 0
	}
	b := math.Log2(float64(n))
	if p < 1 {
		b += p*math.Log2(p) + (1-p)*math.Log2((1-p)/float64(n-1))
	}
	return b
}

// Selection è una selezione registrata.
type Selection struct {
	TUs        int64   `json:"t_us"` // orologio del tracker
	Key        string  `json:"key"`
	Expected   string  `json:"expected"`
	Correct    bool    `json:"correct"`
	DwellMs    float64 `json:"dwell_ms"`    // fissazione sul tasto prima dell'attivazione
	RequiredMs float64 `json:"required_ms"` // tempo richiesto dal selettore
	Posterior  float64 `json:"posterior"`
	PriorRatio float64 `json:"prior_ratio"`
	LatencyUs  float64 `json:"latency_us"` // dalla ricezione USB del campione alla decisione
	// compito "testo"
	Kind string `json:"kind,omitempty"` // lettera, spazio, canc, annulla, suggerimento
	Text string `json:"text,omitempty"` // testo dopo la selezione
}

// Block è un blocco dell'esercizio di copiatura, in corso o concluso.
type Block struct {
	ID         int         `json:"id"`
	Condition  string      `json:"condition"` // "B" ibrido ("A", il dwell fisso, solo nelle sessioni del banco di prova)
	Label      string      `json:"label"`
	Phrase     string      `json:"phrase"`
	NKeys      int         `json:"n_keys"`
	StartUs    int64       `json:"start_us"`
	StartedAt  time.Time   `json:"started_at"`
	Selections []Selection `json:"selections"`
	Completed  bool        `json:"completed"`

	// compito "testo"
	Task        string `json:"task,omitempty"`
	Text        string `json:"text,omitempty"` // testo finale
	ErrorsMade  int    `json:"errors_made,omitempty"`
	Backspaces  int    `json:"backspaces,omitempty"`
	Undos       int    `json:"undos,omitempty"`
	AutoFixes   int    `json:"auto_fixes,omitempty"`
	Suggestions int    `json:"suggestions,omitempty"`

	PupilSum, PupilZSum float64
	PupilN              int
	PupilCompensated    bool
}

// Result sono le prestazioni di un blocco.
type Result struct {
	ID            int       `json:"id"`
	Condition     string    `json:"condition"`
	Label         string    `json:"label"`
	Phrase        string    `json:"phrase"`
	StartedAt     time.Time `json:"started_at"`
	Chars         int       `json:"chars"`
	Selections    int       `json:"selections"`
	Errors        int       `json:"errors"`
	Accuracy      float64   `json:"accuracy"`
	SpanS         float64   `json:"span_s"`  // prima → ultima selezione corretta
	TotalS        float64   `json:"total_s"` // inizio del blocco → ultima selezione
	CharsPerMin   float64   `json:"chars_per_min"`
	WPM           float64   `json:"wpm"`
	SelPerMin     float64   `json:"sel_per_min"`
	BitsPerSel    float64   `json:"bits_per_sel"`
	ITR           float64   `json:"itr_bits_per_min"`
	MeanIntervalS float64   `json:"mean_interval_s"` // tra selezioni consecutive
	MedIntervalS  float64   `json:"median_interval_s"`
	MeanDwellMs   float64   `json:"mean_dwell_ms"`
	MeanLatencyUs float64   `json:"mean_decision_latency_us"`
	PupilResidMM  float64   `json:"pupil_resid_mm"` // media del residuo (al netto della luce se compensato)
	PupilZ        float64   `json:"pupil_z"`
	PupilComp     bool      `json:"pupil_compensated"`

	// compito "testo"
	Task        string  `json:"task,omitempty"`
	Text        string  `json:"text,omitempty"`
	TargetLen   int     `json:"target_len,omitempty"`
	CER         float64 `json:"cer"`
	ErrorsMade  int     `json:"errors_made"` // selezioni che hanno portato il testo fuori strada
	Backspaces  int     `json:"backspaces"`
	Undos       int     `json:"undos"`
	AutoFixes   int     `json:"auto_fixes"`
	Suggestions int     `json:"suggestions"`
	KSPC        float64 `json:"kspc"`
}

// Result calcola le prestazioni del blocco.
func (b *Block) Result() Result {
	r := Result{ID: b.ID, Condition: b.Condition, Label: b.Label, Phrase: b.Phrase, StartedAt: b.StartedAt,
		Selections: len(b.Selections), PupilComp: b.PupilCompensated}
	var first, last int64 = -1, -1
	var dwell, lat float64
	var intervals []float64
	for i, s := range b.Selections {
		if s.Correct {
			r.Chars++
			if first < 0 {
				first = s.TUs
			}
			last = s.TUs
		} else {
			r.Errors++
		}
		dwell += s.DwellMs
		lat += s.LatencyUs
		if i > 0 {
			intervals = append(intervals, float64(s.TUs-b.Selections[i-1].TUs)/1e6)
		}
	}
	if r.Selections > 0 {
		r.Accuracy = float64(r.Chars) / float64(r.Selections)
		r.MeanDwellMs = dwell / float64(r.Selections)
		r.MeanLatencyUs = lat / float64(r.Selections)
		r.TotalS = float64(b.Selections[r.Selections-1].TUs-b.StartUs) / 1e6
	}
	if r.Chars > 1 && last > first {
		r.SpanS = float64(last-first) / 1e6
		r.CharsPerMin = float64(r.Chars-1) / r.SpanS * 60
		r.WPM = r.CharsPerMin / 5
	}
	if len(intervals) > 0 {
		var s float64
		for _, v := range intervals {
			s += v
		}
		r.MeanIntervalS = s / float64(len(intervals))
		sort.Float64s(intervals)
		r.MedIntervalS = intervals[len(intervals)/2]
		r.SelPerMin = 60 / r.MeanIntervalS
	}
	r.BitsPerSel = WolpawBits(b.NKeys, r.Accuracy)
	r.ITR = r.BitsPerSel * r.SelPerMin
	if b.Task == TaskText {
		b.textResult(&r)
	}
	if b.PupilN > 0 {
		r.PupilResidMM = b.PupilSum / float64(b.PupilN)
		r.PupilZ = b.PupilZSum / float64(b.PupilN)
	}
	return r
}

// textResult calcola le metriche del compito "testo" sul testo finale.
func (b *Block) textResult(r *Result) {
	r.Task, r.Text = TaskText, strings.TrimSpace(b.Text)
	r.TargetLen = len([]rune(b.Phrase))
	r.Chars = len([]rune(r.Text))
	r.Errors = EditDistance(r.Text, b.Phrase)
	r.ErrorsMade, r.Backspaces, r.Undos, r.AutoFixes, r.Suggestions = b.ErrorsMade, b.Backspaces, b.Undos, b.AutoFixes, b.Suggestions
	r.CharsPerMin, r.WPM, r.SpanS = 0, 0, 0
	if n := len(b.Selections); n > 1 {
		r.SpanS = float64(b.Selections[n-1].TUs-b.Selections[0].TUs) / 1e6
		if r.Chars > 1 && r.SpanS > 0 {
			r.CharsPerMin = float64(r.Chars-1) / r.SpanS * 60
			r.WPM = r.CharsPerMin / 5
		}
	}
	if r.TargetLen > 0 {
		r.CER = float64(r.Errors) / float64(r.TargetLen)
		r.KSPC = float64(r.Selections) / float64(r.TargetLen+1) // con lo spazio finale
	}
	r.Accuracy = math.Max(0, 1-r.CER)
	r.BitsPerSel = WolpawBits(CharsKeys, r.Accuracy)
	r.ITR = r.BitsPerSel * r.CharsPerMin
}

// Aggregate sono le prestazioni su tutti i blocchi completati di una
// condizione (i tempi si sommano, non si mediano i rapporti).
type Aggregate struct {
	Condition   string  `json:"condition"`
	Label       string  `json:"label"`
	Blocks      int     `json:"blocks"`
	Chars       int     `json:"chars"`
	Selections  int     `json:"selections"`
	Errors      int     `json:"errors"`
	Accuracy    float64 `json:"accuracy"`
	CharsPerMin float64 `json:"chars_per_min"`
	WPM         float64 `json:"wpm"`
	SelPerMin   float64 `json:"sel_per_min"`
	ITR         float64 `json:"itr_bits_per_min"`
	MeanDwellMs float64 `json:"mean_dwell_ms"`
	MeanIntS    float64 `json:"mean_interval_s"`
	PupilResid  float64 `json:"pupil_resid_mm"`
	PupilComp   bool    `json:"pupil_compensated"`

	// compito "testo"
	Task        string  `json:"task,omitempty"`
	CER         float64 `json:"cer"`
	ErrorsMade  int     `json:"errors_made"`
	Backspaces  int     `json:"backspaces"`
	AutoFixes   int     `json:"auto_fixes"`
	Suggestions int     `json:"suggestions"`
	KSPC        float64 `json:"kspc"`
}

// Combine aggrega i risultati di una condizione.
func Combine(cond string, rs []Result, nKeys int) Aggregate {
	a := Aggregate{Condition: cond}
	var span, intSum, dwellSum, pupil float64
	var nInt, nPupil, targetLen int
	a.PupilComp = true
	for _, r := range rs {
		if r.Condition != cond {
			continue
		}
		a.Blocks++
		a.Label = r.Label
		a.Chars += r.Chars
		a.Selections += r.Selections
		a.Errors += r.Errors
		if r.Task == TaskText {
			a.Task = TaskText
			targetLen += r.TargetLen
			a.ErrorsMade += r.ErrorsMade
			a.Backspaces += r.Backspaces
			a.AutoFixes += r.AutoFixes
			a.Suggestions += r.Suggestions
		}
		if r.Chars > 1 {
			span += r.SpanS
			a.CharsPerMin += float64(r.Chars - 1) // numeratore, diviso dopo
		}
		if r.Selections > 1 {
			intSum += r.MeanIntervalS * float64(r.Selections-1)
			nInt += r.Selections - 1
		}
		dwellSum += r.MeanDwellMs * float64(r.Selections)
		pupil += r.PupilResidMM
		nPupil++
		a.PupilComp = a.PupilComp && r.PupilComp
	}
	if a.Blocks == 0 {
		return a
	}
	if span > 0 {
		a.CharsPerMin = a.CharsPerMin / span * 60
		a.WPM = a.CharsPerMin / 5
	} else {
		a.CharsPerMin = 0
	}
	if a.Selections > 0 {
		a.Accuracy = float64(a.Chars) / float64(a.Selections)
		a.MeanDwellMs = dwellSum / float64(a.Selections)
	}
	if nInt > 0 {
		a.MeanIntS = intSum / float64(nInt)
		a.SelPerMin = 60 / a.MeanIntS
	}
	a.ITR = WolpawBits(nKeys, a.Accuracy) * a.SelPerMin
	if a.Task == TaskText && targetLen > 0 {
		a.CER = float64(a.Errors) / float64(targetLen)
		a.Accuracy = math.Max(0, 1-a.CER)
		a.KSPC = float64(a.Selections) / float64(targetLen+a.Blocks) // con gli spazi finali
		a.ITR = WolpawBits(CharsKeys, a.Accuracy) * a.CharsPerMin
	}
	a.PupilResid = pupil / float64(nPupil)
	return a
}
