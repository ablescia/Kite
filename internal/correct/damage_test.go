// SPDX-License-Identifier: AGPL-3.0-or-later

package correct

import (
	"sort"
	"testing"
)

// Le parole valide scritte senza errori non vengono cambiate (al più ne
// cambiano accenti e apostrofi): un campione di mille parole dalle 30.000
// più frequenti.
func TestValidWordsUntouched(t *testing.T) {
	lex := model.Lex
	idx := make([]int, len(lex.Entries))
	for i := range idx {
		idx[i] = i
	}
	sort.Slice(idx, func(a, b int) bool { return lex.Entries[idx[a]].Count > lex.Entries[idx[b]].Count })
	c := New(DefaultParams(), model)
	n := 0
	for i := 0; i < 30000 && i < len(idx); i += 30 {
		k := lex.Entries[idx[i]].Key
		n++
		if r := c.Commit(typed(k)); r.Kind == Fixed {
			t.Errorf("%q scritta senza errori è diventata %q", k, r.Text)
		}
	}
	t.Logf("%d parole valide, nessuna cambiata", n)
}
