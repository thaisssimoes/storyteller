"""
Varre todos os capítulos e lista frases com mais de THRESHOLD caracteres,
ordenadas por comprimento descendente.
"""
import re
from pathlib import Path

CHAPTERS_DIR = Path(
    r"c:\Users\tsuba\GolandProjects\InconsistencyFixer\output\writer"
    r"\Completed-stories\vow-of-the-bloodthorne\chapters-revised"
)
THRESHOLD = 220  # caracteres

def read_chapter(path):
    raw = path.read_bytes()
    if raw[:2] in (b'\xff\xfe', b'\xfe\xff'):
        return raw.decode("utf-16")
    elif raw[:3] == b'\xef\xbb\xbf':
        return raw.decode("utf-8-sig")
    return raw.decode("utf-8", errors="replace")

# Divide texto em frases simples (split em '.', '!', '?')
SENT_RE = re.compile(r'[^.!?]+[.!?]+')

results = []

files = sorted(CHAPTERS_DIR.glob("chapter_*.txt"))
for f in files:
    content = read_chapter(f)
    # Skip POV header line
    lines = content.strip().split("\n")
    body = "\n".join(lines[1:])
    # Split into sentences
    for sent in SENT_RE.finditer(body):
        s = sent.group().strip()
        # Remove scene-break markers
        if s in ("* * *", "*"):
            continue
        if len(s) >= THRESHOLD:
            results.append((len(s), f.name, s))

results.sort(reverse=True)

print(f"Frases >= {THRESHOLD} chars: {len(results)}\n")
print(f"{'Chars':>6}  {'Arquivo':<30}  Frase")
print("-" * 100)
for length, fname, sent in results[:60]:
    preview = sent[:120].replace("\n", " ")
    print(f"{length:>6}  {fname:<30}  {preview}{'...' if len(sent) > 120 else ''}")
