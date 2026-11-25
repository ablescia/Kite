// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Antonio Blescia

package speech

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPrepare(t *testing.T) {
	cases := map[string]string{
		"":                       "",
		"   ":                    "",
		"ciao come stai ":        "ciao come stai.",
		"  ho\nsete  ":           "ho sete.",
		"perché no?":             "perché no?",
		"l'acqua è fresca!":      "l'acqua è fresca!",
		"un po'":                 "un po'.",
		"vieni   qui \t  subito": "vieni qui subito.",
	}
	for in, want := range cases {
		if got := Prepare(in); got != want {
			t.Errorf("Prepare(%q) = %q, atteso %q", in, got, want)
		}
	}
}

// fakePiper scrive uno script che si comporta come Piper: annota l'avvio,
// legge una riga di testo e risponde con dell'"audio".
func fakePiper(t *testing.T, body string) (cmd, log string) {
	t.Helper()
	dir := t.TempDir()
	log = filepath.Join(dir, "log")
	cmd = filepath.Join(dir, "piper")
	script := "#!/bin/sh\necho start >> \"$FAKE_PIPER_LOG.starts\"\nprintf '%s\\n' \"$*\" > \"$FAKE_PIPER_LOG.args\"\n" + body
	if err := os.WriteFile(cmd, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKE_PIPER_LOG", log)
	return cmd, log
}

func fakeVoice(t *testing.T) string {
	t.Helper()
	m := filepath.Join(t.TempDir(), "it_IT-prova-medium.onnx")
	os.WriteFile(m, nil, 0o644)
	os.WriteFile(m+".json", []byte(`{"audio":{"sample_rate":16000}}`), 0o644)
	return m
}

func lines(path string) []string {
	b, _ := os.ReadFile(path)
	return strings.Fields(string(b))
}

// La catena completa: il testo arriva al processo pronto, l'audio arriva
// intatto al riproduttore, Say ritorna a riproduzione finita e un altro
// processo si prepara per la volta dopo.
func TestSayPipeline(t *testing.T) {
	cmd, log := fakePiper(t, "read -r line\nprintf '%s\\n' \"$line\" >> \"$FAKE_PIPER_LOG.text\"\nprintf 'PCM-%s' \"$line\"\n")
	out := filepath.Join(t.TempDir(), "out.raw")
	p, err := NewPiper(Config{Cmd: cmd, Voice: fakeVoice(t), Speed: 0.8,
		Player: []string{"sh", "-c", "sleep 0.2; cat > " + out + "; echo fine >> " + out + ".fine"}})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if p.rate != 16000 || p.Voice() != "it_IT-prova-medium" {
		t.Errorf("voce %q a %d Hz", p.Voice(), p.rate)
	}
	// il primo processo parte subito, prima di qualunque testo
	waitFor(t, func() bool { return len(lines(log+".starts")) == 1 }, "avvio anticipato")

	t0 := time.Now()
	if err := p.Say(context.Background(), "  ciao come\nstai "); err != nil {
		t.Fatal(err)
	}
	if el := time.Since(t0); el < 200*time.Millisecond {
		t.Errorf("Say è tornato prima della fine della riproduzione (%v)", el)
	}
	if got := lines(out + ".fine"); len(got) != 1 {
		t.Error("il riproduttore non aveva finito quando Say è tornato")
	}
	if b, _ := os.ReadFile(out); string(b) != "PCM-ciao come stai." {
		t.Errorf("audio al riproduttore: %q", b)
	}
	if b, _ := os.ReadFile(log + ".args"); !strings.Contains(string(b), "--output-raw") || !strings.Contains(string(b), "--length_scale 1.250") {
		t.Errorf("argomenti di Piper: %q", b)
	}
	waitFor(t, func() bool { return len(lines(log+".starts")) == 2 }, "nuovo processo pronto dopo la frase")

	// la seconda frase usa il processo già pronto
	if err := p.Say(context.Background(), "sì"); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(out); string(b) != "PCM-sì." {
		t.Errorf("seconda frase: %q", b)
	}
	if got := lines(log + ".text"); len(got) != 4 { // "ciao come stai." e "sì."
		t.Errorf("testi ricevuti: %q", got)
	}
	// testo vuoto: nessuna voce
	if err := p.Say(context.Background(), "  "); err != nil {
		t.Error(err)
	}
}

// Annullare il contesto ferma subito la voce.
func TestSayCancel(t *testing.T) {
	cmd, _ := fakePiper(t, "read -r line\nprintf 'PCM'\n")
	p, err := NewPiper(Config{Cmd: cmd, Voice: fakeVoice(t),
		Player: []string{"sh", "-c", "cat > /dev/null; exec sleep 10"}})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(150*time.Millisecond, cancel)
	t0 := time.Now()
	err = p.Say(ctx, "una frase lunghissima")
	if !errors.Is(err, context.Canceled) {
		t.Errorf("errore %v, atteso context.Canceled", err)
	}
	if el := time.Since(t0); el > 2*time.Second {
		t.Errorf("la voce si è fermata dopo %v", el)
	}
	// Close interrompe anche una frase in corso
	go func() {
		time.Sleep(150 * time.Millisecond)
		p.Close()
	}()
	if err := p.Say(context.Background(), "ancora"); err == nil {
		t.Error("Close doveva interrompere la voce")
	}
}

// Un errore di Piper arriva a chi chiama, con l'ultima riga del suo stderr.
func TestSayPiperError(t *testing.T) {
	cmd, _ := fakePiper(t, "read -r line\necho 'ValueError: voce rotta' >&2\nexit 3\n")
	p, err := NewPiper(Config{Cmd: cmd, Voice: fakeVoice(t), Player: []string{"sh", "-c", "cat > /dev/null"}})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	err = p.Say(context.Background(), "ciao")
	if err == nil || !strings.Contains(err.Error(), "voce rotta") {
		t.Errorf("errore: %v", err)
	}
}

func TestFindVoice(t *testing.T) {
	data := t.TempDir()
	t.Setenv("XDG_DATA_HOME", data)
	put := func(rel string) string {
		p := filepath.Join(data, rel)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, nil, 0o644)
		return p
	}
	if _, err := FindVoice("it_IT-inesistente"); err == nil {
		t.Error("voce inesistente trovata")
	}
	paola := put("piper/voices/it/it_IT/paola/medium/it_IT-paola-medium.onnx")
	if got, err := FindVoice("it_IT-paola-medium"); err != nil || got != paola {
		t.Errorf("voce per nome in una sottocartella: %q %v", got, err)
	}
	serena := put("piper/voices/it_IT-serena-high.onnx")
	if got, _ := FindVoice(""); got != serena {
		t.Errorf("senza nome deve scegliere la voce più naturale: %q", got)
	}
	if got, err := FindVoice(paola); err != nil || got != paola {
		t.Errorf("voce per percorso: %q %v", got, err)
	}
}

// Con Piper e una voce italiana installati, la sintesi vera produce audio.
func TestRealPiper(t *testing.T) {
	if testing.Short() {
		t.Skip("-short")
	}
	if _, err := FindPiper(""); err != nil {
		t.Skip(err)
	}
	if _, err := FindVoice(""); err != nil {
		t.Skip(err)
	}
	out := filepath.Join(t.TempDir(), "out.raw")
	p, err := NewPiper(Config{Player: []string{"sh", "-c", "cat > " + out}})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	t0 := time.Now()
	if err := p.Say(context.Background(), "ciao, come stai"); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(out)
	if err != nil {
		t.Fatal(err)
	}
	sec := float64(st.Size()) / 2 / float64(p.rate)
	t.Logf("%s: %.2f s di audio in %v (avvio a freddo compreso)", p.Name(), sec, time.Since(t0).Round(time.Millisecond))
	if sec < 0.5 || sec > 5 {
		t.Errorf("durata dell'audio: %.2f s", sec)
	}
}

func waitFor(t *testing.T, ok func() bool, what string) {
	t.Helper()
	for i := 0; i < 200; i++ {
		if ok() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Errorf("%s: non è successo entro 2 s", what)
}
