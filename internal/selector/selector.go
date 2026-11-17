// SPDX-License-Identifier: AGPL-3.0-or-later

package selector

import (
	"math"

	"github.com/ablescia/kite/internal/gazein"
	"github.com/ablescia/kite/internal/oculo"
)

// Input è ciò che un selettore riceve a ogni campione (90 Hz).
type Input struct {
	TUs       int64      // orologio del tracker
	Gaze      [2]float64 // sguardo filtrato dal firmware
	GazeValid bool
	Oc        oculo.State // saccadi e fissazioni (sullo sguardo non filtrato)
	Eye       [3]float64
	Prior     []float64 // probabilità a priori di ogni tasto (nil = uniforme; 0 = tasto spento)
	PupilZ    float64   // indice di carico cognitivo
	PupilOK   bool
}

// Decision è l'esito per un campione.
type Decision struct {
	Selected   int     // tasto attivato in questo campione, −1 nessuno
	Candidate  int     // tasto che si sta per attivare, −1 nessuno
	Progress   float64 // 0..1 verso l'attivazione
	ElapsedMs  float64
	RequiredMs float64
	Posterior  float64 // probabilità del candidato dato lo sguardo e il priore
	PriorRatio float64 // priore del candidato × numero di tasti (1 = come il caso)
	Aim        float64 // probabilità che la saccade mirasse al candidato (1 se non valutata)
	FixID      uint64
	Wait       string // perché il candidato non è ancora scattato o ha un tempo più lungo (Wait*; "" = tempo base)
}

// Motivi di Decision.Wait. I primi tre fermano il conteggio, gli altri
// allungano il tempo richiesto.
const (
	WaitSaccade = "saccade"     // dopo una selezione: un altro tasto richiede una saccade
	WaitLeave   = "esci"        // doppia improbabile: si ripete uscendo e rientrando nel tasto
	WaitDrift   = "instabile"   // fissazione dispersa senza saccade: il conteggio riparte
	WaitRepeat  = "ripeti"      // stessa lettera tenendo il tasto
	WaitLanding = "atterraggio" // possibile atterraggio corto: si aspetta la saccade correttiva
	WaitAim     = "mira"        // la saccade mirava altrove: tempo massimo
	WaitCaution = "prudenza"    // dopo un errore o all'attivazione della tastiera
	WaitRecover = "ripresa"     // tasto di modifica scelto dalla finestra recente di una fissazione dispersa
)

// Selector è una strategia di attivazione.
type Selector interface {
	Update(in *Input) Decision
	Reset()
	Name() string
}

// Learner è un selettore che usa l'esito delle proprie selezioni, quando è
// noto: giusta (copiatura) o sbagliata (copiatura, o cancellata).
type Learner interface {
	Feedback(ok bool)
}

// ---------------------------------------------------------------------------
// Decoder bayesiano: espansione invisibile dei bersagli

// Valori predefiniti del decoder.
const (
	DefaultSigmaDeg = 1.6
	DefaultCoreFrac = 0.6
)

// Decoder sceglie il tasto più probabile dato un punto di sguardo e il
// priore del modello di lingua:
//
//	P(k | g) ∝ P(k) · exp(−d_k(g)² / 2σ²)
//
// d_k è la distanza dalla zona che si mira del tasto: il centro per un tasto
// quadrato, la linea centrale per uno allungato come la barra spaziatrice
// (il rettangolo ristretto di metà del lato corto per parte). Con priori
// uguali i confini sono quelli geometrici; un tasto probabile allarga la sua
// area verso i vicini meno probabili di σ²·ln(P_a/P_b)/d (d = distanza tra i
// centri). L'ancoraggio impedisce che un tasto perda il proprio nucleo
// centrale (CoreFrac della sua dimensione): guardando il centro di un tasto
// si ottiene sempre quel tasto.
type Decoder struct {
	KB        *Keyboard
	Geom      gazein.Geometry
	SigmaDeg  float64 // dispersione dello sguardo attorno alla zona mirata del tasto
	CoreFrac  float64
	MarginDeg float64 // oltre l'area della tastiera + margine: nessun tasto
	// EditMarginDeg: i tasti di modifica (Strict) accettano anche lo sguardo
	// fino a questa distanza fuori dal tasto, se non cade su una lettera; tra
	// due tasti vince il più vicino (0 = solo dentro il tasto).
	EditMarginDeg float64
	logp          []float64
	aim           [][4]float64 // zona mirata di ogni tasto, normalizzata
}

// aims calcola le zone mirate: il rettangolo del tasto ristretto, in mm,
// di metà del suo lato corto su ogni lato (un punto o un segmento).
func (d *Decoder) aims() [][4]float64 {
	if len(d.aim) == len(d.KB.Keys) {
		return d.aim
	}
	d.aim = make([][4]float64, len(d.KB.Keys))
	for i, k := range d.KB.Keys {
		w := (k.Rect[2] - k.Rect[0]) * d.Geom.WidthMM
		h := (k.Rect[3] - k.Rect[1]) * d.Geom.HeightMM
		s := math.Min(w, h) / 2
		sx, sy := s/d.Geom.WidthMM, s/d.Geom.HeightMM
		d.aim[i] = [4]float64{k.Rect[0] + sx, k.Rect[1] + sy, k.Rect[2] - sx, k.Rect[3] - sy}
		if d.aim[i][0] > d.aim[i][2] {
			d.aim[i][0], d.aim[i][2] = k.Center[0], k.Center[0]
		}
		if d.aim[i][1] > d.aim[i][3] {
			d.aim[i][1], d.aim[i][3] = k.Center[1], k.Center[1]
		}
	}
	return d.aim
}

// Decode riempie post (lunghezza = tasti) e restituisce il tasto scelto e la
// sua probabilità a posteriori; −1 se il punto è fuori dalla tastiera.
func (d *Decoder) Decode(p [2]float64, eye [3]float64, prior []float64, post []float64) (int, float64) {
	kb := d.KB
	mmDeg := d.Geom.MMPerDeg(eye)
	g := d.Geom.ToMM(p)
	// Dentro un tasto stretto acceso (o entro EditMarginDeg) vale solo quel
	// tasto: niente contesa con le lettere vicine.
	if k := d.strictAt(p, g, prior, mmDeg); k >= 0 {
		for i := range post {
			post[i] = 0
		}
		post[k] = 1
		return k, 1
	}
	// Nessun tasto se lo sguardo è a più di MarginDeg da tutte le lettere:
	// la striscia del testo e le zone vuote della tastiera sfalsata (ai lati
	// della Z, della M, della barra) non attivano nulla.
	margin := d.MarginDeg * mmDeg
	near := false
	for i := range kb.Keys {
		if off(prior, i) || kb.Keys[i].Strict {
			continue
		}
		r := &kb.Keys[i].Rect
		c := d.Geom.ToMM([2]float64{math.Max(r[0], math.Min(p[0], r[2])), math.Max(r[1], math.Min(p[1], r[3]))})
		if math.Hypot(g[0]-c[0], g[1]-c[1]) <= margin {
			near = true
			break
		}
	}
	if !near {
		return -1, 0
	}
	if len(d.logp) != len(kb.Keys) {
		d.logp = make([]float64, len(kb.Keys))
	}
	sigma := d.SigmaDeg * mmDeg
	best, maxL := -1, math.Inf(-1)
	for i := range kb.Keys {
		if off(prior, i) || kb.Keys[i].Strict {
			d.logp[i] = math.Inf(-1)
			continue
		}
		c := d.Geom.ToMM(d.AimPoint(i, p))
		dx, dy := g[0]-c[0], g[1]-c[1]
		pr := 1.0 / float64(len(kb.Keys))
		if prior != nil {
			pr = prior[i]
		}
		l := math.Log(math.Max(pr, 1e-12)) - (dx*dx+dy*dy)/(2*sigma*sigma)
		d.logp[i] = l
		if l > maxL {
			best, maxL = i, l
		}
	}
	var s float64
	for i, l := range d.logp {
		post[i] = math.Exp(l - maxL)
		s += post[i]
	}
	for i := range post {
		post[i] /= s
	}
	if best < 0 {
		return -1, 0
	}
	// Ancoraggio: il nucleo centrale appartiene sempre al suo tasto.
	if k := kb.Hit(p); k >= 0 && !off(prior, k) {
		key := &kb.Keys[k]
		hw := (key.Rect[2] - key.Rect[0]) * d.CoreFrac / 2
		hh := (key.Rect[3] - key.Rect[1]) * d.CoreFrac / 2
		if math.Abs(p[0]-key.Center[0]) <= hw && math.Abs(p[1]-key.Center[1]) <= hh {
			best = k
		}
	}
	return best, post[best]
}

// strictAt è il tasto stretto acceso che contiene p (g in mm) o, con
// EditMarginDeg > 0, il più vicino entro quella distanza; −1 se nessuno.
// Lo sguardo che cade su una lettera resta alla lettera.
func (d *Decoder) strictAt(p [2]float64, g [3]float64, prior []float64, mmDeg float64) int {
	kb := d.KB
	if k := kb.Hit(p); k >= 0 {
		if !kb.Keys[k].Strict {
			return -1
		}
		if !off(prior, k) {
			return k
		}
	}
	if d.EditMarginDeg <= 0 {
		return -1
	}
	best, bd := -1, d.EditMarginDeg*mmDeg
	for i := range kb.Keys {
		if !kb.Keys[i].Strict || off(prior, i) {
			continue
		}
		r := &kb.Keys[i].Rect
		c := d.Geom.ToMM([2]float64{math.Max(r[0], math.Min(p[0], r[2])), math.Max(r[1], math.Min(p[1], r[3]))})
		if dd := math.Hypot(g[0]-c[0], g[1]-c[1]); dd <= bd {
			best, bd = i, dd
		}
	}
	return best
}

// off dice se il tasto i è spento (priore esattamente 0: per esempio uno
// spazio dei suggerimenti vuoto).
func off(prior []float64, i int) bool { return prior != nil && prior[i] == 0 }

// AimPoint è il punto della zona mirata del tasto k più vicino a p. La scala
// tra coordinate normalizzate e mm è separata per asse, quindi basta limitare
// ciascuna coordinata.
func (d *Decoder) AimPoint(k int, p [2]float64) [2]float64 {
	a := d.aims()[k]
	return [2]float64{math.Max(a[0], math.Min(p[0], a[2])), math.Max(a[1], math.Min(p[1], a[3]))}
}

// Landing descrive dove atterra una saccade diretta a un bersaglio: in media
// copre Gain della distanza (le saccadi lunghe cadono corte, poi una saccade
// correttiva completa il tragitto), con dispersione GainSD lungo la
// direzione e, di traverso, PerpFrac della distanza (almeno PerpMinDeg).
type Landing struct {
	Gain, GainSD         float64
	PerpFrac, PerpMinDeg float64
}

// DefaultLanding: valori misurati sulle sessioni reali con il 4C (saccadi
// oltre 8° verso il tasto poi selezionato: guadagno mediano 0,93–0,95,
// scarto interquartile 0,15–0,18).
func DefaultLanding() Landing {
	return Landing{Gain: 0.94, GainSD: 0.12, PerpFrac: 0.08, PerpMinDeg: 0.7}
}

// AimPosterior riempie out con la probabilità che la saccade partita da from
// e atterrata in to fosse diretta a ciascun tasto, dato il priore. Un tasto
// più lontano lungo la stessa direzione resta plausibile anche se lo sguardo
// è atterrato sul vicino: è il caso dell'atterraggio corto.
func (d *Decoder) AimPosterior(from, to [2]float64, eye [3]float64, prior []float64, m Landing, out []float64) {
	mmDeg := d.Geom.MMPerDeg(eye)
	o, l := d.Geom.ToMM(from), d.Geom.ToMM(to)
	ux, uy := l[0]-o[0], l[1]-o[1]
	maxL := math.Inf(-1)
	for i := range d.KB.Keys {
		c := d.Geom.ToMM(d.AimPoint(i, to))
		vx, vy := c[0]-o[0], c[1]-o[1]
		D := math.Hypot(vx, vy)
		if D < mmDeg {
			out[i] = math.Inf(-1) // il tasto da cui parte la saccade
			continue
		}
		g := (ux*vx + uy*vy) / (D * D)
		perp := (ux*vy - uy*vx) / D
		sp := math.Max(m.PerpMinDeg*mmDeg, m.PerpFrac*D)
		pr := 1.0 / float64(len(d.KB.Keys))
		if prior != nil {
			pr = prior[i]
		}
		dg := (g - m.Gain) / m.GainSD
		out[i] = math.Log(math.Max(pr, 1e-12)) - dg*dg/2 - perp*perp/(2*sp*sp) - math.Log(m.GainSD*D*sp)
		maxL = math.Max(maxL, out[i])
	}
	var s float64
	for i := range out {
		out[i] = math.Exp(out[i] - maxL)
		s += out[i]
	}
	for i := range out {
		out[i] /= s
	}
}

// ---------------------------------------------------------------------------
// Selettore ibrido: dwell adattivo guidato dalle saccadi

// HybridParams sono i parametri del selettore ibrido.
//
// I campi aggiunti dopo la prima versione valgono "spento" a zero: una
// sessione registrata prima si rigioca (cmd/replay) con i propri parametri e
// dà le stesse selezioni.
type HybridParams struct {
	TMinMs, TMaxMs float64 // tempo di fissazione richiesto: minimo e massimo
	// Il tempo dipende dal rapporto r = priore × tasti: r ≥ RHi → TMinMs,
	// r ≤ RLo → TMaxMs, in mezzo lineare in log(r).
	RHi, RLo float64
	SHi      float64 // posteriore sotto SHi (sguardo ambiguo tra due tasti) allunga il tempo
	// Stessa lettera senza uscire dal tasto: tempo minimo dalla selezione.
	// Con RepeatMinMs > 0 dipende da quanto la doppia è probabile, come i
	// tempi di attivazione: RepeatMinMs se r ≥ RHi, RepeatMs se r ≤ RLo.
	RepeatMs, RepeatMinMs float64
	// PupilGain: con indice di carico Z > 0 i tempi crescono di
	// PupilGain·min(Z, 2) (0 = la pupilla non interviene).
	PupilGain     float64
	UsePrediction bool // false: priore uniforme, niente espansione e tempi uguali per tutti

	// LockFixation: dopo una selezione nessun altro tasto si attiva finché
	// l'occhio non compie una saccade (lo sguardo che resta o scivola sul
	// vicino non scrive), e lo stesso tasto si ripete solo uscendo e
	// rientrando oppure tenendolo RepeatMs. Senza: solo la ripetizione nella
	// stessa fissazione è frenata (prima versione).
	LockFixation bool

	// Atterraggio: nella prima fissazione dopo una saccade di almeno
	// LandAmpDeg, se la probabilità che la saccade mirasse al tasto sotto lo
	// sguardo è sotto LandConf (atterraggio forse corto rispetto a un tasto
	// più lontano), il tasto richiede almeno LandMs, il tempo entro cui di
	// solito parte la saccade correttiva. LandMs = 0: spento.
	LandMs, LandAmpDeg, LandConf float64
	Landing                      Landing

	// Prudenza dopo un errore: per le CautionSel selezioni successive il
	// tempo richiesto è almeno CautionMs (0 = spento). Interrompe le catene
	// di errori mentre la persona cerca il tasto giusto.
	CautionMs  float64
	CautionSel int

	// Arm: armamento della tastiera. Quando la tastiera si attiva (Reset) o
	// torna attiva dopo una pausa (Rearm, per esempio a voce finita) il
	// tempo non si conta da prima di quell'istante, anche se lo sguardo era
	// già fermo su un tasto, e la prima selezione chiede almeno CautionMs:
	// chi apre la tastiera deve avere il tempo di guardare dove vuole.
	Arm bool

	// Tasti di modifica (cancella, suggerimenti, parla): tempo fisso EditMs,
	// SpeakMs per parla, che detta la frase e la cancella; non dipendono dal
	// modello di lingua né da TMaxMs (0 = come le lettere improbabili,
	// TMaxMs). CancRepeatMs: cancella ripetuto tenendo il tasto (0 =
	// RepeatMs).
	EditMs, SpeakMs, CancRepeatMs float64

	// RepeatHoldMinR: una lettera si ripete tenendo il tasto solo se la
	// doppia è plausibile (r ≥ RepeatHoldMinR); altrimenti si esce e si
	// rientra (0 = sempre, prima versione). Una doppia improbabile tenuta a
	// lungo è quasi sempre una pausa per pensare, non una doppia.
	RepeatHoldMinR float64

	// AimVeto: dopo una saccade lunga, una lettera a cui la saccade
	// probabilmente non mirava (probabilità < AimVeto) richiede TMaxMs: lo
	// sguardo è finito lì per strada, o il modello di lingua ne ha allargato
	// l'area oltre la lettera guardata (0 = spento).
	AimVeto float64

	// RestartUnstable: se la fissazione si disperde senza una saccade
	// (oculo Drift), il conteggio riparte da capo e l'avanzamento torna a
	// zero, invece di restare pieno senza poter scattare.
	RestartUnstable bool
	// EditRecover: in una fissazione dispersa, un tasto di modifica guardato
	// adesso (finestra recente stabile) si può ancora scegliere, con il
	// conteggio da capo. Le lettere no: uno sguardo che scivola a lungo su
	// una lettera è quasi sempre una pausa, non una scelta.
	EditRecover bool
}

// DefaultHybrid sono i valori di partenza: i tempi sono quelli del preset
// "normale" (Speeds).
func DefaultHybrid() HybridParams {
	return HybridParams{TMinMs: 200, TMaxMs: 1000, RHi: 4, RLo: 0.1, SHi: 0.95, RepeatMs: 1500, RepeatMinMs: 700,
		PupilGain: 0.25, UsePrediction: true,
		LockFixation: true,
		LandMs:       280, LandAmpDeg: 8, LandConf: 0.8, Landing: DefaultLanding(),
		CautionMs: 500, CautionSel: 1,
		Arm:    true,
		EditMs: 700, SpeakMs: 1000, CancRepeatMs: 1000,
		RepeatHoldMinR:  0.5,
		AimVeto:         0.3,
		RestartUnstable: true, EditRecover: true}
}

// Speed è un preset dei tempi delle lettere.
type Speed struct {
	Name           string
	TMinMs, TMaxMs float64
}

// Speeds sono i preset, dal più veloce al più prudente. Misurati rigiocando
// le sessioni registrate (cmd/replay): rispetto ai tempi di prima (120–700
// ms) "normale" dimezza abbondantemente le lettere sbagliate e costa circa
// 0,1 s per lettera.
var Speeds = []Speed{
	{"veloce", 200, 800},
	{"normale", 200, 1000},
	{"prudente", 250, 1100},
}

// DefaultSpeed è il preset predefinito.
const DefaultSpeed = "normale"

// SpeedByName cerca un preset per nome.
func SpeedByName(name string) (Speed, bool) {
	for _, s := range Speeds {
		if s.Name == name {
			return s, true
		}
	}
	return Speed{}, false
}

// Hybrid è il selettore ibrido.
type Hybrid struct {
	P   HybridParams
	Dec *Decoder

	post        []float64
	aim         []float64
	cand        int
	candFix     uint64
	candSinceUs int64
	candRecov   bool // candidato scelto dalla finestra recente (EditRecover)
	lastKey     int
	lastFix     uint64
	lastSelUs   int64
	lastPoint   [2]float64 // punto della selezione
	moved, left bool       // dall'ultima selezione: una saccade; una fissazione fuori dal tasto
	caution     int
	uniform     []float64
	armUs       int64 // istante dell'attivazione (−1: al prossimo campione)
}

// Armer è un selettore che va riarmato quando la tastiera torna attiva dopo
// una pausa.
type Armer interface {
	Rearm()
}

// NewHybrid crea il selettore.
func NewHybrid(p HybridParams, dec *Decoder) *Hybrid {
	h := &Hybrid{P: p, Dec: dec}
	h.Reset()
	return h
}

func (h *Hybrid) Name() string { return "ibrido" }

func (h *Hybrid) Reset() {
	h.cand, h.lastKey, h.candFix, h.lastFix = -1, -1, 0, 0
	h.moved, h.left, h.caution = true, true, 0
	h.armUs = -1
	h.arm()
}

// Rearm riarma la tastiera tornata attiva (vedi HybridParams.Arm): conta
// solo il tempo da adesso e la prima selezione è prudente. L'ultima
// selezione resta quella di prima.
func (h *Hybrid) Rearm() {
	h.armUs, h.cand = -1, -1
	h.arm()
}

func (h *Hybrid) arm() {
	if h.P.Arm && h.P.CautionMs > 0 {
		h.caution = max(h.caution, h.P.CautionSel, 1)
	}
}

// uPrior porta il rapporto r col caso su [0, 1]: 0 se r ≥ RHi (tasto molto
// probabile), 1 se r ≤ RLo, in mezzo lineare in log(r).
func (p *HybridParams) uPrior(r float64) float64 {
	u := (math.Log(p.RHi) - math.Log(math.Max(r, 1e-9))) / (math.Log(p.RHi) - math.Log(p.RLo))
	return math.Max(0, math.Min(1, u))
}

// RepeatReqMs è il tempo per ripetere la lettera senza uscire dal tasto.
func (p *HybridParams) RepeatReqMs(r float64) float64 {
	if p.RepeatMinMs <= 0 {
		return p.RepeatMs
	}
	return p.RepeatMinMs + (p.RepeatMs-p.RepeatMinMs)*p.uPrior(r)
}

// pupil è il fattore con cui il carico cognitivo allunga i tempi.
func (p *HybridParams) pupil(pupilZ float64, pupilOK bool) float64 {
	if p.PupilGain > 0 && pupilOK {
		return 1 + p.PupilGain*math.Max(0, math.Min(pupilZ, 2))
	}
	return 1
}

// RequiredMs è il tempo di fissazione richiesto per un tasto, dati il
// rapporto r col caso, la posteriore spaziale e l'indice pupillare.
func (p *HybridParams) RequiredMs(r, post, pupilZ float64, pupilOK bool) float64 {
	uSpat := (p.SHi - post) / (p.SHi - 0.5)
	u := math.Max(0, math.Min(1, math.Max(p.uPrior(r), uSpat)))
	return (p.TMinMs + (p.TMaxMs-p.TMinMs)*u) * p.pupil(pupilZ, pupilOK)
}

// EditReqMs è il tempo di un tasto di modifica (con EditMs > 0).
func (p *HybridParams) EditReqMs(c rune, pupilZ float64, pupilOK bool) float64 {
	t := p.EditMs
	if c == KeySpeak && p.SpeakMs > 0 {
		t = p.SpeakMs
	}
	return t * p.pupil(pupilZ, pupilOK)
}

// Feedback riceve l'esito dell'ultima selezione: se era sbagliata, prudenza
// per le selezioni successive.
func (h *Hybrid) Feedback(ok bool) {
	if !ok && h.lastKey >= 0 && h.P.CautionMs > 0 {
		h.caution = h.P.CautionSel
	}
}

func (h *Hybrid) Update(in *Input) Decision {
	dec := Decision{Selected: -1, Candidate: -1, Aim: 1}
	if h.armUs < 0 {
		h.armUs = in.TUs
	}
	oc := &in.Oc
	if oc.Phase != oculo.Fixation || oc.FixN == 0 {
		h.cand = -1
		return dec
	}
	n := len(h.Dec.KB.Keys)
	if len(h.post) != n {
		h.post = make([]float64, n)
		h.aim = make([]float64, n)
		h.uniform = make([]float64, n)
		for i := range h.uniform {
			h.uniform[i] = 1 / float64(n)
		}
	}
	prior := in.Prior
	if !h.P.UsePrediction || prior == nil {
		// senza predizione: tutti i tasti accesi alla pari
		for i := range h.uniform {
			h.uniform[i] = 1 / float64(h.Dec.KB.Chars)
			if off(in.Prior, i) {
				h.uniform[i] = 0
			}
		}
		prior = h.uniform
	}
	p := oc.FixCentroid
	stable := oc.FixStable
	k, post := h.Dec.Decode(p, in.Eye, prior, h.post)
	// Fissazione dispersa: un tasto di modifica su cui lo sguardo si è
	// fermato adesso resta selezionabile, con il conteggio da capo.
	recov := false
	if h.P.EditRecover && oc.Drift && oc.WinStable {
		if kw, pw := h.Dec.Decode(oc.WinCentroid, in.Eye, prior, h.post); kw >= 0 && h.Dec.KB.Keys[kw].Strict {
			p, k, post, stable, recov = oc.WinCentroid, kw, pw, true, true
		}
	}

	// Movimento dall'ultima selezione: una fissazione nuova dopo una saccade
	// (o, dopo un ammiccamento, lontana dal punto della selezione), e poi
	// una fissazione su un altro tasto o fuori dalla tastiera.
	if !h.moved && oc.FixID != h.lastFix {
		far := h.Dec.Geom.AngleDeg(in.Eye, oc.FixCentroid, h.lastPoint) > 1.5
		h.moved = oc.AfterSaccade || far
	}
	if h.moved && k != h.lastKey {
		h.left = true
	}
	if k < 0 {
		h.cand = -1
		return dec
	}
	if k != h.cand || oc.FixID != h.candFix || recov != h.candRecov {
		// Fissazione nuova: il conteggio parte dalla fine della saccade.
		// Stesso sguardo che scivola su un altro tasto, o tasto ripreso dalla
		// finestra recente: parte da adesso.
		since := oc.FixStartUs
		if oc.FixID == h.candFix && h.cand >= 0 || recov {
			since = in.TUs
		}
		h.cand, h.candFix, h.candSinceUs, h.candRecov = k, oc.FixID, since, recov
	}
	key := &h.Dec.KB.Keys[k]
	edit := key.Strict
	// r si misura sui tasti di lettere e spazio: i tasti di modifica non
	// cambiano i tempi delle lettere.
	r := prior[k] * float64(h.Dec.KB.Chars)
	req, wait := h.P.RequiredMs(r, post, in.PupilZ, in.PupilOK), ""
	if edit && h.P.EditMs > 0 {
		req = h.P.EditReqMs(key.Char, in.PupilZ, in.PupilOK)
	}
	if recov {
		wait = WaitRecover
	}
	dec.Candidate, dec.Posterior, dec.PriorRatio, dec.FixID = k, post, r, oc.FixID
	if h.P.RestartUnstable && oc.Drift && !recov {
		// lo sguardo è scivolato: niente avanzamento finché non si ferma
		// con una saccade nuova
		h.candSinceUs = in.TUs
		dec.RequiredMs, dec.Wait = req, WaitDrift
		return dec
	}
	start := h.candSinceUs
	if h.P.Arm && start < h.armUs {
		start = h.armUs
	}

	if h.P.LockFixation && h.lastKey >= 0 {
		if k != h.lastKey && !h.moved {
			// lo sguardo è ancora quello della selezione: aspetta una saccade
			dec.Wait = WaitSaccade
			return dec
		}
		if k == h.lastKey && !h.left {
			// ripetizione senza uscire dal tasto: va tenuto più a lungo,
			// e una doppia improbabile si fa solo uscendo e rientrando
			if h.P.RepeatHoldMinR > 0 && !edit && r < h.P.RepeatHoldMinR {
				dec.Wait = WaitLeave
				return dec
			}
			start = max(start, h.lastSelUs)
			rq := h.P.RepeatReqMs(r)
			if key.Char == KeyBackspace && h.P.CancRepeatMs > 0 {
				rq = h.P.CancRepeatMs
			}
			if rq > req {
				req, wait = rq, WaitRepeat
			}
		}
	} else if k == h.lastKey && (oc.FixID == h.lastFix || !oc.AfterSaccade) {
		// Prima versione: ripetere la stessa lettera senza una saccade nel
		// mezzo (stessa fissazione, o ripresa dopo un ammiccamento) richiede
		// un tempo minimo.
		start = max(start, h.lastSelUs)
		if h.P.RepeatMs > req {
			req, wait = h.P.RepeatMs, WaitRepeat
		}
	}

	// Atterraggio dopo una saccade lunga: se l'occhio può essere caduto corto
	// rispetto a un tasto più lontano, si aspetta la saccade correttiva; se
	// la saccade mirava proprio altrove, la lettera chiede il tempo massimo.
	if h.P.LandMs > 0 && oc.AfterSaccade && oc.Last.AmplitudeDeg >= h.P.LandAmpDeg {
		h.Dec.AimPosterior(oc.Last.From, p, in.Eye, prior, h.P.Landing, h.aim)
		dec.Aim = h.aim[k]
		if dec.Aim < h.P.LandConf && h.P.LandMs > req {
			req, wait = h.P.LandMs, WaitLanding
		}
		if !edit && dec.Aim < h.P.AimVeto && h.P.TMaxMs > req {
			req, wait = h.P.TMaxMs, WaitAim
		}
	}
	if h.caution > 0 && h.P.CautionMs > req {
		req, wait = h.P.CautionMs, WaitCaution
	}

	el := float64(in.TUs-start) / 1000
	dec.ElapsedMs, dec.RequiredMs, dec.Wait = el, req, wait
	dec.Progress = math.Max(0, math.Min(1, el/req))
	if stable && el >= req {
		dec.Selected = k
		h.commit(k, in.TUs, oc, p)
	}
	return dec
}

func (h *Hybrid) commit(k int, tUs int64, oc *oculo.State, p [2]float64) {
	h.lastKey, h.lastFix, h.lastSelUs = k, oc.FixID, tUs
	h.lastPoint = p
	h.moved, h.left = false, false
	if h.caution > 0 {
		h.caution--
	}
}

// Assume registra come avvenuta una selezione del tasto k, con lo stato
// oculare di quell'istante. Serve al replay: una lettera che il selettore non
// ha attivato prima che la persona lasciasse il tasto, ma che dal vivo lei
// avrebbe atteso continuando a guardarla.
func (h *Hybrid) Assume(k int, tUs int64, oc oculo.State) {
	h.commit(k, tUs, &oc, oc.FixCentroid)
}

// Regions classifica una griglia di punti con il decoder (per mostrare le
// aree di tocco effettive): out[y*nx+x] = tasto, −1 fuori.
func (d *Decoder) Regions(prior []float64, nx, ny int, out []int) {
	post := make([]float64, len(d.KB.Keys))
	for y := 0; y < ny; y++ {
		for x := 0; x < nx; x++ {
			p := [2]float64{(float64(x) + 0.5) / float64(nx), (float64(y) + 0.5) / float64(ny)}
			k, _ := d.Decode(p, gazein.DefaultEye, prior, post)
			out[y*nx+x] = k
		}
	}
}
