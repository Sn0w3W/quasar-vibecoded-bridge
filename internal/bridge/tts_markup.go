package bridge

import (
	"regexp"
	"strings"

	"quasarbridge/internal/ttsvoice"
)

var ttsMarkupPrompt = buildTTSPrompt()

var ttsMarkup = regexp.MustCompile(
	`</?speaker(?:\s+[^<>]*)?/?>|` +
		`\bsil\s*<\[\s*[0-9]+\s*\]>|\s*<\[\s*[a-z]+(?:\s+[a-z]+)*\s*\]>|\+([АЕЁИОУЫЭЮЯаеёиоуыэюя])`)

func (e *Engine) ttsInstructions() string {
	if e.TTSMarkup {
		return ttsMarkupPrompt
	}
	return ""
}

func (r *Resolution) setModelAnswer(text string, useMarkup bool) {
	plain := ttsMarkup.ReplaceAllString(text, "$1")
	r.Answer = plain
	if plain != text {
		r.Answer = strings.Join(strings.Fields(plain), " ")
		if useMarkup {
			r.speech = text
		}
	}
}

// buildTTSPrompt describes only measured controls, from the shared catalog.
func buildTTSPrompt() string {
	var voices, emotions, effects []string
	for _, v := range ttsvoice.Voices {
		item := v.ID + " (" + v.Name + ", " + v.Kind
		if v.Note != "" {
			item += ", " + v.Note
		}
		voices = append(voices, item+")")
	}
	for _, e := range ttsvoice.Emotions {
		emotions = append(emotions, e.ID+" — "+e.Note)
	}
	for _, e := range ttsvoice.Effects {
		effects = append(effects, e.ID+" — "+e.Note)
	}
	return `
Финальный ответ озвучивает синтезатор Алисы. По умолчанию пиши обычный текст без разметки: так отвечает сама Алиса. Разметку добавляй по просьбе или когда её требует жанр: сказка, сценка, анекдот по ролям, объявление, поздравление.
Теги <speaker ...> не закрываются и действуют до следующего тега; атрибуты сохраняются, поэтому в теге каждой реплики явно задавай голос, эмоцию и темп. Неподдерживаемую разметку Bridge удаляет.
Шёпот: <speaker is_whisper="true">, обычная речь: <speaker is_whisper="false">. Шептать умеет только штатный голос Алисы. Просьбу ответить шёпотом и шёпот самого пользователя Bridge обработает сам: отвечай спокойно и коротко.
Эмоции (emotion) есть только у голоса Алисы: ` + strings.Join(emotions, "; ") + `. Других эмоций нет: грусть, нежность и усталость передавай медленным темпом, паузами и многоточием.
Темп: speed от 0.5 до 2, где 1 обычный, например <speaker speed="0.85">.
Голоса (voice): ` + strings.Join(voices, "; ") + `. Голоса кроме Алисы не шепчут и не выражают эмоций, только меняют темп.
Подбор голосов: рассказчик — штатный голос; мужской персонаж — zahar, ermil, kostya или valtz; женский — jane, oksana, alyss или omazh; ребёнок, зверёк, мультяшный герой — krosh; робот или компьютер — robot. Персонажу, которому нужны эмоции, дай голос Алисы с эффектом: <speaker voice="shitova" effect="pitch_down" emotion="negative">.
Эффекты (effect): ` + strings.Join(effects, "; ") + `; снять эффект: effect="-". Например <speaker effect="megaphone">; голос и эффект можно задать в одном теге.
Пауза: sil <[500]> в миллисекундах. Ударение: + перед ударной гласной (зам+ок). Фонемы: <[...]> после слова. Язык: <speaker lang="en-US">.
Звук из библиотеки Алисы: <speaker audio="alice-sounds-game-win-1.opus">. Если доступен search_tts_catalog, там описаны голоса, эмоции, эффекты и звуки: ищи по названию, описанию или «все».
Не используй SSML (<speak>, <prosody>) и атрибуты, которых здесь нет. Пиши разметку прямо в тексте для синтеза, не описывай её словами.
`
}
