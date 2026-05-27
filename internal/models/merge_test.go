package models_test

import (
	"testing"

	"inconsistencyfixer/internal/models"
)

func TestMergeWorldBibles_PreservesBaseCharacters(t *testing.T) {
	base := models.WorldBible{
		Characters: []models.Character{
			{Name: "Elara", Limitations: []string{"wolf never stirred"}},
			{Name: "Kaelan", Role: "Emperor"},
		},
	}
	// updated omits Elara entirely (simulates a hallucinating batch)
	updated := models.WorldBible{
		Characters: []models.Character{
			{Name: "Anya", Role: "Unknown"}, // hallucinated character
		},
	}

	result := models.MergeWorldBibles(base, updated)

	names := make(map[string]bool)
	for _, c := range result.Characters {
		names[c.Name] = true
	}
	if !names["Elara"] {
		t.Error("Elara was lost after merge — base characters must be preserved")
	}
	if !names["Kaelan"] {
		t.Error("Kaelan was lost after merge")
	}
	if !names["Anya"] {
		t.Error("Anya (new character) should have been added")
	}
	if len(result.Characters) != 3 {
		t.Errorf("expected 3 characters, got %d", len(result.Characters))
	}
}

func TestMergeWorldBibles_PreservesLimitations(t *testing.T) {
	base := models.WorldBible{
		Characters: []models.Character{
			{Name: "Elara", Limitations: []string{"wolf never stirred", "cannot shift"}},
		},
	}
	// updated has same character but with different/partial limitations
	updated := models.WorldBible{
		Characters: []models.Character{
			{Name: "Elara", Limitations: []string{"single mother"}},
		},
	}

	result := models.MergeWorldBibles(base, updated)

	elara := result.Characters[0]
	limSet := make(map[string]bool)
	for _, l := range elara.Limitations {
		limSet[l] = true
	}
	if !limSet["wolf never stirred"] {
		t.Error("'wolf never stirred' limitation was lost in merge")
	}
	if !limSet["cannot shift"] {
		t.Error("'cannot shift' limitation was lost in merge")
	}
	if !limSet["single mother"] {
		t.Error("new 'single mother' limitation was not added")
	}
}

func TestMergeWorldBibles_CaseInsensitiveName(t *testing.T) {
	// "Kaelan" (base) and "Kaelen" (updated) are the same character.
	// The merge should treat them as the same entry and NOT create a duplicate.
	base := models.WorldBible{
		Characters: []models.Character{
			{Name: "Kaelan", Role: "Emperor"},
		},
	}
	updated := models.WorldBible{
		Characters: []models.Character{
			{Name: "kaelan", Role: "Alpha Emperor"}, // lowercase drift
		},
	}

	result := models.MergeWorldBibles(base, updated)

	if len(result.Characters) != 1 {
		t.Errorf("expected 1 character (merged), got %d: %v",
			len(result.Characters), result.Characters)
	}
	if result.Characters[0].Role != "Alpha Emperor" {
		t.Errorf("role not updated: got %q", result.Characters[0].Role)
	}
}

func TestMergeWorldBibles_DeduplicatesWorldMechanics(t *testing.T) {
	base := models.WorldBible{WorldMechanics: []string{"wolves have inner spirits"}}
	updated := models.WorldBible{WorldMechanics: []string{"wolves have inner spirits", "mates are destined"}}

	result := models.MergeWorldBibles(base, updated)

	if len(result.WorldMechanics) != 2 {
		t.Errorf("expected 2 mechanics (deduped), got %d: %v",
			len(result.WorldMechanics), result.WorldMechanics)
	}
}

func TestMergeWorldBibles_AppendsNewInteractions(t *testing.T) {
	base := models.WorldBible{
		CharacterInteractions: []models.CharacterInteraction{
			{Chapter: 5, Characters: []string{"Elara", "Kaelan"}, Medium: "transmission stone"},
		},
	}
	updated := models.WorldBible{
		CharacterInteractions: []models.CharacterInteraction{
			{Chapter: 7, Characters: []string{"Elara", "Kaelan"}, Medium: "transmission stone"},
		},
	}

	result := models.MergeWorldBibles(base, updated)

	if len(result.CharacterInteractions) != 2 {
		t.Errorf("expected 2 interactions, got %d", len(result.CharacterInteractions))
	}
}

func TestMergeWorldBibles_MagicObjectsUpsertByName(t *testing.T) {
	base := models.WorldBible{
		MagicObjects: []models.MagicObject{
			{Name: "Transmission Stone", Properties: []string{"pulses red when Emperor calls"}, FirstSeen: 5},
		},
	}
	updated := models.WorldBible{
		MagicObjects: []models.MagicObject{
			{Name: "Transmission Stone", Properties: []string{"pulses gold on second call"}, FirstSeen: 7},
		},
	}

	result := models.MergeWorldBibles(base, updated)

	if len(result.MagicObjects) != 1 {
		t.Errorf("expected 1 magic object (merged), got %d", len(result.MagicObjects))
	}
	mo := result.MagicObjects[0]
	if len(mo.Properties) != 2 {
		t.Errorf("expected 2 properties after merge, got %d: %v", len(mo.Properties), mo.Properties)
	}
	// FirstSeen should keep the earliest
	if mo.FirstSeen != 5 {
		t.Errorf("FirstSeen should be 5 (earliest), got %d", mo.FirstSeen)
	}
}
