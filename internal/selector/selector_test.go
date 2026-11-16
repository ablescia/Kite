// SPDX-License-Identifier: AGPL-3.0-or-later

package selector

import (
	"math"
	"testing"

	"github.com/ablescia/kite/internal/gazein"
	"github.com/ablescia/kite/internal/oculo"
)

var geom = gazein.Geometry{WidthMM: 700, HeightMM: 390, WidthPx: 1920, HeightPx: 1080}

func keyboard(t *testing.T) *Keyboard {
	kb, err := NewKeyboard("abc", [4]float64{0.02, 0.25, 0.98, 0.97}, 0.08)
	if err != nil {
		t.Fatal(err)
	}
	return kb
}

func decoder(kb *Keyboard) *Decoder {
	return &Decoder{KB: kb, Geom: geom, SigmaDeg: 1.6, CoreFrac: 0.6, MarginDeg: 1}
}

func TestKeyboard(t *testing.T) {
	kb := keyboard(t)
	if len(kb.Keys) != 27 {
		t.Fatalf("%d tasti", len(kb.Keys))
	}
	for i, k := range kb.Keys {
		if kb.Hit(k.Center) != i || kb.IndexOf(k.Char) != i {
			t.Errorf("tasto %q", k.Char)
		}
	}
}

func TestQwertyLayout(t *testing.T) {
	q, err := NewKeyboard("qwerty", [4]float64{0, 0, 1, 1}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(q.Keys) != 27 || q.Rows != 4 || q.Units != 10 {
		t.Fatalf("qwerty: %d tasti, %d righe, %.1f unità", len(q.Keys), q.Rows, q.Units)
	}
	x := func(r rune) float64 { return q.Keys[q.IndexOf(r)].Center[0] * 10 } // in larghezze di tasto
	y := func(r rune) float64 { return q.Keys[q.IndexOf(r)].Center[1] * 4 }
	near := func(a, b float64) bool { return math.Abs(a-b) < 1e-9 }
	// Righe sfalsate come sulle tastiere a schermo: A mezzo tasto a destra di
	// Q, Z sotto S, barra spaziatrice di 5 tasti centrata sotto la tastiera.
	if !near(x('q'), 0.5) || !near(x('a'), 1) || !near(x('z'), x('s')) || !near(x('p'), 9.5) || !near(x('m'), 8) {
		t.Errorf("sfalsamento: Q %.2f A %.2f S %.2f Z %.2f M %.2f P %.2f", x('q'), x('a'), x('s'), x('z'), x('m'), x('p'))
	}
	if !near(y('q'), 0.5) || !near(y('a'), 1.5) || !near(y('z'), 2.5) || !near(y(' '), 3.5) {
		t.Errorf("righe: %v %v %v %v", y('q'), y('a'), y('z'), y(' '))
	}
	sp := q.Keys[q.IndexOf(' ')]
	if !near((sp.Rect[2]-sp.Rect[0])*10, 5) || !near(x(' '), 5) {
		t.Errorf("barra spaziatrice: larga %.2f, centro %.2f", (sp.Rect[2]-sp.Rect[0])*10, x(' '))
	}
	for i, k := range q.Keys {
		if q.Hit(k.Center) != i {
			t.Errorf("tasto %q non colpito al centro", k.Char)
		}
	}
}

// La barra spaziatrice si mira lungo tutta la sua lunghezza: guardarne
// l'estremità (fuori dal nucleo ancorato) dà spazio, non la lettera sopra.
func TestDecoderWideSpaceBar(t *testing.T) {
	q, err := NewKeyboard("qwerty", [4]float64{0.02, 0.25, 0.98, 0.97}, 0.08)
	if err != nil {
		t.Fatal(err)
	}
	d := decoder(q)
	post := make([]float64, len(q.Keys))
	sp := q.IndexOf(' ')
	r := q.Keys[sp].Rect
	unit := (r[2] - r[0]) / 5
	for _, frac := range []float64{0.1, 0.3, 0.7, 0.9} {
		p := [2]float64{r[0] + frac*5*unit*0.98, q.Keys[sp].Center[1]}
		if k, post := d.Decode(p, gazein.DefaultEye, nil, post); k != sp || post < 0.9 {
			t.Errorf("sguardo al %.0f%% della barra: tasto %d (post %.2f), atteso spazio", 100*frac, k, post)
		}
	}
	// Le zone vuote della tastiera sfalsata non appartengono a nessun tasto.
	zk, mk := q.Keys[q.IndexOf('z')], q.Keys[q.IndexOf('m')]
	for _, p := range [][2]float64{
		{zk.Rect[0] - 0.6*unit, zk.Center[1]},   // a sinistra della Z
		{mk.Rect[2] + 0.6*unit, mk.Center[1]},   // a destra della M
		{r[0] - 0.8*unit, q.Keys[sp].Center[1]}, // a sinistra della barra
	} {
		if k, _ := d.Decode(p, gazein.DefaultEye, nil, post); k != -1 {
			t.Errorf("zona vuota %.3f,%.3f assegnata al tasto %d", p[0], p[1], k)
		}
	}
	// A meno di 1° da un tasto lo sguardo gli appartiene ancora (bordo del tracker).
	if k, _ := d.Decode([2]float64{zk.Rect[0] - 0.003, zk.Center[1]}, gazein.DefaultEye, nil, post); k != q.IndexOf('z') {
		t.Errorf("appena fuori dalla Z: tasto %d", k)
	}
	// Le lettere restano mirate al centro: confine geometrico con priori uguali.
	qk, wk := q.Keys[q.IndexOf('q')], q.Keys[q.IndexOf('w')]
	p := [2]float64{qk.Rect[2] - 0.1*(qk.Rect[2]-qk.Rect[0]), qk.Center[1]}
	if k, _ := d.Decode(p, gazein.DefaultEye, nil, post); k != q.IndexOf('q') {
		t.Errorf("bordo destro di Q: tasto %d, atteso Q (W al centro %.3f)", k, wk.Center[0])
	}
}

func TestDecoderExpansionAndAnchoring(t *testing.T) {
	kb := keyboard(t)
	d := decoder(kb)
	post := make([]float64, len(kb.Keys))
	a, b := kb.IndexOf('a'), kb.IndexOf('b') // vicini sulla stessa riga
	ka, kb2 := kb.Keys[a], kb.Keys[b]
	// Punto dentro B, al 12% della sua larghezza dal bordo verso A: fuori dal nucleo di B.
	p := [2]float64{kb2.Rect[0] + 0.12*(kb2.Rect[2]-kb2.Rect[0]), kb2.Center[1]}
	if k, _ := d.Decode(p, gazein.DefaultEye, nil, post); k != b {
		t.Fatalf("priore uniforme: atteso B, ottenuto %d", k)
	}
	prior := make([]float64, len(kb.Keys))
	for i := range prior {
		prior[i] = 0.01 / float64(len(prior)-1)
	}
	prior[a] = 0.99
	if k, _ := d.Decode(p, gazein.DefaultEye, prior, post); k != a {
		t.Errorf("A molto probabile deve espandersi sul bordo di B (ottenuto %d)", k)
	}
	if k, _ := d.Decode(kb2.Center, gazein.DefaultEye, prior, post); k != b {
		t.Errorf("il centro di B resta B (ancoraggio), ottenuto %d", k)
	}
	if k, _ := d.Decode([2]float64{0.5, 0.05}, gazein.DefaultEye, nil, post); k != -1 {
		t.Errorf("sguardo sulla striscia del testo: nessun tasto, ottenuto %d", k)
	}
	_ = ka
}

// fix costruisce lo stato oculare di una fissazione stabile su p.
func fix(id uint64, startUs int64, p [2]float64) oculo.State {
	return oculo.State{Phase: oculo.Fixation, FixID: id, FixStartUs: startUs, FixN: 5,
		FixCentroid: p, FixStable: true, AfterSaccade: true}
}

func runHybrid(h *Hybrid, st oculo.State, prior []float64, fromUs, toUs int64) (sel []int64) {
	for tu := fromUs; tu <= toUs; tu += 11111 {
		in := Input{TUs: tu, Oc: st, Eye: gazein.DefaultEye, Prior: prior}
		if d := h.Update(&in); d.Selected >= 0 {
			sel = append(sel, tu-st.FixStartUs)
		}
	}
	return sel
}

func TestHybridAdaptiveDwell(t *testing.T) {
	kb := keyboard(t)
	p := DefaultHybrid()
	p.PupilGain = 0
	prior := make([]float64, len(kb.Keys))
	for i := range prior {
		prior[i] = 0.2 / float64(len(prior)-1)
	}
	e, z := kb.IndexOf('e'), kb.IndexOf('z')
	prior[e] = 0.8

	// Tasto probabile: si attiva dopo TMin dalla fine della saccade.
	h := NewHybrid(p, decoder(kb))
	sel := runHybrid(h, fix(1, 1_000_000, kb.Keys[e].Center), prior, 1_000_000, 1_400_000)
	if len(sel) != 1 || math.Abs(float64(sel[0])/1000-p.TMinMs) > 12 {
		t.Errorf("tasto probabile: selezioni a %v µs, attesa una a ~%v ms", sel, p.TMinMs)
	}
	// Restando sulla stessa fissazione la doppia arriva dopo il tempo di
	// ripetizione: qui la doppia è probabile (r alto), quindi RepeatMinMs.
	rep := p.RepeatReqMs(prior[e] * float64(len(prior)))
	first := float64(sel[0]) / 1000
	sel = runHybrid(h, fix(1, 1_000_000, kb.Keys[e].Center), prior, 1_400_000, 2_200_000)
	if len(sel) != 1 || math.Abs(float64(sel[0])/1000-(first+rep)) > 12 || rep != p.RepeatMinMs {
		t.Errorf("ripetizione senza saccade: %v µs, attesa a ~%v ms", sel, first+rep)
	}

	// Tasto improbabile: serve un dwell più lungo.
	h = NewHybrid(p, decoder(kb))
	sel = runHybrid(h, fix(7, 5_000_000, kb.Keys[z].Center), prior, 5_000_000, 6_000_000)
	r := prior[z] * float64(len(prior))
	want := p.RequiredMs(r, 1, 0, false)
	if len(sel) != 1 || math.Abs(float64(sel[0])/1000-want) > 12 || want < 500 {
		t.Errorf("tasto improbabile (r=%.2f): selezioni a %v µs, attesa a ~%.0f ms", r, sel, want)
	}

	// Senza predizione: tempo uguale per tutti i tasti.
	p.UsePrediction = false
	h = NewHybrid(p, decoder(kb))
	sel = runHybrid(h, fix(9, 8_000_000, kb.Keys[z].Center), prior, 8_000_000, 9_000_000)
	want = p.RequiredMs(1, 1, 0, false)
	if len(sel) != 1 || math.Abs(float64(sel[0])/1000-want) > 12 {
		t.Errorf("senza predizione: selezioni a %v µs, attesa a ~%.0f ms", sel, want)
	}
	pg := DefaultHybrid()
	t.Logf("dwell richiesto: r=0,1 → %.0f ms, r=1 → %.0f ms, r=4 → %.0f ms; r=4 con carico Z=2 → %.0f ms",
		pg.RequiredMs(0.1, 1, 0, false), pg.RequiredMs(1, 1, 0, false), pg.RequiredMs(4, 1, 0, false), pg.RequiredMs(4, 1, 2, true))
}

func TestHybridIgnoresSaccades(t *testing.T) {
	kb := keyboard(t)
	h := NewHybrid(DefaultHybrid(), decoder(kb))
	st := oculo.State{Phase: oculo.Saccade}
	for tu := int64(0); tu < 2_000_000; tu += 11111 {
		if d := h.Update(&Input{TUs: tu, Oc: st, Eye: gazein.DefaultEye}); d.Selected >= 0 || d.Candidate >= 0 {
			t.Fatal("durante una saccade non si seleziona nulla")
		}
	}
}

func qwerty(t *testing.T) *Keyboard {
	kb, err := NewKeyboard("qwerty", DefaultArea, DefaultGap)
	if err != nil {
		t.Fatal(err)
	}
	return kb
}

// priorWith: priore con i tasti indicati alle probabilità date, il resto uniforme.
func priorWith(kb *Keyboard, keys map[rune]float64) []float64 {
	pr := make([]float64, len(kb.Keys))
	rest := 1.0
	for _, v := range keys {
		rest -= v
	}
	for i := range pr {
		pr[i] = rest / float64(len(pr)-len(keys))
	}
	for c, v := range keys {
		pr[kb.IndexOf(c)] = v
	}
	return pr
}

// Dopo una selezione, lo sguardo che resta nella stessa fissazione e scivola
// sul tasto accanto non scrive: serve una saccade.
func TestHybridLockFixation(t *testing.T) {
	kb := qwerty(t)
	a, s := kb.IndexOf('a'), kb.IndexOf('s')
	prior := priorWith(kb, map[rune]float64{'a': 0.4, 's': 0.4})
	for _, lock := range []bool{true, false} {
		p := DefaultHybrid()
		p.PupilGain, p.LockFixation = 0, lock
		h := NewHybrid(p, decoder(kb))
		if sel := runHybrid(h, fix(1, 1_000_000, kb.Keys[a].Center), prior, 1_000_000, 1_300_000); len(sel) != 1 {
			t.Fatalf("prima selezione: %v", sel)
		}
		// stessa fissazione, centroide scivolato sulla S
		drift := runHybrid(h, fix(1, 1_000_000, kb.Keys[s].Center), prior, 1_300_000, 2_300_000)
		if lock && len(drift) != 0 {
			t.Errorf("con il blocco la S non deve attivarsi nella stessa fissazione: %v", drift)
		}
		if !lock && len(drift) == 0 {
			t.Errorf("senza il blocco (prima versione) la S si attiva: è l'errore che il blocco evita")
		}
		if lock {
			// dopo una saccade la S si attiva normalmente
			sel := runHybrid(h, fix(2, 3_000_000, kb.Keys[s].Center), prior, 3_000_000, 3_400_000)
			if len(sel) != 1 || float64(sel[0])/1000 > p.TMinMs+12 {
				t.Errorf("dopo la saccade: %v µs, attesa a ~%v ms", sel, p.TMinMs)
			}
		}
	}
}

// Il tempo per ripetere una lettera tenendo il tasto dipende da quanto la
// doppia è probabile; uscendo e rientrando non serve.
func TestHybridRepeat(t *testing.T) {
	p := DefaultHybrid()
	if got := p.RepeatReqMs(p.RHi); got != p.RepeatMinMs {
		t.Errorf("doppia probabile: %v ms, attesi %v", got, p.RepeatMinMs)
	}
	if got := p.RepeatReqMs(p.RLo); got != p.RepeatMs {
		t.Errorf("doppia improbabile: %v ms, attesi %v", got, p.RepeatMs)
	}
	p.RepeatMinMs = 0
	if got := p.RepeatReqMs(p.RHi); got != p.RepeatMs {
		t.Errorf("senza RepeatMinMs il tempo è fisso: %v ms", got)
	}

	kb := qwerty(t)
	e, z := kb.IndexOf('e'), kb.IndexOf('z')
	prior := priorWith(kb, map[rune]float64{'e': 0.5})
	p = DefaultHybrid()
	p.PupilGain = 0
	h := NewHybrid(p, decoder(kb))
	runHybrid(h, fix(1, 1_000_000, kb.Keys[e].Center), prior, 1_000_000, 1_300_000)
	// un'occhiata breve alla Z (improbabile: non si attiva), poi di nuovo la E
	if sel := runHybrid(h, fix(2, 1_400_000, kb.Keys[z].Center), prior, 1_400_000, 1_600_000); len(sel) != 0 {
		t.Fatalf("la Z non doveva attivarsi: %v", sel)
	}
	sel := runHybrid(h, fix(3, 1_700_000, kb.Keys[e].Center), prior, 1_700_000, 2_000_000)
	if len(sel) != 1 || float64(sel[0])/1000 > p.TMinMs+12 {
		t.Errorf("uscendo e rientrando la doppia è immediata: %v µs", sel)
	}
}

// Atterraggio corto: una saccade lunga che cade sul tasto prima di quello
// probabile aspetta la saccade correttiva; una che cade sul bersaglio no.
func TestHybridLanding(t *testing.T) {
	kb := qwerty(t)
	q, i, o := kb.IndexOf('q'), kb.IndexOf('i'), kb.IndexOf('o')
	prior := priorWith(kb, map[rune]float64{'o': 0.5, 'i': 0.1})
	p := DefaultHybrid()
	p.PupilGain = 0
	d := decoder(kb)
	from := kb.Keys[q].Center
	landing := func(id uint64, to [2]float64) oculo.State {
		st := fix(id, 1_000_000*int64(id), to)
		st.Last = oculo.SaccadeInfo{From: from, To: to, AmplitudeDeg: geom.AngleDeg(gazein.DefaultEye, from, to)}
		return st
	}
	aim := make([]float64, len(kb.Keys))
	d.AimPosterior(from, kb.Keys[i].Center, gazein.DefaultEye, prior, p.Landing, aim)
	if aim[i] >= p.LandConf || aim[o] < 0.2 {
		t.Errorf("atterrata sulla I partendo dalla Q: mira I %.2f, O %.2f (la O deve restare plausibile)", aim[i], aim[o])
	}
	normal := p.RequiredMs(prior[i]*float64(len(prior)), 1, 0, false)
	if normal >= p.LandMs {
		t.Fatalf("il caso di prova richiede un tasto veloce (%.0f ms)", normal)
	}
	h := NewHybrid(p, d)
	sel := runHybrid(h, landing(1, kb.Keys[i].Center), prior, 1_000_000, 1_600_000)
	if len(sel) != 1 || float64(sel[0])/1000 < p.LandMs-12 {
		t.Errorf("atterraggio corto sulla I: %v µs, attesa dopo %v ms (senza la regola %.0f ms)", sel, p.LandMs, normal)
	}
	h = NewHybrid(p, d)
	sel = runHybrid(h, landing(2, kb.Keys[o].Center), prior, 2_000_000, 2_600_000)
	if len(sel) != 1 || float64(sel[0])/1000 > p.TMinMs+12 {
		t.Errorf("atterraggio sulla O: %v µs, attesa a ~%v ms", sel, p.TMinMs)
	}
	t.Logf("dalla Q: atterrata sulla I → mira I %.2f, O %.2f, P %.2f", aim[i], aim[o], aim[kb.IndexOf('p')])
}

// Dopo un errore la selezione successiva richiede almeno CautionMs, quella
// dopo torna normale.
func TestHybridCaution(t *testing.T) {
	kb := qwerty(t)
	e, r := kb.IndexOf('e'), kb.IndexOf('r')
	prior := priorWith(kb, map[rune]float64{'e': 0.4, 'r': 0.4})
	p := DefaultHybrid()
	p.PupilGain = 0
	h := NewHybrid(p, decoder(kb))
	runHybrid(h, fix(1, 1_000_000, kb.Keys[e].Center), prior, 1_000_000, 1_300_000)
	h.Feedback(false)
	sel := runHybrid(h, fix(2, 2_000_000, kb.Keys[r].Center), prior, 2_000_000, 2_800_000)
	if len(sel) != 1 || math.Abs(float64(sel[0])/1000-p.CautionMs) > 12 {
		t.Errorf("dopo un errore: %v µs, attesa a ~%v ms", sel, p.CautionMs)
	}
	sel = runHybrid(h, fix(3, 3_000_000, kb.Keys[e].Center), prior, 3_000_000, 3_400_000)
	if len(sel) != 1 || float64(sel[0])/1000 > p.TMinMs+12 {
		t.Errorf("la prudenza dura una selezione: %v µs", sel)
	}
}

// I tasti della barra di modifica si attivano solo guardandoli dentro e
// quelli spenti (priore 0) non esistono per il decoder.
func TestEditBar(t *testing.T) {
	// la barra delle sessioni registrate prima della voce resta identica:
	// tre suggerimenti da 2,8 unità e cancella da 1,6
	old := qwerty(t)
	old.AddEditBar(DefaultBar, DefaultGap, false)
	if len(old.Keys) != 31 || old.IndexOf(KeySpeak) >= 0 {
		t.Fatalf("barra senza voce: %d tasti", len(old.Keys))
	}
	for _, c := range []struct {
		r      rune
		x0, x1 float64
	}{{KeySuggest, 0.02384, 0.28496}, {KeyBackspace, 0.83024, 0.97616}} {
		r := old.Keys[old.IndexOf(c.r)].Rect
		if math.Abs(r[0]-c.x0) > 1e-9 || math.Abs(r[2]-c.x1) > 1e-9 {
			t.Errorf("barra senza voce, tasto %q: x %.5f–%.5f, atteso %.5f–%.5f", c.r, r[0], r[2], c.x0, c.x1)
		}
	}

	kb := qwerty(t)
	kb.AddEditBar(DefaultBar, DefaultGap, true)
	if kb.Chars != 27 || len(kb.Keys) != 32 || !kb.HasEditBar() {
		t.Fatalf("tasti %d, lettere %d", len(kb.Keys), kb.Chars)
	}
	sp := kb.IndexOf(KeySpeak)
	if sp < 0 || !kb.Keys[sp].Strict || !IsEdit(KeySpeak) || kb.Keys[sp].Label != "parla" {
		t.Fatalf("tasto parla: %+v", kb.Keys)
	}
	if b, s := kb.Keys[kb.IndexOf(KeyBackspace)].Rect, kb.Keys[sp].Rect; s[0] <= b[2] || s[2] > DefaultBar[2] {
		t.Errorf("parla deve stare a destra di cancella, dentro la barra: %v %v", b, s)
	}
	d := decoder(kb)
	post := make([]float64, len(kb.Keys))
	prior := make([]float64, len(kb.Keys))
	for i := 0; i < kb.Chars; i++ {
		prior[i] = 1.0 / 27
	}
	bs, s1 := kb.IndexOf(KeyBackspace), kb.IndexOf(KeySuggest)
	prior[bs] = 0.1 / 27 // cancella acceso, suggerimenti spenti
	// sguardo dentro cancella: cancella
	if k, _ := d.Decode(kb.Keys[bs].Center, gazein.DefaultEye, prior, post); k != bs {
		t.Errorf("dentro cancella: %d", k)
	}
	// sul bordo basso di cancella, vicino alle lettere: resta cancella
	low := [2]float64{kb.Keys[bs].Center[0], kb.Keys[bs].Rect[3] - 0.002}
	if k, _ := d.Decode(low, gazein.DefaultEye, prior, post); k != bs {
		t.Errorf("bordo basso di cancella: %d", k)
	}
	// appena sopra la barra (sul testo): niente, anche se è entro 1°
	r := kb.Keys[bs].Rect
	above := [2]float64{kb.Keys[bs].Center[0], r[1] - 0.01}
	if k, _ := d.Decode(above, gazein.DefaultEye, prior, post); k >= 0 {
		t.Errorf("sopra la barra: tasto %d (%q)", k, kb.Keys[k].Char)
	}
	// suggerimento spento: guardarlo non attiva nulla
	if k, _ := d.Decode(kb.Keys[s1].Center, gazein.DefaultEye, prior, post); k == s1 {
		t.Error("un suggerimento spento non si attiva")
	}
	// le lettere non cambiano tempi per la presenza della barra: r su 27 tasti
	h := NewHybrid(DefaultHybrid(), d)
	e := kb.IndexOf('e')
	sel := runHybrid(h, fix(1, 1_000_000, kb.Keys[e].Center), prior, 1_000_000, 1_800_000)
	want := h.P.RequiredMs(1, 1, 0, false)
	if len(sel) != 1 || math.Abs(float64(sel[0])/1000-want) > 12 {
		t.Errorf("lettera con priore uniforme: %v µs, attesa a ~%.0f ms", sel, want)
	}
}
