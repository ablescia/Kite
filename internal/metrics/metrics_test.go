// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Antonio Blescia

package metrics

import (
	"math"
	"testing"
)

func TestWolpaw(t *testing.T) {
	if b := WolpawBits(27, 1); math.Abs(b-math.Log2(27)) > 1e-12 {
		t.Errorf("p=1: %v", b)
	}
	// 4 classi, 90%: 2 + 0,9·log2 0,9 + 0,1·log2(0,1/3) = 1,3725 bit.
	if b := WolpawBits(4, 0.9); math.Abs(b-1.3725) > 1e-4 {
		t.Errorf("n=4 p=0,9: %v", b)
	}
	if b := WolpawBits(27, 1.0/27); b != 0 {
		t.Errorf("al caso: %v", b)
	}
}

func TestBlockResult(t *testing.T) {
	b := Block{Condition: "B", NKeys: 27, StartUs: 0}
	// "ciao": 4 corretti, un errore in mezzo, una selezione ogni 0,5 s.
	seq := []bool{true, true, false, true, true}
	for i, ok := range seq {
		b.Selections = append(b.Selections, Selection{TUs: int64(1_000_000 + i*500_000), Correct: ok, DwellMs: 150})
	}
	r := b.Result()
	if r.Chars != 4 || r.Errors != 1 || math.Abs(r.Accuracy-0.8) > 1e-9 {
		t.Fatalf("%+v", r)
	}
	// 3 intervalli tra la prima e l'ultima corretta in 2 s → 90 car/min.
	if math.Abs(r.CharsPerMin-90) > 1e-9 || math.Abs(r.WPM-18) > 1e-9 {
		t.Errorf("car/min %.2f, WPM %.2f", r.CharsPerMin, r.WPM)
	}
	if math.Abs(r.SelPerMin-120) > 1e-9 || math.Abs(r.ITR-120*WolpawBits(27, 0.8)) > 1e-9 {
		t.Errorf("sel/min %.2f ITR %.2f", r.SelPerMin, r.ITR)
	}
	agg := Combine("B", []Result{r, r}, 27)
	if math.Abs(agg.CharsPerMin-90) > 1e-9 || agg.Blocks != 2 || agg.Chars != 8 {
		t.Errorf("aggregato: %+v", agg)
	}
}
