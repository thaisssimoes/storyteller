"""Build single-file PDFs for wolfsbane-bride (completed) and vow-of-the-bloodthorne (in-progress)."""
import os
import re
import glob

from reportlab.lib.pagesizes import A4
from reportlab.lib.styles import getSampleStyleSheet, ParagraphStyle
from reportlab.lib.units import cm
from reportlab.lib.enums import TA_LEFT, TA_CENTER, TA_JUSTIFY
from reportlab.platypus import (
    SimpleDocTemplate, Paragraph, Spacer, PageBreak, KeepTogether
)


ROOT = r"c:\Users\tsuba\GolandProjects\InconsistencyFixer\output\writer"
OUT_DIR = ROOT
JOBS = [
    {
        "title": "Wolfsbane Bride",
        "src": os.path.join(ROOT, "Completed-stories", "wolfsbane-bride", "chapters"),
        "out": os.path.join(OUT_DIR, "Wolfsbane-Bride.pdf"),
    },
    {
        "title": "Vow of the Bloodthorne",
        "src": os.path.join(ROOT, "Completed-stories", "vow-of-the-bloodthorne", "chapters"),
        "out": os.path.join(OUT_DIR, "Vow-of-the-Bloodthorne.pdf"),
    },
]


def collect_chapters(src_dir):
    files = sorted(
        f for f in glob.glob(os.path.join(src_dir, "chapter_*.txt"))
        if "~$" not in os.path.basename(f)
    )
    out = []
    for path in files:
        m = re.search(r"chapter_(\d+)\.txt$", os.path.basename(path))
        if not m:
            continue
        num = int(m.group(1))
        with open(path, "r", encoding="utf-8") as fh:
            text = fh.read()
        out.append((num, text))
    return out


def escape(s):
    return (s.replace("&", "&amp;")
             .replace("<", "&lt;")
             .replace(">", "&gt;"))


def parse_chapter(num, raw_text):
    """Return (title, body_paragraphs).

    Body paragraphs: list of (kind, text) where kind in {pov, scene_break, para}.
    """
    lines = raw_text.splitlines()
    title = None
    i = 0
    while i < len(lines) and not lines[i].strip():
        i += 1
    if i < len(lines):
        first = lines[i].strip()
        if first.startswith("#"):
            title = re.sub(r"^#+\s*", "", first)
            i += 1
    if not title:
        title = f"Chapter {num}"

    body_text = "\n".join(lines[i:]).strip()
    blocks = re.split(r"\n\s*\n", body_text)

    out = []
    for block in blocks:
        block = block.strip()
        if not block:
            continue
        if re.fullmatch(r"\*\s*\*\s*\*", block):
            out.append(("scene_break", "* * *"))
            continue
        joined = " ".join(line.strip() for line in block.splitlines() if line.strip())
        if re.fullmatch(r"[A-Z][A-Za-z'’\- ]+'s POV", joined):
            out.append(("pov", joined))
            continue
        out.append(("para", joined))
    return title, out


def build_pdf(job):
    chapters = collect_chapters(job["src"])
    if not chapters:
        print(f"  No chapters found in {job['src']}")
        return

    doc = SimpleDocTemplate(
        job["out"],
        pagesize=A4,
        leftMargin=2.2*cm, rightMargin=2.2*cm,
        topMargin=2.2*cm, bottomMargin=2.2*cm,
        title=job["title"], author="",
    )

    base = getSampleStyleSheet()
    book_title = ParagraphStyle(
        "BookTitle", parent=base["Title"],
        fontName="Times-Bold", fontSize=28, leading=34,
        alignment=TA_CENTER, spaceAfter=18,
    )
    book_sub = ParagraphStyle(
        "BookSub", parent=base["Normal"],
        fontName="Times-Italic", fontSize=12, leading=16,
        alignment=TA_CENTER, spaceAfter=24,
    )
    chap_title = ParagraphStyle(
        "ChapTitle", parent=base["Heading1"],
        fontName="Times-Bold", fontSize=18, leading=22,
        alignment=TA_CENTER, spaceBefore=0, spaceAfter=18,
    )
    pov = ParagraphStyle(
        "POV", parent=base["Normal"],
        fontName="Times-Italic", fontSize=11, leading=14,
        alignment=TA_CENTER, spaceAfter=16,
    )
    scene = ParagraphStyle(
        "Scene", parent=base["Normal"],
        fontName="Times-Roman", fontSize=12, leading=16,
        alignment=TA_CENTER, spaceBefore=6, spaceAfter=6,
    )
    body = ParagraphStyle(
        "Body", parent=base["Normal"],
        fontName="Times-Roman", fontSize=11.5, leading=16,
        alignment=TA_JUSTIFY, firstLineIndent=14, spaceAfter=2,
    )

    story = []
    story.append(Spacer(1, 6*cm))
    story.append(Paragraph(escape(job["title"]), book_title))
    story.append(Paragraph(f"{len(chapters)} chapters", book_sub))
    story.append(PageBreak())

    for idx, (num, raw) in enumerate(chapters):
        title, blocks = parse_chapter(num, raw)
        head = f"Chapter {num}"
        if not re.match(rf"^Chapter\s+{num}\b", title, re.IGNORECASE):
            head = f"Chapter {num}: {title}"
        elif title and title != f"Chapter {num}":
            head = title
        story.append(Paragraph(escape(head), chap_title))
        for kind, text in blocks:
            if kind == "pov":
                story.append(Paragraph(escape(text), pov))
            elif kind == "scene_break":
                story.append(Paragraph(escape(text), scene))
            else:
                story.append(Paragraph(escape(text), body))
        if idx < len(chapters) - 1:
            story.append(PageBreak())

    doc.build(story)
    size_kb = os.path.getsize(job["out"]) / 1024
    print(f"  Wrote {job['out']} ({len(chapters)} chapters, {size_kb:,.0f} KB)")


def main():
    for job in JOBS:
        print(f"Building: {job['title']}")
        build_pdf(job)


if __name__ == "__main__":
    main()
