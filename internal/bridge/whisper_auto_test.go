package bridge

import (
	"context"
	"encoding/binary"
	"os"
	"path/filepath"
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

func fixtureFrames(t *testing.T, name string) [][]byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "whisperdetect", "testdata", name))
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

func TestNativeAnswersInWhisperWhenTheUserWhispers(t *testing.T) {
	for _, tc := range []struct {
		name    string
		fixture string
		detect  bool
		level   uint32 // speaker volume reported with the request; 0 = not reported
		whisper bool
		lowered bool
	}{
		{"whisper", "whisper.mic", true, 6, true, true},
		{"whisper-level-unknown", "whisper.mic", true, 0, true, true},
		{"whisper-already-quiet", "whisper.mic", true, 3, true, false},
		{"voiced", "voiced.mic", true, 6, false, false},
		{"detection-off", "whisper.mic", false, 6, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			engine := &Engine{TTSMarkup: true, WhisperSoundLevel: 3, LLM: completionFunc(func(_ context.Context, _ []llm.Message, structured bool) (string, error) {
				if structured {
					return `{"route":"llm"}`, nil
				}
				return "Жил-был ёжик.", nil
			})}
			n, up, down := soundSession(t, engine)
			n.DetectWhisper = tc.detect
			req := &request.TAliceRequestProto_TRequest{Event: &common.TEvent{Type: common.EEventType_voice_input.Enum()}}
			if tc.level > 0 {
				req.DeviceStateType = &request.TAliceRequestProto_TRequest_DeviceState{DeviceState: &device.TDeviceState{SoundLevel: new(tc.level)}}
			}
			voice := aa(&client.TClientMessage{Message: &client.TClientMessage_Event{Event: &client.TClientMessage_TEvent{Header: &header.TEventHeader{MessageId: "auto-whisper", StreamId: new(int64(1))}, Event: &client.TClientMessage_TEvent_VoiceInput{VoiceInput: &request.TAliceRequestProto{Format: new("audio/opus"), Request: req}}}}})
			if err := n.Client(t.Context(), websocket.MessageBinary, voice); err != nil {
				t.Fatal(err)
			}
			for _, frame := range fixtureFrames(t, tc.fixture) {
				if err := n.Client(t.Context(), websocket.MessageBinary, append(binary.BigEndian.AppendUint32(nil, 1), frame...)); err != nil {
					t.Fatal(err)
				}
			}
			final := aa(&server.TServerMessage{Message: &server.TServerMessage_Event{Event: &server.TServerMessage_TEvent{Header: &header.TEventHeader{RefMessageId: "auto-whisper"}, Event: &server.TServerMessage_TEvent_AsrResult{AsrResult: &asr.TAsrResult{EndOfUtt: new(true), Recognition: []*recognition.TAsrRecognition{{Normalized: new("расскажи сказку")}}}}}}})
			if err := n.Server(t.Context(), websocket.MessageBinary, final); err != nil {
				t.Fatal(err)
			}
			var text string
			for text == "" {
				text = nextSoundFrame(t, up).Client.GetEvent().GetTtsGenerate().GetText()
			}
			if got := strings.HasPrefix(text, `<speaker voice="shitova" is_whisper="true">`); got != tc.whisper {
				t.Fatalf("synthesis text = %q, whisper want %v", text, tc.whisper)
			}
			sent := finalSoundDirectives(t, down)
			lowered := len(sent) >= 2 && sent[0].GetName() == "sound_set_level" && sent[1].GetName() == "tts_play_placeholder"
			if lowered != tc.lowered {
				t.Fatalf("directives = %v, want volume lowered before speech = %v", sent, tc.lowered)
			}
			if lowered {
				fields := sent[0].GetPayload().GetFields()
				if !fields["is_whisper"].GetBoolValue() || fields["new_level"].GetNumberValue() != 3 || !sent[0].GetIsLedSilent() || !sent[1].GetIsLedSilent() {
					t.Fatalf("directives differ from the cloud's whisper reply: %v", sent)
				}
			}
		})
	}
}
