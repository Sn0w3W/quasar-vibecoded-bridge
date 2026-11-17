package bridge

import (
	"context"
	"encoding/binary"
	"errors"
	"strings"
	"testing"

	"github.com/coder/websocket"
	common "github.com/skrashevich/quasar-go-genproto/proto/alice/megamind/protos/common"
	device "github.com/skrashevich/quasar-go-genproto/proto/alice/protos/api/alicekit/common/device_state"
	request "github.com/skrashevich/quasar-go-genproto/proto/alice/protos/api/alicekit/events/alice_request"
	asr "github.com/skrashevich/quasar-go-genproto/proto/alice/protos/api/alicekit/events/asr_result"
	recognition "github.com/skrashevich/quasar-go-genproto/proto/alice/protos/api/alicekit/events/asr_result/common"
	header "github.com/skrashevich/quasar-go-genproto/proto/alice/protos/api/alicekit/events/common/event_header"
	client "github.com/skrashevich/quasar-go-genproto/proto/alice/protos/api/alicekit/protocol/client"
	server "github.com/skrashevich/quasar-go-genproto/proto/alice/protos/api/alicekit/protocol/server"

	"quasarbridge/internal/llm"
)

type localAirPlay struct {
	active bool
	fail   bool
}

func (a localAirPlay) Control(_ context.Context, action string) (string, error) {
	if a.fail {
		return "", errors.New("receiver failed")
	}
	return map[string]string{"start": "AirPlay включён.", "stop": "AirPlay выключен.", "status": "AirPlay выключен."}[action], nil
}
func (a localAirPlay) Active(context.Context) bool { return a.active }
func (a localAirPlay) ControlMedia(context.Context, string) (string, error) {
	return "Пауза на AirPlay.", nil
}

type localPlayback struct{}

func (localPlayback) Active(context.Context) bool { return true }
func (localPlayback) Control(context.Context, string) (string, error) {
	return "Пауза.", nil
}

type localHome struct{}

func (localHome) Handle(context.Context, string) (string, error) {
	return "Свет включён.", nil
}

type localMusic struct{}

func (localMusic) CanStartMusic(text string) bool {
	return strings.HasPrefix(text, "включи музыку")
}
func (localMusic) StartMusic(context.Context, string) (string, error) {
	return "Включаю музыку.", nil
}

// whisperedRequest sends a voice request with a microphone fixture and the
// recognised text, and returns the text sent to synthesis and the directives.
func whisperedRequest(t *testing.T, engine *Engine, fixture, text string) (string, []string) {
	t.Helper()
	n, up, down := soundSession(t, engine)
	n.DetectWhisper = true
	req := &request.TAliceRequestProto_TRequest{Event: &common.TEvent{Type: common.EEventType_voice_input.Enum()},
		DeviceStateType: &request.TAliceRequestProto_TRequest_DeviceState{DeviceState: &device.TDeviceState{SoundLevel: new(uint32(6))}}}
	voice := aa(&client.TClientMessage{Message: &client.TClientMessage_Event{Event: &client.TClientMessage_TEvent{Header: &header.TEventHeader{MessageId: "local-whisper", StreamId: new(int64(1))}, Event: &client.TClientMessage_TEvent_VoiceInput{VoiceInput: &request.TAliceRequestProto{Format: new("audio/opus"), Request: req}}}}})
	if err := n.Client(t.Context(), websocket.MessageBinary, voice); err != nil {
		t.Fatal(err)
	}
	for _, frame := range fixtureFrames(t, fixture) {
		if err := n.Client(t.Context(), websocket.MessageBinary, append(binary.BigEndian.AppendUint32(nil, 1), frame...)); err != nil {
			t.Fatal(err)
		}
	}
	final := aa(&server.TServerMessage{Message: &server.TServerMessage_Event{Event: &server.TServerMessage_TEvent{Header: &header.TEventHeader{RefMessageId: "local-whisper"}, Event: &server.TServerMessage_TEvent_AsrResult{AsrResult: &asr.TAsrResult{EndOfUtt: new(true), Recognition: []*recognition.TAsrRecognition{{Normalized: new(text)}}}}}}})
	if err := n.Server(t.Context(), websocket.MessageBinary, final); err != nil {
		t.Fatal(err)
	}
	var speech string
	for speech == "" {
		speech = nextSoundFrame(t, up).Client.GetEvent().GetTtsGenerate().GetText()
	}
	var names []string
	for _, d := range finalSoundDirectives(t, down) {
		names = append(names, d.GetName())
	}
	return speech, names
}

// Every command Bridge answers itself, error phrases included, is answered in
// a whisper with the volume lowered when the user whispers, like Alice does.
func TestEveryLocalCommandAnswersAWhisperInWhisper(t *testing.T) {
	llmAnswer := completionFunc(func(_ context.Context, _ []llm.Message, structured bool) (string, error) {
		if structured {
			return `{"route":"llm"}`, nil
		}
		return "Сейчас три часа.", nil
	})
	for _, tc := range []struct {
		name, text, answer string
		engine             func(*Engine)
	}{
		{"airplay start", "включи эйрплей", "AirPlay включён.", func(e *Engine) { e.AirPlay = localAirPlay{} }},
		{"airplay status", "статус эйрплей", "AirPlay выключен.", func(e *Engine) { e.AirPlay = localAirPlay{} }},
		{"airplay media", "пауза", "Пауза на AirPlay.", func(e *Engine) { e.AirPlay = localAirPlay{active: true} }},
		{"airplay error", "выключи эйрплей", "Не удалось изменить состояние AirPlay.", func(e *Engine) { e.AirPlay = localAirPlay{fail: true} }},
		{"playback", "пауза", "Пауза.", func(e *Engine) { e.Playback = localPlayback{} }},
		{"home", "включи свет на кухне", "Свет включён.", func(e *Engine) { e.Home = localHome{} }},
		{"music", "включи музыку битлз", "Включаю музыку.", func(e *Engine) { e.Music = localMusic{} }},
		{"recovery", "аварийный откат квазар бридж", "Откат запланирован.", func(e *Engine) {
			e.Recovery = func(context.Context, string) (string, error) { return "Откат запланирован.", nil }
		}},
		{"dialogue reset", "начни новый разговор", "Начинаем новый разговор.", func(*Engine) {}},
		{"model", "который час", "Сейчас три часа.", func(*Engine) {}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for fixture, whisper := range map[string]bool{"whisper.mic": true, "voiced.mic": false} {
				engine := &Engine{TTSMarkup: true, WhisperSoundLevel: 3, LLM: llmAnswer}
				routing := DefaultRoutingConfig()
				off := false
				routing.DefaultRoute, routing.Classifier.Enabled = "llm", &off
				routing.Rules = []RoutingRule{
					{Name: "home", Route: "home", Match: RoutingMatch{ContainsAny: []string{"свет"}}},
					{Name: "music", Route: "llm", Match: RoutingMatch{StartsWithAny: []string{"включи музыку"}}},
				}
				if err := engine.SetRouting(routing); err != nil {
					t.Fatal(err)
				}
				tc.engine(engine)
				speech, directives := whisperedRequest(t, engine, fixture, tc.text)
				whispered := strings.HasPrefix(speech, `<speaker voice="shitova" is_whisper="true">`)
				if whispered != whisper || !strings.Contains(speech, tc.answer) {
					t.Fatalf("%s: speech = %q, want answer %q whispered %v", fixture, speech, tc.answer, whisper)
				}
				lowered := len(directives) > 0 && directives[0] == "sound_set_level"
				if lowered != whisper {
					t.Fatalf("%s: directives = %v, want volume lowered %v", fixture, directives, whisper)
				}
			}
		})
	}
}
