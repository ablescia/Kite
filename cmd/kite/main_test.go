// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Antonio Blescia

package main

import (
	"os"
	"path/filepath"
	"testing"
)

// Al primo avvio il dizionario personale e le frasi imparate nascono vuoti
// (anche la cartella); quelli che esistono già restano come sono.
func TestCreateEmpty(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "kite")
	words, phrases := filepath.Join(dir, "parole.txt"), filepath.Join(dir, "frasi.txt")
	createEmpty(words, phrases)
	for _, p := range []string{words, phrases} {
		if st, err := os.Stat(p); err != nil || st.Size() != 0 {
			t.Fatalf("%s: %v", p, err)
		}
	}
	if err := os.WriteFile(phrases, []byte("ho sete\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	createEmpty(words, phrases)
	if b, _ := os.ReadFile(phrases); string(b) != "ho sete\n" {
		t.Errorf("frasi esistenti toccate: %q", b)
	}
}
