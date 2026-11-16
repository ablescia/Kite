// SPDX-License-Identifier: AGPL-3.0-or-later

// Package speech dà la voce al testo scritto con lo sguardo.
//
// La sintesi è neurale e tutta locale: Piper (https://github.com/OHF-Voice/piper1-gpl)
// con una voce italiana, di default it_IT-serena-high. Nessun dato esce dal
// computer. L'audio (PCM a 16 bit, mono) va a PipeWire con pw-play, o a
// PulseAudio o ALSA se pw-play manca.
//
// Caricare la voce richiede 1,2–1,6 s, mentre sintetizzare una frase con la
// voce già caricata ne richiede 0,1–0,3. Per questo un processo di Piper resta
// sempre pronto, con la voce in memoria, in attesa del testo. Quando la voce
// parla, il processo pronto riceve il testo e se ne avvia un altro mentre
// l'audio suona. Say ritorna quando il riproduttore ha finito di suonare,
// così chi chiama sa che il testo è stato detto davvero.
package speech

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// Speaker pronuncia un testo.
type Speaker interface {
	// Say pronuncia text e ritorna quando l'audio è finito. Annullare ctx
	// interrompe subito la voce, e Say restituisce ctx.Err().
	Say(ctx context.Context, text string) error
}

// Voices sono le voci italiane di Piper, dalla più naturale.
var Voices = []string{"it_IT-serena-high", "it_IT-paola-medium", "it_IT-serena-medium", "it_IT-riccardo-x_low"}

// Config sceglie motore, voce e riproduttore. I campi vuoti si cercano da soli.
type Config struct {
	Cmd    string   // eseguibile di Piper ("" = piper-tts, ~/.local/bin/piper o piper)
	Voice  string   // file .onnx o nome della voce ("" = la prima installata di Voices)
	Speed  float64  // velocità dell'eloquio (0 o 1 = quella della voce, 0,8 = più lenta)
	Player []string // comando che suona PCM s16 mono letto dallo stdin (nil = pw-play, paplay o aplay)
}

// Piper è un Speaker con Piper.
type Piper struct {
	cmd    string
	args   []string
	model  string
	rate   int
	player []string

	ctx    context.Context // annullato da Close: ferma anche la voce in corso
	cancel context.CancelFunc

	mu     sync.Mutex
	warm   *synth // processo con la voce caricata, in attesa del testo
	closed bool
}

// NewPiper trova Piper, la voce e il riproduttore, e avvia il primo processo
// pronto.
func NewPiper(c Config) (*Piper, error) {
	cmd, err := FindPiper(c.Cmd)
	if err != nil {
		return nil, err
	}
	model, err := FindVoice(c.Voice)
	if err != nil {
		return nil, err
	}
	rate, err := sampleRate(model)
	if err != nil {
		return nil, err
	}
	player := c.Player
	if player == nil {
		if player, err = FindPlayer(rate); err != nil {
			return nil, err
		}
	}
	p := &Piper{cmd: cmd, model: model, rate: rate, player: player,
		args: []string{"--model", model, "--output-raw"}}
	if c.Speed > 0 && c.Speed != 1 {
		// --length_scale è la durata dei fonemi: l'inverso della velocità
		p.args = append(p.args, "--length_scale", strconv.FormatFloat(1/c.Speed, 'f', 3, 64))
	}
	p.ctx, p.cancel = context.WithCancel(context.Background())
	if p.warm, err = p.start(); err != nil {
		return nil, err
	}
	return p, nil
}

// Voice è il nome della voce, per esempio "it_IT-serena-high".
func (p *Piper) Voice() string { return strings.TrimSuffix(filepath.Base(p.model), ".onnx") }

// Name descrive voce e riproduzione.
func (p *Piper) Name() string {
	return fmt.Sprintf("%s (Piper, %d Hz, %s)", p.Voice(), p.rate, filepath.Base(p.player[0]))
}

// Close ferma la voce in corso e il processo pronto.
func (p *Piper) Close() {
	p.mu.Lock()
	p.closed = true
	w := p.warm
	p.warm = nil
	p.mu.Unlock()
	p.cancel()
	if w != nil {
		w.kill()
		<-w.done
	}
}

// Say pronuncia text: il processo pronto lo sintetizza, il riproduttore lo
// suona man mano che arriva.
func (p *Piper) Say(ctx context.Context, text string) error {
	text = Prepare(text)
	if text == "" {
		return nil
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	defer context.AfterFunc(p.ctx, cancel)()

	s, err := p.take()
	if err != nil {
		return err
	}
	pl := exec.Command(p.player[0], p.player[1:]...)
	pin, err := pl.StdinPipe()
	if err != nil {
		s.kill()
		return err
	}
	plErr := &tail{}
	pl.Stderr = plErr
	if err := pl.Start(); err != nil {
		s.kill()
		return fmt.Errorf("riproduzione con %s: %w", p.player[0], err)
	}
	// I canali si chiudono alla fine, così si possono attendere più volte.
	var plWait, copyErr error
	plDone, copied := make(chan struct{}), make(chan struct{})
	go func() {
		plWait = pl.Wait()
		close(plDone)
	}()
	go func() {
		io.WriteString(s.in, text+"\n")
		s.in.Close() // fine del testo: Piper lo sintetizza ed esce
	}()
	go func() {
		_, copyErr = io.Copy(pin, s.out)
		s.out.Close()
		pin.Close() // fine dell'audio: il riproduttore suona il resto ed esce
		close(copied)
	}()

	stop := func() error {
		s.kill()
		pl.Process.Kill()
		<-copied
		<-s.done
		<-plDone
		return ctx.Err()
	}
	select {
	case <-copied:
	case <-ctx.Done():
		return stop()
	}
	// Sintesi finita: la voce per la prossima volta si carica adesso,
	// mentre l'audio finisce di suonare, senza rubare CPU alla sintesi.
	p.prewarm()
	select {
	case <-s.done:
	case <-ctx.Done():
		return stop()
	}
	if s.err != nil {
		pl.Process.Kill()
		<-plDone
		return fmt.Errorf("piper: %v%s", s.err, s.errs.String())
	}
	select {
	case <-plDone:
		if plWait != nil {
			return fmt.Errorf("%s: %v%s", filepath.Base(p.player[0]), plWait, plErr.String())
		}
	case <-ctx.Done():
		return stop()
	}
	if copyErr != nil {
		return fmt.Errorf("audio verso %s: %w", filepath.Base(p.player[0]), copyErr)
	}
	return nil
}

// take restituisce il processo pronto, o ne avvia uno se manca o è morto.
func (p *Piper) take() (*synth, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil, errors.New("voce chiusa")
	}
	s := p.warm
	p.warm = nil
	if s != nil {
		select {
		case <-s.done:
			s = nil // uscito da solo (errore all'avvio?): se ne avvia un altro
		default:
		}
	}
	if s == nil {
		return p.start()
	}
	return s, nil
}

func (p *Piper) prewarm() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.warm == nil && !p.closed {
		p.warm, _ = p.start() // un errore qui si rivede al prossimo Say
	}
}

// synth è un processo di Piper: la voce si carica subito, il testo arriva
// dallo stdin, l'audio esce dallo stdout.
type synth struct {
	cmd  *exec.Cmd
	in   io.WriteCloser
	out  *os.File
	errs *tail
	done chan struct{} // chiuso all'uscita del processo
	err  error         // esito, valido dopo done
}

func (p *Piper) start() (*synth, error) {
	c := exec.Command(p.cmd, p.args...)
	in, err := c.StdinPipe()
	if err != nil {
		return nil, err
	}
	// Lo stdout è una pipe propria: con StdoutPipe, Wait la chiuderebbe
	// all'uscita di Piper, anche con dell'audio ancora da leggere.
	r, w, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	c.Stdout = w
	s := &synth{cmd: c, in: in, out: r, errs: &tail{}, done: make(chan struct{})}
	c.Stderr = s.errs
	err = c.Start()
	w.Close()
	if err != nil {
		r.Close()
		return nil, fmt.Errorf("avvio di Piper (%s): %w", p.cmd, err)
	}
	go func() {
		s.err = c.Wait()
		close(s.done)
	}()
	return s, nil
}

func (s *synth) kill() {
	s.cmd.Process.Kill()
	s.in.Close()
}

// tail tiene gli ultimi byte di uno stderr, per i messaggi di errore.
type tail struct {
	mu sync.Mutex
	b  []byte
}

func (t *tail) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.b = append(t.b, p...)
	if n := len(t.b); n > 2048 {
		t.b = append(t.b[:0], t.b[n-2048:]...)
	}
	return len(p), nil
}

// String è l'ultima riga non vuota, preceduta da ": " (vuota se non c'è).
func (t *tail) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	lines := strings.Split(strings.TrimSpace(string(t.b)), "\n")
	if l := strings.TrimSpace(lines[len(lines)-1]); l != "" {
		return ": " + l
	}
	return ""
}

// Prepare porta il testo su una riga e, se manca, aggiunge il punto finale:
// senza punteggiatura la voce chiude la frase con un'intonazione sospesa.
func Prepare(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if s == "" {
		return ""
	}
	if r := []rune(s); !strings.ContainsRune(".!?…;:", r[len(r)-1]) {
		s += "."
	}
	return s
}

// FindPiper trova l'eseguibile di Piper. Il nome "piper" nel PATH viene per
// ultimo: è anche quello del programma di configurazione dei mouse.
func FindPiper(cmd string) (string, error) {
	if cmd != "" {
		return exec.LookPath(cmd)
	}
	if p, err := exec.LookPath("piper-tts"); err == nil {
		return p, nil
	}
	home, _ := os.UserHomeDir()
	for _, p := range []string{filepath.Join(home, ".local", "bin", "piper"), "/opt/piper-tts/piper", "/opt/piper/piper"} {
		if st, err := os.Stat(p); err == nil && !st.IsDir() && st.Mode()&0o111 != 0 {
			return p, nil
		}
	}
	if p, err := exec.LookPath("piper"); err == nil {
		return p, nil
	}
	return "", errors.New("Piper non trovato: installalo con «uv tool install piper-tts» (vedi il README, «La voce»)")
}

// VoiceDirs sono le cartelle in cui si cercano le voci, anche nelle
// sottocartelle: quella dell'utente e quelle dei pacchetti di sistema.
func VoiceDirs() []string {
	data := os.Getenv("XDG_DATA_HOME")
	if data == "" {
		home, _ := os.UserHomeDir()
		data = filepath.Join(home, ".local", "share")
	}
	return []string{filepath.Join(data, "piper", "voices"), filepath.Join(data, "piper"),
		"/usr/share/piper-voices", "/usr/local/share/piper-voices", "/usr/share/piper/voices"}
}

// FindVoice trova il file .onnx della voce. name è un percorso, un nome
// ("it_IT-paola-medium") o vuoto: la prima installata di Voices.
func FindVoice(name string) (string, error) {
	if strings.ContainsRune(name, os.PathSeparator) || strings.HasSuffix(name, ".onnx") {
		if _, err := os.Stat(name); err != nil {
			return "", fmt.Errorf("voce: %w", err)
		}
		return name, nil
	}
	names := Voices
	if name != "" {
		names = []string{name}
	}
	for _, n := range names {
		for _, d := range VoiceDirs() {
			if p := findFile(d, n+".onnx", 5); p != "" {
				return p, nil
			}
		}
	}
	return "", fmt.Errorf("nessuna voce di Piper (%s) in %s: scaricala come spiega il README, «La voce»",
		strings.Join(names, ", "), VoiceDirs()[0])
}

// findFile cerca name in dir fino a depth livelli di sottocartelle.
func findFile(dir, name string, depth int) string {
	found := ""
	base := strings.Count(filepath.Clean(dir), string(os.PathSeparator))
	filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return fs.SkipDir
		}
		if d.IsDir() && strings.Count(p, string(os.PathSeparator))-base >= depth {
			return fs.SkipDir
		}
		if !d.IsDir() && d.Name() == name {
			found = p
			return fs.SkipAll
		}
		return nil
	})
	return found
}

// sampleRate legge la frequenza di campionamento della voce dal suo .onnx.json.
func sampleRate(model string) (int, error) {
	b, err := os.ReadFile(model + ".json")
	if err != nil {
		return 0, fmt.Errorf("configurazione della voce: %w", err)
	}
	var c struct {
		Audio struct {
			SampleRate int `json:"sample_rate"`
		} `json:"audio"`
	}
	if err := json.Unmarshal(b, &c); err != nil {
		return 0, fmt.Errorf("configurazione della voce %s: %w", model+".json", err)
	}
	if c.Audio.SampleRate <= 0 {
		return 22050, nil
	}
	return c.Audio.SampleRate, nil
}

// FindPlayer restituisce il comando che suona PCM s16 mono a rate Hz letto
// dallo stdin: pw-play (PipeWire), paplay (PulseAudio) o aplay (ALSA).
func FindPlayer(rate int) ([]string, error) {
	r := strconv.Itoa(rate)
	for _, c := range [][]string{
		{"pw-play", "--raw", "--rate", r, "--channels", "1", "--format", "s16",
			"-P", `{ media.name = "voce" application.name = "kite" }`, "-"},
		{"paplay", "--raw", "--rate=" + r, "--channels=1", "--format=s16le",
			"--client-name=kite", "--stream-name=voce"},
		{"aplay", "-q", "-t", "raw", "-f", "S16_LE", "-r", r, "-c", "1"},
	} {
		if p, err := exec.LookPath(c[0]); err == nil {
			c[0] = p
			return c, nil
		}
	}
	return nil, errors.New("nessun programma per suonare l'audio (pw-play, paplay o aplay)")
}
