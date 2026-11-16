// SPDX-License-Identifier: AGPL-3.0-or-later

package driver

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeDriver scrive un eseguibile finto che stampa le righe date (log JSON
// del driver), salva gli argomenti ricevuti ed esce con il codice dato.
func fakeDriver(t *testing.T, lines []string, code int) (exe, argsFile string) {
	dir := t.TempDir()
	exe, argsFile = filepath.Join(dir, "tobii4c"), filepath.Join(dir, "args")
	var b strings.Builder
	b.WriteString("#!/bin/sh\necho \"$@\" > " + argsFile + "\n")
	for _, l := range lines {
		b.WriteString("printf '%s\\n' '" + l + "'\n")
	}
	b.WriteString("exit " + string(rune('0'+code)) + "\n")
	if err := os.WriteFile(exe, []byte(b.String()), 0o755); err != nil {
		t.Fatal(err)
	}
	return exe, argsFile
}

// fakeSystem sostituisce il sistema: nessun servizio, nessun processo che
// tiene il tracker, geometria prima e dopo la calibrazione.
func fakeSystem(t *testing.T, exe string, before, after Display) {
	oldI, oldL := inspect, loadDisplay
	t.Cleanup(func() { inspect, loadDisplay = oldI, oldL })
	inspect = func(string) State { return State{Exe: exe, Connected: true, Display: before} }
	loadDisplay = func() Display { return after }
}

func TestCalibrateReadsTheDriverLog(t *testing.T) {
	lines := []string{
		`{"time":"2026-11-16T10:00:00Z","level":"INFO","msg":"calibration window open","monitor":"HDMI-A-1"}`,
		`{"level":"INFO","msg":"display area applied","area":"…"}`,
		`{"level":"INFO","msg":"calibration started","backup_bytes":4096}`,
	}
	errs := []string{"0.50", "0.70", "0.60", "0.90", "0.80"}
	for _, e := range errs {
		lines = append(lines, `{"level":"INFO","msg":"validation point","n":120,"error_deg":"`+e+`","precision_deg":"0.20"}`)
	}
	lines = append(lines, `{"level":"INFO","msg":"calibration finished","bytes":4096,"saved":"/x/calibration.bin"}`)
	exe, argsFile := fakeDriver(t, lines, 0)
	d := Display{WidthMM: 700, HeightMM: 390}
	fakeSystem(t, exe, d, d)
	before, after := false, false
	logPath := filepath.Join(t.TempDir(), "gaze-calibration.log")
	r := Calibrate(context.Background(), Options{Monitor: "HDMI-A-1", LogPath: logPath,
		Before: func() { before = true }, After: func() { after = !after && before }})
	if r.Err != nil || !r.Finished || r.Aborted {
		t.Fatalf("esito: %+v", r)
	}
	if !before || !after {
		t.Errorf("Before/After non chiamate in ordine: %v %v", before, after)
	}
	acc, prec, ok := r.Mean()
	if !ok || len(r.Points) != 5 || math.Abs(acc-0.70) > 1e-9 || math.Abs(prec-0.20) > 1e-9 {
		t.Errorf("verifica: %d punti, accuratezza %.2f°, precisione %.2f°", len(r.Points), acc, prec)
	}
	if r.DisplayChanged() {
		t.Error("geometria invariata segnalata come cambiata")
	}
	args, _ := os.ReadFile(argsFile)
	if got := strings.TrimSpace(string(args)); got != "calibrate -log-format json -monitor HDMI-A-1" {
		t.Errorf("argomenti del driver: %q", got)
	}
	saved, _ := os.ReadFile(logPath)
	if strings.Count(string(saved), "\n") != len(lines) || r.Log != logPath {
		t.Errorf("log salvato in %q: %d righe su %d", r.Log, strings.Count(string(saved), "\n"), len(lines))
	}
}

func TestCalibrateAbortAndRepeatedVerification(t *testing.T) {
	lines := []string{`{"msg":"calibration started"}`}
	for i := 0; i < 5; i++ {
		lines = append(lines, `{"msg":"validation point","error_deg":"2.00","precision_deg":"0.30"}`)
	}
	// ricalibrazione dal risultato: la verifica vecchia non conta più
	lines = append(lines, `{"msg":"calibration started"}`)
	for i := 0; i < 5; i++ {
		lines = append(lines, `{"msg":"validation point","error_deg":0.6,"precision_deg":0.2}`)
	}
	lines = append(lines, `{"msg":"calibration finished"}`, `{"msg":"calibration started"}`,
		`{"msg":"calibration aborted","restored_previous":true}`)
	exe, _ := fakeDriver(t, lines, 0)
	fakeSystem(t, exe, Display{WidthMM: 700, HeightMM: 390}, Display{WidthMM: 698, HeightMM: 393})
	r := Calibrate(context.Background(), Options{})
	if r.Err != nil || r.Finished || !r.Aborted || !r.Restored {
		t.Fatalf("interruzione: %+v", r)
	}
	if len(r.Points) != 0 {
		t.Errorf("dopo la nuova partenza la verifica precedente non vale: %d punti", len(r.Points))
	}
	if !r.DisplayChanged() {
		t.Error("geometria cambiata non segnalata")
	}
}

func TestCalibrateErrors(t *testing.T) {
	exe, _ := fakeDriver(t, []string{`{"level":"ERROR","msg":"tracker non trovato","err":"libusb: not found"}`}, 1)
	fakeSystem(t, exe, Display{}, Display{})
	r := Calibrate(context.Background(), Options{})
	if r.Err == nil || !strings.Contains(r.Err.Error(), "tracker non trovato: libusb: not found") {
		t.Errorf("errore del driver: %v", r.Err)
	}
	exe, _ = fakeDriver(t, []string{"errore: connessione a X11 ($DISPLAY): no display"}, 1)
	fakeSystem(t, exe, Display{}, Display{})
	if r := Calibrate(context.Background(), Options{}); r.Err == nil ||
		r.Err.Error() != "tobii4c calibrate: connessione a X11 ($DISPLAY): no display" {
		t.Errorf("errore in chiaro: %v", r.Err)
	}
	// un processo avviato a mano tiene il tracker: non si parte
	old := inspect
	t.Cleanup(func() { inspect = old })
	inspect = func(string) State {
		return State{Exe: exe, Manual: []Holder{{PID: 42, Comm: "tobii4c", Args: []string{"/usr/bin/tobii4c", "run", "-headpose", "off"}}}}
	}
	if r := Calibrate(context.Background(), Options{}); r.Err == nil ||
		!strings.Contains(r.Err.Error(), "tobii4c run -headpose off (PID 42)") {
		t.Errorf("tracker occupato: %v", r.Err)
	}
}

func TestUserUnitAndFindExe(t *testing.T) {
	cg := "0::/user.slice/user-1000.slice/user@1000.service/app.slice/tobii4c.service\n"
	if u := userUnit(cg); u != "tobii4c.service" {
		t.Errorf("unit: %q", u)
	}
	if u := userUnit("0::/user.slice/user-1000.slice/session-2.scope\n"); u != "" {
		t.Errorf("processo da terminale come servizio: %q", u)
	}
	if _, err := FindExe(filepath.Join(t.TempDir(), "manca")); err == nil {
		t.Error("eseguibile mancante accettato")
	}
	exe, _ := fakeDriver(t, nil, 0)
	if p, err := FindExe(exe); err != nil || p != exe {
		t.Errorf("FindExe(%q) = %q, %v", exe, p, err)
	}
}
