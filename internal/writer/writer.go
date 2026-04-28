package writer

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	_ "embed"

	"inconsistencyfixer/internal/claude"
	"inconsistencyfixer/internal/models"
	"inconsistencyfixer/internal/story"
)

//go:embed writer_prompt.md
var systemPrompt string

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

// ChapterOutline is the plan for one chapter.
type ChapterOutline struct {
	Number         int    `json:"number"`
	Title          string `json:"title"`
	Summary        string `json:"summary"`
	Purpose        string `json:"purpose"`
	DramaticMoment string `json:"dramaticMoment"`
	Hook           string `json:"hook"`
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

// Run drives the full writer pipeline: interview → outline → write chapters.
func Run(outputDir string, client *claude.Client) error {
	writerDir := filepath.Join(outputDir, "writer")
	chaptersDir := filepath.Join(writerDir, "chapters")
	if err := os.MkdirAll(chaptersDir, 0755); err != nil {
		return fmt.Errorf("creating writer dir: %w", err)
	}

	fmt.Println()
	fmt.Println("╔══════════════════════════════════════════╗")
	fmt.Println("║      InconsistencyFixer — Escritor       ║")
	fmt.Println("╚══════════════════════════════════════════╝")
	fmt.Println()

	prefs, err := interview()
	if err != nil {
		return fmt.Errorf("entrevista: %w", err)
	}

	outline, err := getOrCreateOutline(client, prefs, writerDir)
	if err != nil {
		return fmt.Errorf("esboço: %w", err)
	}

	return writeAllChapters(client, prefs, outline, writerDir, chaptersDir)
}

// ─── Interview ────────────────────────────────────────────────────────────────

func interview() (StoryPrefs, error) {
	fmt.Println("Vou fazer algumas perguntas antes de escrever sua história.")
	fmt.Println("Para cada pergunta, veja os exemplos e responda livremente.\n")

	ask := func(question string, examples []string) (string, error) {
		fmt.Println("──────────────────────────────────────────")
		fmt.Println(question)
		if len(examples) > 0 {
			fmt.Println("Exemplos:")
			for _, e := range examples {
				fmt.Println("  •", e)
			}
		}
		fmt.Print("\nSua resposta: ")
		r := bufio.NewReader(os.Stdin)
		line, err := r.ReadString('\n')
		fmt.Println()
		return strings.TrimSpace(line), err
	}

	askInt := func(question string, examples []string, min, max int) (int, error) {
		for {
			s, err := ask(question, examples)
			if err != nil {
				return 0, err
			}
			n, err := strconv.Atoi(strings.TrimSpace(s))
			if err != nil || n < min || n > max {
				fmt.Printf("  ⚠ Por favor, digite um número entre %d e %d.\n\n", min, max)
				continue
			}
			return n, nil
		}
	}

	var p StoryPrefs
	var err error

	p.Language, err = ask(
		"1. Em qual idioma a história deve ser escrita?",
		[]string{
			"Português (Brasil)",
			"English",
			"Español",
		},
	)
	if err != nil {
		return p, err
	}

	p.Genre, err = ask(
		"2. Qual é o gênero da história?",
		[]string{
			"Romance — amor, relacionamentos, emoção",
			"Fantasia — magia, mundos inventados, criaturas",
			"Thriller — suspense, perigo, mistério",
			"Drama — conflitos pessoais e emocionais intensos",
			"Ficção científica — tecnologia futurista, espaço, IA",
			"Dark romance — romance com elementos sombrios e complexos",
		},
	)
	if err != nil {
		return p, err
	}

	p.Protagonist, err = ask(
		"3. Descreva brevemente o(a) protagonista:",
		[]string{
			"Mulher de 28 anos, advogada ambiciosa com um segredo do passado",
			"Garoto de 16 anos com poderes mágicos que não sabe controlar",
			"Detetive cynical que recebe um caso ligado à sua família",
			"Herdeira de um reino que foi traída por quem amava",
		},
	)
	if err != nil {
		return p, err
	}

	p.Setting, err = ask(
		"4. Onde e quando a história acontece?",
		[]string{
			"Nova York contemporânea, arranha-céus e vida acelerada",
			"Reino medieval fictício em guerra com um império vizinho",
			"2150, colônia humana em Marte tentando sobreviver",
			"Uma pequena cidade do interior com segredos enterrados",
		},
	)
	if err != nil {
		return p, err
	}

	p.CentralConflict, err = ask(
		"5. Qual é o conflito central da história?",
		[]string{
			"Uma mulher traída descobre que foi marcada pelo irmão do seu ex",
			"Um herdeiro ilegítimo deve provar seu valor antes de usurpadores tomarem o trono",
			"Dois rivais de mundos opostos se apaixonam enquanto seus clãs entram em guerra",
			"Uma cientista descobre que a cura que criou tem um preço que não esperava pagar",
		},
	)
	if err != nil {
		return p, err
	}

	p.Tone, err = ask(
		"6. Qual o tom geral da história?",
		[]string{
			"Sombrio e intenso — poucos momentos de alívio, atmosfera pesada",
			"Equilibrado — drama real misturado com momentos de leveza e humor",
			"Esperançoso — mesmo com dificuldades, há sempre luz no horizonte",
			"Épico — grandioso, com stakes que afetam o mundo inteiro",
		},
	)
	if err != nil {
		return p, err
	}

	p.ChapterCount, err = askInt(
		"7. Quantos capítulos você quer? (digite um número)",
		[]string{
			"15 capítulos — história focada, ritmo rápido, sem subplots",
			"40 capítulos — desenvolvimento completo com 2-3 subplots",
			"80 capítulos — épico com múltiplos arcos e personagens secundários ricos",
		},
		5, 200,
	)
	if err != nil {
		return p, err
	}

	p.DramaLevel, err = askInt(
		"8. Nível de drama (1 a 10):",
		[]string{
			"1-3 → conflitos suaves; ex: casal briga e faz as pazes no mesmo capítulo",
			"4-6 → tensão crescente; ex: traição revelada que abala tudo, mas deixa esperança",
			"7-9 → intensidade alta; ex: personagem amado morre, protagonista perde tudo",
			"10  → tragédia; perdas permanentes, vitória (se houver) vem com cicatrizes profundas",
		},
		1, 10,
	)
	if err != nil {
		return p, err
	}

	p.TwistLevel, err = askInt(
		"9. Nível de plot twists (1 a 10):",
		[]string{
			"1-3 → previsível e confortável; o vilão é óbvio, o final é esperado",
			"4-6 → algumas reviravoltas; ex: o mentor estava do lado errado desde o início",
			"7-9 → reviravoltas frequentes; identidades falsas, alianças que se invertem",
			"10  → nada é o que parece; a realidade do mundo pode ser subvertida",
		},
		1, 10,
	)
	if err != nil {
		return p, err
	}

	p.SpecialRequests, err = ask(
		"10. Há algo específico que você quer incluir ou EVITAR? (deixe em branco se não houver)",
		[]string{
			"Incluir: uma cena de batalha épica no clímax",
			"Evitar: violência gráfica — a história é para jovens adultos",
			"Incluir: o antagonista deve ter uma jornada de redenção no final",
			"Evitar: desfechos abertos — quero tudo resolvido",
		},
	)
	if err != nil {
		return p, err
	}

	return p, nil
}

// ─── Outline ──────────────────────────────────────────────────────────────────

func getOrCreateOutline(client *claude.Client, prefs StoryPrefs, writerDir string) (StoryOutline, error) {
	outlinePath := filepath.Join(writerDir, "outline.json")

	if data, err := os.ReadFile(outlinePath); err == nil {
		var existing StoryOutline
		if json.Unmarshal(data, &existing) == nil && len(existing.Chapters) > 0 {
			fmt.Printf("Encontrei um esboço existente: %q (%d capítulos)\n", existing.Title, len(existing.Chapters))
			if askYesNo("Usar esse esboço e continuar de onde parou?") {
				return existing, nil
			}
		}
	}

	outline, err := generateAndApproveOutline(client, prefs)
	if err != nil {
		return StoryOutline{}, err
	}

	data, _ := json.MarshalIndent(outline, "", "  ")
	_ = os.WriteFile(outlinePath, data, 0644)
	return outline, nil
}

func generateAndApproveOutline(client *claude.Client, prefs StoryPrefs) (StoryOutline, error) {
	for {
		fmt.Println("\nGerando o esboço da história... (isso pode levar alguns instantes)")

		outline, err := generateOutline(client, prefs)
		if err != nil {
			return StoryOutline{}, fmt.Errorf("gerando esboço: %w", err)
		}

		displayOutline(outline)

		fmt.Println("\nO que deseja fazer?")
		fmt.Println("  s — Aprovar e começar a escrever")
		fmt.Println("  e — Pedir ajustes no esboço")
		fmt.Println("  r — Gerar um esboço completamente diferente")
		fmt.Print("\nEscolha: ")

		r := bufio.NewReader(os.Stdin)
		choice, _ := r.ReadString('\n')
		choice = strings.ToLower(strings.TrimSpace(choice))
		fmt.Println()

		switch choice {
		case "s", "sim", "y", "yes":
			return outline, nil
		case "e", "editar", "ajuste", "ajustar":
			fmt.Print("O que deve ser alterado no esboço? ")
			feedback, _ := r.ReadString('\n')
			prefs.SpecialRequests += "\n[AJUSTE SOLICITADO]: " + strings.TrimSpace(feedback)
		case "r", "refazer", "regenerar":
			// loop again
		default:
			fmt.Println("Digite 's', 'e' ou 'r'.")
		}
	}
}

func generateOutline(client *claude.Client, p StoryPrefs) (StoryOutline, error) {
	prompt := fmt.Sprintf(`Crie um esboço completo para uma história com estas especificações:

Idioma da história: %s
Gênero: %s
Protagonista: %s
Cenário: %s
Conflito central: %s
Tom: %s
Número de capítulos: %d
Nível de drama: %d/10
Nível de plot twists: %d/10
Pedidos especiais: %s

Siga rigorosamente as regras do seu script de escritor.

Distribua o arco dramático em três atos proporcionais ao número de capítulos.
Para o nível de twist %d/10, plante sementes visíveis antes de cada revelação.
Para o nível de drama %d/10, calibre as perdas e stakes de acordo.

Responda SOMENTE com JSON válido, sem markdown, sem explicações:
{
  "title": "título da história",
  "logline": "uma frase: quem é o protagonista, o que está em jogo, o que impede",
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
      "summary": "o que acontece neste capítulo — 2-3 frases",
      "purpose": "função narrativa: apresentar X, revelar Y, escalar Z",
      "dramaticMoment": "o momento mais tenso ou emocional do capítulo (ou vazio se não houver)",
      "hook": "o que fará o leitor precisar do próximo capítulo"
    }
  ]
}`,
		p.Language, p.Genre, p.Protagonist, p.Setting,
		p.CentralConflict, p.Tone, p.ChapterCount,
		p.DramaLevel, p.TwistLevel, p.SpecialRequests,
		p.TwistLevel, p.DramaLevel,
	)

	resp, err := client.CompleteWithSystem(context.Background(), systemPrompt, 8192, []claude.Message{
		claude.UserMessage(claude.TextBlock(prompt)),
	})
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

// ─── Chapter writing ──────────────────────────────────────────────────────────

func writeAllChapters(client *claude.Client, prefs StoryPrefs, outline StoryOutline, writerDir, chaptersDir string) error {
	outlineText := buildOutlineText(outline)
	outlineJSON, _ := json.MarshalIndent(outline, "", "  ")

	total := len(outline.Chapters)
	written := 0
	var prevContent string

	for _, chOutline := range outline.Chapters {
		chPath := filepath.Join(chaptersDir, fmt.Sprintf("chapter_%04d.txt", chOutline.Number))

		// Resume: load existing content if already written
		if data, err := os.ReadFile(chPath); err == nil {
			log.Printf("[%d/%d] Capítulo %d já existe — pulando", chOutline.Number, total, chOutline.Number)
			ch, parseErr := parseChapterFile(chPath, string(data))
			if parseErr == nil {
				prevContent = ch.Content
			}
			written++
			continue
		}

		log.Printf("[%d/%d] Escrevendo capítulo %d: %s...", chOutline.Number, total, chOutline.Number, chOutline.Title)

		content, err := writeChapter(client, prefs, outline, outlineText, string(outlineJSON), prevContent, chOutline)
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

	fmt.Printf("\n✓ História concluída! %d/%d capítulos escritos.\n", written, total)
	fmt.Printf("  História completa → %s\n", storyPath)
	fmt.Printf("  Capítulos         → %s\n", chaptersDir)
	fmt.Println()
	fmt.Println("Dica: rode 'go run . read' para verificar inconsistências na história gerada.")
	return nil
}

func writeChapter(
	client *claude.Client,
	prefs StoryPrefs,
	outline StoryOutline,
	outlineText, outlineJSON string,
	prevContent string,
	ch ChapterOutline,
) (string, error) {
	prev := ""
	if prevContent != "" {
		prev = fmt.Sprintf("\n\n[CAPÍTULO ANTERIOR — para continuidade imediata]\n%s", prevContent)
	}

	prompt := fmt.Sprintf(`Escreva o capítulo %d de "%s".

ESPECIFICAÇÕES:
- Idioma: %s
- Gênero: %s
- Nível de drama: %d/10
- Nível de plot twists: %d/10
- Tom: %s

PLANO DO CAPÍTULO %d — "%s":
Resumo: %s
Função narrativa: %s
Momento dramático: %s
Gancho para o próximo: %s

[ESBOÇO COMPLETO DA HISTÓRIA — para consistência de longo prazo]
%s
%s

Escreva agora o capítulo %d. Siga o plano, aplique os níveis de drama e twist, e retorne APENAS o texto do capítulo.`,
		ch.Number, outline.Title,
		prefs.Language, prefs.Genre,
		prefs.DramaLevel, prefs.TwistLevel, prefs.Tone,
		ch.Number, ch.Title,
		ch.Summary, ch.Purpose, ch.DramaticMoment, ch.Hook,
		outlineText,
		prev,
		ch.Number,
	)

	// Cache the outline JSON for reuse across all chapter calls
	resp, err := client.CompleteWithSystem(context.Background(), systemPrompt, 4096, []claude.Message{
		claude.UserMessage(
			claude.CachedTextBlock(fmt.Sprintf("[PERSONAGENS E TWISTS — fonte da verdade]\n%s", outlineJSON)),
			claude.TextBlock(prompt),
		),
	})
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(resp), nil
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

func parseChapterFile(path, raw string) (models.Chapter, error) {
	raw = strings.ReplaceAll(raw, "\r\n", "\n")
	nl := strings.Index(raw, "\n")
	if nl < 0 {
		return models.Chapter{}, fmt.Errorf("sem newline em %s", path)
	}
	body := strings.TrimSpace(raw[nl:])
	return models.Chapter{Content: body, Path: path}, nil
}
