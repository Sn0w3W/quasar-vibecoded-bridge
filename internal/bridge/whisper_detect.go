package bridge

import (
	"context"
	"encoding/binary"
	"log/slog"
	"strings"
	"time"

	"quasarbridge/internal/whisperdetect"
	"quasarbridge/internal/wire"
)

// whisperWait bounds how long a finished answer waits for the end of the
// utterance; the model usually takes far longer than the microphone stream.
const whisperWait = 400 * time.Millisecond

// The caller holds n.mu.
func (n *Native) beginWhisperDetection(p *nativeRequest, streamID uint32, format string) {
	if !n.DetectWhisper || !strings.HasPrefix(format, "audio/opus") {
		return
	}
	detector, err := whisperdetect.New()
	if err != nil {
		return
	}
	p.whisper, p.whisperDone = detector, make(chan struct{})
	if n.detectors == nil {
		n.detectors = map[uint32]*nativeRequest{}
	}
	n.detectors[streamID] = p
	p.ctx = whisperdetect.WithSource(p.ctx, func(ctx context.Context) whisperdetect.Result {
		select {
		case <-p.whisperDone:
		case <-ctx.Done():
		case <-time.After(whisperWait):
		}
		return detector.Result()
	})
}

// finishWhisper stops feeding at the end of the utterance and logs the decision
// (counts only, never audio).
func (n *Native) finishWhisper(p *nativeRequest) {
	p.whisperOnce.Do(func() {
		close(p.whisperDone)
		n.mu.Lock()
		for stream, q := range n.detectors {
			if q == p {
				delete(n.detectors, stream)
			}
		}
		n.mu.Unlock()
		r := p.whisper.Result()
		slog.Info("native_whisper_detect", "request", requestTag(p.id), "whisper", r.Whisper, "share", r.Share, "whisper_frames", r.WhisperFrames, "voiced_frames", r.VoicedFrames, "active_frames", r.ActiveFrames, "frames", r.Frames, "cpu_ms", r.CPU.Milliseconds(), "error", r.Err)
	})
}

// clientAudio feeds microphone frames to the request's whisper detector and
// ends detection when the speaker closes the stream. Decoding runs outside n.mu.
func (n *Native) clientAudio(f wire.Frame, data []byte) {
	n.mu.Lock()
	if len(n.detectors) == 0 {
		n.mu.Unlock()
		return
	}
	var target, closed *nativeRequest
	if f.Client == nil {
		if len(data) > 4 {
			target = n.detectors[binary.BigEndian.Uint32(data)]
		}
	} else if sc := f.Client.GetStreamControl(); sc != nil && sc.GetClose() != nil {
		closed = n.detectors[uint32(sc.GetStreamId())]
	}
	n.mu.Unlock()
	if target != nil {
		select {
		case <-target.whisperDone:
		default:
			target.whisper.Feed(data[4:])
		}
	}
	if closed != nil {
		n.finishWhisper(closed)
	}
}
