// SPDX-License-Identifier: AGPL-3.0-or-later

package engine

import (
	"github.com/ablescia/kite/internal/correct"
	"github.com/ablescia/kite/internal/oculo"
	"github.com/ablescia/kite/internal/selector"
)

// Observer trasforma la selezione di una lettera in un'osservazione per il
// correttore: quanto lo sguardo indica ciascuna lettera, dal decoder
// spaziale senza predizione e, dopo una saccade lunga, dal modello
// dell'atterraggio corto (una "b" raggiunta da sinistra può essere una "n").
type Observer struct {
	Dec    *selector.Decoder
	Hybrid selector.HybridParams // LandAmpDeg e Landing

	letterPr, like, aim []float64
}

// NewObserver crea un osservatore sul decoder dec.
func NewObserver(dec *selector.Decoder, hp selector.HybridParams) *Observer {
	o := &Observer{Dec: dec, Hybrid: hp}
	n := len(dec.KB.Keys)
	o.letterPr, o.like, o.aim = make([]float64, n), make([]float64, n), make([]float64, n)
	for i, k := range dec.KB.Keys {
		if k.Char >= 'a' && k.Char <= 'z' {
			o.letterPr[i] = 1
		}
	}
	return o
}

// Observe descrive la selezione della lettera c con lo stato oculare st.
func (o *Observer) Observe(c rune, st *oculo.State, eye [3]float64) correct.Obs {
	obs := correct.Certain(byte(c))
	if st == nil || st.Phase != oculo.Fixation {
		return obs
	}
	if k, _ := o.Dec.Decode(st.FixCentroid, eye, o.letterPr, o.like); k < 0 {
		return obs
	}
	like := o.like
	if h := o.Hybrid; h.LandAmpDeg > 0 && st.AfterSaccade && st.Last.AmplitudeDeg >= h.LandAmpDeg {
		o.Dec.AimPosterior(st.Last.From, st.FixCentroid, eye, o.letterPr, h.Landing, o.aim)
		for i := range like {
			like[i] = 0.5*like[i] + 0.5*o.aim[i]
		}
	}
	obs.Like = [26]float64{}
	for i, k := range o.Dec.KB.Keys {
		if k.Char >= 'a' && k.Char <= 'z' {
			obs.Like[k.Char-'a'] += like[i]
		}
	}
	return obs
}
