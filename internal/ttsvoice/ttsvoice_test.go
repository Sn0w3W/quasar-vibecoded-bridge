package ttsvoice

import "testing"

func TestSanitizeKeepsOnlyMeasuredControls(t *testing.T) {
	for in, want := range map[string]string{
		// Valid markup passes through byte for byte.
		`<speaker voice='zahar' effect="megaphone" speed="1.2">Готово. <speaker effect="-" audio="alice-sounds-game-win-1.opus" lang="ru-RU">Дальше.`: `<speaker voice='zahar' effect="megaphone" speed="1.2">Готово. <speaker effect="-" audio="alice-sounds-game-win-1.opus" lang="ru-RU">Дальше.`,
		// An unknown voice would make the cloud answer "Unsupported voice" and stay silent.
		`<speaker voice="alena">Привет.`: `Привет.`,
		// Ignored attributes and values that only add noise are dropped.
		`<speaker pitch="high" volume="0.2" emotion="sad">Грустно.`: `Грустно.`,
		`<speaker emotion="negative" pitch="low">Злюсь.`:            `<speaker emotion="negative">Злюсь.`,
		// Emotions apply to Alice's voice only, including with an effect.
		`<speaker voice="zahar" emotion="happy">Ура.`:                         `<speaker voice="zahar">Ура.`,
		`<speaker voice="shitova" effect="pitch_down" emotion="negative">Ну.`: `<speaker voice="shitova" effect="pitch_down" emotion="negative">Ну.`,
		`<speaker voice="zahar">А. <speaker emotion="happy">Б.`:               `<speaker voice="zahar">А. Б.`,
		// Speed is clamped to the measured range; garbage is dropped.
		`<speaker speed="5">Быстро. <speaker speed="0.1">Медленно. <speaker speed="fast">Так.`: `<speaker speed="2">Быстро. <speaker speed="0.5">Медленно. Так.`,
		// SSML is read aloud by the synthesizer, so its tags are removed.
		`<speak><prosody rate="slow">Тише.</prosody></speak>`: `Тише.`,
		`<speaker is_whisper="true">Тсс.</speaker>`:           `<speaker is_whisper="true">Тсс.`,
		`<speaker is_whisper="maybe" lang="english">Да.`:      `Да.`,
		// An invented sound ID returns no audio at all for the answer.
		`<speaker audio="alice-sounds-transport-train-1.opus">Поезд.`: `Поезд.`,
		`Пауза sil <[500]> и зам+ок <[z a m o k]>.`:                   `Пауза sil <[500]> и зам+ок <[z a m o k]>.`,
	} {
		if got := Sanitize(in); got != want {
			t.Errorf("Sanitize(%q)\n got %q\nwant %q", in, got, want)
		}
	}
}

func TestVoiceCatalogIsConsistent(t *testing.T) {
	seen := map[string]bool{}
	for _, v := range Voices {
		if v.ID == "" || v.Name == "" || v.Kind == "" || seen[v.ID] {
			t.Fatalf("bad voice entry %+v", v)
		}
		seen[v.ID] = true
		if (v.ID == Alice) != v.Emotions || (v.ID == Alice) != v.Whisper {
			t.Fatalf("only Alice's voice whispers and has emotions: %+v", v)
		}
	}
	if !seen[Alice] || len(Emotions) != 4 || len(Effects) != 7 {
		t.Fatalf("catalog: %d voices, %d emotions, %d effects", len(Voices), len(Emotions), len(Effects))
	}
}
