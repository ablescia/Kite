// SPDX-License-Identifier: AGPL-3.0-or-later

package engine

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Logger scrive i dati grezzi della sessione per l'analisi offline:
//
//	samples.csv   un campione per riga (90 Hz): sguardo, pupille, stato
//	              oculomotore, candidato, indice pupillare, luminanza, e
//	              perché il candidato non scatta ancora (wait)
//	events.jsonl  saccadi, selezioni, inizio e fine dei blocchi, calibrazioni
//	summary.json  risultati dell'esercizio, per frase e in totale
type Logger struct {
	Dir     string
	mu      sync.Mutex
	sf, ef  *os.File
	samples *bufio.Writer
	events  *bufio.Writer
}

const sampleHeader = "host_ns,dev_us,seq,mode,block,gx,gy,g_valid,rx,ry,r_valid,pupil_l,pupil_r,pupil_l_valid,pupil_r_valid," +
	"eye_x,eye_y,eye_z,phase,vel_deg_s,peak_thr,onset_thr,fix_id,fix_cx,fix_cy,fix_rms_deg,fix_stable," +
	"cand,progress,required_ms,selected,pupil_meas,pupil_pred,pupil_resid,pupil_z,pupil_valid,lum_eff,lum_global," +
	"fix_drift,wait\n"

// NewLogger crea la cartella della sessione e i file.
func NewLogger(dir string) (*Logger, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	sf, err := os.Create(filepath.Join(dir, "samples.csv"))
	if err != nil {
		return nil, err
	}
	ef, err := os.Create(filepath.Join(dir, "events.jsonl"))
	if err != nil {
		sf.Close()
		return nil, err
	}
	l := &Logger{Dir: dir, sf: sf, ef: ef, samples: bufio.NewWriterSize(sf, 1<<16), events: bufio.NewWriter(ef)}
	l.samples.WriteString(sampleHeader)
	return l, nil
}

// Samplef aggiunge una riga a samples.csv.
func (l *Logger) Samplef(format string, args ...any) {
	if l == nil {
		return
	}
	l.mu.Lock()
	fmt.Fprintf(l.samples, format, args...)
	l.mu.Unlock()
}

// Event aggiunge un evento a events.jsonl.
func (l *Logger) Event(kind string, v map[string]any) {
	if l == nil {
		return
	}
	if v == nil {
		v = map[string]any{}
	}
	v["event"] = kind
	v["wall"] = time.Now().Format(time.RFC3339Nano)
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	l.mu.Lock()
	l.events.Write(append(b, '\n'))
	l.mu.Unlock()
}

// WriteJSON scrive (atomicamente) un file JSON nella cartella della sessione.
func (l *Logger) WriteJSON(name string, v any) error {
	if l == nil {
		return nil
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	p := filepath.Join(l.Dir, name)
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

// Flush svuota i buffer su disco.
func (l *Logger) Flush() {
	if l == nil {
		return
	}
	l.mu.Lock()
	l.samples.Flush()
	l.events.Flush()
	l.mu.Unlock()
}

// Close svuota e chiude i file.
func (l *Logger) Close() {
	if l == nil {
		return
	}
	l.Flush()
	l.sf.Close()
	l.ef.Close()
}
