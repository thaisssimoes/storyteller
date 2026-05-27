"""
Renumera os capítulos de Vow of the Bloodthorne integrando os capítulos
do Kael (arquivos *K.txt) na sequência principal.

Ordem de leitura planejada:
- 0001K vai antes de 0001 (capítulo 1 do livro final)
- 0001  vai depois (capítulo 2)
- etc.

Execute para gerar apenas o mapeamento sem renomear.
Execute com --apply para renomear de verdade.
"""

import os
import sys
from pathlib import Path

CHAPTERS_DIR = Path(
    r"c:\Users\tsuba\GolandProjects\InconsistencyFixer\output\writer"
    r"\Completed-stories\vow-of-the-bloodthorne\chapters-revised"
)

# Ordem de leitura: (numero_original, is_kael)
# K chapters são inseridos APÓS o capítulo Iseult do mesmo número base.
# Ex: 0010K vai depois de 0010 (e antes de 0011).
# Exceção: 0001K vai ANTES de 0001 (abre o livro).

READING_ORDER = [
    # (arquivo_base, is_kael)
    # PARTE 1: A infiltração (caps 1-50)
    ("0001K", True),   # Kael recebe a oferta
    ("0001",  False),
    ("0002",  False),
    ("0003",  False),
    ("0003K", True),   # Kael observa do corredor
    ("0004",  False),
    ("0005",  False),
    ("0006",  False),
    ("0007",  False),
    ("0007K", True),   # Kael: a decisão do compacto — ela trouxe o pino de volta
    ("0008",  False),
    ("0009",  False),
    ("0010",  False),
    ("0010K", True),   # Kael carrega ela ferida
    ("0011",  False),
    ("0012",  False),
    ("0013",  False),
    ("0014",  False),
    ("0014K", True),   # Kael: 12 minutos antes de Toren
    ("0015",  False),
    ("0016",  False),
    ("0017",  False),
    ("0018",  False),
    ("0019",  False),
    ("0020",  False),
    ("0020K", True),   # Kael: dia 11 — o vínculo aprofunda (ele sente o shift)
    ("0021",  False),
    ("0022",  False),
    ("0023",  False),
    ("0024",  False),
    ("0025",  False),
    ("0023K", True),   # Kael: manhã do dia 11 — ele parte antes do sino, sente a mudança
    ("0026",  False),
    ("0027",  False),
    # ("0028",  False),  # FALTANDO — capítulo perdido, criar depois
    ("0029",  False),
    ("0031K", True),   # Kael: na audiência — assiste o pai encarar Iseult
    ("0030",  False),
    ("0031",  False),
    ("0032",  False),
    ("0033",  False),
    ("0034",  False),
    ("0035",  False),
    ("0036",  False),
    ("0037",  False),
    ("0037K", True),   # Kael: ela pergunta sobre o vale — warded chamber, genealogia
    ("0038",  False),
    ("0039",  False),
    ("0040",  False),
    ("0040K", True),   # Kael: Brennan diz "decida"
    ("0041",  False),
    ("0042",  False),
    ("0043",  False),
    ("0044",  False),
    ("0045K", True),   # Kael: a confissão — ele se ajoelha, abre as mãos
    ("0045",  False),
    ("0046",  False),
    ("0047",  False),
    ("0051K", True),   # Kael: o Ball — pai deposto, ela do outro lado do salão
    ("0048",  False),
    ("0049",  False),
    ("0050",  False),
    # PARTE 2: O Sanctum e a junção (caps 51-100)
    ("0051",  False),
    ("0052",  False),
    ("0053",  False),
    ("0054",  False),
    ("0054K", True),   # Kael: o Sanctum, madrugada — realiza que estava errado
    ("0059K", True),   # Kael: manhã depois do Sanctum — ela faz pão, saem para o sul
    ("0055",  False),
    ("0056",  False),
    ("0057",  False),
    ("0058",  False),
    ("0059",  False)
    ("0060",  False),
    ("0061",  False),
    ("0062",  False),
    ("0063",  False),
    ("0063K", True),   # Kael: Hallowmere, o meio-segundo
    ("0064",  False),
    ("0065",  False),
    ("0066",  False),
    ("0067",  False),
    ("0068",  False),
    ("0067K", True),   # Kael: assembleia formal — ela responde Scholar Pellerin
    ("0069",  False),
    ("0070",  False),
    ("0071",  False),
    ("0072",  False),
    ("0073",  False),
    ("0074",  False),
    ("0074K", True),   # Kael: o scholar força a ward — ele intervém no corredor
    ("0075",  False),
    ("0076",  False),
    ("0077",  False),
    ("0078",  False),
    ("0079",  False),
    ("0079K", True),   # Kael: véspera da cerimônia da junção — 84 anos
    ("0080",  False),
    ("0081",  False),
    ("0081K", True),   # Kael: a cerimônia da junção
    ("0082",  False),
    ("0083",  False),
    ("0084",  False),
    ("0085",  False),
    ("0085K", True),   # Kael: ela acorda antes dele
    ("0086",  False),
    ("0087",  False),
    ("0088",  False),
    ("0089",  False),
    ("0090",  False),
    ("0091",  False),
    ("0091K", True),   # Kael: manhã das 43 petições — o segundo escrivaninha
    ("0092",  False),
    ("0093",  False),
    ("0094",  False),
    ("0095",  False),
    ("0096",  False),
    ("0096K", True),   # Kael: "eu estava errado"
    ("0097",  False),
    ("0098",  False),
    ("0099",  False),
    ("0100K", True),   # Kael: o terceiro "eu te amo"
    # PARTE 3: Pós-junção (caps 101-135)
    ("0101",  False),
    ("0102",  False),
    ("0103",  False),
    ("0104",  False),
    ("0105",  False),
    ("0106",  False),
    ("0107",  False),
    ("0107K", True),   # Kael: petição Aldara — ela vê a conexão Vael sem que ele diga
    ("0108",  False),
    ("0109",  False),
    ("0110",  False),
    ("0111",  False),
    ("0112",  False),
    ("0113",  False),
    ("0113K", True),   # Kael: os números da longevidade
    ("0114",  False),
    ("0115",  False),
    ("0116",  False),
    ("0117",  False),
    ("0118",  False),
    ("0119",  False),
    ("0120",  False),
    ("0120K", True),   # Kael: Naia diz o nome do pai dela — ele decide contar
    ("0121",  False),
    ("0122",  False),
    ("0123",  False),
    ("0124",  False),
    ("0125",  False),
    ("0126",  False),
    ("0127",  False),
    ("0128",  False),
    ("0129",  False),
    ("0130",  False),
    ("0131",  False),
    ("0131K", True),   # Kael: torre leste na véspera da viagem — ele vai com ela
    ("0132",  False),
    ("0133",  False),
    ("0134",  False),
    ("0135",  False),
    # PARTE 4: Wolf Moon (caps 136+)
    ("0136",  False),
    ("0137K", True),   # Kael: a decisão de contar — arquivo: chapter_0137K.txt
    ("0138",  False),
    ("0139",  False),
    # PARTE 5: O chalé e o Wolf Moon
    ("0151",  False),  # Iseult: a contagem na pedra da lareira
    ("0152K", True),   # Kael: a noite do Wolf Moon no chalé
    ("0153",  False),  # Iseult: a noite do Wolf Moon — cena 5
    ("0154",  False),  # Iseult: manhã depois
    ("0155K", True),   # Kael: a estrada de volta
    ("0156",  False),  # Iseult: retorno ao Citadel — encerramento
]


def get_filename(base: str, is_kael: bool) -> str:
    # base may already end with K (e.g. "0001K"); don't double it
    if is_kael and not base.endswith("K"):
        return f"chapter_{base}K.txt"
    return f"chapter_{base}.txt"


def main():
    apply = "--apply" in sys.argv

    print(f"Diretório: {CHAPTERS_DIR}")
    print(f"Modo: {'RENOMEAR' if apply else 'SIMULAÇÃO (use --apply para renomear)'}")
    print()

    missing = []
    found = []

    for base, is_kael in READING_ORDER:
        fname = get_filename(base, is_kael)
        fpath = CHAPTERS_DIR / fname
        if fpath.exists():
            found.append((base, is_kael, fname))
        else:
            missing.append(fname)

    print(f"Encontrados: {len(found)}")
    print(f"Faltando: {len(missing)}")
    if missing:
        print("\nArquivos faltando:")
        for m in missing:
            print(f"  {m}")

    print(f"\nOrdem de leitura final: {len(found)} capítulos")
    print()

    # Gerar nova numeração
    renames = []
    for i, (base, is_kael, fname) in enumerate(found, start=1):
        new_name = f"chapter_{i:04d}.txt"
        pov = "Kael" if is_kael else "Iseult"
        renames.append((fname, new_name, pov))

    if apply and renames:
        print("\nRenomeando...")
        # Primeiro para nomes temporários para evitar conflitos
        temp_renames = []
        for old_name, new_name, pov in renames:
            old_path = CHAPTERS_DIR / old_name
            temp_path = CHAPTERS_DIR / f"_temp_{old_name}"
            os.rename(old_path, temp_path)
            temp_renames.append((temp_path, CHAPTERS_DIR / new_name))

        # Depois para os nomes finais
        for temp_path, final_path in temp_renames:
            os.rename(temp_path, final_path)

        print(f"Renomeados: {len(renames)} arquivos.")
    elif apply:
        print("Nada para renomear.")


if __name__ == "__main__":
    main()
