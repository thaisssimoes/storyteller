package reader

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"inconsistencyfixer/internal/claude"
	"inconsistencyfixer/internal/models"
	"inconsistencyfixer/internal/story"
)

const (
	macroMaxTokens = 12288
	sceneMaxTokens = 6144
	pairMaxTokens  = 4096

	macroCallTimeout = 25 * time.Minute
	sceneCallTimeout = 15 * time.Minute
	pairCallTimeout  = 10 * time.Minute
)

// Run analyses all chapters and writes an inconsistency report to outputDir.
//
// Three passes:
//  1. Macro (Robust model): batched chapters → World Bible + cross-chapter
//     inconsistencies. The Bible is the foundation for the next two passes,
//     so quality matters here.
//  2. Scene (Fast model): each chapter alone, structured JSON output, lots of
//     calls — the Fast model handles this volume well.
//  3. Continuity (Fast model): every adjacent (n, n+1) pair — only when chapter
//     n+1 reads as a direct continuation of chapter n.
func Run(outputDir string, pair *claude.Pair) error {
	return RunDir(outputDir, "chapters", pair)
}

// RunDir lets the caller pick the chapters subdirectory (used by writer).
func RunDir(outputDir, chaptersSubdir string, pair *claude.Pair) error {
	chaptersDir := filepath.Join(outputDir, chaptersSubdir)
	reportPath := filepath.Join(outputDir, "inconsistencies.json")
	humanPath := filepath.Join(outputDir, "inconsistencies_report.txt")

	chapters, err := story.LoadChapters(chaptersDir)
	if err != nil {
		return fmt.Errorf("loading chapters: %w", err)
	}
	if len(chapters) == 0 {
		return fmt.Errorf("no chapters found in %s — run 'scrape' first", chaptersDir)
	}

	log.Printf("Reader: %d chapters | provider: %s", len(chapters), pair.Provider())
	log.Printf("  Robust model (macro): %s", pair.Robust.Model())
	log.Printf("  Fast model (scene/continuity): %s", pair.Fast.Model())
	log.Println("Three passes — macro, scene, continuity. Quality > speed.")

	// --- Pass 1: macro (Robust) ---
	log.Println("[1/3] Macro pass — building World Bible (Robust model)")
	bible, macroInc, macroErr := runMacroPass(pair.Robust, chapters)
	if macroErr != nil {
		return fmt.Errorf("macro pass failed: %w", macroErr)
	}
	log.Printf("  Macro: %d characters, %d locations, %d plot events, %d inconsistencies",
		len(bible.Characters), len(bible.Locations), len(bible.PlotEvents), len(macroInc))

	// --- Pass 2: scene-level per-chapter scan (Fast) ---
	log.Println("[2/3] Scene pass — per-chapter fine-grained scan (Fast model)")
	sceneInc := runScenePass(pair.Fast, bible, chapters)
	log.Printf("  Scene: %d inconsistencies", len(sceneInc))

	// --- Pass 3: cross-chapter continuity (Fast) ---
	log.Println("[3/3] Continuity pass — adjacent chapters (Fast model)")
	pairInc := runContinuityPass(pair.Fast, bible, chapters)
	log.Printf("  Continuity: %d inconsistencies", len(pairInc))

	// Merge + dedupe
	all := mergeInconsistencies(macroInc, sceneInc, pairInc)
	for i := range all {
		all[i].ID = i + 1
	}

	report := models.Report{WorldBible: bible, Inconsistencies: all}

	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling report: %w", err)
	}
	if err := os.WriteFile(reportPath, data, 0644); err != nil {
		return fmt.Errorf("writing JSON report: %w", err)
	}
	if err := os.WriteFile(humanPath, []byte(buildTextReport(report)), 0644); err != nil {
		log.Printf("Warning: could not write text report: %v", err)
	}

	log.Printf("Analysis complete — %d inconsistencies found across all passes", len(all))
	log.Printf("  JSON:  %s", reportPath)
	log.Printf("  Text:  %s", humanPath)
	return nil
}

// ─── Pass 1: macro ────────────────────────────────────────────────────────────

func runMacroPass(client *claude.Client, chapters []models.Chapter) (models.WorldBible, []models.Inconsistency, error) {
	batchSize := client.BatchSize()
	var bible models.WorldBible
	var allInc []models.Inconsistency
	var batchErrs []error
	totalBatches := (len(chapters) + batchSize - 1) / batchSize

	for i := 0; i < len(chapters); i += batchSize {
		end := i + batchSize
		if end > len(chapters) {
			end = len(chapters)
		}
		batch := chapters[i:end]
		log.Printf("  [macro] batch %d/%d (chapters %d–%d)",
			i/batchSize+1, totalBatches, batch[0].Number, batch[len(batch)-1].Number)

		var (
			updatedBible models.WorldBible
			newInc       []models.Inconsistency
			callErr      error
		)
		if i == 0 {
			updatedBible, newInc, callErr = analyseFirstBatch(client, batch)
		} else {
			updatedBible, newInc, callErr = analyseNextBatch(client, bible, batch)
		}

		if callErr != nil {
			log.Printf("    ! batch failed: %v", callErr)
			batchErrs = append(batchErrs, fmt.Errorf("chapters %d-%d: %w", batch[0].Number, batch[len(batch)-1].Number, callErr))
			continue
		}

		bible = updatedBible
		allInc = append(allInc, newInc...)
		log.Printf("    ✓ %d inconsistencies in this batch (running total: %d)", len(newInc), len(allInc))
	}

	// Fail loud if half the batches died — silent zeros are misleading.
	if len(batchErrs) > 0 && len(batchErrs) > totalBatches/2 {
		return bible, allInc, fmt.Errorf("%d/%d macro batches failed: %w",
			len(batchErrs), totalBatches, errors.Join(batchErrs...))
	}
	if len(batchErrs) > 0 {
		log.Printf("  ! %d/%d macro batches failed (continuing with partial bible)",
			len(batchErrs), totalBatches)
	}
	return bible, allInc, nil
}

func analyseFirstBatch(client *claude.Client, chapters []models.Chapter) (models.WorldBible, []models.Inconsistency, error) {
	prompt := fmt.Sprintf(`%s

CHAPTERS:
%s`, macroPromptFirst, renderChapters(chapters))

	resp, err := callJSON(client, "", []claude.Message{
		claude.UserMessage(claude.TextBlock(prompt)),
	}, macroMaxTokens, macroCallTimeout)
	if err != nil {
		return models.WorldBible{}, nil, err
	}
	return parseResponse(resp)
}

func analyseNextBatch(client *claude.Client, bible models.WorldBible, chapters []models.Chapter) (models.WorldBible, []models.Inconsistency, error) {
	bibleJSON, _ := json.MarshalIndent(bible, "", "  ")
	prompt := fmt.Sprintf(`%s

NEW CHAPTERS (starting at chapter %d):
%s`, fmt.Sprintf(macroPromptContinue, chapters[0].Number, chapters[0].Number), chapters[0].Number, renderChapters(chapters))

	resp, err := callJSON(client, "", []claude.Message{
		claude.UserMessage(
			claude.CachedTextBlock(fmt.Sprintf("WORLD BIBLE (established facts so far):\n%s", string(bibleJSON))),
			claude.TextBlock(prompt),
		),
	}, macroMaxTokens, macroCallTimeout)
	if err != nil {
		return bible, nil, err
	}
	return parseResponse(resp)
}

// ─── Pass 2: scene-level per-chapter scan ────────────────────────────────────

func runScenePass(client *claude.Client, bible models.WorldBible, chapters []models.Chapter) []models.Inconsistency {
	bibleJSON, _ := json.MarshalIndent(bible, "", "  ")
	cachedBible := claude.CachedTextBlock(fmt.Sprintf("WORLD BIBLE (source of truth):\n%s", string(bibleJSON)))

	var all []models.Inconsistency
	for idx, ch := range chapters {
		log.Printf("  [scene] chapter %d (%d/%d)", ch.Number, idx+1, len(chapters))

		prompt := fmt.Sprintf(`%s

CHAPTER %d — "%s":
%s`, scenePrompt, ch.Number, ch.Title, ch.Content)

		resp, err := callJSON(client, "", []claude.Message{
			claude.UserMessage(cachedBible, claude.TextBlock(prompt)),
		}, sceneMaxTokens, sceneCallTimeout)
		if err != nil {
			log.Printf("    ! scene scan failed for ch %d: %v", ch.Number, err)
			continue
		}
		_, inc, perr := parseResponse(resp)
		if perr != nil {
			log.Printf("    ! parse error for ch %d: %v", ch.Number, perr)
			continue
		}
		// Force the chapter number on each result (model sometimes drifts)
		for i := range inc {
			if inc[i].Chapter == 0 {
				inc[i].Chapter = ch.Number
			}
		}
		log.Printf("    ✓ %d scene-level issues", len(inc))
		all = append(all, inc...)
	}
	return all
}

// ─── Pass 3: cross-chapter continuity (adjacent pairs) ───────────────────────

func runContinuityPass(client *claude.Client, bible models.WorldBible, chapters []models.Chapter) []models.Inconsistency {
	if len(chapters) < 2 {
		return nil
	}

	bibleJSON, _ := json.MarshalIndent(bible, "", "  ")
	cachedBible := claude.CachedTextBlock(fmt.Sprintf("WORLD BIBLE (source of truth):\n%s", string(bibleJSON)))

	var all []models.Inconsistency
	for i := 0; i < len(chapters)-1; i++ {
		a, b := chapters[i], chapters[i+1]
		log.Printf("  [continuity] chapters %d → %d", a.Number, b.Number)

		// Send only the tail of A and the head of B — saves tokens, keeps focus.
		aTail := tail(a.Content, 1500)
		bHead := head(b.Content, 1500)

		prompt := fmt.Sprintf(`%s

CHAPTER %d ENDING — "%s":
... %s

CHAPTER %d OPENING — "%s":
%s ...`, continuityPrompt, a.Number, a.Title, aTail, b.Number, b.Title, bHead)

		resp, err := callJSON(client, "", []claude.Message{
			claude.UserMessage(cachedBible, claude.TextBlock(prompt)),
		}, pairMaxTokens, pairCallTimeout)
		if err != nil {
			log.Printf("    ! continuity scan failed for %d→%d: %v", a.Number, b.Number, err)
			continue
		}
		_, inc, perr := parseResponse(resp)
		if perr != nil {
			log.Printf("    ! parse error for %d→%d: %v", a.Number, b.Number, perr)
			continue
		}
		for j := range inc {
			if inc[j].Chapter == 0 {
				inc[j].Chapter = b.Number
			}
		}
		if len(inc) > 0 {
			log.Printf("    ✓ %d continuity break(s)", len(inc))
		}
		all = append(all, inc...)
	}
	return all
}

// ─── Prompt bodies ───────────────────────────────────────────────────────────

const categoryChecklist = `Inconsistency categories — be EXHAUSTIVE, report even minor cases:

  SCENE_PRESENCE — A character is established as present in a scene, then ignored
    for paragraphs, then suddenly speaks/acts as if they were always there — without
    any narration of them leaving and returning. Or the reverse: someone appears
    speaking mid-scene without ever being established as present.
    Example: "Riley sat on the couch. Marcus drew his sword... [3 paragraphs of
    action]... 'I'll go too,' Riley said." — when did she leave? Did she stay?

  WARDROBE_DRIFT — Clothing, hair, makeup, jewellery, or surface appearance changes
    inside the same scene with no change-of-clothes event between mentions.
    Example: scene opens with her in a blue gown; mid-conversation she's in a tunic;
    nobody narrated her leaving to change.

  DIALOGUE_RETCON — A character responds to something nobody said, or references
    a past conversation/event that does not appear earlier in the story.
    Example: "Like you said before, the army is already marching" — but no one
    has said that.

  ABILITY_DRIFT — A power, supernatural sense, or skill appears, vanishes, or
    behaves differently from what was established.
    Example: protagonist "feels her wolf" in chapter 12 without ever having been
    set up as a werewolf; or set up but only senses it once and never again
    despite obvious triggers.

  PERSONALITY_SHIFT — Voice, tone, or behaviour changes abruptly without a
    narrative trigger (trauma, revelation, time-skip).
    Example: a shy character suddenly making sarcastic jokes with no incident
    in between.

  DESCRIPTION_DRIFT — Permanent physical traits change: eye colour, hair colour,
    height, age, scars, tattoos.

  NAME_DRIFT — Names or titles used inconsistently. "Lena" → "Elena", "Sir Marcus"
    → "Lord Marcus" without explanation.

  PLOT_CONTRADICTION — World facts contradict each other. Distances, hierarchies,
    magic rules, established past events.

  TIMELINE_BREAK — Impossible temporal sequence. Character in two places, time
    moving inconsistently between scenes, ages that do not add up.

  RELATIONSHIP_DRIFT — Relationship dynamic changes without a narrative cause
    (intimacy, hostility, trust suddenly different).

  KNOWLEDGE_LEAK — A character knows something they could not plausibly know
    given their on-page experience.

  SCENE_TRANSITION — Scene break between chapters or within a chapter that
    breaks continuity (location jumped without travel narration, characters
    in/out of room without transition, wrong time of day).

For severity:
  high   — breaks the reader's immersion / contradicts a major plot fact
  medium — noticeable, would make a careful reader pause
  low    — small detail, but still a slip worth fixing

Always quote the offending text in originalText (≤200 chars, verbatim from the
chapter, between double quotes). Without a quote we cannot fix it.
Do NOT filter — report every drift you can prove with a quote.`

const macroPromptFirst = `You are analysing a long-form novel for internal consistency. Read the chapters
below carefully and produce TWO things:

1. A "World Bible" — the established facts of this story so far:
   - Characters (full name, aliases, physical description, personality, role,
     relationships, current status, first chapter they appear in)
   - Locations (name + short description)
   - World mechanics (magic systems, supernatural rules, technology, society)
   - Plot events (one bullet per significant event, tied to a chapter)

2. A list of macro-level inconsistencies between the chapters in this batch.

` + categoryChecklist + `

Respond with ONLY valid JSON — no markdown fences, no preamble, no commentary:
{
  "worldBible": {
    "characters": [
      {
        "name": "string",
        "aliases": ["string"],
        "description": "physical appearance + personality",
        "role": "protagonist|antagonist|supporting",
        "relationships": ["string"],
        "status": "alive|dead|missing|unknown",
        "firstAppearance": 1
      }
    ],
    "locations": [{"name": "string", "description": "string"}],
    "worldMechanics": ["string"],
    "plotEvents": [{"chapter": 1, "description": "string"}]
  },
  "inconsistencies": [
    {
      "chapter": 1,
      "type": "SCENE_PRESENCE|WARDROBE_DRIFT|DIALOGUE_RETCON|ABILITY_DRIFT|PERSONALITY_SHIFT|DESCRIPTION_DRIFT|NAME_DRIFT|PLOT_CONTRADICTION|TIMELINE_BREAK|RELATIONSHIP_DRIFT|KNOWLEDGE_LEAK|SCENE_TRANSITION",
      "severity": "high|medium|low",
      "description": "Specific description of the inconsistency",
      "conflictingFacts": ["fact A from chapter X: \"quote\"", "fact B from chapter Y: \"quote\""],
      "originalText": "verbatim ≤200-char quote of the offending text",
      "suggestedFix": "concrete suggestion"
    }
  ]
}`

const macroPromptContinue = `Before updating the World Bible, strictly AUDIT the new chapters against the established facts. LLMs often mistake author continuity errors for 'new lore'. Do NOT auto-correct or blindly update.

    If a character suddenly has a power they lacked (e.g., a dormant wolf suddenly speaking without a plot trigger).

    If a character's history contradicts previous statements.
    
	You MUST flag this as an inconsistency FIRST. ONLY update the World Bible if the change is a logical, explicitly narrated progression of the story.

	Using the World Bible above, analyse the new chapters (starting at chapter %d).

1. UPDATE the World Bible — add new characters/locations/events; refine existing
   entries with new facts. Return the FULL updated bible, not a diff.
2. Report any inconsistencies between these new chapters and the established
   bible, or within the new chapters themselves.

` + categoryChecklist + `

Respond with ONLY valid JSON:
{
  "worldBible": { /* complete updated bible */ },
  "inconsistencies": [
    {
      "chapter": %d,
      "type": "SCENE_PRESENCE|WARDROBE_DRIFT|DIALOGUE_RETCON|ABILITY_DRIFT|PERSONALITY_SHIFT|DESCRIPTION_DRIFT|NAME_DRIFT|PLOT_CONTRADICTION|TIMELINE_BREAK|RELATIONSHIP_DRIFT|KNOWLEDGE_LEAK|SCENE_TRANSITION",
      "severity": "high|medium|low",
      "description": "string",
      "conflictingFacts": ["string"],
      "originalText": "verbatim ≤200-char quote",
      "suggestedFix": "string"
    }
  ]
}`

const scenePrompt = `Focus exclusively on intra-chapter, intra-scene consistency.

Walk through the chapter SCENE BY SCENE. For each scene, build a mental list of:
  - Who is in the room / present
  - What each character is wearing, holding, doing
  - What time of day / weather / atmosphere
  - What has been said so far in dialogue
  - What sensory details (smells, sounds) have been established

Then watch for these specific drifts as the scene progresses:

` + categoryChecklist + `

Pay special attention to:
  - Long stretches of dialogue where a third character was established as present
    but never speaks/reacts — at the end of the scene, are they suddenly
    interjecting without ever leaving and returning? That's SCENE_PRESENCE.
  - "She fixed her dress" or "she pulled her cloak tight" when no dress/cloak
    was mentioned in the scene's opening description.
  - A character noticing something only the narrator knows (KNOWLEDGE_LEAK).
  - A power being used casually that was framed as rare/forbidden earlier in the
    bible.

Do not invent inconsistencies. Each finding must be backed by a verbatim quote
in originalText. If the chapter is internally clean, return an empty array.

Respond with ONLY valid JSON (no World Bible — this pass does not modify it):
{
  "inconsistencies": [
    {
      "chapter": 0,
      "type": "SCENE_PRESENCE|WARDROBE_DRIFT|DIALOGUE_RETCON|ABILITY_DRIFT|PERSONALITY_SHIFT|DESCRIPTION_DRIFT|NAME_DRIFT|PLOT_CONTRADICTION|TIMELINE_BREAK|RELATIONSHIP_DRIFT|KNOWLEDGE_LEAK|SCENE_TRANSITION",
      "severity": "high|medium|low",
      "description": "string",
      "conflictingFacts": ["string"],
      "originalText": "verbatim ≤200-char quote",
      "suggestedFix": "string"
    }
  ]
}`

const continuityPrompt = `You are checking continuity between two adjacent chapter snippets.

First decide: does chapter B's opening read as a DIRECT continuation of chapter
A's ending (same scene continuing, or scene picking up moments later in the same
location with the same characters)? Or is there an explicit cut/time-skip/POV
change?

  - If B is a clear new scene (different time, location, POV change, or explicit
    transition), return an EMPTY inconsistencies array — there is nothing to flag.
  - If B should be a direct continuation, then check carefully:
      • Are the same characters present? Did anyone appear/disappear with no
        bridge?
      • Is the location consistent? Same room, same furniture, same lighting?
      • Is the time of day consistent? Did "evening" become "morning" with no gap?
      • Are characters wearing the same clothes / holding the same objects?
      • Does the emotional state carry over plausibly?
      • Does the dialogue pick up where it left off, or does someone reference
        something not yet said?

Use only the categories below; the most common ones here will be
SCENE_TRANSITION, SCENE_PRESENCE, WARDROBE_DRIFT, TIMELINE_BREAK,
DIALOGUE_RETCON.

` + categoryChecklist + `

Respond with ONLY valid JSON:
{
  "inconsistencies": [
    {
      "chapter": 0,
      "type": "SCENE_TRANSITION|SCENE_PRESENCE|WARDROBE_DRIFT|TIMELINE_BREAK|DIALOGUE_RETCON|...",
      "severity": "high|medium|low",
      "description": "string",
      "conflictingFacts": ["string"],
      "originalText": "verbatim ≤200-char quote",
      "suggestedFix": "string"
    }
  ]
}`

// ─── Helpers ─────────────────────────────────────────────────────────────────

func renderChapters(chapters []models.Chapter) string {
	parts := make([]string, len(chapters))
	for i, ch := range chapters {
		parts[i] = fmt.Sprintf("=== Chapter %d: %s ===\n\n%s", ch.Number, ch.Title, ch.Content)
	}
	return strings.Join(parts, "\n\n---\n\n")
}

// callJSON wraps the client with a per-call timeout and one retry at half the
// max-token budget. The retry catches the most common failure mode: response
// truncated past the JSON close brace, then `extractJSON` returns garbage.
func callJSON(client *claude.Client, system string, msgs []claude.Message, maxTokens int, callTimeout time.Duration) (string, error) {
	tryOnce := func(tokens int) (string, error) {
		ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
		defer cancel()
		return client.CompleteEx(ctx, system, msgs, claude.Options{
			MaxTokens: tokens,
			JSONMode:  true,
		})
	}

	resp, err := tryOnce(maxTokens)
	if err == nil && looksParseable(resp) {
		return resp, nil
	}

	if err != nil {
		log.Printf("    retry (first attempt failed: %v)", err)
	} else {
		log.Printf("    retry (first attempt response did not parse cleanly)")
	}
	// Retry with fewer tokens forces a tighter response that tends to fit.
	resp2, err2 := tryOnce(maxTokens / 2)
	if err2 != nil {
		if err != nil {
			return "", fmt.Errorf("two attempts failed: %v; %v", err, err2)
		}
		return "", err2
	}
	return resp2, nil
}

func looksParseable(s string) bool {
	js := extractJSON(s)
	var any any
	return json.Unmarshal([]byte(js), &any) == nil
}

func parseResponse(resp string) (models.WorldBible, []models.Inconsistency, error) {
	jsonStr := extractJSON(resp)

	var result struct {
		AuditReasoning  string                 `json:"auditReasoning"`
		WorldBible      models.WorldBible      `json:"worldBible"`
		Inconsistencies []models.Inconsistency `json:"inconsistencies"`
	}

	if err := json.Unmarshal([]byte(jsonStr), &result); err != nil {
		preview := jsonStr
		if len(preview) > 600 {
			preview = preview[:600] + "..."
		}
		return models.WorldBible{}, nil, fmt.Errorf("JSON parse error: %w\nResponse start: %s", err, preview)
	}

	if result.AuditReasoning != "" {
        log.Printf("    [Audit Logic]: %s", result.AuditReasoning)
    }
	
	return result.WorldBible, result.Inconsistencies, nil
}

// extractJSON strips markdown fences and finds the outermost JSON object.
func extractJSON(s string) string {
	s = strings.TrimSpace(s)

	if strings.HasPrefix(s, "```") {
		if nl := strings.Index(s, "\n"); nl >= 0 {
			s = s[nl+1:]
		}
		s = strings.TrimSuffix(strings.TrimSpace(s), "```")
		s = strings.TrimSpace(s)
	}

	start := strings.Index(s, "{")
	end := strings.LastIndex(s, "}")
	if start >= 0 && end > start {
		s = s[start : end+1]
	}
	return s
}

func mergeInconsistencies(slices ...[]models.Inconsistency) []models.Inconsistency {
	seen := make(map[string]bool)
	var out []models.Inconsistency
	for _, s := range slices {
		for _, inc := range s {
			key := fmt.Sprintf("%d|%s|%s", inc.Chapter, inc.Type, normaliseQuote(inc.OriginalText))
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, inc)
		}
	}
	return out
}

func normaliseQuote(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\t", " ")
	for strings.Contains(s, "  ") {
		s = strings.ReplaceAll(s, "  ", " ")
	}
	if len(s) > 80 {
		s = s[:80]
	}
	return s
}

func head(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

func tail(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[len(r)-n:])
}

// ─── Human-readable report ───────────────────────────────────────────────────

func buildTextReport(report models.Report) string {
	var sb strings.Builder

	sb.WriteString("=== INCONSISTENCY REPORT ===\n\n")
	total := len(report.Inconsistencies)
	sb.WriteString(fmt.Sprintf("Total inconsistencies: %d\n", total))

	if total == 0 {
		sb.WriteString("No inconsistencies found across all three passes.\n")
		return sb.String()
	}

	high := countBySeverity(report.Inconsistencies, "high")
	medium := countBySeverity(report.Inconsistencies, "medium")
	low := countBySeverity(report.Inconsistencies, "low")
	sb.WriteString(fmt.Sprintf("  High:   %d\n  Medium: %d\n  Low:    %d\n\n", high, medium, low))

	// Breakdown by type
	byType := make(map[string]int)
	for _, inc := range report.Inconsistencies {
		byType[inc.Type]++
	}
	sb.WriteString("By category:\n")
	for t, n := range byType {
		sb.WriteString(fmt.Sprintf("  %-22s %d\n", t, n))
	}
	sb.WriteString("\n")

	sb.WriteString("=== DETAILS ===\n\n")
	for _, inc := range report.Inconsistencies {
		sb.WriteString(fmt.Sprintf("--- #%d  Chapter %d  [%s]  [%s] ---\n",
			inc.ID, inc.Chapter, inc.Type, strings.ToUpper(inc.Severity)))
		sb.WriteString(fmt.Sprintf("Description:   %s\n", inc.Description))
		if len(inc.ConflictingFacts) > 0 {
			sb.WriteString("Conflicts:\n")
			for _, f := range inc.ConflictingFacts {
				sb.WriteString(fmt.Sprintf("  • %s\n", f))
			}
		}
		if inc.OriginalText != "" {
			sb.WriteString(fmt.Sprintf("Original text: %q\n", inc.OriginalText))
		}
		sb.WriteString(fmt.Sprintf("Suggested fix: %s\n\n", inc.SuggestedFix))
	}

	sb.WriteString("=== WORLD BIBLE SUMMARY ===\n\n")
	sb.WriteString(fmt.Sprintf("Characters (%d):\n", len(report.WorldBible.Characters)))
	for _, c := range report.WorldBible.Characters {
		sb.WriteString(fmt.Sprintf("  • %s (%s, %s)\n    %s\n", c.Name, c.Role, c.Status, c.Description))
	}
	sb.WriteString(fmt.Sprintf("\nLocations (%d):\n", len(report.WorldBible.Locations)))
	for _, l := range report.WorldBible.Locations {
		sb.WriteString(fmt.Sprintf("  • %s: %s\n", l.Name, l.Description))
	}

	return sb.String()
}

func countBySeverity(incs []models.Inconsistency, s string) int {
	n := 0
	for _, inc := range incs {
		if inc.Severity == s {
			n++
		}
	}
	return n
}
