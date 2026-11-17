// Package ttsvoice is the measured capability map of Alice's cloud speech
// synthesis (UniProxy TtsGenerate) and the markup rules derived from it.
//
// Every entry was verified on a Yandex station: an ignored attribute returns
// audio bit-identical to plain text, an unknown voice fails the whole answer
// with "Unsupported voice", and only Alice's own voice (shitova) whispers or
// reacts to emotion, including under an effect such as pitch_down.
package ttsvoice

import (
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// Alice is the default voice, the only one with whisper and emotions.
const Alice = "shitova"

const (
	MinSpeed = 0.5
	MaxSpeed = 2.0
)

type Voice struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	Note     string `json:"note,omitempty"`
	Whisper  bool   `json:"whisper"`
	Emotions bool   `json:"emotions"`
}

// Voices that returned speech for ru-RU; notes summarise measured pitch and pace.
var Voices = []Voice{
	{ID: Alice, Name: "Алиса", Kind: "женский", Note: "штатный голос; единственный шепчет и выражает эмоции", Whisper: true, Emotions: true},
	{ID: "alyss", Name: "Алисс", Kind: "женский"},
	{ID: "jane", Name: "Джейн", Kind: "женский", Note: "быстрее среднего"},
	{ID: "oksana", Name: "Оксана", Kind: "женский"},
	{ID: "good_oksana", Name: "Оксана добрая", Kind: "женский", Note: "мягкая подача"},
	{ID: "omazh", Name: "Омаж", Kind: "женский", Note: "медленная размеренная речь"},
	{ID: "zahar", Name: "Захар", Kind: "мужской"},
	{ID: "kostya", Name: "Костя", Kind: "мужской", Note: "быстрая речь"},
	{ID: "valtz", Name: "Вальц", Kind: "мужской"},
	{ID: "ermil", Name: "Ермил", Kind: "мужской", Note: "низкий"},
	{ID: "smoky", Name: "Смоки", Kind: "мужской", Note: "очень низкий"},
	{ID: "dude", Name: "Чувак", Kind: "мужской", Note: "очень низкий"},
	{ID: "krosh", Name: "Крош", Kind: "мультяшный", Note: "высокий, для детей и персонажей"},
	{ID: "robot", Name: "Робот", Kind: "синтетический"},
}

type Style struct {
	ID   string `json:"id"`
	Note string `json:"note"`
}

// Emotions that stood out from random values on two different texts.
var Emotions = []Style{
	{ID: "happy", Note: "радость, бодрая вежливость"},
	{ID: "energetic", Note: "азарт, волнение, паника"},
	{ID: "negative", Note: "раздражение, злость"},
	{ID: "neutral", Note: "спокойно; сбрасывает эмоцию"},
}

// Effects that changed the sound; effect="-" removes the current one.
var Effects = []Style{
	{ID: "behind_the_wall", Note: "голос из-за стены"},
	{ID: "hamster", Note: "высокий ускоренный, как у хомяка"},
	{ID: "megaphone", Note: "мегафон"},
	{ID: "pitch_down", Note: "ниже тон; второй персонаж голосом Алисы с эмоциями"},
	{ID: "psychodelic", Note: "психоделический"},
	{ID: "pulse", Note: "пульсирующий"},
	{ID: "train_announce", Note: "вокзальное объявление"},
}

func KnownVoice(id string) bool {
	return slices.ContainsFunc(Voices, func(v Voice) bool { return v.ID == id })
}

func KnownSound(id string) bool {
	return slices.ContainsFunc(Sounds, func(s Sound) bool { return s.ID == id })
}

func knownStyle(list []Style, id string) bool {
	return slices.ContainsFunc(list, func(s Style) bool { return s.ID == id })
}

// TagPattern matches one <speaker ...> tag. Tags are never closed.
var TagPattern = regexp.MustCompile(`<speaker(\s[^<>]*?)?\s*/?>`)

var (
	attrPattern = regexp.MustCompile(`([A-Za-z_][A-Za-z0-9_\-]*)\s*=\s*("[^"]*"|'[^']*')`)
	// SSML and closing tags are not markup for this synthesizer: it reads them aloud.
	foreignTag  = regexp.MustCompile(`(?i)</?(?:speak|prosody|emphasis|break|say-as|sub|voice|p|s|lang|phoneme|audio|mark)\b[^<>]*>|</speaker\s*>`)
	langPattern = regexp.MustCompile(`^[a-z]{2}-[A-Z]{2}$`)
)

type Attr struct{ Name, Raw string }

// Value is the attribute value without its quotes.
func (a Attr) Value() string { return a.Raw[1 : len(a.Raw)-1] }

func ParseTag(tag string) []Attr {
	var attrs []Attr
	for _, m := range attrPattern.FindAllStringSubmatch(tag, -1) {
		attrs = append(attrs, Attr{Name: strings.ToLower(m[1]), Raw: m[2]})
	}
	return attrs
}

func FormatTag(attrs []Attr) string {
	var b strings.Builder
	b.WriteString("<speaker")
	for _, a := range attrs {
		b.WriteString(" " + a.Name + "=" + a.Raw)
	}
	b.WriteString(">")
	return b.String()
}

// SetAttr replaces an attribute in place or appends it.
func SetAttr(attrs []Attr, name, value string) []Attr {
	for i := range attrs {
		if attrs[i].Name == name {
			attrs[i].Raw = `"` + value + `"`
			return attrs
		}
	}
	return append(attrs, Attr{Name: name, Raw: `"` + value + `"`})
}

func LookupAttr(attrs []Attr, name string) (string, bool) {
	for _, a := range attrs {
		if a.Name == name {
			return a.Value(), true
		}
	}
	return "", false
}

// Sanitize keeps only controls the synthesizer honours. Unknown voices and
// sounds are removed because one would silence the whole answer; ignored attributes,
// noise-only emotions and SSML are removed; speed is clamped. Valid tags are
// returned byte for byte.
func Sanitize(speech string) string {
	speech = foreignTag.ReplaceAllString(speech, "")
	voice := Alice
	return TagPattern.ReplaceAllStringFunc(speech, func(tag string) string {
		attrs := ParseTag(tag)
		var kept []Attr
		changed := false
		for _, a := range attrs {
			v := a.Value()
			ok := false
			switch a.Name {
			case "voice":
				if ok = KnownVoice(v); ok {
					voice = v
				}
			case "emotion":
				ok = knownStyle(Emotions, v)
			case "effect":
				ok = v == "-" || knownStyle(Effects, v)
			case "is_whisper":
				ok = v == "true" || v == "false"
			case "lang":
				ok = langPattern.MatchString(v)
			case "audio":
				ok = KnownSound(v)
			case "speed":
				if f, err := strconv.ParseFloat(v, 64); err == nil {
					clamped := min(max(f, MinSpeed), MaxSpeed)
					if clamped != f {
						a.Raw = `"` + strconv.FormatFloat(clamped, 'g', -1, 64) + `"`
						changed = true
					}
					ok = true
				}
			}
			if ok {
				kept = append(kept, a)
			} else {
				changed = true
			}
		}
		if voice != Alice {
			before := len(kept)
			kept = slices.DeleteFunc(kept, func(a Attr) bool { return a.Name == "emotion" })
			changed = changed || len(kept) != before
		}
		switch {
		case len(kept) == 0:
			return ""
		case !changed:
			return tag
		default:
			return FormatTag(kept)
		}
	})
}
