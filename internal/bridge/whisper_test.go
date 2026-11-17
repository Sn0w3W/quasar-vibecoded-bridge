package bridge

import (
	"context"
	"strings"
	"testing"

	"github.com/coder/websocket"
	common "github.com/skrashevich/quasar-go-genproto/proto/alice/megamind/protos/common"
	request "github.com/skrashevich/quasar-go-genproto/proto/alice/protos/api/alicekit/events/alice_request"
	header "github.com/skrashevich/quasar-go-genproto/proto/alice/protos/api/alicekit/events/common/event_header"
	client "github.com/skrashevich/quasar-go-genproto/proto/alice/protos/api/alicekit/protocol/client"

	"quasarbridge/internal/dialogue"
	"quasarbridge/internal/llm"
	"quasarbridge/internal/ttsvoice"
)

func TestWantsWhisper(t *testing.T) {
	for text, want := range map[string]bool{
		"расскажи сказку шёпотом":                          true,
		"Спроси нейросеть, пожелай спокойной ночи шепотом": true,
		"ответь полушёпотом":                               true,
		"Шёпотом расскажи, что было вчера":                 true,
		"прошепчи мне секрет":                              true,
		"шепни что-нибудь":                                 true,
		"шепчите все":                                      true,
		"не шёпотом, а нормально":                          false,
		"не надо шёпотом":                                  false,
		"только не шепчи":                                  false,
		"хватит шептать":                                   false,
		"почему небо голубое":                              false,
		"шёпотомер сломался":                               false,
	} {
		if got := wantsWhisper(text); got != want {
			t.Errorf("wantsWhisper(%q) = %v, want %v", text, got, want)
		}
	}
}

func TestWhisperSpeechForcesAliceWhisperEverywhere(t *testing.T) {
	got := whisperSpeech(`Привет. <speaker voice="zahar" effect="megaphone" is_whisper="false" emotion="happy">Как дела? <speaker voice='jane' speed="0.9">Пока.`)
	if !strings.HasPrefix(got, `<speaker voice="shitova" is_whisper="true">Привет.`) {
		t.Fatalf("whisper prefix missing: %q", got)
	}
	for _, banned := range []string{"zahar", "jane", `is_whisper="false"`} {
		if strings.Contains(got, banned) {
			t.Fatalf("%q survived forced whisper: %q", banned, got)
		}
	}
	for _, kept := range []string{`emotion="happy"`, `speed="0.9"`, `effect="megaphone"`, "Как дела?", "Пока."} {
		if !strings.Contains(got, kept) {
			t.Fatalf("%q lost in forced whisper: %q", kept, got)
		}
	}
	if tags := ttsvoice.TagPattern.FindAllString(got, -1); len(tags) != 3 {
		t.Fatalf("tags = %q", tags)
	} else {
		for _, tag := range tags {
			if !strings.Contains(tag, `is_whisper="true"`) {
				t.Fatalf("tag %q does not whisper", tag)
			}
		}
	}
}

func TestWhisperedSegmentsUseAliceVoice(t *testing.T) {
	in := `<speaker voice="zahar">Я Захар. <speaker is_whisper="true">Тихо. <speaker voice="jane" is_whisper="true" emotion="happy">Тише. <speaker is_whisper="false" voice="zahar">Громко.`
	want := `<speaker voice="zahar">Я Захар. <speaker is_whisper="true" voice="shitova">Тихо. <speaker voice="shitova" is_whisper="true" emotion="happy">Тише. <speaker is_whisper="false" voice="zahar">Громко.`
	if got := whisperSegmentsWithAliceVoice(in); got != want {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
	unchanged := `<speaker is_whisper="true">П+ауза. <speaker voice='zahar' effect="megaphone">Готово.`
	if got := whisperSegmentsWithAliceVoice(unchanged); got != unchanged {
		t.Fatalf("default-voice whisper changed: %q", got)
	}
}

func TestFallbackPreparesWhisperSpeech(t *testing.T) {
	for _, tc := range []struct {
		name, question, model string
		markup                bool
		speechPrefix          string
		level                 int
	}{
		{"requested", "пожелай спокойной ночи шёпотом", `Спокойной ночи. <speaker voice="zahar">Сладких снов.`, true, `<speaker voice="shitova" is_whisper="true">Спокойной ночи.`, 0},
		{"model whispers all", "что сказать спящему коту", `<speaker is_whisper="true">Тсс, он спит.`, true, `<speaker is_whisper="true">`, 0},
		{"model whispers a part", "расскажи анекдот", `Начало. <speaker is_whisper="true">Шёпот. <speaker is_whisper="false">Конец.`, true, "Начало.", 0},
		{"markup disabled", "пожелай спокойной ночи шёпотом", "Спокойной ночи.", false, "", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			engine := &Engine{TTSMarkup: tc.markup, WhisperSoundLevel: 3, LLM: completionFunc(func(_ context.Context, _ []llm.Message, structured bool) (string, error) {
				if structured {
					return `{"route":"llm"}`, nil
				}
				return tc.model, nil
			})}
			r, err := engine.Fallback(dialogue.WithSpeaker(t.Context(), "whisper-"+tc.name), tc.question)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.HasPrefix(r.speech, tc.speechPrefix) || (tc.speechPrefix == "" && r.speech != "") {
				t.Fatalf("speech = %q, want prefix %q", r.speech, tc.speechPrefix)
			}
			if strings.Contains(r.Answer, "<speaker") {
				t.Fatalf("displayed answer keeps markup: %q", r.Answer)
			}
			if r.whisperLevel != tc.level {
				t.Fatalf("whisper level = %d, want %d", r.whisperLevel, tc.level)
			}
		})
	}
}

func TestNativeWhisperRequestInWordsKeepsVolume(t *testing.T) {
	engine := &Engine{TTSMarkup: true, WhisperSoundLevel: 3, LLM: completionFunc(func(_ context.Context, _ []llm.Message, structured bool) (string, error) {
		if structured {
			return `{"route":"llm"}`, nil
		}
		return "Жил-был тихий ёжик.", nil
	})}
	n, up, down := soundSession(t, engine)
	input := &client.TClientMessage{Message: &client.TClientMessage_Event{Event: &client.TClientMessage_TEvent{Header: &header.TEventHeader{MessageId: "whisper-words"}, Event: &client.TClientMessage_TEvent_TextInput{TextInput: &request.TAliceRequestProto{Request: &request.TAliceRequestProto_TRequest{Event: &common.TEvent{Text: new("расскажи сказку шёпотом")}}}}}}}
	if err := n.Client(t.Context(), websocket.MessageBinary, aa(input)); err != nil {
		t.Fatal(err)
	}
	// Like the cloud's answer to «скажи шёпотом»: whisper markup, no volume change.
	for _, d := range finalSoundDirectives(t, down) {
		if d.GetName() == "sound_set_level" {
			t.Fatal("a whisper requested in words must not change the speaker volume")
		}
	}
	for {
		f := nextSoundFrame(t, up)
		if text := f.Client.GetEvent().GetTtsGenerate().GetText(); text != "" {
			if !strings.HasPrefix(text, `<speaker voice="shitova" is_whisper="true">Жил-был`) {
				t.Fatalf("synthesis text = %q", text)
			}
			break
		}
	}
}
