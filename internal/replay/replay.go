// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Antonio Blescia

// Package replay rigioca una sessione registrata (session.json, samples.csv,
// events.jsonl) attraverso il rilevatore di saccadi e il selettore ibrido con
// parametri a scelta, per misurare l'effetto di una modifica sui dati reali
// prima di provarla dal vivo.
//
// Lo sguardo registrato è quello che la persona ha prodotto con il sistema di
// allora, quindi il replay è un'approssimazione: la persona non vede le
// decisioni del selettore nuovo. Per non perdere la sincronia con quello che
// stava facendo, il replay segue i tasti che lei ha scritto senza sbagliare
// (le selezioni registrate che non hanno introdotto un errore, con il testo
// che c'era prima di ciascuna):
//
//   - il bersaglio corrente è il tasto successivo di questa sequenza;
//   - una selezione giusta lo fa avanzare (come nel motore), una sbagliata no;
//     i tasti di modifica (cancella, suggerimenti) non contano;
//   - se la persona, nella registrazione, aveva già scritto quel tasto e lo
//     ha lasciato (fine della fissazione che conteneva la selezione
//     registrata) senza che il selettore nuovo l'abbia attivato, la lettera è
//     "in ritardo" del tempo che mancava, perché dal vivo avrebbe continuato a
//     guardarla, e il bersaglio avanza.
//
// Nel compito "rigida" la sequenza è il testo da copiare. Con i parametri
// registrati il replay riproduce le selezioni del log: è il controllo di
// fedeltà (Result.Same). Nel compito "testo" la riproduzione è approssimata
// (i suggerimenti mostrati allora non si ricostruiscono).
//
// Anche la scrittura libera si rigioca, come blocchi del compito "testo"
// (condizione CondFree, "L"): ogni tratto di scrittura libera è un blocco, e
// le selezioni che la persona ha poi cancellato contano come sbagliate. È il
// dato più vicino all'uso reale di Kite.
//
// Legge le sessioni di Kite e quelle del banco di prova da cui Kite nasce,
// che hanno lo stesso formato. Lì i blocchi "A" erano scritti con un dwell
// fisso di 1 s: il loro sguardo, rigiocato con il selettore ibrido, dice
// come si comporterebbe il selettore con lo sguardo di chi non lo conosce.
package replay

import (
	"bufio"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/ablescia/kite/internal/correct"
	"github.com/ablescia/kite/internal/engine"
	"github.com/ablescia/kite/internal/gazein"
	"github.com/ablescia/kite/internal/lm"
	"github.com/ablescia/kite/internal/metrics"
	"github.com/ablescia/kite/internal/oculo"
	"github.com/ablescia/kite/internal/selector"
)

// Condizioni registrate nei blocchi.
const (
	CondDwell  = "A" // dwell fisso di 1 s: solo nelle sessioni del banco di prova
	CondHybrid = engine.CondHybrid
	CondFree   = "L" // scrittura libera
)

// Info sono i parametri della sessione (session.json). Le sessioni
// registrate prima della barra di modifica non hanno area, barra e modello
// di lingua: valgono quelli di allora.
type Info struct {
	Layout   string                `json:"layout"`
	Geometry gazein.Geometry       `json:"geometry"`
	Hybrid   selector.HybridParams `json:"hybrid"`
	Sigma    float64               `json:"sigma_deg"`
	Core     float64               `json:"core"`
	Eps      float64               `json:"eps"`
	DwellMs  float64               `json:"dwell_ms"`
	LM       string                `json:"lm"` // "base" (vuoto nelle sessioni vecchie) o "italiano"
	Task     string                `json:"task"`
	Area     [4]float64            `json:"area"`
	Bar      [4]float64            `json:"bar"`
	Gap      float64               `json:"gap"`
	EditBar  bool                  `json:"edit_bar"`
	// tolleranza dei tasti di modifica (assente nelle sessioni precedenti: 0)
	EditMargin float64 `json:"edit_margin_deg"`
	SpeakKey   bool    `json:"speak_key"` // barra con il tasto parla (dopo l'arrivo della voce)
}

// Row è un campione registrato, con i campi che servono al replay.
type Row struct {
	DevUs      int64
	Block      int
	InBlock    bool
	Free       bool // scrittura libera
	Raw        [2]float64
	RawValid   bool
	Gaze       [2]float64
	GazeValid  bool
	Eye        [3]float64
	PupilZ     float64
	PupilValid bool
	PupilComp  bool // pupilla compensata per la luminanza (previsione presente)
	// dal rilevatore registrato: per sapere quando la persona ha lasciato un tasto
	Saccade bool
	FixID   uint64
}

// Selection è una selezione registrata.
type Selection struct {
	DevUs    int64  `json:"dev_us"`
	Key      string `json:"key"`
	Expected string `json:"expected"`
	Correct  bool   `json:"correct"`
	Kind     string `json:"kind"` // compito "testo"
	Text     string `json:"text"` // testo dopo la selezione (compito "testo")
}

// Target è un tasto che la persona ha scritto senza sbagliare.
type Target struct {
	Key     string // lettera o spazio
	DevUs   int64
	Context string // testo per il modello di lingua prima della selezione
	Leave   int64  // quando la persona ha lasciato il tasto
}

// Block è un blocco di copiatura registrato.
type Block struct {
	ID         int
	Cond       string
	Label      string
	Phrase     string
	Task       string
	Compensate bool // modello pupillare valido (la pupilla poteva intervenire)
	// parametri del selettore registrati all'inizio del blocco (nil nelle
	// sessioni precedenti: valgono quelli di session.json)
	Hybrid     *selector.HybridParams
	Selections []Selection
	Targets    []Target
	First      int // indici dei campioni del blocco in Session.Rows
	Last       int
}

// Session è una sessione caricata.
type Session struct {
	Dir    string
	Info   Info
	Rows   []Row
	Blocks []*Block
	models map[string]*lm.Model
	loose  []Selection // tutte le selezioni, per la scrittura libera
	frees  []freeStart // inizi della scrittura libera registrati
}

// freeStart è l'inizio registrato di un tratto di scrittura libera.
type freeStart struct {
	DevUs      int64
	Label      string
	Compensate *bool
	Hybrid     *selector.HybridParams
}

// Load legge una cartella di sessione.
func Load(dir string) (*Session, error) {
	s := &Session{Dir: dir}
	b, err := os.ReadFile(filepath.Join(dir, "session.json"))
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, &s.Info); err != nil {
		return nil, fmt.Errorf("session.json: %w", err)
	}
	if err := s.loadEvents(filepath.Join(dir, "events.jsonl")); err != nil {
		return nil, err
	}
	if err := s.loadSamples(filepath.Join(dir, "samples.csv")); err != nil {
		return nil, err
	}
	// campioni di ogni blocco
	for _, bl := range s.Blocks {
		bl.First, bl.Last = -1, -1
	}
	byID := map[int]*Block{}
	for _, bl := range s.Blocks {
		byID[bl.ID] = bl
	}
	for i, r := range s.Rows {
		if !r.InBlock {
			continue
		}
		if bl := byID[r.Block]; bl != nil {
			if bl.First < 0 {
				bl.First = i
			}
			bl.Last = i
		}
	}
	// L'ultima selezione chiude il blocco prima che il suo campione sia
	// registrato: quel campione risulta già fuori dal blocco.
	for _, bl := range s.Blocks {
		if bl.Last < 0 || len(bl.Selections) == 0 {
			continue
		}
		t := bl.Selections[len(bl.Selections)-1].DevUs
		for j := bl.Last + 1; j < len(s.Rows) && s.Rows[j].DevUs <= t; j++ {
			bl.Last = j
		}
	}
	s.freeBlocks()
	kept := s.Blocks[:0]
	for _, bl := range s.Blocks {
		if bl.First >= 0 {
			bl.targets()
			for i := range bl.Targets {
				bl.Targets[i].Leave = s.leaveTime(bl.Targets[i].DevUs, bl.First, bl.Last)
			}
			kept = append(kept, bl)
		}
	}
	s.Blocks = kept
	return s, nil
}

// targets ricava la sequenza dei tasti scritti senza sbagliare.
func (bl *Block) targets() {
	bl.Targets = nil
	if bl.Task != metrics.TaskText {
		pos := 0
		phrase := []rune(bl.Phrase)
		for _, sel := range bl.Selections {
			if sel.Correct && pos <= len(phrase) {
				bl.Targets = append(bl.Targets, Target{Key: sel.Key, DevUs: sel.DevUs, Context: string(phrase[:pos])})
				pos++
			}
		}
		return
	}
	prev := ""
	for _, sel := range bl.Selections {
		if sel.Correct && (sel.Kind == "lettera" || sel.Kind == "spazio") {
			bl.Targets = append(bl.Targets, Target{Key: sel.Key, DevUs: sel.DevUs, Context: " " + lm.Fold(lastWord(prev))})
		}
		prev = sel.Text
	}
}

// lastWord è la parola in corso alla fine di t (dopo uno spazio o un apostrofo).
func lastWord(t string) string {
	if i := strings.LastIndexAny(t, " '"); i >= 0 {
		return t[i+1:]
	}
	return t
}

// Keyboard ricostruisce la tastiera della sessione.
func (s *Session) Keyboard() (*selector.Keyboard, error) {
	area, gap := selector.LegacyArea, selector.DefaultGap
	if s.Info.Area != [4]float64{} {
		area = s.Info.Area
	}
	if s.Info.Gap > 0 {
		gap = s.Info.Gap
	}
	kb, err := selector.NewKeyboard(s.Info.Layout, area, gap)
	if err != nil {
		return nil, err
	}
	if s.Info.EditBar {
		kb.AddEditBar(s.Info.Bar, gap, s.Info.SpeakKey)
	}
	return kb, nil
}

// Model restituisce il modello di lingua per nome ("" = quello della sessione).
func (s *Session) Model(name string) *lm.Model {
	if name == "" {
		name = s.Info.LM
		if name == "" {
			name = "base"
		}
	}
	if s.models == nil {
		s.models = map[string]*lm.Model{}
	}
	if m, ok := s.models[name]; ok {
		return m
	}
	m := lm.New()
	if name == "base" {
		m = lm.NewBasic()
	}
	s.models[name] = m
	return m
}

func (s *Session) loadEvents(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	byID := map[int]*Block{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		var ev struct {
			Event  string                 `json:"event"`
			Block  int                    `json:"block"`
			Cond   string                 `json:"cond"`
			Label  string                 `json:"label"`
			Phrase string                 `json:"phrase"`
			Comp   *bool                  `json:"pupil_compensated"`
			Task   string                 `json:"task"`
			Hybrid *selector.HybridParams `json:"hybrid"`
			Selection
		}
		if err := json.Unmarshal(sc.Bytes(), &ev); err != nil {
			return fmt.Errorf("events.jsonl: %w", err)
		}
		switch ev.Event {
		case "block_start":
			bl := &Block{ID: ev.Block, Cond: ev.Cond, Label: ev.Label, Phrase: ev.Phrase, Compensate: ev.Comp != nil && *ev.Comp,
				Task: ev.Task, Hybrid: ev.Hybrid}
			byID[ev.Block] = bl
			s.Blocks = append(s.Blocks, bl)
		case "free_start":
			s.frees = append(s.frees, freeStart{DevUs: ev.DevUs, Label: ev.Label, Compensate: ev.Comp, Hybrid: ev.Hybrid})
		case "selection":
			s.loose = append(s.loose, ev.Selection)
			if bl := byID[ev.Block]; bl != nil {
				bl.Selections = append(bl.Selections, ev.Selection)
			}
		}
	}
	return sc.Err()
}

func (s *Session) loadSamples(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	r := csv.NewReader(bufio.NewReaderSize(f, 1<<20))
	r.ReuseRecord = true
	head, err := r.Read()
	if err != nil {
		return fmt.Errorf("samples.csv: %w", err)
	}
	col := map[string]int{}
	for i, h := range head {
		col[h] = i
	}
	need := []string{"dev_us", "mode", "block", "gx", "gy", "g_valid", "rx", "ry", "r_valid",
		"eye_x", "eye_y", "eye_z", "phase", "fix_id", "pupil_z", "pupil_valid"}
	for _, n := range need {
		if _, ok := col[n]; !ok {
			return fmt.Errorf("samples.csv: manca la colonna %s", n)
		}
	}
	for {
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("samples.csv: %w", err)
		}
		fl := func(n string) float64 { v, _ := strconv.ParseFloat(rec[col[n]], 64); return v }
		in := func(n string) int64 { v, _ := strconv.ParseInt(rec[col[n]], 10, 64); return v }
		comp := false
		if _, ok := col["pupil_pred"]; ok {
			comp = fl("pupil_pred") != 0
		}
		s.Rows = append(s.Rows, Row{
			DevUs:      in("dev_us"),
			Block:      int(in("block")),
			InBlock:    rec[col["mode"]] == "block",
			Free:       rec[col["mode"]] == "free",
			Raw:        [2]float64{fl("rx"), fl("ry")},
			RawValid:   rec[col["r_valid"]] == "1",
			Gaze:       [2]float64{fl("gx"), fl("gy")},
			GazeValid:  rec[col["g_valid"]] == "1",
			Eye:        [3]float64{fl("eye_x"), fl("eye_y"), fl("eye_z")},
			PupilZ:     fl("pupil_z"),
			PupilValid: rec[col["pupil_valid"]] == "1",
			PupilComp:  comp,
			Saccade:    rec[col["phase"]] == "saccade",
			FixID:      uint64(in("fix_id")),
		})
	}
	return nil
}

// Variant è un insieme di parametri da provare.
type Variant struct {
	Name       string
	Hybrid     selector.HybridParams
	Sigma      float64
	Core       float64
	Eps        float64
	EditMargin float64 // tolleranza dei tasti di modifica (gradi)
	LM         string  // modello di lingua: "" quello della sessione, "base" o "italiano"
	// PerBlock: i blocchi che hanno registrato i parametri del selettore
	// (dopo l'arrivo dei preset di velocità) si rigiocano con quelli.
	PerBlock bool
}

// Recorded è la variante con i parametri della sessione.
func (s *Session) Recorded() Variant {
	return Variant{Name: "registrato", Hybrid: s.Info.Hybrid, Sigma: s.Info.Sigma, Core: s.Info.Core, Eps: s.Info.Eps,
		EditMargin: s.Info.EditMargin, PerBlock: true}
}

// Pick è una selezione del replay.
type Pick struct {
	Block    int
	Cond     string // condizione registrata del blocco
	TUs      int64
	Key      string
	Expected string
	Correct  bool
	Missed   bool // lettera non attivata prima che la persona lasciasse il tasto
	Late     bool // ... ma attivabile continuando a guardarla: Dec è la decisione più avanzata
	Dec      selector.Decision
}

// Result riassume un replay, per condizione registrata dei blocchi ("A",
// sguardo di chi usava il dwell di 1 s, o "B").
type Result struct {
	Variant string
	Cond    string
	Targets int // lettere da scrivere
	Correct int // attivate giuste
	Errors  int // attivazioni sbagliate
	Late    int // non attivate prima che la persona lasciasse il tasto, con ritardo stimabile
	Missed  int // non attivate e mai diventate candidate: ritardo sconosciuto
	// Anticipo (−) o ritardo (+) medio delle lettere giuste (anche in
	// ritardo) rispetto al log. Sono escluse quelle anticipate di oltre
	// DesyncMs: lì il replay ha perso la sincronia con la persona (ha contato
	// un'occhiata alla lettera successiva mentre lei era ancora indietro).
	DeltaMs  float64
	NDelta   int
	Desync   int
	Deltas   []float64
	Same     int // selezioni uguali al log (stesso tasto, al più un campione di scarto)
	Logged   int // selezioni nel log
	LogError int // errori nel log
	Picks    []Pick
}

// Accuracy = giuste / attivazioni (le lettere in ritardo contano come giuste).
func (r *Result) Accuracy() float64 {
	ok := r.Correct + r.Late
	if ok+r.Errors == 0 {
		return 0
	}
	return float64(ok) / float64(ok+r.Errors)
}

// DesyncMs: oltre questo anticipo una lettera non conta nel Δ.
const DesyncMs = 1000

// Summarize calcola DeltaMs, NDelta e Desync da Deltas.
func (r *Result) Summarize() {
	r.NDelta, r.Desync, r.DeltaMs = 0, 0, 0
	for _, d := range r.Deltas {
		if d < -DesyncMs {
			r.Desync++
			continue
		}
		r.DeltaMs += d
		r.NDelta++
	}
	if r.NDelta > 0 {
		r.DeltaMs /= float64(r.NDelta)
	}
}

// ErrorCostS è il costo stimato di un errore nella copiatura: circa una
// selezione in più (intervallo medio tra selezioni misurato nei blocchi B).
const ErrorCostS = 0.9

// ErrorCostFreeS è il costo di un errore nella scrittura libera, dove va
// visto e cancellato con lo sguardo: mediana misurata sulle sessioni del
// 2026-11 (dalla lettera sbagliata alla successiva giusta, meno una lettera
// normale, per cancella). La media è più del doppio.
const ErrorCostFreeS = 3.9

// NetS è la variazione stimata del tempo totale rispetto al log: anticipi e
// ritardi delle lettere giuste più il costo degli errori in più o in meno.
// Le lettere mancate senza stima non sono contate.
func (r *Result) NetS() float64 {
	cost := ErrorCostS
	if r.Cond == CondFree {
		cost = ErrorCostFreeS
	}
	return r.DeltaMs*float64(r.NDelta)/1000 + float64(r.Errors-r.LogError)*cost
}

// Run rigioca la sessione con la variante v e restituisce i risultati per
// condizione registrata (A e B). Il rilevatore di saccadi scorre tutta la
// sessione, come nel motore.
func (s *Session) Run(v Variant) (map[string]*Result, error) {
	kb, err := s.Keyboard()
	if err != nil {
		return nil, err
	}
	model := s.Model(v.LM)
	geom := s.Info.Geometry
	det := oculo.New(oculo.DefaultConfig(), geom)
	dec := &selector.Decoder{KB: kb, Geom: geom, SigmaDeg: v.Sigma, CoreFrac: v.Core, MarginDeg: 1, EditMarginDeg: v.EditMargin}
	res := map[string]*Result{}

	bi := 0
	var (
		bl     *Block
		h      *selector.Hybrid
		pos    int
		prior  []float64
		logSel map[int64]string
		r      *Result
		best   selector.Decision // decisione più avanzata sul bersaglio, nella fissazione in corso
		bestT  int64
		bestOc oculo.State
		skip   int64
	)
	priorAt := func(pos int) []float64 {
		if pos >= len(bl.Targets) {
			return prior
		}
		pr := engine.KeyPrior(model, kb, bl.Targets[pos].Context, v.Eps)
		if bl.Task == metrics.TaskText {
			engine.EditPrior(pr, kb, v.Eps, engine.EditKeys{Backspace: true})
		}
		return pr
	}
	for i := range s.Rows {
		row := &s.Rows[i]
		st, _ := det.Update(row.DevUs, row.Raw, row.RawValid, row.Eye)
		if bi < len(s.Blocks) && i == s.Blocks[bi].First {
			bl = s.Blocks[bi]
			bi++
			// Le componenti attive nel blocco B sono nell'etichetta; lo
			// sguardo dei blocchi A si rigioca con il selettore completo.
			p := v.Hybrid
			if v.PerBlock && bl.Hybrid != nil {
				p = *bl.Hybrid
			}
			if bl.Cond != CondDwell {
				p.UsePrediction = strings.Contains(bl.Label, "predizione")
				if !strings.Contains(bl.Label, "pupilla") {
					p.PupilGain = 0
				}
			}
			h = selector.NewHybrid(p, dec)
			pos, skip = 0, 0
			prior = priorAt(0)
			best = selector.Decision{Candidate: -1}
			logSel = map[int64]string{}
			for _, sel := range bl.Selections {
				logSel[sel.DevUs] = sel.Key
			}
			if r = res[bl.Cond]; r == nil {
				r = &Result{Variant: v.Name, Cond: bl.Cond}
				res[bl.Cond] = r
			}
			r.Targets += len(bl.Targets)
			r.Logged += len(bl.Selections)
			for _, sel := range bl.Selections {
				if !sel.Correct {
					r.LogError++
				}
			}
		}
		if bl == nil || i < bl.First || i > bl.Last || pos >= len(bl.Targets) {
			continue
		}
		// la persona ha già lasciato il tasto corrente
		for pos < len(bl.Targets) && row.DevUs > bl.Targets[pos].Leave {
			tg := bl.Targets[pos]
			pk := Pick{Block: bl.ID, Cond: bl.Cond, TUs: tg.Leave, Expected: tg.Key, Missed: true, Dec: best}
			if best.Candidate >= 0 && best.RequiredMs > 0 {
				// Dal vivo la persona avrebbe continuato a guardare la
				// lettera fino all'attivazione.
				pk.Late = true
				t := bestT + int64((best.RequiredMs-best.ElapsedMs)*1000)
				pk.TUs = t
				r.Late++
				r.Deltas = append(r.Deltas, float64(t-tg.DevUs)/1000)
				h.Assume(best.Candidate, t, bestOc)
				h.Feedback(true)
			} else {
				r.Missed++
			}
			r.Picks = append(r.Picks, pk)
			pos++
			best = selector.Decision{Candidate: -1}
			prior = priorAt(pos)
		}
		if pos >= len(bl.Targets) || row.DevUs <= skip {
			continue
		}
		in := selector.Input{TUs: row.DevUs, Gaze: row.Gaze, GazeValid: row.GazeValid, Oc: st, Eye: row.Eye,
			Prior: prior, PupilZ: row.PupilZ, PupilOK: row.PupilValid && bl.Compensate}
		d := h.Update(&in)
		exp := bl.Targets[pos].Key
		if st.FixID != bestOc.FixID {
			// conta solo l'ultima fissazione: quella che finisce quando la
			// persona lascia il tasto
			best = selector.Decision{Candidate: -1}
		}
		if d.Candidate >= 0 && string(kb.Keys[d.Candidate].Char) == exp && d.Progress >= best.Progress {
			best, bestT, bestOc = d, row.DevUs, st
		} else if best.Candidate < 0 {
			bestOc = st
		}
		if d.Selected < 0 || selector.IsEdit(kb.Keys[d.Selected].Char) {
			continue
		}
		key := string(kb.Keys[d.Selected].Char)
		ok := key == exp
		r.Picks = append(r.Picks, Pick{Block: bl.ID, Cond: bl.Cond, TUs: row.DevUs, Key: key, Expected: exp, Correct: ok, Dec: d})
		for _, dt := range []int64{0, -11_111, 11_111} {
			if k, found := nearSel(logSel, row.DevUs+dt); found && k == key {
				r.Same++
				break
			}
		}
		h.Feedback(ok)
		if !ok {
			r.Errors++
			continue
		}
		r.Correct++
		r.Deltas = append(r.Deltas, float64(row.DevUs-bl.Targets[pos].DevUs)/1000)
		if bl.Cond == CondDwell {
			// Con il dwell di 1 s la persona restava sul tasto fino alla
			// selezione registrata: dal vivo, vista la selezione, sarebbe
			// passata oltre. Il resto della fissazione non conta.
			skip = bl.Targets[pos].Leave
		}
		pos++
		best = selector.Decision{Candidate: -1}
		prior = priorAt(pos)
	}
	for _, r := range res {
		r.Summarize()
	}
	return res, nil
}

// nearSel cerca una selezione registrata a ±2 ms da t (i tempi del tracker
// hanno passi di ~11,1 ms con piccole variazioni).
func nearSel(m map[int64]string, t int64) (string, bool) {
	for d := int64(-2000); d <= 2000; d++ {
		if k, ok := m[t+d]; ok {
			return k, true
		}
	}
	return "", false
}

// leaveTime è l'istante in cui la persona ha lasciato il tasto della
// selezione registrata in t: la prima saccade (o il cambio di fissazione)
// dopo t, secondo il rilevatore registrato.
func (s *Session) leaveTime(t int64, from, last int) int64 {
	i := sort.Search(last-from+1, func(k int) bool { return s.Rows[from+k].DevUs >= t }) + from
	if i > last {
		return s.Rows[last].DevUs
	}
	fix := s.Rows[i].FixID
	for j := i + 1; j <= last; j++ {
		if s.Rows[j].Saccade || s.Rows[j].FixID != fix {
			return s.Rows[j].DevUs
		}
	}
	return s.Rows[last].DevUs
}

// Sessions elenca le cartelle di sessione sotto root (le più vecchie prima).
func Sessions(root string) ([]string, error) {
	ents, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range ents {
		if !e.IsDir() {
			continue
		}
		d := filepath.Join(root, e.Name())
		if _, err := os.Stat(filepath.Join(d, "samples.csv")); err == nil {
			out = append(out, d)
		}
	}
	sort.Strings(out)
	return out, nil
}

// Word è una parola copiata con tutte le lettere selezionate per scriverla,
// errori compresi: quello che sarebbe entrato nel testo se gli errori non
// fossero stati scartati (compito "rigida": la persona, dopo un errore,
// selezionava la lettera giusta, quindi gli errori sono lettere in più).
type Word struct {
	Block  int
	Cond   string
	Target string
	Typed  string
	Obs    []correct.Obs
}

// Words ricostruisce le parole dei blocchi "rigida", con l'osservazione dello
// sguardo di ogni selezione calcolata come nel motore (Observer).
func (s *Session) Words(v Variant) ([]Word, error) {
	kb, err := s.Keyboard()
	if err != nil {
		return nil, err
	}
	geom := s.Info.Geometry
	det := oculo.New(oculo.DefaultConfig(), geom)
	dec := &selector.Decoder{KB: kb, Geom: geom, SigmaDeg: v.Sigma, CoreFrac: v.Core, MarginDeg: 1, EditMarginDeg: v.EditMargin}
	obsv := engine.NewObserver(dec, v.Hybrid)
	type selAt struct {
		bl  *Block
		sel Selection
	}
	at := map[int64]selAt{}
	for _, bl := range s.Blocks {
		if bl.Task == metrics.TaskText {
			continue
		}
		for _, sel := range bl.Selections {
			at[sel.DevUs] = selAt{bl, sel}
		}
	}
	var out []Word
	cur := map[*Block]*Word{}
	widx := map[*Block]int{}
	closeWord := func(bl *Block) {
		w := cur[bl]
		words := strings.Fields(bl.Phrase)
		if w != nil && widx[bl] < len(words) && len(w.Obs) > 0 {
			w.Target = words[widx[bl]]
			out = append(out, *w)
		}
		widx[bl]++
		cur[bl] = nil
	}
	for i := range s.Rows {
		row := &s.Rows[i]
		st, _ := det.Update(row.DevUs, row.Raw, row.RawValid, row.Eye)
		x, ok := at[row.DevUs]
		if !ok {
			continue
		}
		bl, sel := x.bl, x.sel
		if sel.Key == " " {
			if sel.Correct {
				closeWord(bl)
			}
			continue // uno spazio sbagliato non entra nella parola
		}
		c := []rune(sel.Key)[0]
		if c < 'a' || c > 'z' {
			continue
		}
		if cur[bl] == nil {
			cur[bl] = &Word{Block: bl.ID, Cond: bl.Cond}
		}
		w := cur[bl]
		w.Typed += sel.Key
		w.Obs = append(w.Obs, obsv.Observe(c, &st, row.Eye))
		// ultima lettera del blocco: la parola finale si chiude qui
		if sel.Correct && sel.DevUs == bl.Selections[len(bl.Selections)-1].DevUs {
			closeWord(bl)
		}
	}
	return out, nil
}

// freeBlocks ricava i blocchi della scrittura libera: ogni tratto di
// campioni in modalità "free" con le selezioni che vi cadono. Le stesse
// selezioni escono dai blocchi di copiatura: nella scrittura libera il log
// riporta l'id dell'ultimo blocco, e prima finivano lì.
func (s *Session) freeBlocks() {
	type seg struct{ t0, t1 int64 }
	var segs []seg
	n := 0
	for i := 0; i < len(s.Rows); {
		if !s.Rows[i].Free {
			i++
			continue
		}
		j := i
		for j+1 < len(s.Rows) && s.Rows[j+1].Free {
			j++
		}
		// l'ultima selezione (parla, o quella che chiude) può arrivare un
		// campione dopo la fine del tratto
		g := seg{s.Rows[i].DevUs, s.Rows[j].DevUs + 50_000}
		segs = append(segs, g)
		bl := &Block{ID: 1000 + n, Cond: CondFree, Label: engine.HybridLabel(true, true, true), Task: metrics.TaskText,
			First: i, Last: j}
		n++
		for k := i; k <= j && !bl.Compensate; k++ {
			bl.Compensate = s.Rows[k].PupilComp
		}
		// l'inizio registrato più recente: etichetta, pupilla e parametri
		for _, f := range s.frees {
			if f.DevUs <= g.t0 {
				if f.Label != "" {
					bl.Label = f.Label
				}
				if f.Compensate != nil {
					bl.Compensate = *f.Compensate
				}
				bl.Hybrid = f.Hybrid
			}
		}
		for _, sel := range s.loose {
			if sel.DevUs >= g.t0 && sel.DevUs <= g.t1 {
				bl.Selections = append(bl.Selections, sel)
			}
		}
		markDeleted(bl.Selections)
		if len(bl.Selections) > 0 {
			s.Blocks = append(s.Blocks, bl)
		}
		i = j + 1
	}
	inFree := func(t int64) bool {
		for _, g := range segs {
			if t >= g.t0 && t <= g.t1 {
				return true
			}
		}
		return false
	}
	for _, bl := range s.Blocks {
		if bl.Cond == CondFree {
			continue
		}
		kept := bl.Selections[:0]
		for _, sel := range bl.Selections {
			if !inFree(sel.DevUs) {
				kept = append(kept, sel)
			}
		}
		bl.Selections = kept
	}
	sort.SliceStable(s.Blocks, func(a, b int) bool { return s.Blocks[a].First < s.Blocks[b].First })
}

// markDeleted segna come sbagliate le lettere e gli spazi che la persona ha
// poi cancellato. Segue il testo dopo ogni selezione: una lettera o uno
// spazio aggiungono un carattere, cancella toglie gli ultimi; le altre
// modifiche (suggerimenti, correzioni, parla) riallineano senza segnare.
func markDeleted(sels []Selection) {
	var stack []int // selezione che ha scritto ciascun carattere (−1: ignota)
	del := map[int]bool{}
	for i, sel := range sels {
		n := len([]rune(sel.Text))
		switch sel.Kind {
		case "lettera", "spazio":
			stack = append(stack, i)
			if len(stack) > n {
				stack = stack[len(stack)-n:]
			}
		case "canc":
			for len(stack) > n {
				if k := stack[len(stack)-1]; k >= 0 {
					del[k] = true
				}
				stack = stack[:len(stack)-1]
			}
		default:
			if len(stack) > n {
				stack = stack[:n]
			}
			for len(stack) < n {
				stack = append([]int{-1}, stack...)
			}
		}
	}
	for i := range sels {
		sels[i].Correct = !del[i]
	}
}
