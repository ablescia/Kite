// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Antonio Blescia

// Package gazein riceve i campioni del driver tobii4c (datagrammi UDP T4CG
// v2, 192 byte little-endian, uno ogni ~11 ms a 90 Hz) e fornisce la
// geometria per passare dalle coordinate normalizzate dello schermo ai gradi
// di angolo visivo.
//
// Il formato è quello di github.com/ablescia/tobii4c-linux (internal/sink,
// sezione "Output UDP" del README): qui si leggono solo i campi che servono.
package gazein

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"net"
	"sync/atomic"
	"time"
)

// Sample è un campione del tracker, ridotto ai campi usati da Kite.
type Sample struct {
	Seq      uint64
	HostNs   int64 // ricezione USB nel driver, CLOCK_REALTIME
	DeviceUs int64 // orologio del tracker: intervalli precisi tra campioni

	// Sguardo combinato, normalizzato [0,1] sulla display area (0,0 in alto a
	// sinistra). Gaze è quello filtrato dal firmware (colonna 0x1c), Raw quello
	// non filtrato ("responsive", 0x20): le saccadi si misurano su Raw, perché
	// il filtro le smussa e le ritarda.
	Gaze      [2]float64
	GazeValid bool
	Raw       [2]float64
	RawValid  bool

	PupilMM    [2]float64 // sinistra, destra
	PupilValid [2]bool

	// Centro corneale in mm dal centro dello schermo (+X destra, +Y su, +Z
	// verso l'utente): il vertice degli angoli visivi.
	EyeMM    [2][3]float64
	EyeValid [2]bool

	Presence uint8 // 0 sconosciuta, 1 assente, 2 presente
}

// Bit dei flag di validità T4CG (offset 6 e 120).
const (
	flagGaze        = 1 << 0
	flagGazeRaw     = 1 << 1
	flagLeftPupil   = 1 << 10
	flagRightPupil  = 1 << 11
	flag2LeftOrigin = 1 << 0
	flag2RightOrig  = 1 << 1
	packetSize      = 192
)

// Parse decodifica un datagramma T4CG v2.
func Parse(p []byte, s *Sample) error {
	if len(p) < packetSize || string(p[0:4]) != "T4CG" {
		return errors.New("non è un pacchetto T4CG v2")
	}
	if p[4] != 2 {
		return fmt.Errorf("versione T4CG %d non supportata (serve la 2)", p[4])
	}
	le := binary.LittleEndian
	f := le.Uint16(p[6:])
	f2 := le.Uint32(p[120:])
	f32 := func(off int) float64 { return float64(math.Float32frombits(le.Uint32(p[off:]))) }
	*s = Sample{
		Seq:        le.Uint64(p[8:]),
		HostNs:     int64(le.Uint64(p[16:])),
		DeviceUs:   int64(le.Uint64(p[24:])),
		Gaze:       [2]float64{f32(32), f32(36)},
		GazeValid:  f&flagGaze != 0,
		Raw:        [2]float64{f32(40), f32(44)},
		RawValid:   f&flagGazeRaw != 0,
		PupilMM:    [2]float64{f32(96), f32(100)},
		PupilValid: [2]bool{f&flagLeftPupil != 0, f&flagRightPupil != 0},
		EyeMM: [2][3]float64{
			{f32(136), f32(140), f32(144)},
			{f32(148), f32(152), f32(156)},
		},
		EyeValid: [2]bool{f2&flag2LeftOrigin != 0, f2&flag2RightOrig != 0},
		Presence: p[124],
	}
	return nil
}

// Stats sono i contatori del ricevitore (lettura concorrente sicura).
type Stats struct {
	Packets   atomic.Uint64
	Bad       atomic.Uint64
	SeqGaps   atomic.Uint64
	LastRecvN atomic.Int64 // time.Now().UnixNano() dell'ultimo pacchetto
}

// Listen riceve datagrammi su addr e li inoltra su out finché ctx non termina.
// Se out è pieno il campione più vecchio non viene scartato: si blocca la
// lettura (il kernel tiene i datagrammi nel buffer del socket), perché un
// campione perso falserebbe velocità e tempi di fissazione.
func Listen(ctx context.Context, addr string, out chan<- Sample, st *Stats) error {
	la, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return err
	}
	conn, err := net.ListenUDP("udp", la)
	if err != nil {
		return fmt.Errorf("ascolto UDP su %s: %w (c'è già un altro ricevitore?)", addr, err)
	}
	_ = conn.SetReadBuffer(1 << 20)
	go func() { <-ctx.Done(); conn.Close() }()
	buf := make([]byte, 2048)
	var last uint64
	for {
		n, _, err := conn.ReadFromUDP(buf)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		var s Sample
		if Parse(buf[:n], &s) != nil {
			st.Bad.Add(1)
			continue
		}
		st.Packets.Add(1)
		st.LastRecvN.Store(time.Now().UnixNano())
		if last != 0 && s.Seq != last+1 {
			st.SeqGaps.Add(1)
		}
		last = s.Seq
		select {
		case out <- s:
		case <-ctx.Done():
			return nil
		}
	}
}

// Geometry descrive lo schermo fisico su cui è normalizzato lo sguardo.
type Geometry struct {
	WidthMM, HeightMM float64
	WidthPx, HeightPx int
}

// DefaultEye è la posizione degli occhi quando il tracker non la fornisce:
// 62 cm davanti al centro dello schermo.
var DefaultEye = [3]float64{0, 0, 620}

// ToMM converte coordinate normalizzate in mm sul piano dello schermo, nel
// sistema del driver (origine al centro, +Y in alto).
func (g Geometry) ToMM(p [2]float64) [3]float64 {
	return [3]float64{(p[0] - 0.5) * g.WidthMM, (0.5 - p[1]) * g.HeightMM, 0}
}

// AngleDeg è l'angolo visivo tra due punti dello schermo visti dall'occhio.
func (g Geometry) AngleDeg(eye [3]float64, a, b [2]float64) float64 {
	pa, pb := g.ToMM(a), g.ToMM(b)
	va := [3]float64{pa[0] - eye[0], pa[1] - eye[1], pa[2] - eye[2]}
	vb := [3]float64{pb[0] - eye[0], pb[1] - eye[1], pb[2] - eye[2]}
	cx := va[1]*vb[2] - va[2]*vb[1]
	cy := va[2]*vb[0] - va[0]*vb[2]
	cz := va[0]*vb[1] - va[1]*vb[0]
	dot := va[0]*vb[0] + va[1]*vb[1] + va[2]*vb[2]
	return math.Atan2(math.Sqrt(cx*cx+cy*cy+cz*cz), dot) * 180 / math.Pi
}

// MMPerDeg è quanti mm sullo schermo sottende un grado, vicino al centro,
// per un occhio alla distanza di eye.
func (g Geometry) MMPerDeg(eye [3]float64) float64 {
	d := eye[2]
	if d < 200 {
		d = DefaultEye[2]
	}
	return d * math.Tan(math.Pi/180)
}

// PxPerMM: pixel per mm in orizzontale e verticale.
func (g Geometry) PxPerMM() (float64, float64) {
	return float64(g.WidthPx) / g.WidthMM, float64(g.HeightPx) / g.HeightMM
}
