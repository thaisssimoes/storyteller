package writer

import (
	"bufio"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"inconsistencyfixer/internal/claude"
	"inconsistencyfixer/internal/fixer"
	"inconsistencyfixer/internal/models"
	"inconsistencyfixer/internal/reader"
	"inconsistencyfixer/internal/story"
)

//go:embed writer_prompt.md
var systemPrompt string

const (
	outlineMaxTokens = 12288
	metaMaxTokens    = 4096 // meta-only: title + logline + characters + twists, no chapters
	chapterMaxTokens = 10240

	outlineTimeout = 30 * time.Minute
	chapterTimeout = 15 * time.Minute
)

// ─── Data types ───────────────────────────────────────────────────────────────

// StoryPrefs holds everything collected during the interview.
type StoryPrefs struct {
	Language        string
	Genre           string
	Protagonist     string
	Setting         string
	CentralConflict string
	Tone            string
	ChapterCount    int
	DramaLevel      int
	TwistLevel      int
	SpecialRequests string
}

// CharacterSheet is a full profile for one character.
type CharacterSheet struct {
	Name         string   `json:"name"`
	Age          string   `json:"age"`
	Race         string   `json:"race"`
	Role         string   `json:"role"` // "protagonist" | "love interest" | "antagonist" | "supporting"
	Appearance   string   `json:"appearance"`
	Personality  string   `json:"personality"`
	Backstory    string   `json:"backstory"`
	Motivation   string   `json:"motivation"`
	Fear         string   `json:"fear"`
	Interests    []string `json:"interests"`
	Powers       []string `json:"powers"`
	Limitations  []string `json:"limitations"`
	CharacterArc string   `json:"characterArc"`
	Voice        string   `json:"voice"` // speech style and verbal patterns
}

// PlotSummary is the high-level story arc before chapter-level planning.
type PlotSummary struct {
	Premise  string   `json:"premise"`
	Act1     string   `json:"act1"`
	Act2     string   `json:"act2"`
	Act3     string   `json:"act3"`
	Subplots []string `json:"subplots"`
	Themes   []string `json:"themes"`
	Twists   []string `json:"twists"`
}

// ChapterOutline is the plan for one chapter.
type ChapterOutline struct {
	Number         int      `json:"number"`
	Title          string   `json:"title"`
	Summary        string   `json:"summary"`
	Purpose        string   `json:"purpose"`
	DramaticMoment string   `json:"dramaticMoment"`
	Hook           string   `json:"hook"`
	POV            string   `json:"pov"`           // whose perspective narrates this chapter
	OpeningAnchor  string   `json:"openingAnchor"` // "continues from ch N" OR "X hours/days later, location"
	Scenes         []string `json:"scenes"`        // 3-5 ordered scene beats: what happens, who, what changes
}

// StoryOutline is the full story plan.
type StoryOutline struct {
	Title          string             `json:"title"`
	Logline        string             `json:"logline"`
	DramaticArc    string             `json:"dramaticArc"`
	MainCharacters []models.Character `json:"mainCharacters"`
	KeyTwists      []string           `json:"keyTwists"`
	Chapters       []ChapterOutline   `json:"chapters"`
}

// ─── Entry point ──────────────────────────────────────────────────────────────

// Run drives the full writer pipeline: interview → outline → write chapters →
// consistency check → fix → final story.
func Run(outputDir string, pair *claude.Pair) error {
	fmt.Println()
	fmt.Println("╔══════════════════════════════════════════╗")
	fmt.Println("║      InconsistencyFixer — Escritor       ║")
	fmt.Println("╚══════════════════════════════════════════╝")
	fmt.Println()
	log.Printf("Provider: %s | Robust: %s | Fast: %s",
		pair.Provider(), pair.Robust.Model(), pair.Fast.Model())

	prefs, err := interview()
	if err != nil {
		return fmt.Errorf("entrevista: %w", err)
	}

	writerBase := filepath.Join(outputDir, "writer")
	if err := os.MkdirAll(writerBase, 0755); err != nil {
		return fmt.Errorf("creating writer base dir: %w", err)
	}

	sheets, err := approveCharacterSheets(pair, prefs)
	if err != nil {
		return fmt.Errorf("fichas de personagens: %w", err)
	}

	plotSummary, err := approvePlotSummary(pair, prefs, sheets)
	if err != nil {
		return fmt.Errorf("sumário do plot: %w", err)
	}

	outline, writerDir, err := resolveOutlineAndDir(pair, prefs, sheets, plotSummary, writerBase)
	if err != nil {
		return fmt.Errorf("esboço: %w", err)
	}

	// Persist character sheets and plot summary alongside the story files
	if data, _ := json.MarshalIndent(sheets, "", "  "); data != nil {
		_ = os.WriteFile(filepath.Join(writerDir, "character_sheets.json"), data, 0644)
	}
	if data, _ := json.MarshalIndent(plotSummary, "", "  "); data != nil {
		_ = os.WriteFile(filepath.Join(writerDir, "plot_summary.json"), data, 0644)
	}

	chaptersDir := filepath.Join(writerDir, "chapters")
	if err := os.MkdirAll(chaptersDir, 0755); err != nil {
		return fmt.Errorf("creating writer dir: %w", err)
	}

	if err := writeAllChapters(pair, prefs, outline, sheets, writerDir, chaptersDir); err != nil {
		return fmt.Errorf("escrevendo capítulos: %w", err)
	}

	// ── Self-check: run reader + fixer over the writer's own output ──
	fmt.Println()
	fmt.Println("╔══════════════════════════════════════════╗")
	fmt.Println("║     Verificação de consistência final     ║")
	fmt.Println("╚══════════════════════════════════════════╝")
	fmt.Println()

	log.Println("Rodando o leitor sobre os capítulos gerados (3 passes)...")
	if err := reader.RunDir(writerDir, "chapters", pair); err != nil {
		log.Printf("Aviso: leitor falhou: %v", err)
		log.Println("Continuando — você pode rodar 'go run . read' manualmente apontando para o diretório da história")
	} else {
		log.Println("Rodando o fixer para resolver as inconsistências encontradas...")
		if err := fixer.RunDir(writerDir, "chapters", pair); err != nil {
			log.Printf("Aviso: fixer falhou: %v", err)
		} else {
			log.Println("✓ História final corrigida em " + filepath.Join(writerDir, "story_fixed.txt"))
		}
	}

	return nil
}

// ─── Interview ────────────────────────────────────────────────────────────────

// romanticTropes is the pick-list presented to the user. The model uses the
// selected entry as the creative anchor; everything else (protagonist, setting,
// tone, drama/twist level) is decided by the model to match the genre.
var romanticTropes = []struct{ name, desc string }{
	{"Fated Mates", "par destinado, vínculo de alma, reconhecimento instantâneo"},
	{"Secret Baby", "bebê secreto — pai poderoso que não sabe que tem um filho"},
	{"Enemies to Lovers", "inimigos forçados a conviver que se apaixonam"},
	{"Rejected Mate", "rejeitada pelo par destinado, que se transforma e é reivindicada"},
	{"Arranged Marriage", "casamento forçado entre dois desconhecidos"},
	{"Hidden Identity", "identidade oculta — nobreza ou poder revelado no 3° ato"},
	{"Second Chance", "amor do passado que reaparece com stakes mais altos"},
}

func interview() (StoryPrefs, error) {
	fmt.Println("Três perguntas rápidas e o modelo cuida do resto.")
	fmt.Println()

	r := bufio.NewReader(os.Stdin)

	// ── Pergunta 1: idioma ────────────────────────────────────────────────────
	fmt.Println("──────────────────────────────────────────")
	fmt.Println("1. Em qual idioma a história deve ser escrita?")
	fmt.Println("   Exemplos: Português (Brasil) · English · Español")
	fmt.Print("\nSua resposta: ")
	lang, err := r.ReadString('\n')
	fmt.Println()
	if err != nil {
		return StoryPrefs{}, err
	}
	lang = strings.TrimSpace(lang)
	if lang == "" {
		lang = "Português (Brasil)"
	}

	// ── Pergunta 2: número de capítulos ──────────────────────────────────────
	fmt.Println("──────────────────────────────────────────")
	fmt.Println("2. Quantos capítulos você quer?")
	fmt.Println("   40 = história completa  ·  80 = webnovel padrão  ·  120 = saga longa")

	var chapterCount int
	for {
		fmt.Print("\nNúmero de capítulos: ")
		s, readErr := r.ReadString('\n')
		fmt.Println()
		if readErr != nil {
			return StoryPrefs{}, readErr
		}
		n, convErr := strconv.Atoi(strings.TrimSpace(s))
		if convErr != nil || n < 3 || n > 200 {
			fmt.Println("  ⚠ Digite um número entre 3 e 200.")
			continue
		}
		chapterCount = n
		break
	}

	// ── Pergunta 3: trope ─────────────────────────────────────────────────────
	fmt.Println("──────────────────────────────────────────")
	fmt.Println("3. Escolha uma trope (ou Enter para o modelo escolher):")
	fmt.Println()
	for i, t := range romanticTropes {
		fmt.Printf("  %d. %-22s %s\n", i+1, t.name, t.desc)
	}
	fmt.Print("\nSua escolha (1-7 ou Enter): ")
	choiceStr, err := r.ReadString('\n')
	fmt.Println()
	if err != nil {
		return StoryPrefs{}, err
	}
	choiceStr = strings.TrimSpace(choiceStr)

	// Accept "1", "1 e 2", "1,2", etc. — pick the first valid number found.
	selectedTrope := ""
	if choiceStr != "" {
		for _, field := range strings.FieldsFunc(choiceStr, func(r rune) bool {
			return r == ',' || r == ' ' || r == 'e' || r == 'E' || r == '/'
		}) {
			if idx, convErr := strconv.Atoi(strings.TrimSpace(field)); convErr == nil && idx >= 1 && idx <= len(romanticTropes) {
				t := romanticTropes[idx-1]
				selectedTrope = t.name + ": " + t.desc
				break
			}
		}
	}

	// ── Assemble prefs — model decides the rest ───────────────────────────────
	conflict := selectedTrope
	special := ""
	if selectedTrope == "" {
		conflict = "(escolha a melhor trope romantasa para esta história)"
		special = "Escolha uma das tropes clássicas: fated mates, secret baby, enemies to lovers, rejected mate, arranged marriage, hidden identity, ou second chance."
	}

	return StoryPrefs{
		Language:        lang,
		Genre:           "Romantasia (romance + fantasia, lobisomens/vampiros/fae, hierarquia alpha, magia)",
		Protagonist:     "(decida você — heroína romantasa: mulher jovem com poder ou identidade oculta, starts low, rises)",
		Setting:         "(decida você — escolha qualquer cenário clássico: reino medieval de lobisomens, império com corte alpha, ou corte sobrenatural)",
		CentralConflict: conflict,
		Tone:            "slow-burn com momentos spicy, tensão emocional crescente",
		ChapterCount:    chapterCount,
		DramaLevel:      6,
		TwistLevel:      5,
		SpecialRequests: special,
	}, nil
}

// ─── Outline ──────────────────────────────────────────────────────────────────

// resolveOutlineAndDir generates (or resumes) a story outline and returns the
// approved outline together with the title-based output directory.
//
// Flow:
//  1. Generate outline and get user approval.
//  2. Derive a slug from the title and build the candidate writerDir.
//  3. If that dir already has a valid outline.json → ask to continue.
//     - Yes: return existing outline + dir.
//     - No: loop — generate a fresh outline (new title expected).
//  4. No existing dir → save outline and return.
func resolveOutlineAndDir(pair *claude.Pair, prefs StoryPrefs, sheets []CharacterSheet, summary PlotSummary, outputDir string) (StoryOutline, string, error) {
	for {
		outline, err := generateAndApproveOutline(pair, prefs, sheets, summary)
		if err != nil {
			return StoryOutline{}, "", err
		}

		slug := titleToSlug(outline.Title)
		writerDir := filepath.Join(outputDir, slug)
		outlinePath := filepath.Join(writerDir, "outline.json")

		if data, err := os.ReadFile(outlinePath); err == nil {
			var existing StoryOutline
			if json.Unmarshal(data, &existing) == nil && len(existing.Chapters) > 0 {
				fmt.Printf("\nJá existe uma história em %q (%d capítulos planejados).\n", writerDir, len(existing.Chapters))
				if askYesNo("Continuar de onde parou?") {
					return existing, writerDir, nil
				}
				fmt.Println("Gerando uma história com título diferente...")
				continue
			}
		}

		if err := os.MkdirAll(writerDir, 0755); err != nil {
			return StoryOutline{}, "", fmt.Errorf("criando diretório da história: %w", err)
		}
		data, _ := json.MarshalIndent(outline, "", "  ")
		_ = os.WriteFile(outlinePath, data, 0644)
		log.Printf("✓ Diretório da história: %s", writerDir)
		return outline, writerDir, nil
	}
}

// titleToSlug converts a story title into a safe directory name.
// "O Lobo da Corte Sombria" → "o-lobo-da-corte-sombria"
func titleToSlug(title string) string {
	accents := strings.NewReplacer(
		"á", "a", "à", "a", "ã", "a", "â", "a", "ä", "a",
		"é", "e", "è", "e", "ê", "e", "ë", "e",
		"í", "i", "ì", "i", "î", "i", "ï", "i",
		"ó", "o", "ò", "o", "õ", "o", "ô", "o", "ö", "o",
		"ú", "u", "ù", "u", "û", "u", "ü", "u",
		"ç", "c", "ñ", "n",
		"Á", "a", "À", "a", "Ã", "a", "Â", "a",
		"É", "e", "Ê", "e",
		"Í", "i", "Î", "i",
		"Ó", "o", "Õ", "o", "Ô", "o",
		"Ú", "u", "Û", "u",
		"Ç", "c",
	)
	s := accents.Replace(strings.ToLower(title))

	var sb strings.Builder
	prevHyphen := false
	for _, r := range s {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			sb.WriteRune(r)
			prevHyphen = false
		case r == ' ' || r == '-' || r == '_':
			if !prevHyphen {
				sb.WriteRune('-')
				prevHyphen = true
			}
		}
	}

	slug := strings.Trim(sb.String(), "-")
	if len(slug) > 60 {
		slug = slug[:60]
		slug = strings.TrimRight(slug, "-")
	}
	if slug == "" {
		slug = "historia"
	}
	return slug
}

// ─── Character Sheets ─────────────────────────────────────────────────────────

func approveCharacterSheets(pair *claude.Pair, prefs StoryPrefs) ([]CharacterSheet, error) {
	r := bufio.NewReader(os.Stdin)
	fmt.Println()
	fmt.Println("╔══════════════════════════════════════════╗")
	fmt.Println("║        Passo 1: Fichas de Personagens     ║")
	fmt.Println("╚══════════════════════════════════════════╝")

	var sheets []CharacterSheet
	for {
		if sheets == nil {
			fmt.Println("\nGerando fichas de personagens (Robust)...")
			var err error
			sheets, err = generateCharacterSheets(pair.Robust, prefs)
			if err != nil {
				return nil, err
			}
		}

		displayCharacterSheets(sheets)
		fmt.Println("  s — Fichas aprovadas!")
		fmt.Println("  e — Quero mudar algo")
		fmt.Println("  r — Gerar personagens diferentes")
		fmt.Print("\nEscolha: ")

		choice, _ := r.ReadString('\n')
		choice = strings.ToLower(strings.TrimSpace(choice))
		fmt.Println()

		switch choice {
		case "s", "sim", "y", "yes", "":
			return sheets, nil
		case "e", "editar", "mudar":
			fmt.Print("O que você quer mudar? ")
			feedback, _ := r.ReadString('\n')
			feedback = strings.TrimSpace(feedback)
			fmt.Println()
			if feedback == "" {
				fmt.Println("Nenhuma sugestão digitada.")
				continue
			}
			fmt.Println("Aplicando sugestão...")
			revised, err := refineCharacterSheets(pair.Robust, prefs, sheets, feedback)
			if err != nil {
				log.Printf("  Aviso: não foi possível aplicar (%v)", err)
				continue
			}
			sheets = revised
		case "r", "refazer":
			sheets = nil
		default:
			fmt.Println("Digite 's', 'e' ou 'r'.")
		}
	}
}

func generateCharacterSheets(client *claude.Client, p StoryPrefs) ([]CharacterSheet, error) {
	prompt := fmt.Sprintf(`Crie fichas detalhadas para os personagens principais de uma história com estas especificações:

Idioma da história: %s
Gênero: %s
Conflito central: %s
Tom: %s
Número de capítulos: %d
Pedidos especiais: %s

Gere fichas para: protagonista feminina, interesse romântico masculino, antagonista, e 1-2 personagens de suporte importantes.
Cada ficha deve ser completa e internamente consistente. Os poderes (se houver) devem ter limitações claras.
O arco de personagem deve refletir crescimento real ao longo dos %d capítulos.

Responda SOMENTE com JSON válido, sem markdown:
{
  "characters": [
    {
      "name": "nome original e único",
      "age": "idade ou faixa etária",
      "race": "raça/espécie",
      "role": "protagonist | love interest | antagonist | supporting",
      "appearance": "descrição física detalhada",
      "personality": "traços de personalidade, quirks, contradições",
      "backstory": "passado relevante que molda quem ela/ele é hoje",
      "motivation": "o que quer e por quê",
      "fear": "o maior medo ou fraqueza emocional",
      "interests": ["interesse 1", "interesse 2"],
      "powers": ["poder 1 com descrição", "poder 2"],
      "limitations": ["limitação 1", "limitação 2"],
      "characterArc": "como muda do início ao fim da história",
      "voice": "como fala — tom, vocabulário, padrões verbais"
    }
  ]
}`,
		p.Language, p.Genre, p.CentralConflict, p.Tone, p.ChapterCount, p.SpecialRequests, p.ChapterCount)

	ctx, cancel := context.WithTimeout(context.Background(), outlineTimeout)
	defer cancel()
	resp, err := client.CompleteEx(ctx, systemPrompt, []claude.Message{
		claude.UserMessage(claude.TextBlock(prompt)),
	}, claude.Options{MaxTokens: outlineMaxTokens, JSONMode: true})
	if err != nil {
		return nil, err
	}

	jsonStr := extractJSON(resp)
	var wrap struct {
		Characters []CharacterSheet `json:"characters"`
	}
	if err := json.Unmarshal([]byte(jsonStr), &wrap); err != nil {
		return nil, fmt.Errorf("parse das fichas: %w\nResposta: %.400s", err, resp)
	}
	return wrap.Characters, nil
}

func refineCharacterSheets(client *claude.Client, p StoryPrefs, sheets []CharacterSheet, feedback string) ([]CharacterSheet, error) {
	existing, _ := json.MarshalIndent(map[string]any{"characters": sheets}, "", "  ")
	prompt := fmt.Sprintf(`Estas são as fichas de personagens atuais:

%s

O usuário quer a seguinte mudança: "%s"

Revise apenas o que foi pedido. Mantenha tudo o mais intacto.
Responda SOMENTE com JSON válido no mesmo formato.`, string(existing), feedback)

	ctx, cancel := context.WithTimeout(context.Background(), outlineTimeout)
	defer cancel()
	resp, err := client.CompleteEx(ctx, systemPrompt, []claude.Message{
		claude.UserMessage(claude.TextBlock(prompt)),
	}, claude.Options{MaxTokens: outlineMaxTokens, JSONMode: true})
	if err != nil {
		return nil, err
	}
	jsonStr := extractJSON(resp)
	var wrap struct {
		Characters []CharacterSheet `json:"characters"`
	}
	if err := json.Unmarshal([]byte(jsonStr), &wrap); err != nil {
		return nil, err
	}
	return wrap.Characters, nil
}

func displayCharacterSheets(sheets []CharacterSheet) {
	fmt.Printf("\n╔══ FICHAS DE PERSONAGENS (%d) ══╗\n\n", len(sheets))
	for _, s := range sheets {
		fmt.Printf("▸ %s — %s | %s | %s\n", s.Name, s.Role, s.Age, s.Race)
		fmt.Printf("  Aparência:    %s\n", s.Appearance)
		fmt.Printf("  Personalidade:%s\n", s.Personality)
		fmt.Printf("  Passado:      %s\n", s.Backstory)
		fmt.Printf("  Motivação:    %s\n", s.Motivation)
		fmt.Printf("  Medo:         %s\n", s.Fear)
		if len(s.Powers) > 0 {
			fmt.Printf("  Poderes:      %s\n", strings.Join(s.Powers, " / "))
			fmt.Printf("  Limitações:   %s\n", strings.Join(s.Limitations, " / "))
		}
		fmt.Printf("  Arco:         %s\n", s.CharacterArc)
		fmt.Printf("  Voz:          %s\n", s.Voice)
		fmt.Println()
	}
}

// ─── Plot Summary ─────────────────────────────────────────────────────────────

func approvePlotSummary(pair *claude.Pair, prefs StoryPrefs, sheets []CharacterSheet) (PlotSummary, error) {
	r := bufio.NewReader(os.Stdin)
	fmt.Println()
	fmt.Println("╔══════════════════════════════════════════╗")
	fmt.Println("║         Passo 2: Sumário do Plot          ║")
	fmt.Println("╚══════════════════════════════════════════╝")

	var summary PlotSummary
	empty := true
	for {
		if empty {
			fmt.Println("\nGerando sumário do plot (Robust)...")
			var err error
			summary, err = generatePlotSummary(pair.Robust, prefs, sheets)
			if err != nil {
				return PlotSummary{}, err
			}
			empty = false
		}

		displayPlotSummary(summary)
		fmt.Println("  s — Sumário aprovado!")
		fmt.Println("  e — Quero mudar algo")
		fmt.Println("  r — Gerar sumário diferente")
		fmt.Print("\nEscolha: ")

		choice, _ := r.ReadString('\n')
		choice = strings.ToLower(strings.TrimSpace(choice))
		fmt.Println()

		switch choice {
		case "s", "sim", "y", "yes", "":
			return summary, nil
		case "e", "editar", "mudar":
			fmt.Print("O que você quer mudar? ")
			feedback, _ := r.ReadString('\n')
			feedback = strings.TrimSpace(feedback)
			fmt.Println()
			if feedback == "" {
				fmt.Println("Nenhuma sugestão digitada.")
				continue
			}
			fmt.Println("Aplicando sugestão...")
			revised, err := refinePlotSummary(pair.Robust, prefs, sheets, summary, feedback)
			if err != nil {
				log.Printf("  Aviso: não foi possível aplicar (%v)", err)
				continue
			}
			summary = revised
		case "r", "refazer":
			empty = true
		default:
			fmt.Println("Digite 's', 'e' ou 'r'.")
		}
	}
}

func generatePlotSummary(client *claude.Client, p StoryPrefs, sheets []CharacterSheet) (PlotSummary, error) {
	sheetsJSON, _ := json.MarshalIndent(map[string]any{"characters": sheets}, "", "  ")
	prompt := fmt.Sprintf(`Com base nestas fichas de personagens e nas especificações abaixo, crie o sumário do plot em três atos.

FICHAS DE PERSONAGENS:
%s

ESPECIFICAÇÕES:
Idioma: %s | Gênero: %s | Tom: %s | Capítulos: %d
Conflito central: %s
Nível de drama: %d/10 | Nível de twists: %d/10
Pedidos especiais: %s

O sumário deve ser específico: eventos reais, não abstrações. Cada ato deve ter beats claros.
Os twists devem estar plantados antes de serem revelados.

Responda SOMENTE com JSON válido:
{
  "premise": "conflito central e stakes em 2 frases",
  "act1": "capítulos 1 a N: o que acontece — apresentação, inciting incident, primeiro encontro",
  "act2": "capítulos N a M: escalada, midpoint, reviravolta, ponto mais sombrio",
  "act3": "capítulos M ao fim: confronto, resolução, destino de cada personagem",
  "subplots": ["subplot 1", "subplot 2"],
  "themes": ["tema 1", "tema 2"],
  "twists": ["twist 1: o que é e quando acontece aproximadamente", "twist 2"]
}`,
		string(sheetsJSON),
		p.Language, p.Genre, p.Tone, p.ChapterCount,
		p.CentralConflict, p.DramaLevel, p.TwistLevel, p.SpecialRequests)

	ctx, cancel := context.WithTimeout(context.Background(), outlineTimeout)
	defer cancel()
	resp, err := client.CompleteEx(ctx, systemPrompt, []claude.Message{
		claude.UserMessage(claude.TextBlock(prompt)),
	}, claude.Options{MaxTokens: metaMaxTokens, JSONMode: true})
	if err != nil {
		return PlotSummary{}, err
	}
	jsonStr := extractJSON(resp)
	var summary PlotSummary
	if err := json.Unmarshal([]byte(jsonStr), &summary); err != nil {
		return PlotSummary{}, fmt.Errorf("parse do sumário: %w\nResposta: %.400s", err, resp)
	}
	return summary, nil
}

func refinePlotSummary(client *claude.Client, p StoryPrefs, sheets []CharacterSheet, summary PlotSummary, feedback string) (PlotSummary, error) {
	existing, _ := json.MarshalIndent(summary, "", "  ")
	sheetsJSON, _ := json.MarshalIndent(map[string]any{"characters": sheets}, "", "  ")
	prompt := fmt.Sprintf(`Sumário atual:
%s

Fichas de personagens (para referência):
%s

O usuário quer: "%s"

Revise apenas o que foi pedido. Responda SOMENTE com JSON válido no mesmo formato.`,
		string(existing), string(sheetsJSON), feedback)

	ctx, cancel := context.WithTimeout(context.Background(), outlineTimeout)
	defer cancel()
	resp, err := client.CompleteEx(ctx, systemPrompt, []claude.Message{
		claude.UserMessage(claude.TextBlock(prompt)),
	}, claude.Options{MaxTokens: metaMaxTokens, JSONMode: true})
	if err != nil {
		return PlotSummary{}, err
	}
	jsonStr := extractJSON(resp)
	var revised PlotSummary
	if err := json.Unmarshal([]byte(jsonStr), &revised); err != nil {
		return PlotSummary{}, err
	}
	return revised, nil
}

func displayPlotSummary(s PlotSummary) {
	fmt.Println("\n╔══ SUMÁRIO DO PLOT ══╗")
	fmt.Printf("\nPremissa: %s\n", s.Premise)
	fmt.Printf("\nAto 1: %s\n", s.Act1)
	fmt.Printf("\nAto 2: %s\n", s.Act2)
	fmt.Printf("\nAto 3: %s\n", s.Act3)
	if len(s.Subplots) > 0 {
		fmt.Printf("\nSubplots:\n")
		for _, sp := range s.Subplots {
			fmt.Printf("  • %s\n", sp)
		}
	}
	if len(s.Themes) > 0 {
		fmt.Printf("\nTemas: %s\n", strings.Join(s.Themes, " · "))
	}
	if len(s.Twists) > 0 {
		fmt.Printf("\nTwists planejados:\n")
		for i, t := range s.Twists {
			fmt.Printf("  %d. %s\n", i+1, t)
		}
	}
	fmt.Println()
}

// ─── Outline ──────────────────────────────────────────────────────────────────

func generateAndApproveOutline(pair *claude.Pair, prefs StoryPrefs, sheets []CharacterSheet, summary PlotSummary) (StoryOutline, error) {
	r := bufio.NewReader(os.Stdin)
	fmt.Println()
	fmt.Println("╔══════════════════════════════════════════╗")
	fmt.Println("║       Passo 3: Esboço por Capítulo        ║")
	fmt.Println("╚══════════════════════════════════════════╝")
	for {
		fmt.Println("\nGerando esboço dos capítulos...")
		fmt.Println("(Isso pode levar alguns minutos. Vou imprimir o progresso.)")

		outline, err := generateOutline(pair, prefs, sheets, summary)
		if err != nil {
			return StoryOutline{}, fmt.Errorf("gerando esboço: %w", err)
		}

		for {
			displaySynopsis(outline)

			fmt.Println("  s — Adorei, pode escrever!")
			fmt.Println("  e — Quero sugerir uma mudança")
			fmt.Println("  r — Gerar uma história completamente diferente")
			fmt.Print("\nEscolha: ")

			choice, _ := r.ReadString('\n')
			choice = strings.ToLower(strings.TrimSpace(choice))
			fmt.Println()

			switch choice {
			case "s", "sim", "y", "yes", "":
				return outline, nil

			case "e", "editar", "edit", "mudar":
				fmt.Print("O que você quer mudar? ")
				feedback, _ := r.ReadString('\n')
				feedback = strings.TrimSpace(feedback)
				fmt.Println()
				if feedback == "" {
					fmt.Println("Nenhuma sugestão digitada. Tente novamente.")
					continue
				}
				fmt.Println("Aplicando sua sugestão (Robust)...")
				revised, revErr := refineOutline(pair.Robust, prefs, outline, feedback)
				if revErr != nil {
					log.Printf("  Aviso: não foi possível aplicar a sugestão (%v) — mostrando esboço original", revErr)
					continue
				}
				outline = revised
				// loop back to show the updated synopsis

			case "r", "refazer", "regenerar":
				break // break inner loop → outer loop generates fresh outline

			default:
				fmt.Println("Digite 's', 'e' ou 'r'.")
				continue
			}

			// "r" breaks the inner loop; everything else continues it
			if choice == "r" || choice == "refazer" || choice == "regenerar" {
				break
			}
		}
	}
}

// refineOutline asks the model to revise an existing outline based on user feedback,
// keeping everything that was not explicitly mentioned unchanged.
func refineOutline(client *claude.Client, prefs StoryPrefs, outline StoryOutline, feedback string) (StoryOutline, error) {
	existing, _ := json.MarshalIndent(outline, "", "  ")

	prompt := fmt.Sprintf(`Você criou este esboço para uma história:

%s

O usuário quer a seguinte mudança:
"%s"

Revise o esboço incorporando exatamente o que foi pedido. Mantenha tudo o que não foi mencionado (título, personagens, capítulos não afetados, twists, etc.). Não invente mudanças além do solicitado.

O array "chapters" deve continuar com EXATAMENTE %d entradas.

Responda SOMENTE com JSON válido no mesmo formato do esboço acima, sem markdown, sem explicações.`,
		string(existing), feedback, len(outline.Chapters))

	resp, err := callOutline(client, prompt, outlineMaxTokens)
	if err != nil {
		return StoryOutline{}, err
	}

	jsonStr := extractJSON(resp)
	var revised StoryOutline
	if err := json.Unmarshal([]byte(jsonStr), &revised); err != nil {
		return StoryOutline{}, fmt.Errorf("parse do esboço revisado: %w", err)
	}
	if len(revised.Chapters) == 0 {
		revised.Chapters = outline.Chapters
	}
	return revised, nil
}

// generateOutline produces the full StoryOutline. For long stories (>30 chapters)
// the work is split into two distinct phases to avoid truncation:
//  1. Meta only (title, logline, arc, characters, twists) — small response,
//     generated with the Robust model.
//  2. Chapters in chunks of 20 — each chunk is a focused call.
//     The first chunk uses the Robust model; the rest use Fast.
//
// For short stories (<=30 chapters) a single call produces everything.
func generateOutline(pair *claude.Pair, p StoryPrefs, sheets []CharacterSheet, summary PlotSummary) (StoryOutline, error) {
	const chunkSize = 8

	if p.ChapterCount <= 30 {
		const maxAttempts = 3
		for attempt := 1; attempt <= maxAttempts; attempt++ {
			log.Printf("  Gerando esboço completo em uma única chamada (Robust) — tentativa %d/%d...", attempt, maxAttempts)
			outline, err := generateOutlineSingle(pair.Robust, p, sheets, summary)
			if err != nil {
				log.Printf("    tentativa %d falhou: %v", attempt, err)
				continue
			}
			if len(outline.Chapters) < p.ChapterCount {
				log.Printf("    outline gerado com %d capítulos em vez de %d — tentando novamente", len(outline.Chapters), p.ChapterCount)
				continue
			}
			return outline, nil
		}
		return StoryOutline{}, fmt.Errorf("não foi possível gerar outline com %d capítulos após %d tentativas", p.ChapterCount, maxAttempts)
	}

	log.Printf("  Esboço longo (%d capítulos). Meta no Robust, capítulos em blocos.", p.ChapterCount)

	meta, err := generateOutlineMeta(pair.Robust, p, sheets, summary)
	if err != nil {
		return StoryOutline{}, fmt.Errorf("meta: %w", err)
	}
	log.Printf("  ✓ Meta: %q | %d personagens | %d twists", meta.Title, len(meta.MainCharacters), len(meta.KeyTwists))

	for len(meta.Chapters) < p.ChapterCount {
		startCh := len(meta.Chapters) + 1
		endCh := min(startCh+chunkSize-1, p.ChapterCount)

		client := pair.Fast
		if startCh == 1 {
			client = pair.Robust
		}
		log.Printf("  Gerando capítulos %d-%d/%d...", startCh, endCh, p.ChapterCount)

		moreCh, err := generateOutlineChapters(client, p, meta, startCh, endCh)
		if err != nil {
			return StoryOutline{}, fmt.Errorf("capítulos %d-%d: %w", startCh, endCh, err)
		}
		meta.Chapters = append(meta.Chapters, moreCh...)
		log.Printf("  ✓ Capítulos %d-%d gerados", startCh, endCh)
	}

	return meta, nil
}

func generateOutlineSingle(client *claude.Client, p StoryPrefs, sheets []CharacterSheet, summary PlotSummary) (StoryOutline, error) {
	sheetsJSON, _ := json.MarshalIndent(map[string]any{"characters": sheets}, "", "  ")
	summaryJSON, _ := json.MarshalIndent(summary, "", "  ")

	prompt := fmt.Sprintf(`Crie um esboço capítulo a capítulo com base nas fichas de personagens e no sumário do plot abaixo.

FICHAS DE PERSONAGENS:
%s

SUMÁRIO DO PLOT:
%s

ESPECIFICAÇÕES:
Idioma: %s | Gênero: %s | Tom: %s
Número de capítulos: %d | Drama: %d/10 | Twists: %d/10
Pedidos especiais: %s

OBRIGATÓRIO: o array "chapters" deve conter EXATAMENTE %d entradas, numeradas de 1 a %d.
Cada capítulo deve ter 3 a 5 cenas concretas no campo "scenes".
O campo "openingAnchor" deve indicar onde a cena começa em relação ao capítulo anterior.
Se o JSON ficar grande, reduza "summary" a 1 frase — mas gere todos os %d capítulos com todas as cenas.

Responda SOMENTE com JSON válido, sem markdown:
%s`,
		string(sheetsJSON), string(summaryJSON),
		p.Language, p.Genre, p.Tone,
		p.ChapterCount, p.DramaLevel, p.TwistLevel, p.SpecialRequests,
		p.ChapterCount, p.ChapterCount, p.ChapterCount,
		outlineSchema,
	)

	resp, err := callOutline(client, prompt, outlineMaxTokens)
	if err != nil {
		return StoryOutline{}, err
	}

	jsonStr := extractJSON(resp)
	var outline StoryOutline
	if err := json.Unmarshal([]byte(jsonStr), &outline); err != nil {
		return StoryOutline{}, fmt.Errorf("parse do esboço: %w\nResposta: %.500s", err, resp)
	}
	return outline, nil
}

// metaOnlySchema is a compact version of outlineSchema with an empty chapters
// array. Used for the meta-only call in long-story generation so the model
// does not try to write chapters — those come from separate chunk calls.
const metaOnlySchema = `{
  "title": "título da história",
  "logline": "uma frase: quem é a protagonista, o que está em jogo, o que impede",
  "dramaticArc": "arco dramático em 2-3 frases: ato 1 / ato 2 / ato 3",
  "mainCharacters": [
    {
      "name": "nome completo",
      "aliases": [],
      "description": "aparência e personalidade em 2-3 frases",
      "role": "protagonist|antagonist|supporting",
      "relationships": ["descrição dos relacionamentos"],
      "status": "alive",
      "firstAppearance": 1
    }
  ],
  "keyTwists": ["descrição do twist — sem revelar o capítulo"],
  "chapters": []
}`

// generateOutlineMeta produces ONLY the creative foundation of the story:
// title, logline, dramaticArc, mainCharacters, and keyTwists. The chapters
// array is intentionally empty — chapter generation is done in separate chunk
// calls. Keeping this response small prevents the truncation that occurred
// when the old spine call tried to generate meta + 20 chapters at once.
func generateOutlineMeta(client *claude.Client, p StoryPrefs, sheets []CharacterSheet, summary PlotSummary) (StoryOutline, error) {
	sheetsJSON, _ := json.MarshalIndent(map[string]any{"characters": sheets}, "", "  ")
	summaryJSON, _ := json.MarshalIndent(summary, "", "  ")

	prompt := fmt.Sprintf(`Crie a espinha criativa de uma história com base nas fichas e no sumário abaixo.

FICHAS DE PERSONAGENS:
%s

SUMÁRIO DO PLOT:
%s

ESPECIFICAÇÕES:
Idioma: %s | Gênero: %s | Tom: %s
Capítulos totais: %d | Drama: %d/10 | Twists: %d/10
Pedidos especiais: %s

Gere APENAS: título, sinopse de uma linha, arco dramático (2-3 frases),
personagens principais e twists planejados.
A lista "chapters" deve ser um array VAZIO.

Responda SOMENTE com JSON válido:
%s`,
		string(sheetsJSON), string(summaryJSON),
		p.Language, p.Genre, p.Tone, p.ChapterCount,
		p.DramaLevel, p.TwistLevel, p.SpecialRequests,
		metaOnlySchema,
	)

	resp, err := callOutline(client, prompt, metaMaxTokens)
	if err != nil {
		return StoryOutline{}, err
	}
	jsonStr := extractJSON(resp)
	var outline StoryOutline
	if err := json.Unmarshal([]byte(jsonStr), &outline); err != nil {
		return StoryOutline{}, fmt.Errorf("parse da meta: %w\nResposta: %.500s", err, resp)
	}
	return outline, nil
}

// generateOutlineChapters produces only ChapterOutline entries [startCh, endCh],
// using the existing spine as anchor.
func generateOutlineChapters(client *claude.Client, p StoryPrefs, spine StoryOutline, startCh, endCh int) ([]ChapterOutline, error) {
	spineJSON, _ := json.MarshalIndent(spine, "", "  ")

	prompt := fmt.Sprintf(`Continue o esboço da história "%s". Use a espinha abaixo como verdade absoluta —
NÃO invente novos personagens principais nem mude twists planejados.

[ESPINHA EXISTENTE]
%s

Agora gere os capítulos %d a %d (de %d totais). Calibre o ritmo sabendo onde
estes capítulos caem dentro do arco de três atos:
  - Ato 1 (capítulos 1 a %d): apresentação
  - Ato 2 (capítulos %d a %d): escalada (ponto mais sombrio nos últimos 20%% deste ato)
  - Ato 3 (capítulos %d a %d): confronto e resolução

Cada capítulo deve incluir um gancho que torne o próximo irresistível.
Para o nível de twist %d/10, plante e/ou pague twists prometidos na espinha.

Responda SOMENTE com JSON válido neste formato:
{
  "chapters": [
    {
      "number": %d,
      "title": "string",
      "pov": "nome do narrador deste capítulo",
      "openingAnchor": "'continua do cap N' OU 'X horas/dias depois, local'",
      "summary": "1 frase objetiva sobre o que acontece",
      "scenes": [
        "cena 1: o que acontece, quem, o que muda",
        "cena 2: ...",
        "cena 3: ..."
      ],
      "purpose": "função narrativa",
      "dramaticMoment": "momento mais tenso",
      "hook": "gancho para o próximo"
    }
  ]
}`,
		spine.Title,
		string(spineJSON),
		startCh, endCh, p.ChapterCount,
		p.ChapterCount/5,
		p.ChapterCount/5+1, 4*p.ChapterCount/5,
		4*p.ChapterCount/5+1, p.ChapterCount,
		p.TwistLevel,
		startCh,
	)

	resp, err := callOutline(client, prompt, outlineMaxTokens)
	if err != nil {
		return nil, err
	}
	jsonStr := extractJSON(resp)

	var wrap struct {
		Chapters []ChapterOutline `json:"chapters"`
	}
	if err := json.Unmarshal([]byte(jsonStr), &wrap); err != nil {
		return nil, fmt.Errorf("parse de capítulos: %w\nResposta: %.500s", err, resp)
	}

	// Force-correct numbering in case the model drifts
	for i := range wrap.Chapters {
		expected := startCh + i
		if wrap.Chapters[i].Number != expected {
			wrap.Chapters[i].Number = expected
		}
	}
	return wrap.Chapters, nil
}

// callOutline wraps the LLM call with the long timeout used for outline-style
// generations. On a parse failure it retries once with the same token budget —
// NOT with halved tokens, which would worsen truncation for responses that have
// a minimum size (e.g. a chapter outline with six fields per entry).
func callOutline(client *claude.Client, prompt string, maxTokens int) (string, error) {
	tryOnce := func() (string, error) {
		ctx, cancel := context.WithTimeout(context.Background(), outlineTimeout)
		defer cancel()
		return client.CompleteEx(ctx, systemPrompt, []claude.Message{
			claude.UserMessage(claude.TextBlock(prompt)),
		}, claude.Options{MaxTokens: maxTokens, JSONMode: true})
	}

	resp, err := tryOnce()
	if err == nil && jsonParseable(resp) {
		return resp, nil
	}
	if err != nil {
		log.Printf("    primeira tentativa falhou (%v) — tentando novamente", err)
	} else {
		log.Println("    resposta não passou no parse — tentando novamente")
	}
	resp2, err2 := tryOnce()
	if err2 != nil {
		if err != nil {
			return "", fmt.Errorf("duas tentativas falharam: %v; %v", err, err2)
		}
		return "", err2
	}
	return resp2, nil
}

func jsonParseable(s string) bool {
	js := extractJSON(s)
	var any any
	return json.Unmarshal([]byte(js), &any) == nil
}

const outlineSchema = `{
  "title": "título da história",
  "logline": "uma frase: quem é a protagonista, o que está em jogo, o que impede",
  "dramaticArc": "descrição do arco dramático em 2-3 frases",
  "mainCharacters": [
    {
      "name": "nome completo",
      "aliases": ["apelidos"],
      "description": "aparência física e personalidade em 2-3 frases",
      "role": "protagonist|antagonist|supporting",
      "relationships": ["descrição dos relacionamentos"],
      "status": "alive",
      "firstAppearance": 1
    }
  ],
  "keyTwists": ["descrição do twist — não revele em qual capítulo"],
  "chapters": [
    {
      "number": 1,
      "title": "título do capítulo",
      "pov": "nome do personagem que narra (protagonista na maioria; interesse romântico ou outro em capítulos pontuais)",
      "openingAnchor": "para cap 1: âncora sensorial de lugar/tempo. Para demais: 'continua do cap N' OU 'X horas/dias depois, local'",
      "summary": "o que acontece neste capítulo — 1 frase objetiva",
      "scenes": [
        "cena 1: o que acontece, quem está presente, o que muda",
        "cena 2: ...",
        "cena 3: ...",
        "cena 4 (opcional)",
        "cena 5 (opcional)"
      ],
      "purpose": "função narrativa: apresentar X, revelar Y, escalar Z",
      "dramaticMoment": "o momento mais tenso ou emocional do capítulo",
      "hook": "o que fará a leitora precisar do próximo capítulo"
    }
  ]
}`

// ─── Chapter writing ──────────────────────────────────────────────────────────

// writeAllChapters writes each planned chapter using the ROBUST model — prose
// quality is the deliverable, this is not a place for the fast model.
func writeAllChapters(pair *claude.Pair, prefs StoryPrefs, outline StoryOutline, sheets []CharacterSheet, writerDir, chaptersDir string) error {
	client := pair.Robust
	outlineText := buildOutlineText(outline)
	outlineJSON, _ := json.MarshalIndent(outline, "", "  ")
	sheetsJSON, _ := json.MarshalIndent(map[string]any{"characters": sheets}, "", "  ")

	total := len(outline.Chapters)
	written := 0
	var prevContent string

	for _, chOutline := range outline.Chapters {
		chPath := filepath.Join(chaptersDir, fmt.Sprintf("chapter_%04d.txt", chOutline.Number))

		// Resume: load existing content if already written
		if data, err := os.ReadFile(chPath); err == nil {
			log.Printf("[%d/%d] Capítulo %d já existe — pulando", chOutline.Number, total, chOutline.Number)
			if ch, parseErr := parseChapterFile(chPath, string(data)); parseErr == nil {
				prevContent = ch.Content
			}
			written++
			continue
		}

		log.Printf("[%d/%d] Escrevendo capítulo %d: %s...", chOutline.Number, total, chOutline.Number, chOutline.Title)

		content, err := writeChapter(client, prefs, outline, outlineText, string(outlineJSON), string(sheetsJSON), prevContent, chOutline)
		if err != nil {
			log.Printf("  Erro no capítulo %d: %v", chOutline.Number, err)
			continue
		}

		ch := models.Chapter{
			Number:  chOutline.Number,
			Title:   chOutline.Title,
			Content: content,
		}
		if err := story.SaveChapter(chaptersDir, ch); err != nil {
			log.Printf("  Falha ao salvar capítulo %d: %v", chOutline.Number, err)
			continue
		}

		prevContent = content
		written++
		log.Printf("  ✓ Capítulo %d escrito (%d palavras aprox.)", chOutline.Number, wordCount(content))
	}

	// Assemble final story
	chapters, err := story.LoadChapters(chaptersDir)
	if err != nil {
		return fmt.Errorf("carregando capítulos: %w", err)
	}

	storyPath := filepath.Join(writerDir, "story.txt")
	if err := story.WriteStory(storyPath, chapters); err != nil {
		return fmt.Errorf("salvando história: %w", err)
	}

	// Save world bible from outline for use with reader/fixer
	biblePath := filepath.Join(writerDir, "world_bible.json")
	bible := models.WorldBible{
		Characters: outline.MainCharacters,
	}
	if bdata, err := json.MarshalIndent(bible, "", "  "); err == nil {
		_ = os.WriteFile(biblePath, bdata, 0644)
	}

	fmt.Printf("\n✓ Primeira passada concluída: %d/%d capítulos escritos.\n", written, total)
	fmt.Printf("  História bruta    → %s\n", storyPath)
	fmt.Printf("  Capítulos         → %s\n", chaptersDir)
	return nil
}

func writeChapter(
	client *claude.Client,
	prefs StoryPrefs,
	outline StoryOutline,
	outlineText, outlineJSON, sheetsJSON string,
	prevContent string,
	ch ChapterOutline,
) (string, error) {
	prev := ""
	if prevContent != "" {
		// Only the last ~150 words — just enough to know where the scene ended.
		// Passing more risks the model treating it as already-written content for
		// this chapter and either repeating it or continuing mid-scene incorrectly.
		tail := lastWords(prevContent, 150)
		prev = fmt.Sprintf("\n\n[ÚLTIMAS LINHAS DO CAPÍTULO ANTERIOR — o capítulo atual começa DEPOIS deste ponto. NÃO repita este conteúdo.]\n%s", tail)
	}

	// Build the ordered scene list for the prompt
	scenePlan := ""
	if len(ch.Scenes) > 0 {
		var sb strings.Builder
		for i, s := range ch.Scenes {
			sb.WriteString(fmt.Sprintf("  %d. %s\n", i+1, s))
		}
		scenePlan = sb.String()
	} else {
		scenePlan = "  (sem cenas detalhadas — siga o resumo e o momento dramático)\n"
	}

	pov := ch.POV
	if pov == "" {
		pov = "protagonista"
	}

	openingInstruction := ""
	if ch.Number == 1 || ch.OpeningAnchor == "" {
		openingInstruction = "Abra com âncora sensorial (cheiro, som, temperatura ou toque) para instalar o leitor na cena."
	} else {
		openingInstruction = fmt.Sprintf("Abertura: %s — NÃO repita conteúdo do capítulo anterior. Comece no exato ponto onde a história continua.", ch.OpeningAnchor)
	}

	prompt := fmt.Sprintf(`Escreva o capítulo %d de "%s".

ESPECIFICAÇÕES:
- Idioma: %s
- Gênero: %s
- Nível de drama: %d/10
- Nível de plot twists: %d/10
- Tom: %s
- Narrador deste capítulo: %s

PLANO DO CAPÍTULO %d — "%s":
Resumo: %s
Função narrativa: %s
Momento dramático: %s
Gancho para o próximo: %s

CENAS (escreva nesta ordem, desenvolvendo cada uma com diálogo e reações):
%s
[CONTEXTO DE CONTINUIDADE]
%s

[ESBOÇO COMPLETO DA HISTÓRIA — para consistência de longo prazo]
%s

ESTILO OBRIGATÓRIO (webnovel de romantasia):
1. Primeira linha: nome do narrador seguido de "POV" (ex: "Aria's POV").
2. %s
3. Comprimento obrigatório: mínimo 1500 palavras, máximo 4500 palavras. Desenvolva cada cena com diálogo, sensações e reações — não resuma.
4. Parágrafos de 1 a 4 frases. Linha em branco entre cada parágrafo.
5. Revelações e choques emocionais em linhas isoladas de uma frase.
6. A voz interna do espírito/lobo aparece APENAS com asteriscos de itálico (*assim*). NUNCA use '>' ou qualquer marcador markdown.
7. Emoção é física: mãos que tremem, visão que estreita, estômago que despenca.
8. Narre em 1ª pessoa. Nunca em 3ª.
9. Personagens estabelecidos como presentes na cena permanecem até saírem on-page. Vestuário só muda quando trocado on-page.

Retorne APENAS o texto do capítulo (sem título, sem comentários, sem meta-texto).`,
		ch.Number, outline.Title,
		prefs.Language, prefs.Genre,
		prefs.DramaLevel, prefs.TwistLevel, prefs.Tone,
		pov,
		ch.Number, ch.Title,
		ch.Summary, ch.Purpose, ch.DramaticMoment, ch.Hook,
		scenePlan,
		prev,
		outlineText,
		openingInstruction,
	)

	const minWords = 1500
	const maxContinuations = 3

	ctx, cancel := context.WithTimeout(context.Background(), chapterTimeout)
	defer cancel()

	cachedOutline := claude.CachedTextBlock(fmt.Sprintf("[FICHAS DE PERSONAGENS — fonte da verdade]\n%s\n\n[ESBOÇO — estrutura da história]\n%s", sheetsJSON, outlineJSON))

	resp, err := client.CompleteEx(ctx, systemPrompt, []claude.Message{
		claude.UserMessage(cachedOutline, claude.TextBlock(prompt)),
	}, claude.Options{MaxTokens: chapterMaxTokens})
	if err != nil {
		return "", err
	}
	content := strings.TrimSpace(resp)

	for i := 0; i < maxContinuations && wordCount(content) < minWords; i++ {
		missing := minWords - wordCount(content)
		log.Printf("    capítulo com %d palavras (mínimo %d) — continuando... (%d/%d)",
			wordCount(content), minWords, i+1, maxContinuations)

		// Multi-turn: assistant "already wrote" content, user asks to continue.
		// This prevents the model from restarting the chapter from scratch.
		contMessages := []claude.Message{
			claude.UserMessage(cachedOutline, claude.TextBlock(prompt)),
			{Role: "assistant", Content: []claude.ContentBlock{claude.TextBlock(content)}},
			claude.UserMessage(claude.TextBlock(fmt.Sprintf(
				"The chapter is only %d words but needs at least %d. "+
					"Continue writing directly from where you stopped — do NOT start over, do NOT repeat anything already written. "+
					"Add at least %d more words of new content.",
				wordCount(content), minWords, missing,
			))),
		}

		contCtx, contCancel := context.WithTimeout(context.Background(), chapterTimeout)
		continuation, contErr := client.CompleteEx(contCtx, systemPrompt, contMessages,
			claude.Options{MaxTokens: chapterMaxTokens})
		contCancel()

		if contErr != nil {
			log.Printf("    continuação %d falhou: %v", i+1, contErr)
			break
		}
		content = content + "\n\n" + strings.TrimSpace(continuation)
	}

	if wc := wordCount(content); wc < minWords {
		log.Printf("    aviso: capítulo finalizado com %d palavras (abaixo do mínimo %d)", wc, minWords)
	}

	return content, nil
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

func buildOutlineText(o StoryOutline) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Título: %s\nSinopse: %s\n\nCapítulos:\n", o.Title, o.Logline))
	for _, ch := range o.Chapters {
		sb.WriteString(fmt.Sprintf("  %02d. %s: %s\n", ch.Number, ch.Title, ch.Summary))
	}
	return sb.String()
}

func displaySynopsis(o StoryOutline) {
	fmt.Printf("\n╔══ %s ══╗\n\n", o.Title)
	fmt.Printf("%s\n\n", o.Logline)
}

func displayOutline(o StoryOutline) {
	fmt.Printf("\n╔══ ESBOÇO: %s ══╗\n", o.Title)
	fmt.Printf("Sinopse: %s\n", o.Logline)
	fmt.Printf("Arco dramático: %s\n\n", o.DramaticArc)

	fmt.Printf("PERSONAGENS (%d):\n", len(o.MainCharacters))
	for _, c := range o.MainCharacters {
		fmt.Printf("  • %s (%s): %s\n", c.Name, c.Role, c.Description)
	}

	if len(o.KeyTwists) > 0 {
		fmt.Printf("\nPLOT TWISTS PLANEJADOS (%d):\n", len(o.KeyTwists))
		for i, t := range o.KeyTwists {
			fmt.Printf("  %d. %s\n", i+1, t)
		}
	}

	fmt.Printf("\nCAPÍTULOS (%d):\n", len(o.Chapters))
	for _, ch := range o.Chapters {
		fmt.Printf("  %02d. %-35s %s\n", ch.Number, ch.Title, ch.Summary)
		if ch.Hook != "" {
			fmt.Printf("      ↳ Gancho: %s\n", ch.Hook)
		}
	}
	fmt.Println()
}

func askYesNo(question string) bool {
	r := bufio.NewReader(os.Stdin)
	fmt.Printf("%s (s/n): ", question)
	line, _ := r.ReadString('\n')
	line = strings.ToLower(strings.TrimSpace(line))
	fmt.Println()
	return line == "s" || line == "sim" || line == "y" || line == "yes"
}

func extractJSON(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") {
		if nl := strings.Index(s, "\n"); nl >= 0 {
			s = s[nl+1:]
		}
		s = strings.TrimSuffix(strings.TrimSpace(s), "```")
		s = strings.TrimSpace(s)
	}
	if start := strings.Index(s, "{"); start >= 0 {
		if end := strings.LastIndex(s, "}"); end > start {
			return s[start : end+1]
		}
	}
	return s
}

func wordCount(s string) int {
	return len(strings.Fields(s))
}

// lastWords returns the last n words of s, preserving paragraph breaks.
func lastWords(s string, n int) string {
	words := strings.Fields(s)
	if len(words) <= n {
		return s
	}
	return strings.Join(words[len(words)-n:], " ")
}

func parseChapterFile(path, raw string) (models.Chapter, error) {
	raw = strings.ReplaceAll(raw, "\r\n", "\n")
	nl := strings.Index(raw, "\n")
	if nl < 0 {
		return models.Chapter{}, fmt.Errorf("sem newline em %s", path)
	}
	body := strings.TrimSpace(raw[nl:])
	return models.Chapter{Content: body, Path: path}, nil
}
