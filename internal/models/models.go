package models

import (
	"encoding/json"
	"fmt"
)

// Chapter represents a single story chapter.
type Chapter struct {
	Number  int
	Title   string
	Content string
	Path    string
}

// Character holds everything we know about a character from the story.
type Character struct {
	Name            string   `json:"name"`
	Aliases         []string `json:"aliases"`
	Description     string   `json:"description"`
	Role            string   `json:"role"`
	Relationships   []string `json:"relationships"`
	Status          string   `json:"status"`
	FirstAppearance int      `json:"firstAppearance"`
}

// UnmarshalJSON tolerates aliases and relationships arriving as a single string
// (some local models do that) and silently coerces them to a one-element list.
func (c *Character) UnmarshalJSON(data []byte) error {
	type alias Character
	type raw struct {
		alias
		Aliases       json.RawMessage `json:"aliases"`
		Relationships json.RawMessage `json:"relationships"`
	}
	var r raw
	if err := json.Unmarshal(data, &r); err != nil {
		return err
	}
	*c = Character(r.alias)
	c.Aliases = stringList(r.Aliases)
	c.Relationships = stringList(r.Relationships)
	return nil
}

// Location is a place mentioned in the story.
type Location struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// PlotEvent is a significant story event tied to a chapter.
type PlotEvent struct {
	Chapter     int    `json:"chapter"`
	Description string `json:"description"`
}

// WorldBible is the accumulated knowledge base extracted from the story.
type WorldBible struct {
	Characters     []Character `json:"characters"`
	Locations      []Location  `json:"locations"`
	WorldMechanics []string    `json:"worldMechanics"`
	PlotEvents     []PlotEvent `json:"plotEvents"`
}

// UnmarshalJSON makes the bible robust to LLM output variations:
//   - characters / locations / plotEvents may come as an ARRAY (spec) OR as an
//     OBJECT keyed by name (common with smaller models).
//   - worldMechanics may come as []string or as a string.
//   - extra unknown fields are silently dropped (default Go behaviour).
func (b *WorldBible) UnmarshalJSON(data []byte) error {
	var raw struct {
		Characters     json.RawMessage `json:"characters"`
		Locations      json.RawMessage `json:"locations"`
		WorldMechanics json.RawMessage `json:"worldMechanics"`
		PlotEvents     json.RawMessage `json:"plotEvents"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("worldBible: %w", err)
	}

	if len(raw.Characters) > 0 {
		b.Characters = parseCharacters(raw.Characters)
	}
	if len(raw.Locations) > 0 {
		b.Locations = parseLocations(raw.Locations)
	}
	if len(raw.WorldMechanics) > 0 {
		b.WorldMechanics = stringList(raw.WorldMechanics)
	}
	if len(raw.PlotEvents) > 0 {
		b.PlotEvents = parsePlotEvents(raw.PlotEvents)
	}
	return nil
}

// parseCharacters accepts either []Character (spec) or map[string]Character
// (where the key is the character name). When an object is used, the map key
// fills in missing Name fields.
func parseCharacters(raw json.RawMessage) []Character {
	var arr []Character
	if err := json.Unmarshal(raw, &arr); err == nil {
		return arr
	}

	var obj map[string]Character
	if err := json.Unmarshal(raw, &obj); err == nil {
		out := make([]Character, 0, len(obj))
		for k, v := range obj {
			if v.Name == "" {
				v.Name = k
			}
			out = append(out, v)
		}
		return out
	}
	return nil
}

func parseLocations(raw json.RawMessage) []Location {
	var arr []Location
	if err := json.Unmarshal(raw, &arr); err == nil {
		return arr
	}

	// object map: {"Capital": {"description": "..."}} or {"Capital": "description"}
	var objStruct map[string]Location
	if err := json.Unmarshal(raw, &objStruct); err == nil {
		out := make([]Location, 0, len(objStruct))
		for k, v := range objStruct {
			if v.Name == "" {
				v.Name = k
			}
			out = append(out, v)
		}
		return out
	}

	var objStr map[string]string
	if err := json.Unmarshal(raw, &objStr); err == nil {
		out := make([]Location, 0, len(objStr))
		for k, v := range objStr {
			out = append(out, Location{Name: k, Description: v})
		}
		return out
	}
	return nil
}

func parsePlotEvents(raw json.RawMessage) []PlotEvent {
	var arr []PlotEvent
	if err := json.Unmarshal(raw, &arr); err == nil {
		return arr
	}

	// object form: {"1": "first event", "2": "second event"} OR
	//              {"chapter1": {"chapter": 1, "description": "..."}}
	var objStruct map[string]PlotEvent
	if err := json.Unmarshal(raw, &objStruct); err == nil {
		out := make([]PlotEvent, 0, len(objStruct))
		for _, v := range objStruct {
			out = append(out, v)
		}
		return out
	}

	var objStr map[string]string
	if err := json.Unmarshal(raw, &objStr); err == nil {
		out := make([]PlotEvent, 0, len(objStr))
		for _, v := range objStr {
			out = append(out, PlotEvent{Description: v})
		}
		return out
	}
	return nil
}

// stringList accepts []string OR a single string OR an object whose values are
// concatenated. Returns nil for null/empty.
func stringList(raw json.RawMessage) []string {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var arr []string
	if err := json.Unmarshal(raw, &arr); err == nil {
		return arr
	}
	var single string
	if err := json.Unmarshal(raw, &single); err == nil && single != "" {
		return []string{single}
	}
	var obj map[string]string
	if err := json.Unmarshal(raw, &obj); err == nil {
		out := make([]string, 0, len(obj))
		for _, v := range obj {
			if v != "" {
				out = append(out, v)
			}
		}
		return out
	}
	return nil
}

// Inconsistency describes a single detected problem in the story.
type Inconsistency struct {
	ID               int      `json:"id"`
	Chapter          int      `json:"chapter"`
	Type             string   `json:"type"`
	Severity         string   `json:"severity"`
	Description      string   `json:"description"`
	ConflictingFacts []string `json:"conflictingFacts"`
	OriginalText     string   `json:"originalText"`
	SuggestedFix     string   `json:"suggestedFix"`
}

// UnmarshalJSON tolerates conflictingFacts as a single string.
func (i *Inconsistency) UnmarshalJSON(data []byte) error {
	type alias Inconsistency
	type raw struct {
		alias
		ConflictingFacts json.RawMessage `json:"conflictingFacts"`
	}
	var r raw
	if err := json.Unmarshal(data, &r); err != nil {
		return err
	}
	*i = Inconsistency(r.alias)
	i.ConflictingFacts = stringList(r.ConflictingFacts)
	return nil
}

// Report is the full output of the reader pass.
type Report struct {
	WorldBible      WorldBible      `json:"worldBible"`
	Inconsistencies []Inconsistency `json:"inconsistencies"`
}
