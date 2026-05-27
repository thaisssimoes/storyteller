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

		// Merge instead of replace: keeps all facts established in earlier batches
		// even when a later response omits them or drifts the schema.
		bible = models.MergeWorldBibles(bible, updatedBible)
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
	// Pass an empty Bible as cached context so the model sees the expected JSON
	// schema before the instructions — same pattern as analyseNextBatch, which
	// reliably produces the correct output structure.
	emptyBible := models.WorldBible{
		Characters:            []models.Character{},
		Locations:             []models.Location{},
		WorldMechanics:        []string{},
		PlotEvents:            []models.PlotEvent{},
		CharacterInteractions: []models.CharacterInteraction{},
		MagicObjects:          []models.MagicObject{},
	}
	emptyBibleJSON, _ := json.MarshalIndent(emptyBible, "", "  ")

	prompt := fmt.Sprintf(`%s

--- BEGIN CHAPTERS TO ANALYZE ---
%s
--- END CHAPTERS TO ANALYZE ---

{"auditReasoning": "`, macroPromptFirst, renderChapters(chapters))

	resp, err := callJSON(client, strictSystemPrompt, []claude.Message{
		claude.UserMessage(
			claude.CachedTextBlock(fmt.Sprintf("WORLD BIBLE (no entries yet — first batch):\n%s", string(emptyBibleJSON))),
			claude.TextBlock(prompt),
		),
	}, macroMaxTokens, macroCallTimeout, macroJSONSchema)
	if err != nil {
		return models.WorldBible{}, nil, err
	}
	return parseResponse(resp)
}

func analyseNextBatch(client *claude.Client, bible models.WorldBible, chapters []models.Chapter) (models.WorldBible, []models.Inconsistency, error) {
	bibleJSON, _ := json.MarshalIndent(bible, "", "  ")

	limChecklist := buildLimitationsChecklist(bible)

	// Build the instructions block first (instruction-first ordering improves
	// local-model comprehension vs. content-first).
	instructions := fmt.Sprintf(macroPromptContinue, chapters[0].Number, chapters[0].Number)
	if limChecklist != "" {
		instructions = limChecklist + "\n" + instructions
	}

	prompt := fmt.Sprintf(`%s

--- BEGIN NEW CHAPTERS (starting at chapter %d) ---
%s
--- END NEW CHAPTERS ---`, instructions, chapters[0].Number, renderChapters(chapters))

	resp, err := callJSON(client, strictSystemPrompt, []claude.Message{
		claude.UserMessage(
			claude.CachedTextBlock(fmt.Sprintf("WORLD BIBLE (established facts so far):\n%s", string(bibleJSON))),
			claude.TextBlock(prompt),
		),
	}, macroMaxTokens, macroCallTimeout, macroJSONSchema)
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
		}, sceneMaxTokens, sceneCallTimeout, sceneJSONSchema)
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
		}, pairMaxTokens, pairCallTimeout, sceneJSONSchema)
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

const macroPromptFirst = `[SYSTEM OVERRIDE: DATA EXTRACTION MODE]
You are an automated data extraction script parsing raw narrative logs.
DO NOT act as a literary critic. DO NOT output keys like "title", "author", "genre", or "plot summary".

Your task is to extract facts from the text segments below to build a relational database (the "worldBible") and catch continuity logic errors.

POV IDENTIFICATION: Each chapter may start with a line like "Elara's POV" or "Kaelan's POV".
That character is the NARRATOR of that chapter. The narrator is usually the protagonist or a
major character — record them as such. Do NOT confuse supporting characters who appear in the
same scene with the narrator.

LIMITATIONS FROM FIRST-PERSON NARRATION: When the narrator says things like "my wolf had never
stirred", "I cannot shift", "I have no power", those are LIMITATIONS of the narrator character
and MUST be recorded in that character's "limitations" array verbatim. First-person statements
about lacking powers are just as mandatory as third-person statements.

SPECIES EXTRACTION: Extract each character's species from explicit statements AND implicit clues.
Do NOT default to "human" without evidence:
  - Words like "alpha", "pack", "bloodline", "shift", "pelt", "wolf", "fang", "howl" → werewolf/shifter
  - "practically human", "cannot shift", "no wolf" → human-adjacent or wolf dormant (still a werewolf world)
  - "fae", "vampire", "blood magic", "immortal" → respective species
  - Record in the "species" field. Use "unknown" when unclear.

INNER WOLF / SPIRIT COMPANION RULE: In supernatural fiction an inner wolf, inner spirit, or named
spiritual voice that speaks INSIDE a character's mind (e.g. "Moonlight whispered inside Elara",
"Alex surged forward in Kaelan's chest") is NOT a separate character. It is a supernatural
capability or aspect of the host character. Record it in that character's "capabilities" or
"limitations" array — NEVER create a separate character entry for it.
Examples:
  "Moonlight urged me forward" → Elara capabilities: ["inner wolf spirit named Moonlight"]
  "Alex detonated inside my chest" → Kaelan capabilities: ["inner wolf spirit named Alex"]
  "my wolf had never stirred" + "Moonlight" appears later → Elara limitation: ["wolf dormant — Moonlight never stirred"]
A wolf that physically walks beside a character as a distinct entity (separate body) IS its own character.

` + categoryChecklist + `

INTERACTION TRACKING: For every scene where characters communicate directly (face-to-face, transmission stone, letter, etc.), add an entry to "characterInteractions". This is critical: a character later claiming they "never spoke" to someone or "only heard them once" can only be verified if every interaction is logged here.

MAGIC OBJECT TRACKING: For every enchanted or magical object, record it in "magicObjects" with ALL observed visual/behavioural properties per sender/context (e.g., "pulses deep red when Emperor calls", "pulses soft amber for Claire").

PLOT OBJECT TRACKING: Record non-magical items that carry narrative or symbolic significance and will recur throughout the story in "plotObjects". Examples: a dress worn at a pivotal scene, a pendant given as a betrayal, a pin left as a promise. Capture owner, physical description (colour, material, details), and why it matters to the plot. Any future reappearance of the object must match this record.

CRITICAL RULE: You MUST output a JSON object with EXACTLY these three root keys.
1. "auditReasoning": Write a brief summary of the text to prove you read it, THEN write your step-by-step logic for finding continuity errors.
2. "inconsistencies": Array of objects detailing any errors.
3. "worldBible": The exhaustive database of characters, locations, events, interactions, and objects.

EXAMPLE OF THE EXACT REQUIRED JSON STRUCTURE:
{
  "auditReasoning": "Comparing the new logs against the Bible...",
  "inconsistencies": [
    {
      "chapter": 3,
      "type": "ABILITY_DRIFT",
      "severity": "high",
      "description": "string",
      "conflictingFacts": ["string"],
      "originalText": "string",
      "suggestedFix": "string"
    }
  ],
  "worldBible": {
    "characters": [
      {
        "name": "string",
        "aliases": ["string"],
        "description": "string",
        "species": "werewolf|human|fae|vampire|unknown -- derive from clues, never assume human",
        "capabilities": ["string -- include inner wolf spirits here, e.g. 'inner wolf spirit named Moonlight'"],
        "limitations": ["string -- CRITICAL: quote exact text when a power/ability is explicitly absent, e.g. 'wolf never stirred', 'cannot shift'"],
        "role": "string",
        "relationships": ["string"],
        "status": "string",
        "firstAppearance": 1
      }
    ],
    "locations": [],
    "worldMechanics": [],
    "plotEvents": [],
    "characterInteractions": [
      {
        "chapter": 1,
        "characters": ["Name A", "Name B"],
        "medium": "face-to-face",
        "summary": "brief description of what was communicated"
      }
    ],
    "magicObjects": [
      {
        "name": "Transmission Stone",
        "description": "Enchanted stone used for direct communication between palace staff",
        "properties": ["pulses deep red when Emperor calls", "pulses soft amber for Claire's signature"],
        "firstSeen": 5
      }
    ],
    "plotObjects": [
      {
        "name": "Ice-blue silk dress",
        "description": "Ice-blue silk gown with silver thread embroidery",
        "owner": "Elara",
        "significance": "Worn at the masquerade where she met Kaelan; symbol of transformation and hidden identity",
        "properties": ["ice-blue silk", "silver thread embroidery", "deep neckline"],
        "firstSeen": 2
      }
    ]
  }
}

ORIGINALTEXT RULE: The "originalText" field in every inconsistency MUST be a verbatim quote
copied from the chapter text between the chapter markers below. DO NOT quote from these
instructions or from the JSON schema example above. If the text you want to quote cannot be
found word-for-word in the chapters, do not report that inconsistency.

Respond ONLY with the complete JSON object starting with { and ending with }.`


const macroPromptContinue = `[SYSTEM OVERRIDE: DATA EXTRACTION MODE]
Before updating the World Bible database, strictly AUDIT the new narrative logs against the established facts.
DO NOT act as a literary critic. DO NOT auto-correct the author's mistakes.

GROUNDING RULE — CRITICAL: Only extract facts that are EXPLICITLY written in the chapter text
provided between the "--- BEGIN NEW CHAPTERS ---" and "--- END NEW CHAPTERS ---" markers.
DO NOT invent characters, objects, events, or abilities from outside those markers.
DO NOT draw on your training data to fill gaps — if something is not in the provided text, it does not exist.
Characters or objects from the World Bible that do NOT appear in the new chapters must be carried
forward unchanged. Do NOT remove or modify existing Bible entries just because a chapter omits them.

ANTI-FALSE-POSITIVE: Do NOT flag a change as inconsistent if it is explained anywhere within the same chapter. Only flag unexplained contradictions.

POV IDENTIFICATION: A line like "Elara's POV" at the start of a chapter means Elara is the
narrator. Do NOT confuse supporting characters who appear in the same scene with the narrator.

LIMITATIONS FROM FIRST-PERSON NARRATION: Statements like "my wolf had never stirred" or
"I cannot shift" are narrator limitations and MUST be recorded in that character's "limitations"
array, even though they are written in first person.

INNER WOLF / SPIRIT COMPANION RULE: A named voice or spirit that speaks INSIDE a character's
mind (e.g. "Moonlight", "Alex") is NOT a separate character. Record it in the host character's
"capabilities" array. Never create a standalone character entry for an inner spirit.

SPECIES: Update each character's "species" field from evidence in the text (werewolf, human, fae,
etc.). Do not default to "human" without evidence — clues like "alpha", "shift", "pack" indicate
werewolf.

ABILITY ENFORCEMENT: If a character's "limitations" in the World Bible state they lack a power (e.g., "wolf never stirred", "cannot shift"), and the new text shows that power being used WITHOUT any in-chapter explanation of how they gained it, flag it as ABILITY_DRIFT with high severity.

INTERACTION CONTRADICTION: Before accepting any character's claim about their history with another character (e.g., "I only heard her voice twice", "we have never spoken"), cross-check the "characterInteractions" log in the World Bible. If a direct communication is already recorded there, flag the contradiction as PLOT_CONTRADICTION.

NAME DRIFT: Check every character name spelling in the new chapters against the World Bible. Different spellings of the same name (e.g., "Kaelen" vs "Kaelan") must be flagged as NAME_DRIFT.

INTERACTION TRACKING: For every new scene where characters communicate (face-to-face, stone, letter), add it to "characterInteractions" in the updated Bible.

MAGIC OBJECT TRACKING: If a magical object's visual/behavioural properties differ from what is in "magicObjects" (e.g., a stone that pulsed red now pulses gold with no explanation), flag it as DESCRIPTION_DRIFT.

PLOT OBJECT TRACKING: If a non-magical plot object reappears (a dress, pendant, pin, letter, etc.) with a description that contradicts the one recorded in "plotObjects", flag it as DESCRIPTION_DRIFT. Also add any new plot-significant objects to "plotObjects" in the updated Bible.

Using the World Bible above, analyse the new segments (starting at chapter %d).

CRITICAL RULE: You MUST output a JSON object with EXACTLY these three root keys.
1. "auditReasoning": Write your step-by-step audit comparing new logs vs established World Bible.
2. "inconsistencies": Array of objects detailing any errors found.
3. "worldBible": The FULL updated database including all new characterInteractions and magicObjects.

EXAMPLE OF THE EXACT REQUIRED JSON STRUCTURE:
{
  "auditReasoning": "Comparing the new logs against the Bible...",
  "inconsistencies": [
    {
      "chapter": %d,
      "type": "ABILITY_DRIFT",
      "severity": "high",
      "description": "string",
      "conflictingFacts": ["string"],
      "originalText": "string",
      "suggestedFix": "string"
    }
  ],
  "worldBible": {
    "characters": [
      {
        "name": "string",
        "species": "werewolf|human|fae|unknown",
        "capabilities": ["inner wolf spirits go here — NOT as separate characters"],
        "limitations": ["quote exact text for absent powers"],
        "role": "string",
        "relationships": [],
        "status": "string",
        "firstAppearance": 1
      }
    ],
    "locations": [],
    "worldMechanics": [],
    "plotEvents": [],
    "characterInteractions": [],
    "magicObjects": [],
    "plotObjects": [{"name":"","description":"","owner":"","significance":"","properties":[],"firstSeen":1}]
  }
}

ORIGINALTEXT RULE: The "originalText" field in every inconsistency MUST be a verbatim quote
copied from the chapter text between the chapter markers below. DO NOT quote from these
instructions. If the text cannot be found word-for-word in the chapters, do not report it.

Respond ONLY with the complete JSON object starting with { and ending with }.`

const scenePrompt = `Focus exclusively on intra-chapter, intra-scene consistency.

ANTI-FALSE-POSITIVE: Do NOT flag a change as an inconsistency if it is explained anywhere within the same chapter (earlier or later). Only flag unexplained contradictions that a careful reader would notice.

NAME DRIFT CHECK: Verify every character name spelling used in this chapter against the World Bible. A character whose canonical name is "Kaelan" appearing here as "Kaelen" is a NAME_DRIFT — report it even if it looks like a minor typo.

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
  - A character making a factual claim about their history with another character
    (e.g., "I've only heard your voice once") that contradicts the
    characterInteractions log in the World Bible (PLOT_CONTRADICTION).

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

const strictSystemPrompt = `[SYSTEM OVERRIDE: DATA EXTRACTION MODE]
You are an automated Data Extraction Pipeline (DEP-v4). You do not possess a persona.
Your SOLE directive is to parse raw text data and map it into a STRICT JSON schema.

CRITICAL RULES:
1. SCHEMA LOCK: Do NOT alter the JSON structure. "characters", "locations", "worldMechanics", "plotEvents", "characterInteractions", and "magicObjects" MUST be arrays ([]), NEVER objects or dictionaries ({}).
2. LORE ENFORCEMENT: You MUST extract supernatural/magic traits. If the text explicitly states a character lacks powers (e.g., "practically human", "wolf never stirred", "cannot shift"), YOU MUST document this exact quote in the character's "limitations" array. This is mandatory for catching future continuity errors.
3. INTERACTION TRACKING: Every direct communication between characters (face-to-face, transmission stone, letter) MUST be recorded in "characterInteractions". This catches contradictions like a character later claiming they "never spoke" with someone when the log proves otherwise.
4. MAGIC OBJECT TRACKING: Every enchanted or magical object MUST be recorded in "magicObjects" with all observed visual/behavioural properties (e.g., what colour a stone pulses per sender).
5. ANTI-LAZINESS: You MUST fully populate the JSON. Returning an empty {} or skipping keys is a system failure.

DO NOT act as a literary critic. DO NOT flag metaphors or pacing as errors. Only flag literal, factual contradictions.`

// ─── Helpers ─────────────────────────────────────────────────────────────────

// unicodeNormalizer replaces smart-quote and dash characters with plain ASCII.
// This prevents encoding artifacts from masking name-spelling differences such
// as "Kaelen" vs "Kaelan" hiding behind different apostrophe code points.
var unicodeNormalizer = strings.NewReplacer(
	"’", "'",  // right single quotation mark
	"‘", "'",  // left single quotation mark
	"“", "\"", // left double quotation mark
	"”", "\"", // right double quotation mark
	"—", "--", // em dash
	"–", "-",  // en dash
	" ", " ",  // non-breaking space
)

func normalizeText(s string) string {
	return unicodeNormalizer.Replace(s)
}

// buildLimitationsChecklist extracts character limitations from the Bible and
// formats them as an explicit checklist injected into the continuation prompt.
// This forces the model to compare new chapters against known ability absences
// (e.g. "wolf never stirred") instead of relying on it to recall them from the
// full Bible JSON.
func buildLimitationsChecklist(bible models.WorldBible) string {
	var lines []string
	for _, c := range bible.Characters {
		for _, lim := range c.Limitations {
			if lim != "" {
				lines = append(lines, fmt.Sprintf("  - %s: %s", c.Name, lim))
			}
		}
	}
	if len(lines) == 0 {
		return ""
	}
	return "ESTABLISHED LIMITATIONS — flag ABILITY_DRIFT if any new chapter violates these:\n" +
		strings.Join(lines, "\n") + "\n"
}

func renderChapters(chapters []models.Chapter) string {
	parts := make([]string, len(chapters))
	for i, ch := range chapters {
		parts[i] = fmt.Sprintf("=== Chapter %d: %s ===\n\n%s",
			ch.Number, normalizeText(ch.Title), normalizeText(ch.Content))
	}
	return strings.Join(parts, "\n\n---\n\n")
}

// macroJSONSchema constrains Ollama's structured output down to field level.
// Forcing "name","species","capabilities","limitations" as required on every
// character object ensures the wolf-dormancy limitation is always extracted.
// Forcing "chapter","type","severity","description","originalText" on every
// inconsistency prevents the model from using NLP annotation formats.
var macroJSONSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "auditReasoning": {"type": "string"},
    "inconsistencies": {
      "type": "array",
      "items": {
        "type": "object",
        "properties": {
          "chapter":         {"type": "integer"},
          "type":            {"type": "string"},
          "severity":        {"type": "string"},
          "description":     {"type": "string"},
          "conflictingFacts":{"type": "array", "items": {"type": "string"}},
          "originalText":    {"type": "string"},
          "suggestedFix":    {"type": "string"}
        },
        "required": ["chapter","type","severity","description","originalText"]
      }
    },
    "worldBible": {
      "type": "object",
      "properties": {
        "characters": {
          "type": "array",
          "items": {
            "type": "object",
            "properties": {
              "name":          {"type": "string"},
              "species":       {"type": "string"},
              "capabilities":  {"type": "array", "items": {"type": "string"}},
              "limitations":   {"type": "array", "items": {"type": "string"}},
              "role":          {"type": "string"},
              "relationships": {"type": "array", "items": {"type": "string"}},
              "status":        {"type": "string"},
              "firstAppearance":{"type": "integer"}
            },
            "required": ["name","species","capabilities","limitations"]
          }
        },
        "locations": {
          "type": "array",
          "items": {
            "type": "object",
            "properties": {
              "name":        {"type": "string"},
              "description": {"type": "string"}
            },
            "required": ["name"]
          }
        },
        "worldMechanics": {"type": "array", "items": {"type": "string"}},
        "plotEvents": {
          "type": "array",
          "items": {
            "type": "object",
            "properties": {
              "chapter":     {"type": "integer"},
              "description": {"type": "string"}
            },
            "required": ["description"]
          }
        },
        "characterInteractions": {
          "type": "array",
          "items": {
            "type": "object",
            "properties": {
              "chapter":    {"type": "integer"},
              "characters": {"type": "array", "items": {"type": "string"}},
              "medium":     {"type": "string"},
              "summary":    {"type": "string"}
            },
            "required": ["chapter","characters","summary"]
          }
        },
        "magicObjects": {
          "type": "array",
          "items": {
            "type": "object",
            "properties": {
              "name":        {"type": "string"},
              "description": {"type": "string"},
              "properties":  {"type": "array", "items": {"type": "string"}},
              "firstSeen":   {"type": "integer"}
            },
            "required": ["name"]
          }
        },
        "plotObjects": {
          "type": "array",
          "items": {
            "type": "object",
            "properties": {
              "name":         {"type": "string"},
              "description":  {"type": "string"},
              "owner":        {"type": "string"},
              "significance": {"type": "string"},
              "properties":   {"type": "array", "items": {"type": "string"}},
              "firstSeen":    {"type": "integer"}
            },
            "required": ["name","significance"]
          }
        }
      },
      "required": ["characters","locations","worldMechanics","plotEvents","characterInteractions","magicObjects","plotObjects"]
    }
  },
  "required": ["auditReasoning","inconsistencies","worldBible"]
}`)

// sceneJSONSchema constrains scene/continuity pass output and forces the
// correct field names on each inconsistency item.
var sceneJSONSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "inconsistencies": {
      "type": "array",
      "items": {
        "type": "object",
        "properties": {
          "chapter":         {"type": "integer"},
          "type":            {"type": "string"},
          "severity":        {"type": "string"},
          "description":     {"type": "string"},
          "conflictingFacts":{"type": "array", "items": {"type": "string"}},
          "originalText":    {"type": "string"},
          "suggestedFix":    {"type": "string"}
        },
        "required": ["chapter","type","severity","description","originalText"]
      }
    }
  },
  "required": ["inconsistencies"]
}`)

// callJSON wraps the client with a per-call timeout and a retry.
// Pass an optional JSON schema as the last argument to enable Ollama
// structured output (≥0.4.0), which constrains the top-level fields and
// prevents the model from generating story summaries or writing feedback.
func callJSON(client *claude.Client, system string, msgs []claude.Message, maxTokens int, callTimeout time.Duration, schema ...json.RawMessage) (string, error) {
	if system == "" {
		system = strictSystemPrompt
	}

	var s json.RawMessage
	if len(schema) > 0 {
		s = schema[0]
	}

	tryOnce := func(tokens int) (string, error) {
		ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
		defer cancel()
		return client.CompleteEx(ctx, system, msgs, claude.Options{
			MaxTokens: tokens,
			JSONMode:  true,
			Schema:    s,
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
		
	log.Printf("\n=== RAW LLM RESPONSE (len: %d) ===\n%s\n==================================\n", len(resp), resp)

	jsonStr := extractJSON(resp)

	var result struct {
		AuditReasoning  string                 `json:"auditReasoning"`
		Inconsistencies []models.Inconsistency `json:"inconsistencies"`
		WorldBible      models.WorldBible      `json:"worldBible"`

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
		for _, lim := range c.Limitations {
			sb.WriteString(fmt.Sprintf("    [LIMITATION] %s\n", lim))
		}
		for _, cap := range c.Capabilities {
			sb.WriteString(fmt.Sprintf("    [CAPABILITY] %s\n", cap))
		}
	}
	sb.WriteString(fmt.Sprintf("\nLocations (%d):\n", len(report.WorldBible.Locations)))
	for _, l := range report.WorldBible.Locations {
		sb.WriteString(fmt.Sprintf("  • %s: %s\n", l.Name, l.Description))
	}

	if len(report.WorldBible.MagicObjects) > 0 {
		sb.WriteString(fmt.Sprintf("\nMagic Objects (%d):\n", len(report.WorldBible.MagicObjects)))
		for _, mo := range report.WorldBible.MagicObjects {
			sb.WriteString(fmt.Sprintf("  • %s (first seen ch.%d): %s\n", mo.Name, mo.FirstSeen, mo.Description))
			for _, p := range mo.Properties {
				sb.WriteString(fmt.Sprintf("    - %s\n", p))
			}
		}
	}

	if len(report.WorldBible.CharacterInteractions) > 0 {
		sb.WriteString(fmt.Sprintf("\nCharacter Interactions (%d):\n", len(report.WorldBible.CharacterInteractions)))
		for _, ci := range report.WorldBible.CharacterInteractions {
			sb.WriteString(fmt.Sprintf("  • Ch.%d [%s] %s — %s\n",
				ci.Chapter, ci.Medium, strings.Join(ci.Characters, " + "), ci.Summary))
		}
	}

	if len(report.WorldBible.PlotObjects) > 0 {
		sb.WriteString(fmt.Sprintf("\nPlot Objects (%d):\n", len(report.WorldBible.PlotObjects)))
		for _, po := range report.WorldBible.PlotObjects {
			sb.WriteString(fmt.Sprintf("  • %s (owner: %s, first seen ch.%d)\n    %s\n    Significance: %s\n",
				po.Name, po.Owner, po.FirstSeen, po.Description, po.Significance))
			for _, p := range po.Properties {
				sb.WriteString(fmt.Sprintf("    - %s\n", p))
			}
		}
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
