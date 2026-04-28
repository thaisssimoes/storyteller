package models

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

// Report is the full output of the reader pass.
type Report struct {
	WorldBible      WorldBible      `json:"worldBible"`
	Inconsistencies []Inconsistency `json:"inconsistencies"`
}
