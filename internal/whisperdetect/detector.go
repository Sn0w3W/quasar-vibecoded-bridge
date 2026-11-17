// Package whisperdetect tells whispered voice requests from voiced speech.
//
// It decodes the speaker's Ogg/Opus microphone stream and measures, per 20 ms
// frame, how much energy lies below 500 Hz. Whisper has no vocal-fold voicing
// and almost no low-band energy; voiced speech keeps most of its energy there
// regardless of loudness or distance. Calibrated on speaker captures: whispers
// had 0.08-0.12 of their energy below 500 Hz, voiced speech 0.64-0.95.
package whisperdetect

import (
	"bytes"
	"fmt"
	"math"
	"slices"
	"sync"
	"time"

	"github.com/pion/opus"
)

const (
	sampleRate       = 16000
	frameSamples     = sampleRate / 50 // 20 ms
	maxFrames        = 1500            // 30 s; longer requests are not analysed further
	maxPacketBytes   = 64 << 10
	activeMarginDB   = 6
	whisperLowRatio  = 0.3
	voicedLowRatio   = 0.5
	whisperShare     = 0.5
	minWhisperFrames = 8
)

// Result summarises one request. Share is whisper-like frames over all
// classified (whisper-like plus voiced-like) active frames.
type Result struct {
	Whisper       bool          `json:"whisper"`
	Share         float64       `json:"share"`
	WhisperFrames int           `json:"whisper_frames"`
	VoicedFrames  int           `json:"voiced_frames"`
	ActiveFrames  int           `json:"active_frames"`
	Frames        int           `json:"frames"`
	CPU           time.Duration `json:"cpu"`
	Err           string        `json:"error,omitempty"`
}

// biquad is a transposed direct form II second-order section (RBJ cookbook).
type biquad struct{ b0, b1, b2, a1, a2, z1, z2 float64 }

func (f *biquad) step(x float64) float64 {
	y := f.b0*x + f.z1
	f.z1 = f.b1*x - f.a1*y + f.z2
	f.z2 = f.b2*x - f.a2*y
	return y
}

func newBiquad(f0, q float64, low bool) biquad {
	w := 2 * math.Pi * f0 / sampleRate
	cos, alpha := math.Cos(w), math.Sin(w)/(2*q)
	a0 := 1 + alpha
	b := biquad{a1: -2 * cos / a0, a2: (1 - alpha) / a0}
	if low {
		b.b0, b.b1, b.b2 = (1-cos)/2/a0, (1-cos)/a0, (1-cos)/2/a0
	} else {
		b.b0, b.b1, b.b2 = (1+cos)/2/a0, -(1+cos)/a0, (1+cos)/2/a0
	}
	return b
}

type frameStat struct{ db, low float64 }

// Detector is fed the speaker's microphone payloads for one request.
type Detector struct {
	mu     sync.Mutex
	dec    opus.Decoder
	pcm    []float32
	carry  []float32
	packet []byte
	hp     biquad
	lp     [2]biquad // 4th-order Butterworth low-pass at 500 Hz
	frames []frameStat
	cpu    time.Duration
	err    string
}

func New() (*Detector, error) {
	dec, err := opus.NewDecoderWithOutput(sampleRate, 1)
	if err != nil {
		return nil, err
	}
	return &Detector{
		dec: dec, pcm: make([]float32, 6*frameSamples*3),
		hp: newBiquad(80, math.Sqrt2/2, false),
		lp: [2]biquad{newBiquad(500, 0.5412, true), newBiquad(500, 1.3066, true)},
	}, nil
}

// Feed accepts one microphone payload: one or more complete Ogg pages.
// Malformed input is recorded and ignored; it never panics.
func (d *Detector) Feed(data []byte) {
	d.mu.Lock()
	defer d.mu.Unlock()
	start := time.Now()
	defer func() { d.cpu += time.Since(start) }()
	for len(data) > 0 {
		if len(data) < 27 || string(data[:4]) != "OggS" || data[4] != 0 {
			d.fail("invalid Ogg page")
			return
		}
		count := int(data[26])
		if len(data) < 27+count {
			d.fail("truncated Ogg page")
			return
		}
		size := 27 + count
		for _, n := range data[27 : 27+count] {
			size += int(n)
		}
		if len(data) < size {
			d.fail("truncated Ogg page")
			return
		}
		if data[5]&1 == 0 {
			d.packet = d.packet[:0] // a new page that does not continue a packet
		}
		pos := 27 + count
		for _, n := range data[27 : 27+count] {
			d.packet = append(d.packet, data[pos:pos+int(n)]...)
			pos += int(n)
			if len(d.packet) > maxPacketBytes {
				d.fail("oversized Opus packet")
				d.packet = d.packet[:0]
				return
			}
			if n < 255 {
				d.decode(d.packet)
				d.packet = d.packet[:0]
			}
		}
		data = data[size:]
	}
}

func (d *Detector) fail(reason string) {
	if d.err == "" {
		d.err = reason
	}
}

func (d *Detector) decode(packet []byte) {
	if len(packet) == 0 || len(d.frames) >= maxFrames {
		return
	}
	// The speaker may start a new logical stream mid-request; headers repeat.
	if bytes.HasPrefix(packet, []byte("OpusHead")) || bytes.HasPrefix(packet, []byte("OpusTags")) {
		return
	}
	n, err := d.safeDecode(packet)
	if err != nil {
		d.fail(err.Error())
		return
	}
	d.carry = append(d.carry, d.pcm[:n]...)
	for len(d.carry) >= frameSamples {
		d.analyse(d.carry[:frameSamples])
		d.carry = d.carry[frameSamples:]
	}
	d.carry = slices.Clone(d.carry)
}

// safeDecode isolates the decoder: a malformed packet must not crash Bridge.
func (d *Detector) safeDecode(packet []byte) (n int, err error) {
	defer func() {
		if r := recover(); r != nil {
			n, err = 0, fmt.Errorf("opus decoder panic")
		}
	}()
	return d.dec.DecodeToFloat32(packet, d.pcm)
}

func (d *Detector) analyse(frame []float32) {
	var total, low float64
	for _, s := range frame {
		x := d.hp.step(float64(s))
		y := d.lp[1].step(d.lp[0].step(x))
		total += x * x
		low += y * y
	}
	db := 10 * math.Log10(total/float64(len(frame))+1e-12)
	d.frames = append(d.frames, frameStat{db: db, low: low / (total + 1e-12)})
}

// Result classifies everything fed so far. Activity is relative to the
// request's own noise floor, so overall loudness does not matter.
func (d *Detector) Result() Result {
	d.mu.Lock()
	defer d.mu.Unlock()
	r := Result{Frames: len(d.frames), CPU: d.cpu, Err: d.err}
	if len(d.frames) == 0 {
		return r
	}
	dbs := make([]float64, len(d.frames))
	for i, f := range d.frames {
		dbs[i] = f.db
	}
	slices.Sort(dbs)
	floor := dbs[len(dbs)/10]
	for _, f := range d.frames {
		if f.db <= floor+activeMarginDB {
			continue
		}
		r.ActiveFrames++
		switch {
		case f.low < whisperLowRatio:
			r.WhisperFrames++
		case f.low > voicedLowRatio:
			r.VoicedFrames++
		}
	}
	if classified := r.WhisperFrames + r.VoicedFrames; classified > 0 {
		r.Share = float64(r.WhisperFrames) / float64(classified)
	}
	r.Whisper = r.WhisperFrames >= minWhisperFrames && r.Share >= whisperShare
	return r
}
