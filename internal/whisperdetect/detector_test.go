package whisperdetect

import (
	"encoding/binary"
	"encoding/json/v2"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// readFrames splits a capture into the WebSocket payloads the speaker sent:
// each record is a 4-byte big-endian length followed by Ogg page bytes.
func readFrames(t *testing.T, path string) [][]byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var frames [][]byte
	for len(data) >= 4 {
		n := int(binary.BigEndian.Uint32(data))
		frames = append(frames, data[4:4+n])
		data = data[4+n:]
	}
	return frames
}

func detect(t *testing.T, path string) Result {
	t.Helper()
	d, err := New()
	if err != nil {
		t.Fatal(err)
	}
	for _, frame := range readFrames(t, path) {
		d.Feed(frame)
	}
	return d.Result()
}

func TestDetectorSeparatesWhisperFromVoiceAtAnyLevel(t *testing.T) {
	for name, want := range map[string]bool{"voiced.mic": false, "voiced-quiet.mic": false, "whisper.mic": true} {
		r := detect(t, filepath.Join("testdata", name))
		if r.Err != "" {
			t.Fatalf("%s: %s", name, r.Err)
		}
		if r.Whisper != want {
			t.Errorf("%s: whisper=%v share=%.2f whisper_frames=%d voiced_frames=%d", name, r.Whisper, r.Share, r.WhisperFrames, r.VoicedFrames)
		}
		if r.Frames < 50 {
			t.Errorf("%s: decoded only %d frames", name, r.Frames)
		}
	}
}

func TestDetectorSurvivesGarbage(t *testing.T) {
	d, err := New()
	if err != nil {
		t.Fatal(err)
	}
	rng := rand.New(rand.NewPCG(1, 2))
	for range 200 {
		buf := make([]byte, rng.IntN(300))
		for i := range buf {
			buf[i] = byte(rng.Uint32())
		}
		if rng.IntN(3) == 0 && len(buf) > 27 {
			copy(buf, "OggS")
		}
		d.Feed(buf)
	}
	if r := d.Result(); r.Whisper {
		t.Fatalf("garbage classified as whisper: %+v", r)
	}
}

func TestDetectorNeedsEnoughSpeech(t *testing.T) {
	d, err := New()
	if err != nil {
		t.Fatal(err)
	}
	frames := readFrames(t, filepath.Join("testdata", "whisper.mic"))
	d.Feed(frames[0]) // headers only
	if r := d.Result(); r.Whisper || r.Frames != 0 {
		t.Fatalf("headers alone gave %+v", r)
	}
}

// Real speaker captures stay outside the repository. Point QB_WHISPER_CAPTURES
// at a directory of .mic files with expected.json {"<file base>": true|false}.
func TestDetectorOnLocalCaptures(t *testing.T) {
	dir := os.Getenv("QB_WHISPER_CAPTURES")
	if dir == "" {
		t.Skip("QB_WHISPER_CAPTURES is not set")
	}
	raw, err := os.ReadFile(filepath.Join(dir, "expected.json"))
	if err != nil {
		t.Fatal(err)
	}
	var expected map[string]bool
	if err := json.Unmarshal(raw, &expected); err != nil {
		t.Fatal(err)
	}
	files, _ := filepath.Glob(filepath.Join(dir, "*.mic"))
	if len(files) == 0 {
		t.Fatal("no captures")
	}
	for _, file := range files {
		base := strings.TrimSuffix(filepath.Base(file), ".mic")
		want, ok := expected[base]
		r := detect(t, file)
		t.Logf("%s want=%v ok=%v got=%v share=%.2f whisper=%d voiced=%d active=%d frames=%d err=%q", base, want, ok, r.Whisper, r.Share, r.WhisperFrames, r.VoicedFrames, r.ActiveFrames, r.Frames, r.Err)
		if ok && r.Whisper != want {
			t.Errorf("%s: whisper=%v, want %v", base, r.Whisper, want)
		}
	}
}

func BenchmarkDetectorWhisperSample(b *testing.B) {
	data, err := os.ReadFile(filepath.Join("testdata", "whisper.mic"))
	if err != nil {
		b.Fatal(err)
	}
	var frames [][]byte
	for len(data) >= 4 {
		n := int(binary.BigEndian.Uint32(data))
		frames = append(frames, data[4:4+n])
		data = data[4+n:]
	}
	for b.Loop() {
		d, _ := New()
		for _, frame := range frames {
			d.Feed(frame)
		}
		_ = d.Result()
	}
}
