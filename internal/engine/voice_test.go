// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Antonio Blescia

package engine

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ablescia/kite/internal/correct"
	"github.com/ablescia/kite/internal/metrics"
	"github.com/ablescia/kite/internal/selector"
)

// fakeSpeaker è una voce finta: annuncia il testo e parla finché il test
// non manda l'esito su finish.
type fakeSpeaker struct {
	said   chan string
	finish chan error
}

func (f *fakeSpeaker) Say(ctx context.Context, text string) error {
	f.said <- text
	select {
	case err := <-f.finish:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func newVoiceEngine(t *testing.T) (*Engine, *fakeSpeaker) {
	t.Helper()
	f := &fakeSpeaker{said: make(chan string, 4), finish: make(chan error)}
	e := newTextEngine(t)
	e.cfg.Speaker = f
	e.StartFree()
	return e, f
}

// locked esegue fn con il lock dell'engine, come la goroutine dei campioni.
func locked(e *Engine, fn func()) {
	e.mu.Lock()
	defer e.mu.Unlock()
	fn()
}

func (e *Engine) keyOn(c rune) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.prior[e.cfg.KB.IndexOf(c)] > 0
}

func pressSpeak(e *Engine) (ed Edit, ok bool) {
	locked(e, func() {
		ed, ok = e.edit(selector.KeySpeak, correct.Obs{}, time.Now())
		e.recomputePrior()
		e.syncTask()
	})
	return
}

func waitSnap(t *testing.T, e *Engine, ok func(Snapshot) bool, what string) Snapshot {
	t.Helper()
	for i := 0; i < 200; i++ {
		if s := e.Snapshot(); ok(s) {
			return s
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("%s: non è successo entro 1 s", what)
	return Snapshot{}
}

func said(t *testing.T, f *fakeSpeaker) string {
	t.Helper()
	select {
	case s := <-f.said:
		return s
	case <-time.After(time.Second):
		t.Fatal("la voce non ha ricevuto il testo")
		return ""
	}
}

// Parla: la voce dice il testo (con l'ultima parola corretta), la tastiera
// tace finché parla, stop si accende solo dopo che lo sguardo è uscito dal
// tasto; a voce finita il testo si cancella e cancella lo ripristina.
func TestVoiceSpeaksThenClears(t *testing.T) {
	e, f := newVoiceEngine(t)
	if e.keyOn(selector.KeySpeak) {
		t.Error("senza testo parla è spento")
	}
	locked(e, func() { typeText(e, "ho sete di sabngue") })
	if !e.keyOn(selector.KeySpeak) || !e.keyOn('a') {
		t.Fatal("con del testo parla e le lettere sono accesi")
	}
	if ed, ok := pressSpeak(e); !ok || ed.Kind != "parla" {
		t.Fatalf("parla: %+v %v", ed, ok)
	}
	if got := said(t, f); got != "ho sete di sangue" {
		t.Errorf("testo alla voce: %q (l'ultima parola si corregge come allo spazio)", got)
	}
	s := e.Snapshot()
	if !s.Speaking || s.Typed != "ho sete di sangue " {
		t.Errorf("mentre parla il testo resta visibile: %+v", s.Typed)
	}
	// la tastiera tace, e stop non c'è finché lo sguardo resta sul tasto
	for _, c := range []rune{'a', 'e', ' ', selector.KeyBackspace, selector.KeySpeak} {
		if e.keyOn(c) {
			t.Errorf("mentre parla il tasto %q deve essere spento", c)
		}
	}
	locked(e, func() {
		typeText(e, "xy<")
		e.armVoice(e.cfg.KB.Keys[e.cfg.KB.IndexOf(selector.KeySpeak)].Center, true)
	})
	if e.ed.String() != "ho sete di sangue " || e.keyOn(selector.KeySpeak) {
		t.Errorf("lettere durante la voce o stop acceso con lo sguardo sul tasto: %q", e.ed.String())
	}
	locked(e, func() { e.armVoice([2]float64{0.5, 0.6}, true) })
	if !e.keyOn(selector.KeySpeak) || e.keyOn('a') {
		t.Error("sguardo uscito dal tasto: stop acceso, lettere ancora spente")
	}

	f.finish <- nil
	s = waitSnap(t, e, func(s Snapshot) bool { return !s.Speaking }, "fine della voce")
	if s.Typed != "" || s.Said != "ho sete di sangue" {
		t.Errorf("a voce finita il testo si cancella: %q, detto %q", s.Typed, s.Said)
	}
	if e.keyOn(selector.KeySpeak) || !e.keyOn('a') {
		t.Error("dopo la voce: parla spento (niente testo), lettere accese")
	}
	// cancella subito dopo ripristina il testo detto, a parola chiusa
	var eds []Edit
	locked(e, func() { eds = typeText(e, "<") })
	if got := e.ed.String(); got != "ho sete di sangue " || eds[0].Kind != "ripristina" {
		t.Errorf("ripristino: %q (%s)", got, eds[0].Kind)
	}
	locked(e, func() { typeText(e, "<") })
	if got := e.ed.String(); got != "ho sete di sangue" {
		t.Errorf("un secondo cancella toglie l'ultimo carattere: %q", got)
	}
}

// Una lettera dopo la voce scrive un messaggio nuovo: cancella non ripristina più.
func TestVoiceNewMessage(t *testing.T) {
	e, f := newVoiceEngine(t)
	locked(e, func() { typeText(e, "ciao ") })
	pressSpeak(e)
	said(t, f)
	f.finish <- nil
	waitSnap(t, e, func(s Snapshot) bool { return !s.Speaking }, "fine della voce")
	locked(e, func() { typeText(e, "si<") })
	if got := e.ed.String(); got != "s" {
		t.Errorf("messaggio nuovo: %q", got)
	}
}

// Stop ferma la voce e il testo resta; anche un errore della voce lascia il testo.
func TestVoiceStopAndError(t *testing.T) {
	e, f := newVoiceEngine(t)
	locked(e, func() { typeText(e, "buongiorno ") })
	pressSpeak(e)
	said(t, f)
	if ed, ok := pressSpeak(e); !ok || ed.Kind != "stop" {
		t.Fatalf("stop: %+v %v", ed, ok)
	}
	time.Sleep(20 * time.Millisecond) // la voce finta vede il contesto annullato
	if s := e.Snapshot(); s.Speaking || s.Typed != "buongiorno " || s.Said != "" {
		t.Errorf("dopo stop: parla %v, testo %q", s.Speaking, s.Typed)
	}
	// la tastiera fisica: Invio parla di nuovo
	e.Speak()
	said(t, f)
	f.finish <- errors.New("nessuna uscita audio")
	s := waitSnap(t, e, func(s Snapshot) bool { return !s.Speaking }, "fine della voce")
	if s.Typed != "buongiorno " || s.VoiceErr != "nessuna uscita audio" {
		t.Errorf("errore della voce: testo %q, errore %q", s.Typed, s.VoiceErr)
	}
	// uscire dalla scrittura libera ferma la voce
	e.Speak()
	said(t, f)
	e.SetIdle()
	if s := e.Snapshot(); s.Speaking {
		t.Error("tornando al menu la voce si ferma")
	}
}

// Nell'esercizio di copiatura parla resta spento: cancellerebbe il testo da misurare.
func TestVoiceOffInBlocks(t *testing.T) {
	e, _ := newVoiceEngine(t)
	e.cfg.Task = metrics.TaskText
	e.StartBlock("casa bella")
	locked(e, func() { typeText(e, "casa ") })
	if e.keyOn(selector.KeySpeak) {
		t.Error("parla acceso in un blocco")
	}
	if _, ok := pressSpeak(e); ok || e.Snapshot().Speaking {
		t.Error("parla in un blocco")
	}
}
