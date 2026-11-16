// SPDX-License-Identifier: AGPL-3.0-or-later

// Package driver usa il driver tobii4c (github.com/ablescia/tobii4c-linux)
// per l'unica cosa che Kite non fa da sé: la calibrazione dello sguardo.
//
// Kite riceve i campioni via UDP e non tocca mai il tracker. La calibrazione
// invece la esegue il driver (`tobii4c calibrate`), che vuole l'accesso
// esclusivo alla USB: il driver che sta trasmettendo va quindi fermato prima
// e riavviato dopo. Se il driver gira come servizio utente di systemd, Kite
// lo ferma e lo riavvia da solo. Se è stato avviato a mano in un terminale,
// Kite non lo chiude: dice chi è e aspetta che venga chiuso.
//
// La ricerca di chi tiene aperto il tracker (nodi USB da sysfs, descrittori
// in /proc, servizio dal cgroup) viene da cmd/tobii4c/busy.go del driver.
package driver

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ServiceUnit è il servizio utente installato dal driver (systemd/tobii4c.service).
const ServiceUnit = "tobii4c.service"

// Identificativi USB del tracker: Tobii, Eye Tracker 4C (IS4) ed ET5.
const vendorTobii = 0x2104

var productIDs = []uint64{0x0127, 0x0313}

// Holder è un processo che ha aperto il nodo USB del tracker.
type Holder struct {
	PID  int
	Comm string
	Args []string // riga di comando
	Unit string   // servizio utente di systemd che lo contiene, "" se avviato a mano
}

// Command è la riga di comando di h, per mostrarla.
func (h Holder) Command() string {
	if len(h.Args) == 0 {
		return h.Comm
	}
	a := slices.Clone(h.Args)
	a[0] = filepath.Base(a[0])
	return strings.Join(a, " ")
}

// State è la situazione del driver prima di una calibrazione.
type State struct {
	Exe        string    // eseguibile tobii4c ("" se non trovato)
	ExeErr     error     // perché non è stato trovato
	Connected  bool      // il tracker è collegato (nodo USB presente)
	Units      []string  // servizi utente da fermare e poi riavviare
	Manual     []Holder  // processi avviati a mano che tengono il tracker
	SystemUnit bool      // un tobii4c.service di sistema è attivo (serve sudo)
	LastCal    time.Time // ultima calibrazione salvata dal driver (zero se nessuna)
	Display    Display   // geometria dello schermo salvata dal driver
}

// Busy dice se un processo che Kite non può fermare tiene il tracker.
func (s State) Busy() bool { return len(s.Manual) > 0 }

// Display è la parte che serve a Kite di ~/.config/tobii4c/display.json,
// scritto dal driver al primo passo della calibrazione.
type Display struct {
	WidthMM   float64 `json:"width_mm"`
	HeightMM  float64 `json:"height_mm"`
	OffsetXMM float64 `json:"offset_x_mm"`
	Monitor   string  `json:"monitor"`
}

// Same dice se due geometrie coincidono (a meno di 0,5 mm).
func (d Display) Same(o Display) bool {
	return math.Abs(d.WidthMM-o.WidthMM) < 0.5 && math.Abs(d.HeightMM-o.HeightMM) < 0.5 &&
		math.Abs(d.OffsetXMM-o.OffsetXMM) < 0.5
}

// ConfigDir è la cartella di configurazione del driver (~/.config/tobii4c).
func ConfigDir() string {
	d, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(d, "tobii4c")
}

// LoadDisplay legge display.json (zero se manca).
func LoadDisplay() Display {
	var d Display
	if dir := ConfigDir(); dir != "" {
		if b, err := os.ReadFile(filepath.Join(dir, "display.json")); err == nil {
			json.Unmarshal(b, &d)
		}
	}
	return d
}

// FindExe cerca l'eseguibile del driver: path se indicato, poi tobii4c nel
// PATH, poi ~/.local/bin/tobii4c (dove lo mette `make install`).
func FindExe(path string) (string, error) {
	if path != "" {
		if st, err := os.Stat(path); err != nil || st.IsDir() {
			return "", fmt.Errorf("%s: eseguibile del driver non trovato", path)
		}
		return path, nil
	}
	if p, err := exec.LookPath("tobii4c"); err == nil {
		return p, nil
	}
	if home, err := os.UserHomeDir(); err == nil {
		p := filepath.Join(home, ".local", "bin", "tobii4c")
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p, nil
		}
	}
	return "", errors.New("driver tobii4c non trovato: installalo da github.com/ablescia/tobii4c-linux (make install) o indicalo con -tobii4c")
}

// Inspect descrive la situazione: l'eseguibile, il tracker, chi lo usa.
func Inspect(exe string) State {
	var s State
	s.Exe, s.ExeErr = FindExe(exe)
	nodes := trackerNodes()
	s.Connected = len(nodes) > 0
	if unitActive(ServiceUnit) {
		s.Units = append(s.Units, ServiceUnit)
	}
	for _, h := range deviceHolders(nodes) {
		switch {
		case h.Unit != "" && !slices.Contains(s.Units, h.Unit):
			s.Units = append(s.Units, h.Unit)
		case h.Unit == "":
			s.Manual = append(s.Manual, h)
		}
	}
	if len(s.Units) == 0 && systemUnitActive(ServiceUnit) {
		s.SystemUnit = true
	}
	s.LastCal = LastCalibration()
	s.Display = LoadDisplay()
	return s
}

// LastCalibration è quando il driver ha salvato l'ultima calibrazione dello
// sguardo (~/.config/tobii4c/calibration.bin), zero se non ce n'è una.
func LastCalibration() time.Time {
	if dir := ConfigDir(); dir != "" {
		if st, err := os.Stat(filepath.Join(dir, "calibration.bin")); err == nil {
			return st.ModTime()
		}
	}
	return time.Time{}
}

// Point è un punto di verifica della calibrazione.
type Point struct {
	ErrorDeg     float64 // accuratezza: angolo tra bersaglio e sguardo medio
	PrecisionDeg float64 // precisione: RMS campione-campione
}

// Result è l'esito di una calibrazione.
type Result struct {
	Err       error   // il driver non è partito, o è terminato con un errore
	Finished  bool    // calibrazione conclusa e salvata
	Aborted   bool    // interrotta (Esc): il driver ha rimesso quella di prima
	Restored  bool    // con l'interruzione la calibrazione precedente è tornata sul tracker
	Points    []Point // verifica finale (l'ultima, se ripetuta)
	Stopped   []string
	Restarted []string // servizi riavviati
	Restart   error    // riavvio non riuscito
	Before    Display  // geometria prima e dopo
	After     Display
	Log       string // dove è salvato l'output del driver ("" se non salvato)
	Took      time.Duration
}

// Mean è l'accuratezza e la precisione medie della verifica.
func (r Result) Mean() (errDeg, precDeg float64, ok bool) {
	if len(r.Points) == 0 {
		return 0, 0, false
	}
	for _, p := range r.Points {
		errDeg += p.ErrorDeg
		precDeg += p.PrecisionDeg
	}
	n := float64(len(r.Points))
	return errDeg / n, precDeg / n, true
}

// DisplayChanged dice se la calibrazione ha cambiato la geometria dello schermo.
func (r Result) DisplayChanged() bool { return !r.Before.Same(r.After) }

// Options configura una calibrazione.
type Options struct {
	Exe     string // eseguibile del driver ("" = cerca)
	Monitor string // monitor su cui calibrare ("" = il primario)
	LogPath string // dove salvare l'output del driver ("" = non si salva)
	// Before è chiamata quando il tracker è libero, subito prima di avviare
	// la finestra del driver (Kite nasconde la sua); After quando la finestra
	// del driver si è chiusa. Possono essere nil.
	Before, After func()
}

// Calibrate esegue `tobii4c calibrate`: ferma i servizi che usano il
// tracker, lancia la calibrazione, aspetta che finisca e riavvia i servizi,
// anche se la calibrazione non è riuscita. Con un processo avviato a mano
// che tiene il tracker non parte: va chiuso prima.
func Calibrate(ctx context.Context, o Options) (r Result) {
	t0 := time.Now()
	st := inspect(o.Exe)
	r.Before = st.Display
	defer func() { r.Took = time.Since(t0) }()
	switch {
	case st.ExeErr != nil:
		r.Err = st.ExeErr
		return r
	case st.Busy():
		h := st.Manual[0]
		r.Err = fmt.Errorf("il tracker è aperto da %s (PID %d): va chiuso prima di calibrare", h.Command(), h.PID)
		return r
	}
	for _, u := range st.Units {
		if err := systemctl("stop", u); err != nil {
			r.Err = err
			break
		}
		r.Stopped = append(r.Stopped, u)
	}
	defer func() {
		for i := len(r.Stopped) - 1; i >= 0; i-- {
			if err := systemctl("start", r.Stopped[i]); err != nil {
				r.Restart = errors.Join(r.Restart, err)
			} else {
				r.Restarted = append(r.Restarted, r.Stopped[i])
			}
		}
	}()
	if r.Err != nil {
		return r
	}
	// Il servizio fermato rilascia la USB in pochi istanti.
	for wait := time.Now(); len(st.Units) > 0 && time.Since(wait) < 5*time.Second; time.Sleep(100 * time.Millisecond) {
		if len(deviceHolders(trackerNodes())) == 0 {
			break
		}
	}
	args := []string{"calibrate", "-log-format", "json"}
	if o.Monitor != "" {
		args = append(args, "-monitor", o.Monitor)
	}
	cmd := exec.CommandContext(ctx, st.Exe, args...)
	// Se Kite chiude, il driver riceve SIGINT come da tastiera: annulla la
	// calibrazione in corso e rimette quella di prima prima di uscire.
	cmd.Cancel = func() error { return cmd.Process.Signal(os.Interrupt) }
	cmd.WaitDelay = 5 * time.Second
	var logf *os.File
	if o.LogPath != "" {
		if f, err := os.Create(o.LogPath); err == nil {
			logf, r.Log = f, o.LogPath
			defer f.Close()
		}
	}
	pr, pw := io.Pipe()
	cmd.Stdout, cmd.Stderr = pw, pw
	var lastLine string
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		sc := bufio.NewScanner(pr)
		sc.Buffer(make([]byte, 64*1024), 1024*1024)
		var pts []Point
		for sc.Scan() {
			line := sc.Text()
			if logf != nil {
				fmt.Fprintln(logf, line)
			}
			if strings.TrimSpace(line) != "" {
				lastLine = line
			}
			parseLine(line, &r, &pts)
		}
		io.Copy(io.Discard, pr)
	}()
	if o.Before != nil {
		o.Before()
	}
	err := cmd.Run()
	pw.Close()
	wg.Wait()
	if o.After != nil {
		o.After()
	}
	if err != nil && ctx.Err() == nil {
		msg := lastLine
		var m map[string]any
		if json.Unmarshal([]byte(lastLine), &m) == nil {
			msg = fmt.Sprint(m["msg"])
			if e, ok := m["err"]; ok {
				msg += ": " + fmt.Sprint(e)
			}
		}
		if msg == "" {
			msg = err.Error()
		}
		r.Err = fmt.Errorf("tobii4c calibrate: %s", strings.TrimPrefix(msg, "errore: "))
	}
	r.After = loadDisplay()
	return r
}

// Sostituibili nei test, che non devono dipendere dal sistema.
var (
	inspect     = Inspect
	loadDisplay = LoadDisplay
)

// parseLine interpreta una riga del log JSON del driver.
func parseLine(line string, r *Result, pts *[]Point) {
	var m map[string]any
	if json.Unmarshal([]byte(line), &m) != nil {
		return
	}
	switch m["msg"] {
	case "calibration started":
		*pts = (*pts)[:0]
		r.Points, r.Finished, r.Aborted = nil, false, false
	case "calibration finished":
		r.Finished, r.Aborted = true, false
	case "calibration aborted":
		r.Aborted = true
		r.Restored, _ = m["restored_previous"].(bool)
	case "validation point":
		p := Point{ErrorDeg: number(m["error_deg"]), PrecisionDeg: number(m["precision_deg"])}
		if !math.IsNaN(p.ErrorDeg) {
			*pts = append(*pts, p)
			// una verifica completa sono 5 punti: una nuova riparte da capo
			if len(*pts) > 5 {
				*pts = (*pts)[len(*pts)-1:]
			}
			r.Points = slices.Clone(*pts)
		}
	}
}

// number legge un numero scritto come numero o come stringa.
func number(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case string:
		if f, err := strconv.ParseFloat(x, 64); err == nil {
			return f
		}
	}
	return math.NaN()
}

// ---------------------------------------------------------------------------
// Chi tiene occupato il tracker (da cmd/tobii4c/busy.go del driver)

// trackerNodes restituisce i nodi /dev/bus/usb/BBB/DDD dei tracker collegati.
func trackerNodes() []string {
	vendors, _ := filepath.Glob("/sys/bus/usb/devices/*/idVendor")
	var out []string
	read := func(dir, name string) string {
		b, _ := os.ReadFile(filepath.Join(dir, name))
		return strings.TrimSpace(string(b))
	}
	for _, v := range vendors {
		dir := filepath.Dir(v)
		vid, err1 := strconv.ParseUint(read(dir, "idVendor"), 16, 16)
		pid, err2 := strconv.ParseUint(read(dir, "idProduct"), 16, 16)
		if err1 != nil || err2 != nil || vid != vendorTobii || !slices.Contains(productIDs, pid) {
			continue
		}
		bus, err1 := strconv.Atoi(read(dir, "busnum"))
		dev, err2 := strconv.Atoi(read(dir, "devnum"))
		if err1 == nil && err2 == nil {
			out = append(out, fmt.Sprintf("/dev/bus/usb/%03d/%03d", bus, dev))
		}
	}
	return out
}

// deviceHolders cerca in /proc i processi che hanno aperto uno dei nodi.
// Vede solo i processi dell'utente (gli altri non si possono ispezionare).
func deviceHolders(nodes []string) []Holder {
	if len(nodes) == 0 {
		return nil
	}
	procs, _ := filepath.Glob("/proc/[0-9]*")
	self := os.Getpid()
	var out []Holder
	for _, p := range procs {
		pid, err := strconv.Atoi(filepath.Base(p))
		if err != nil || pid == self {
			continue
		}
		fds, err := os.ReadDir(filepath.Join(p, "fd"))
		if err != nil {
			continue
		}
		for _, fd := range fds {
			target, err := os.Readlink(filepath.Join(p, "fd", fd.Name()))
			if err != nil || !slices.Contains(nodes, target) {
				continue
			}
			comm, _ := os.ReadFile(filepath.Join(p, "comm"))
			cg, _ := os.ReadFile(filepath.Join(p, "cgroup"))
			cl, _ := os.ReadFile(filepath.Join(p, "cmdline"))
			args := strings.Split(strings.TrimRight(string(cl), "\x00"), "\x00")
			if len(args) == 1 && args[0] == "" {
				args = nil
			}
			out = append(out, Holder{PID: pid, Comm: strings.TrimSpace(string(comm)), Args: args, Unit: userUnit(string(cg))})
			break
		}
	}
	return out
}

// userUnit estrae da /proc/PID/cgroup il servizio utente di systemd che
// contiene il processo, es. ".../user@1000.service/app.slice/tobii4c.service".
func userUnit(cgroup string) string {
	for _, line := range strings.Split(cgroup, "\n") {
		_, path, ok := strings.Cut(line, "::")
		if !ok {
			continue
		}
		inUser := false
		for _, part := range strings.Split(path, "/") {
			if strings.HasPrefix(part, "user@") && strings.HasSuffix(part, ".service") {
				inUser = true
				continue
			}
			if inUser && strings.HasSuffix(part, ".service") {
				return part
			}
		}
	}
	return ""
}

// unitActive dice se l'unit utente è attiva.
func unitActive(unit string) bool {
	if _, err := exec.LookPath("systemctl"); err != nil {
		return false
	}
	return exec.Command("systemctl", "--user", "is-active", "--quiet", unit).Run() == nil
}

// systemUnitActive dice se è attiva un'unit di sistema con quel nome.
func systemUnitActive(unit string) bool {
	if _, err := exec.LookPath("systemctl"); err != nil {
		return false
	}
	return exec.Command("systemctl", "is-active", "--quiet", unit).Run() == nil
}

// systemctl esegue systemctl --user args.
func systemctl(args ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "systemctl", append([]string{"--user"}, args...)...).CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("systemctl --user %s: %s", strings.Join(args, " "), msg)
	}
	return nil
}
