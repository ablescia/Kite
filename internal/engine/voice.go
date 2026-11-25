// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Antonio Blescia

package engine

import (
	"context"
	"strings"
	"time"

	"github.com/ablescia/kite/internal/correct"
	"github.com/ablescia/kite/internal/selector"
)

// La voce: il tasto parla fa dire il testo scritto alla sintesi vocale e,
// quando la voce ha finito, cancella il testo per il messaggio successivo.
//
// Mentre la voce parla la tastiera tace: chi ascolta, o guarda chi ascolta,
// non scrive per sbaglio. Resta acceso solo il tasto parla, che diventa
// stop, ma solo dopo che lo sguardo ne è uscito: chi ha appena scelto parla
// e continua a guardarlo non ferma la voce. Fermata la voce, o se la voce
// non riesce a parlare, il testo resta. Cancella subito dopo la voce
// ripristina il testo detto.
//
// Quando la voce finisce, o viene fermata, la tastiera si riarma: lo sguardo
// che è già fermo su un tasto non lo scrive subito (selector.Armer).
//
// Il tasto parla è acceso solo nella scrittura libera: nell'esercizio di
// copiatura cancellerebbe il testo da misurare.

type voiceState struct {
	on     bool // la voce sta parlando
	armed  bool // lo sguardo è uscito dal tasto: adesso stop si può scegliere
	id     uint64
	text   string
	cancel context.CancelFunc
	start  time.Time
	err    string
	errAt  time.Time
	said   string // ultimo testo detto e cancellato
	saidAt time.Time
}

// speakKey è il tasto parla: fa dire il testo, o ferma la voce se sta già
// parlando.
func (e *Engine) speakKey(now time.Time) (Edit, bool) {
	if e.voice.on {
		e.stopVoice("interrotta")
		return Edit{Kind: "stop"}, true
	}
	if e.cfg.Speaker == nil || e.mode != ModeFree || strings.TrimSpace(e.ed.String()) == "" {
		return Edit{}, false
	}
	// L'ultima parola si chiude come con lo spazio: il correttore la
	// sistema prima che la voce la dica.
	if e.ed.open() {
		e.edit(' ', correct.Obs{}, now)
	}
	text := strings.TrimSpace(e.ed.String())
	e.voice.id++
	id, sp := e.voice.id, e.cfg.Speaker
	ctx, cancel := context.WithCancel(context.Background())
	e.voice.on, e.voice.armed, e.voice.text, e.voice.cancel, e.voice.start = true, false, text, cancel, now
	e.voice.err = ""
	e.cfg.Log.Event("speech_start", map[string]any{"text": text, "chars": len([]rune(text))})
	go func() {
		err := sp.Say(ctx, text)
		e.voiceDone(id, err)
	}()
	return Edit{Kind: "parla"}, true
}

// voiceDone riceve l'esito della voce: a voce finita il testo si cancella.
func (e *Engine) voiceDone(id uint64, err error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if id != e.voice.id || !e.voice.on {
		return // fermata, o superata da un'altra
	}
	now := time.Now()
	e.voice.on = false
	e.voice.cancel()
	ev := map[string]any{"outcome": "finita", "dur_ms": now.Sub(e.voice.start).Milliseconds()}
	if err != nil {
		e.voice.err, e.voice.errAt = err.Error(), now
		ev["outcome"], ev["error"] = "errore", err.Error()
	} else {
		// detto per intero: il messaggio diventa una frase imparata
		e.learnPhrase(e.voice.text)
		if e.ed != nil {
			e.ed.clearSaid()
			e.voice.said, e.voice.saidAt = e.voice.text, now
		}
	}
	e.cfg.Log.Event("speech_end", ev)
	e.rearm()
	e.recomputePrior()
	e.syncTask()
}

// stopVoice ferma la voce (il testo resta).
func (e *Engine) stopVoice(outcome string) {
	if !e.voice.on {
		return
	}
	e.voice.cancel()
	e.voice.on = false
	e.cfg.Log.Event("speech_end", map[string]any{"outcome": outcome, "dur_ms": time.Since(e.voice.start).Milliseconds()})
	e.rearm()
}

// armVoice accende stop quando lo sguardo esce dal tasto parla.
func (e *Engine) armVoice(gaze [2]float64, valid bool) {
	if !e.voice.on || e.voice.armed || !valid {
		return
	}
	if k := e.cfg.KB.IndexOf(selector.KeySpeak); k < 0 || !e.cfg.KB.Keys[k].Contains(gaze) {
		e.voice.armed = true
		e.recomputePrior()
		e.syncTask()
	}
}

// speakOn dice se il tasto parla è acceso.
func (e *Engine) speakOn() bool {
	if e.voice.on {
		return e.voice.armed
	}
	return e.cfg.Speaker != nil && e.mode == ModeFree && e.ed != nil && strings.TrimSpace(e.ed.String()) != ""
}

// Speak è il tasto parla premuto sulla tastiera fisica (Invio, nella
// scrittura libera): dice il testo o ferma la voce.
func (e *Engine) Speak() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.mode != ModeFree || e.ed == nil {
		return
	}
	if _, ok := e.speakKey(time.Now()); !ok {
		return
	}
	e.recomputePrior()
	e.syncTask()
}
