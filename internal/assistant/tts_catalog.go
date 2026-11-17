package assistant

import (
	"encoding/json/v2"
	"errors"
	"strings"
	"unicode"

	"quasarbridge/internal/ttsvoice"
)

type ttsCatalogEntry struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// ttsCatalogItem is one search result: a voice, an emotion, an effect or a sound.
type ttsCatalogItem struct {
	ID       string `json:"id"`
	Name     string `json:"name,omitempty"`
	Kind     string `json:"kind,omitempty"`
	Note     string `json:"note,omitempty"`
	Whisper  *bool  `json:"whisper,omitempty"`
	Emotions *bool  `json:"emotions,omitempty"`
}

func ttsCatalog(kind string) ([]ttsCatalogItem, bool) {
	var items []ttsCatalogItem
	switch kind {
	case "voice":
		for _, v := range ttsvoice.Voices {
			items = append(items, ttsCatalogItem{ID: v.ID, Name: v.Name, Kind: v.Kind, Note: v.Note, Whisper: new(v.Whisper), Emotions: new(v.Emotions)})
		}
	case "emotion", "effect":
		list := ttsvoice.Emotions
		if kind == "effect" {
			list = ttsvoice.Effects
		}
		for _, style := range list {
			items = append(items, ttsCatalogItem{ID: style.ID, Note: style.Note})
		}
	case "audio":
		for _, sound := range ttsvoice.Sounds {
			items = append(items, ttsCatalogItem{ID: sound.ID, Name: sound.Name})
		}
	default:
		return nil, false
	}
	return items, true
}

func searchTTSCatalog(args string) ([]ttsCatalogItem, error) {
	var request struct {
		Kind  string `json:"kind"`
		Query string `json:"query"`
	}
	if err := json.Unmarshal([]byte(args), &request, json.RejectUnknownMembers(true)); err != nil {
		return nil, errors.New("invalid search_tts_catalog arguments")
	}
	entries, ok := ttsCatalog(request.Kind)
	if !ok {
		return nil, errors.New("search_tts_catalog kind must be voice, emotion, effect or audio")
	}
	query := normalizeTTSName(request.Query)
	if query == "" {
		return nil, errors.New("search_tts_catalog requires a name, ID, description or «все»")
	}
	// The full lists of voices, emotions and effects are short; sounds are not.
	if (query == "все" || query == "all") && request.Kind != "audio" {
		return entries, nil
	}
	words := strings.Fields(query)
	id := strings.TrimSpace(request.Query)
	var exact, partial []ttsCatalogItem
	for _, entry := range entries {
		if strings.EqualFold(id, entry.ID) {
			return []ttsCatalogItem{entry}, nil
		}
		name := normalizeTTSName(entry.Name)
		if name != "" && query == name {
			exact = append(exact, entry)
			continue
		}
		text := name + " " + normalizeTTSName(entry.ID) + " " + normalizeTTSName(entry.Kind) + " " + normalizeTTSName(entry.Note)
		matches := true
		for _, word := range words {
			if !strings.Contains(text, word) {
				matches = false
				break
			}
		}
		if matches {
			partial = append(partial, entry)
		}
	}
	result := append(exact, partial...)
	return result[:min(8, len(result))], nil
}

func normalizeTTSName(text string) string {
	text = strings.ReplaceAll(strings.ToLower(text), "ё", "е")
	return strings.Join(strings.FieldsFunc(text, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}), " ")
}
