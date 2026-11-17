package bridge

import (
	"cmp"
	"context"
	"regexp"
	"strings"

	directives "github.com/skrashevich/quasar-go-genproto/proto/alice/protos/api/alicekit/directives"
	"google.golang.org/protobuf/types/known/structpb"

	"quasarbridge/internal/ttsvoice"
	"quasarbridge/internal/whisperdetect"
)

// Measured on UniProxy TtsGenerate: only Alice's own voice (shitova) whispers.
// Other voices ignore is_whisper, so whispered speech must use Alice's voice.
const aliceVoice = ttsvoice.Alice

var (
	// Go's \b is ASCII-only, so Cyrillic word edges are spelled out.
	whisperRequest  = regexp.MustCompile(`(?i)(?:^|[^\p{L}])((?:полу)?ш[её]потом|(?:про)?шепчи(?:те)?|шепни(?:те)?)(?:$|[^\p{L}])`)
	whisperNegation = regexp.MustCompile(`(?i)(?:^|[^\p{L}])(?:не|без)(?:[\s\p{Z}]+(?:надо|нужно|стоит|будем|говори|отвечай))?[\s\p{Z},]*$`)
)

// wantsWhisper reports an explicit request to whisper the answer. A negated
// mention ("не шёпотом", "не шепчи") does not count.
func wantsWhisper(text string) bool {
	for _, m := range whisperRequest.FindAllStringSubmatchIndex(text, -1) {
		if !whisperNegation.MatchString(text[:m[2]]) {
			return true
		}
	}
	return false
}

// whisperSpeech turns the whole answer into Alice's whisper. Other voices are
// removed because they cannot whisper; emotion, speed, effects and sounds stay.
func whisperSpeech(speech string) string {
	body := ttsvoice.TagPattern.ReplaceAllStringFunc(speech, func(tag string) string {
		var attrs []ttsvoice.Attr
		for _, a := range ttsvoice.ParseTag(tag) {
			if a.Name != "voice" {
				attrs = append(attrs, a)
			}
		}
		return ttsvoice.FormatTag(ttsvoice.SetAttr(attrs, "is_whisper", "true"))
	})
	return `<speaker voice="` + aliceVoice + `" is_whisper="true">` + strings.TrimLeft(body, " ")
}

// whisperSegmentsWithAliceVoice keeps model-chosen voices, except in a tag that
// explicitly starts whispering while another voice is active: there the voice
// switches to Alice, otherwise the whisper would be silently ignored.
func whisperSegmentsWithAliceVoice(speech string) string {
	voice := aliceVoice
	return ttsvoice.TagPattern.ReplaceAllStringFunc(speech, func(tag string) string {
		attrs := ttsvoice.ParseTag(tag)
		if v, ok := ttsvoice.LookupAttr(attrs, "voice"); ok {
			voice = cmp.Or(strings.TrimSuffix(strings.TrimSuffix(v, ".gpu"), ".us"), aliceVoice)
		}
		if w, ok := ttsvoice.LookupAttr(attrs, "is_whisper"); !ok || w != "true" || voice == aliceVoice {
			return tag
		}
		voice = aliceVoice
		return ttsvoice.FormatTag(ttsvoice.SetAttr(attrs, "voice", aliceVoice))
	})
}

// prepareSpeech applies whisper rules once the model answer is split into
// displayed text and synthesis text. Markup is never added when disabled.
// Like the cloud, only a whisper heard in the microphone lowers the volume;
// a request in words ("шёпотом") or a model choice changes the voice only.
func (e *Engine) prepareSpeech(ctx context.Context, r *Resolution, question string) {
	if !e.TTSMarkup {
		return
	}
	if r.speech != "" {
		r.speech = ttsvoice.Sanitize(r.speech)
	}
	heard := userWhispered(ctx)
	if heard || wantsWhisper(question) {
		r.speech = whisperSpeech(cmp.Or(r.speech, r.Answer))
		if heard {
			r.whisperLevel = e.WhisperSoundLevel
		}
		return
	}
	if r.speech != "" {
		r.speech = whisperSegmentsWithAliceVoice(r.speech)
	}
}

// PrepareSpeech applies the whisper rules to text Bridge speaks itself: local
// commands (AirPlay, Home Assistant, playback, music, recovery, dialogue
// commands) and error phrases. A model answer already went through them.
func (e *Engine) PrepareSpeech(ctx context.Context, r *Resolution, question string) {
	if r.Answer != "" && r.speech == "" {
		e.prepareSpeech(ctx, r, question)
	}
}

// userWhispered reports a whispered voice request found by the microphone detector.
func userWhispered(ctx context.Context) bool {
	r, ok := whisperdetect.Detect(ctx)
	return ok && r.Whisper
}

// ttsPlaceholderDirective marks where the speech plays in the directive
// sequence; directives before it run before the speaker talks.
func ttsPlaceholderDirective() *directives.TDirective {
	payload, _ := structpb.NewStruct(map[string]any{"channel": "Dialog"})
	return &directives.TDirective{Type: "client_action", Name: "tts_play_placeholder", AnalyticsType: "tts_play_placeholder", IsLedSilent: true, PayloadType: &directives.TDirective_Payload{Payload: payload}}
}

// whisperVolumeDirective mirrors the cloud's whisper reply, which lowers the
// speaker with sound_set_level {is_whisper: true, new_level: N}.
func whisperVolumeDirective(level int) *directives.TDirective {
	if level <= 0 {
		return nil
	}
	payload, err := structpb.NewStruct(map[string]any{"new_level": level, "is_whisper": true})
	if err != nil {
		return nil
	}
	return &directives.TDirective{Type: "client_action", Name: "sound_set_level", AnalyticsType: "sound_set_level", IsLedSilent: true, PayloadType: &directives.TDirective_Payload{Payload: payload}}
}
