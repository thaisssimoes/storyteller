package models

import (
	"encoding/json"
	"fmt"
	"strings"
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
	Species         string   `json:"species"`
	Capabilities    []string `json:"capabilities"`
	Limitations     []string `json:"limitations"`
	Role            string   `json:"role"`
	Relationships   []string `json:"relationships"`
	Status          string   `json:"status"`
	FirstAppearance int      `json:"firstAppearance"`
}

// UnmarshalJSON tolerates aliases, relationships, capabilities and limitations
// arriving as a single string (some local models do that) and coerces them to
// a one-element list.
func (c *Character) UnmarshalJSON(data []byte) error {
	type alias Character
	type raw struct {
		alias
		Aliases       json.RawMessage `json:"aliases"`
		Relationships json.RawMessage `json:"relationships"`
		Capabilities  json.RawMessage `json:"capabilities"`
		Limitations   json.RawMessage `json:"limitations"`
	}
	var r raw
	if err := json.Unmarshal(data, &r); err != nil {
		return err
	}
	*c = Character(r.alias)
	c.Aliases = stringList(r.Aliases)
	c.Relationships = stringList(r.Relationships)
	c.Capabilities = stringList(r.Capabilities)
	c.Limitations = stringList(r.Limitations)
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

// UnmarshalJSON tolerates "event" as an alternate key for "description" (common
// model drift where the model uses "event" instead of "description").
func (pe *PlotEvent) UnmarshalJSON(data []byte) error {
	type alias PlotEvent
	type raw struct {
		alias
		Event string `json:"event"`
	}
	var r raw
	if err := json.Unmarshal(data, &r); err != nil {
		return err
	}
	*pe = PlotEvent(r.alias)
	if pe.Description == "" && r.Event != "" {
		pe.Description = r.Event
	}
	return nil
}

// CharacterInteraction records a direct communication event between characters.
// Tracked so that a later claim like "I only heard her voice twice" can be
// checked against the logged record of stone calls, letters, etc.
type CharacterInteraction struct {
	Chapter    int      `json:"chapter"`
	Characters []string `json:"characters"`
	Medium     string   `json:"medium"`
	Summary    string   `json:"summary"`
}

func (ci *CharacterInteraction) UnmarshalJSON(data []byte) error {
	type alias CharacterInteraction
	type raw struct {
		alias
		Characters json.RawMessage `json:"characters"`
		// alternate field names produced by different models
		Character1      string `json:"character1"`
		Character2      string `json:"character2"`
		Participant1    string `json:"participant1"`
		Participant2    string `json:"participant2"`
		Actor1          string `json:"actor1"`
		Actor2          string `json:"actor2"`
		InteractionType string `json:"interactionType"`
		Method          string `json:"method"`
	}
	var r raw
	if err := json.Unmarshal(data, &r); err != nil {
		return err
	}
	*ci = CharacterInteraction(r.alias)
	ci.Characters = stringList(r.Characters)

	// Fall back to paired string fields when the canonical array is absent.
	if len(ci.Characters) == 0 {
		p1 := firstNonEmpty(r.Character1, r.Participant1, r.Actor1)
		p2 := firstNonEmpty(r.Character2, r.Participant2, r.Actor2)
		if p1 != "" {
			ci.Characters = append(ci.Characters, p1)
		}
		if p2 != "" {
			ci.Characters = append(ci.Characters, p2)
		}
	}
	if ci.Medium == "" {
		ci.Medium = r.Method
	}
	if ci.Summary == "" {
		ci.Summary = r.InteractionType
	}
	return nil
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}

// MagicObject records an enchanted item and its visual/behavioural properties
// so that descriptions like "pulses red" vs "pulses gold" can be detected.
type MagicObject struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Properties  []string `json:"properties"`
	FirstSeen   int      `json:"firstSeen"`
}

func (mo *MagicObject) UnmarshalJSON(data []byte) error {
	type alias MagicObject
	type raw struct {
		alias
		Properties json.RawMessage `json:"properties"`
	}
	var r raw
	if err := json.Unmarshal(data, &r); err != nil {
		return err
	}
	*mo = MagicObject(r.alias)
	mo.Properties = stringList(r.Properties)
	return nil
}

// PlotObject is a non-magical item with narrative or symbolic significance that
// recurs throughout the story. Its description, ownership, and meaning must stay
// consistent every time it reappears (e.g. a dress, a pendant, a pin, a letter).
type PlotObject struct {
	Name         string   `json:"name"`
	Description  string   `json:"description"`
	Owner        string   `json:"owner"`
	Significance string   `json:"significance"`
	Properties   []string `json:"properties"`
	FirstSeen    int      `json:"firstSeen"`
}

func (po *PlotObject) UnmarshalJSON(data []byte) error {
	type alias PlotObject
	type raw struct {
		alias
		Properties json.RawMessage `json:"properties"`
	}
	var r raw
	if err := json.Unmarshal(data, &r); err != nil {
		return err
	}
	*po = PlotObject(r.alias)
	po.Properties = stringList(r.Properties)
	return nil
}

// WorldBible is the accumulated knowledge base extracted from the story.
type WorldBible struct {
	Characters            []Character            `json:"characters"`
	Locations             []Location             `json:"locations"`
	WorldMechanics        []string               `json:"worldMechanics"`
	PlotEvents            []PlotEvent            `json:"plotEvents"`
	CharacterInteractions []CharacterInteraction `json:"characterInteractions"`
	MagicObjects          []MagicObject          `json:"magicObjects"`
	PlotObjects           []PlotObject           `json:"plotObjects"`
}

// UnmarshalJSON makes the bible robust to LLM output variations:
//   - characters / locations / plotEvents may come as an ARRAY (spec) OR as an
//     OBJECT keyed by name (common with smaller models).
//   - worldMechanics may come as []string or as a string.
//   - extra unknown fields are silently dropped (default Go behaviour).
func (b *WorldBible) UnmarshalJSON(data []byte) error {
	var raw struct {
		Characters            json.RawMessage `json:"characters"`
		Locations             json.RawMessage `json:"locations"`
		WorldMechanics        json.RawMessage `json:"worldMechanics"`
		PlotEvents            json.RawMessage `json:"plotEvents"`
		CharacterInteractions json.RawMessage `json:"characterInteractions"`
		MagicObjects          json.RawMessage `json:"magicObjects"`
		PlotObjects           json.RawMessage `json:"plotObjects"`
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
	if len(raw.CharacterInteractions) > 0 {
		b.CharacterInteractions = parseCharacterInteractions(raw.CharacterInteractions)
	}
	if len(raw.MagicObjects) > 0 {
		b.MagicObjects = parseMagicObjects(raw.MagicObjects)
	}
	if len(raw.PlotObjects) > 0 {
		b.PlotObjects = parsePlotObjects(raw.PlotObjects)
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

func parseCharacterInteractions(raw json.RawMessage) []CharacterInteraction {
	var arr []CharacterInteraction
	if err := json.Unmarshal(raw, &arr); err == nil {
		return arr
	}
	return nil
}

func parseMagicObjects(raw json.RawMessage) []MagicObject {
	var arr []MagicObject
	if err := json.Unmarshal(raw, &arr); err == nil {
		return arr
	}
	return nil
}

func parsePlotObjects(raw json.RawMessage) []PlotObject {
	var arr []PlotObject
	if err := json.Unmarshal(raw, &arr); err == nil {
		return arr
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

// ─── World Bible merge ────────────────────────────────────────────────────────

// MergeWorldBibles merges updated into base so that every fact established in
// an earlier batch is preserved even when a later model response omits it.
//
// Rules:
//   - Characters and locations are upserted by lowercased name.
//   - WorldMechanics and Limitations are appended (deduped).
//   - PlotEvents are appended (deduped by chapter+description).
//   - CharacterInteractions are appended (all kept).
//   - MagicObjects are upserted by lowercased name.
func MergeWorldBibles(base, updated WorldBible) WorldBible {
	result := base
	result.Characters = mergeCharacters(base.Characters, updated.Characters)
	result.Locations = mergeLocations(base.Locations, updated.Locations)
	result.WorldMechanics = mergeStringSlice(base.WorldMechanics, updated.WorldMechanics)
	result.PlotEvents = mergePlotEvents(base.PlotEvents, updated.PlotEvents)
	result.CharacterInteractions = append(base.CharacterInteractions, updated.CharacterInteractions...)
	result.MagicObjects = mergeMagicObjects(base.MagicObjects, updated.MagicObjects)
	result.PlotObjects = mergePlotObjects(base.PlotObjects, updated.PlotObjects)
	return result
}

func normName(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

// charTitles lists honorifics stripped before character name comparison so that
// "Sir Cassian" and "Cassian" are treated as the same character.
var charTitles = []string{
	"sir ", "lord ", "lady ", "emperor ", "empress ", "king ", "queen ",
	"prince ", "princess ", "duke ", "duchess ", "baron ", "baroness ",
	"count ", "countess ", "alpha ", "luna ", "the ",
}

// charNameKey strips honorific prefixes and lowercases the name so that
// "Sir Cassian" and "Cassian" both produce "cassian".
func charNameKey(name string) string {
	n := normName(name)
	for _, t := range charTitles {
		n = strings.TrimPrefix(n, t)
	}
	return n
}

func mergeCharacters(base, updated []Character) []Character {
	index := make(map[string]int, len(base))
	result := make([]Character, len(base))
	copy(result, base)
	for i, c := range result {
		index[charNameKey(c.Name)] = i
	}
	for _, c := range updated {
		key := charNameKey(c.Name)
		if idx, ok := index[key]; ok {
			result[idx] = mergeCharacter(result[idx], c)
			continue
		}
		// Prefix match: "elara" == "elara frostfang" (same person, more specific name).
		matched := -1
		matchedKey := ""
		for k, idx := range index {
			if strings.HasPrefix(k, key+" ") || strings.HasPrefix(key, k+" ") {
				matched = idx
				matchedKey = k
				break
			}
		}
		if matched >= 0 {
			result[matched] = mergeCharacter(result[matched], c)
			// Keep the longer (more specific) name in the index.
			if len(key) > len(matchedKey) {
				delete(index, matchedKey)
				index[key] = matched
				if len(charNameKey(c.Name)) > len(charNameKey(result[matched].Name)) {
					result[matched].Name = c.Name
				}
			}
		} else {
			index[key] = len(result)
			result = append(result, c)
		}
	}
	return result
}

func mergeCharacter(base, updated Character) Character {
	r := base
	if updated.Description != "" {
		r.Description = updated.Description
	}
	if updated.Species != "" {
		r.Species = updated.Species
	}
	if updated.Role != "" {
		r.Role = updated.Role
	}
	if updated.Status != "" {
		r.Status = updated.Status
	}
	if updated.FirstAppearance != 0 &&
		(r.FirstAppearance == 0 || updated.FirstAppearance < r.FirstAppearance) {
		r.FirstAppearance = updated.FirstAppearance
	}
	r.Aliases = mergeStringSlice(base.Aliases, updated.Aliases)
	r.Capabilities = mergeStringSlice(base.Capabilities, updated.Capabilities)
	r.Limitations = mergeStringSlice(base.Limitations, updated.Limitations)
	r.Relationships = mergeStringSlice(base.Relationships, updated.Relationships)
	return r
}

func mergeLocations(base, updated []Location) []Location {
	index := make(map[string]int, len(base))
	result := make([]Location, len(base))
	copy(result, base)
	for i, l := range result {
		index[normName(l.Name)] = i
	}
	for _, l := range updated {
		key := normName(l.Name)
		if idx, ok := index[key]; ok {
			if l.Description != "" {
				result[idx].Description = l.Description
			}
		} else {
			index[key] = len(result)
			result = append(result, l)
		}
	}
	return result
}

func mergePlotEvents(base, updated []PlotEvent) []PlotEvent {
	seen := make(map[string]bool, len(base))
	eventKey := func(e PlotEvent) string {
		return fmt.Sprintf("%d|%s", e.Chapter, normName(e.Description))
	}
	for _, e := range base {
		seen[eventKey(e)] = true
	}
	result := append([]PlotEvent{}, base...)
	for _, e := range updated {
		if k := eventKey(e); !seen[k] {
			seen[k] = true
			result = append(result, e)
		}
	}
	return result
}

func mergePlotObjects(base, updated []PlotObject) []PlotObject {
	index := make(map[string]int, len(base))
	result := make([]PlotObject, len(base))
	copy(result, base)
	for i, po := range result {
		index[normName(po.Name)] = i
	}
	for _, po := range updated {
		key := normName(po.Name)
		if idx, ok := index[key]; ok {
			if po.Description != "" {
				result[idx].Description = po.Description
			}
			if po.Owner != "" {
				result[idx].Owner = po.Owner
			}
			if po.Significance != "" {
				result[idx].Significance = po.Significance
			}
			if po.FirstSeen != 0 && (result[idx].FirstSeen == 0 || po.FirstSeen < result[idx].FirstSeen) {
				result[idx].FirstSeen = po.FirstSeen
			}
			result[idx].Properties = mergeStringSlice(result[idx].Properties, po.Properties)
		} else {
			index[key] = len(result)
			result = append(result, po)
		}
	}
	return result
}

func mergeMagicObjects(base, updated []MagicObject) []MagicObject {
	index := make(map[string]int, len(base))
	result := make([]MagicObject, len(base))
	copy(result, base)
	for i, mo := range result {
		index[normName(mo.Name)] = i
	}
	for _, mo := range updated {
		key := normName(mo.Name)
		if idx, ok := index[key]; ok {
			if mo.Description != "" {
				result[idx].Description = mo.Description
			}
			if mo.FirstSeen != 0 &&
				(result[idx].FirstSeen == 0 || mo.FirstSeen < result[idx].FirstSeen) {
				result[idx].FirstSeen = mo.FirstSeen
			}
			result[idx].Properties = mergeStringSlice(result[idx].Properties, mo.Properties)
		} else {
			index[key] = len(result)
			result = append(result, mo)
		}
	}
	return result
}

func mergeStringSlice(base, updated []string) []string {
	seen := make(map[string]bool, len(base))
	for _, s := range base {
		seen[normName(s)] = true
	}
	result := append([]string{}, base...)
	for _, s := range updated {
		if s == "" {
			continue
		}
		if k := normName(s); !seen[k] {
			seen[k] = true
			result = append(result, s)
		}
	}
	return result
}
