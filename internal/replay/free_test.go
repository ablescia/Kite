// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Antonio Blescia

package replay

import "testing"

// Le lettere poi cancellate contano come sbagliate; suggerimenti, correzioni
// e parla riallineano il testo senza segnare nulla.
func TestMarkDeleted(t *testing.T) {
	sels := []Selection{
		{Key: "s", Kind: "lettera", Text: "s"},
		{Key: "i", Kind: "lettera", Text: "si"},
		{Key: "c", Kind: "lettera", Text: "sic"},  // sbagliata
		{Key: "u", Kind: "lettera", Text: "sicu"}, // sbagliata
		{Key: "canc", Kind: "canc", Text: "sic"},
		{Key: "canc", Kind: "canc", Text: "si"},
		{Key: " ", Kind: "spazio", Text: "si "},
		{Key: "c", Kind: "lettera", Text: "si c"},
		{Key: "sugg1", Kind: "suggerimento", Text: "si chiama "},
		{Key: "b", Kind: "lettera", Text: "si chiama b"},
		{Key: "b", Kind: "lettera", Text: "si chiama bb"}, // sbagliata
		{Key: "canc", Kind: "canc", Text: "si chiama b"},
		{Key: "o", Kind: "lettera", Text: "si chiama bo"},
		{Key: "parla", Kind: "parla", Text: "si chiama bo "},
		{Key: "e", Kind: "lettera", Text: "e"}, // messaggio nuovo
	}
	markDeleted(sels)
	for i, s := range sels {
		want := i != 2 && i != 3 && i != 10
		if s.Correct != want {
			t.Errorf("selezione %d (%q → %q): giusta %v, attesa %v", i, s.Key, s.Text, s.Correct, want)
		}
	}
}
