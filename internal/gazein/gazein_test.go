// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Antonio Blescia

package gazein

import (
	"encoding/binary"
	"math"
	"testing"
)

// packet costruisce un datagramma T4CG v2 con gli offset del driver
// (tobii4c-linux, internal/sink/udp.go).
func packet() []byte {
	p := make([]byte, 192)
	le := binary.LittleEndian
	copy(p, "T4CG")
	p[4] = 2
	le.PutUint16(p[6:], flagGaze|flagGazeRaw|flagLeftPupil) // pupilla destra non valida
	le.PutUint64(p[8:], 42)
	le.PutUint64(p[16:], 1_700_000_000_000_000_000)
	le.PutUint64(p[24:], 81_539_184_910)
	put := func(off int, v float32) { le.PutUint32(p[off:], math.Float32bits(v)) }
	put(32, 0.25)
	put(36, 0.75)
	put(40, 0.26)
	put(44, 0.74)
	put(96, 3.5)
	put(100, -1)
	le.PutUint32(p[120:], flag2LeftOrigin|flag2RightOrig)
	p[124] = 2
	for i, v := range []float32{-31, 20, 610, 32, 21, 612} {
		put(136+4*i, v)
	}
	return p
}

func TestParse(t *testing.T) {
	var s Sample
	if err := Parse(packet(), &s); err != nil {
		t.Fatal(err)
	}
	if s.Seq != 42 || s.DeviceUs != 81_539_184_910 || s.HostNs != 1_700_000_000_000_000_000 {
		t.Errorf("intestazione: %+v", s)
	}
	if !s.GazeValid || !s.RawValid || s.Gaze != [2]float64{0.25, 0.75} || math.Abs(s.Raw[0]-0.26) > 1e-6 {
		t.Errorf("sguardo: %+v", s)
	}
	if !s.PupilValid[0] || s.PupilValid[1] || s.PupilMM[0] != 3.5 {
		t.Errorf("pupille: %v %v", s.PupilMM, s.PupilValid)
	}
	if !s.EyeValid[0] || !s.EyeValid[1] || s.EyeMM[0] != [3]float64{-31, 20, 610} || s.EyeMM[1][2] != 612 {
		t.Errorf("occhi: %v %v", s.EyeMM, s.EyeValid)
	}
	if s.Presence != 2 {
		t.Errorf("presenza %d", s.Presence)
	}
	bad := packet()
	bad[4] = 1
	if Parse(bad, &s) == nil {
		t.Error("un pacchetto v1 va rifiutato (mancano gli occhi nel sistema dello schermo)")
	}
}

func TestGeometry(t *testing.T) {
	g := Geometry{WidthMM: 700, HeightMM: 390, WidthPx: 1920, HeightPx: 1080}
	// 10,8 mm a 620 mm ≈ 1°
	d := 620 * math.Tan(math.Pi/180) / 700
	if a := g.AngleDeg(DefaultEye, [2]float64{0.5, 0.5}, [2]float64{0.5 + d, 0.5}); math.Abs(a-1) > 1e-3 {
		t.Errorf("angolo %.4f°, atteso 1°", a)
	}
	if m := g.ToMM([2]float64{0, 0}); m != [3]float64{-350, 195, 0} {
		t.Errorf("angolo in alto a sinistra: %v", m)
	}
}
