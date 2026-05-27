"""
Gera EPUB de Vow of the Bloodthorne a partir dos capítulos revisados.
"""

import os
import re
import zipfile
from pathlib import Path
from html import escape

CHAPTERS_DIR = Path(r"c:\Users\tsuba\GolandProjects\InconsistencyFixer\output\writer\Completed-stories\vow-of-the-bloodthorne\chapters-revised")
OUTPUT_PATH = Path(r"c:\Users\tsuba\GolandProjects\InconsistencyFixer\output\writer\Completed-stories\vow-of-the-bloodthorne\vow-of-the-bloodthorne-revisado.epub")

TITLE = "Vow of the Bloodthorne"
AUTHOR = "Thais Simoes"
LANGUAGE = "en"
IDENTIFIER = "vow-of-the-bloodthorne-revisado-2026"


def text_to_xhtml(raw: str, slug: str) -> str:
    lines = raw.strip().split("\n")

    # First line: POV header
    pov_line = lines[0].strip() if lines else ""
    body_lines = lines[1:] if len(lines) > 1 else []

    # Join body, split into paragraphs by double newline
    body_text = "\n".join(body_lines)
    paragraphs_raw = re.split(r"\n{2,}", body_text)

    html_parts = []
    for para in paragraphs_raw:
        para = para.strip()
        if not para:
            continue
        if para in ("* * *", "*"):
            html_parts.append('<div class="scene-break">* * *</div>')
        else:
            # Convert *italic* markers
            para_escaped = escape(para)
            para_escaped = re.sub(r"\*(.*?)\*", r"<em>\1</em>", para_escaped)
            # Treat each line break within a paragraph as a line break
            para_escaped = para_escaped.replace("\n", "<br/>")
            html_parts.append(f"<p>{para_escaped}</p>")

    body_html = "\n    ".join(html_parts)
    # Chapter number: strip leading zeros
    num_str = slug.rstrip("K").lstrip("0") or "0"
    # POV name from first line ("Kael's POV" → "Kael", "Iseult's POV" → "Iseult")
    pov_name = pov_line.replace("'s POV", "").replace("'s Pov", "").strip()
    if not pov_name:
        pov_name = "Kael" if slug.endswith("K") else "Iseult"
    chapter_title = f"Chapter {num_str} — {pov_name}"

    return f"""<?xml version="1.0" encoding="utf-8"?>
<!DOCTYPE html>
<html xmlns="http://www.w3.org/1999/xhtml" xml:lang="{LANGUAGE}">
<head>
  <meta charset="utf-8"/>
  <title>{escape(chapter_title)}</title>
  <link rel="stylesheet" type="text/css" href="../style.css"/>
</head>
<body>
  <h1 class="chapter-title">{escape(chapter_title)}</h1>
  {body_html}
</body>
</html>"""


COVER_XHTML = f"""<?xml version="1.0" encoding="utf-8"?>
<!DOCTYPE html>
<html xmlns="http://www.w3.org/1999/xhtml" xml:lang="en">
<head>
  <meta charset="utf-8"/>
  <title>Cover</title>
  <link rel="stylesheet" type="text/css" href="../style.css"/>
  <style>
    body {{ margin: 0; padding: 0; background-color: #0d0d0d; }}
    .cover-wrap {{
      display: flex;
      flex-direction: column;
      justify-content: center;
      align-items: center;
      min-height: 95vh;
      padding: 2em;
      text-align: center;
      background-color: #0d0d0d;
    }}
    .cover-ornament {{
      font-size: 1.4em;
      color: #8b0000;
      letter-spacing: 0.4em;
      margin-bottom: 1.8em;
    }}
    .cover-title {{
      font-family: Georgia, serif;
      font-size: 2.4em;
      font-weight: normal;
      color: #e8e0d4;
      letter-spacing: 0.12em;
      line-height: 1.3;
      margin: 0 0 0.3em 0;
      text-transform: uppercase;
    }}
    .cover-subtitle {{
      font-family: Georgia, serif;
      font-size: 1em;
      font-style: italic;
      color: #8b0000;
      letter-spacing: 0.25em;
      margin: 0.5em 0 2.5em 0;
    }}
    .cover-rule {{
      width: 4em;
      border: none;
      border-top: 1px solid #8b0000;
      margin: 1.5em auto;
    }}
    .cover-author {{
      font-family: Georgia, serif;
      font-size: 0.95em;
      color: #9e9e9e;
      letter-spacing: 0.3em;
      text-transform: uppercase;
    }}
  </style>
</head>
<body>
  <div class="cover-wrap">
    <div class="cover-ornament">&#10023; &#10023; &#10023;</div>
    <h1 class="cover-title">Vow of the<br/>Bloodthorne</h1>
    <p class="cover-subtitle">a dark romantasy</p>
    <hr class="cover-rule"/>
    <p class="cover-author">Thais Simoes</p>
  </div>
</body>
</html>"""


def make_opf(chapters: list) -> str:
    manifest_items = [
        '    <item id="cover" href="text/cover.xhtml" media-type="application/xhtml+xml" properties="svg"/>'
    ]
    spine_items = [
        '    <itemref idref="cover"/>'
    ]

    for slug, _, _ in chapters:
        item_id = f"ch{slug}"
        manifest_items.append(
            f'    <item id="{item_id}" href="text/ch{slug}.xhtml" media-type="application/xhtml+xml"/>'
        )
        spine_items.append(f'    <itemref idref="{item_id}"/>')

    manifest = "\n".join(manifest_items)
    spine = "\n".join(spine_items)

    return f"""<?xml version="1.0" encoding="utf-8"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0" unique-identifier="uid">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
    <dc:identifier id="uid">{IDENTIFIER}</dc:identifier>
    <dc:title>{escape(TITLE)}</dc:title>
    <dc:creator>{escape(AUTHOR)}</dc:creator>
    <dc:language>{LANGUAGE}</dc:language>
    <meta name="cover" content="cover"/>
  </metadata>
  <manifest>
    <item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/>
    <item id="ncx" href="toc.ncx" media-type="application/x-dtbncx+xml"/>
    <item id="css" href="style.css" media-type="text/css"/>
{manifest}
  </manifest>
  <spine toc="ncx">
{spine}
  </spine>
</package>"""


def make_nav(chapters: list) -> str:
    items = ['      <li><a href="text/cover.xhtml">Cover</a></li>']
    for slug, _, content in chapters:
        num_str = slug.rstrip("K").lstrip("0") or "0"
        pov_line = content.strip().split("\n")[0].strip()
        pov_name = pov_line.replace("'s POV", "").replace("'s Pov", "").strip()
        if not pov_name:
            pov_name = "Kael" if slug.endswith("K") else "Iseult"
        label = f"Chapter {num_str} — {pov_name}"
        items.append(
            f'      <li><a href="text/ch{slug}.xhtml">{escape(label)}</a></li>'
        )
    nav_items = "\n".join(items)
    return f"""<?xml version="1.0" encoding="utf-8"?>
<html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops" xml:lang="{LANGUAGE}">
<head><title>{escape(TITLE)} — Navigation</title></head>
<body>
  <nav epub:type="toc" id="toc">
    <h2>Contents</h2>
    <ol>
{nav_items}
    </ol>
  </nav>
</body>
</html>"""


def make_ncx(chapters: list) -> str:
    nav_points = []
    for i, (slug, _, content) in enumerate(chapters, start=1):
        num_str = slug.rstrip("K").lstrip("0") or "0"
        pov_line = content.strip().split("\n")[0].strip()
        pov_name = pov_line.replace("'s POV", "").replace("'s Pov", "").strip()
        if not pov_name:
            pov_name = "Kael" if slug.endswith("K") else "Iseult"
        label = f"Chapter {num_str} — {pov_name}"
        nav_points.append(f"""  <navPoint id="np{i}" playOrder="{i}">
    <navLabel><text>{escape(label)}</text></navLabel>
    <content src="text/ch{slug}.xhtml"/>
  </navPoint>""")
    points = "\n".join(nav_points)
    return f"""<?xml version="1.0" encoding="utf-8"?>
<ncx xmlns="http://www.daisy.org/z3986/2005/ncx/" version="2005-1">
  <head>
    <meta name="dtb:uid" content="{IDENTIFIER}"/>
  </head>
  <docTitle><text>{escape(TITLE)}</text></docTitle>
  <navMap>
{points}
  </navMap>
</ncx>"""


CSS = """body {
  font-family: Georgia, serif;
  font-size: 1em;
  line-height: 1.6;
  margin: 1em 2em;
  color: #1a1a1a;
}
h1.chapter-title {
  font-size: 1.3em;
  margin-bottom: 0.2em;
  font-weight: normal;
  letter-spacing: 0.05em;
}
p.pov-header {
  font-size: 0.85em;
  font-style: italic;
  color: #555;
  margin-top: 0;
  margin-bottom: 1.5em;
}
p {
  margin: 0 0 0.7em 0;
  text-indent: 1.2em;
}
p:first-of-type {
  text-indent: 0;
}
.scene-break {
  text-align: center;
  margin: 1.5em 0;
  color: #888;
  letter-spacing: 0.3em;
}
em {
  font-style: italic;
}
"""

CONTAINER_XML = """<?xml version="1.0" encoding="utf-8"?>
<container xmlns="urn:oasis:names:tc:opendocument:xmlns:container" version="1.0">
  <rootfiles>
    <rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/>
  </rootfiles>
</container>"""


def file_sort_key(path):
    """Sort key: chapter_0007.txt < chapter_0007K.txt < chapter_0008.txt"""
    m = re.match(r"chapter_(\d+)(K?)\.txt", path.name)
    if not m:
        return (99999, "")
    return (int(m.group(1)), m.group(2))


def main():
    # Collect chapters (both regular and K-suffix)
    files = sorted(CHAPTERS_DIR.glob("chapter_*.txt"), key=file_sort_key)
    chapters = []
    for f in files:
        m = re.match(r"chapter_(\d+)(K?)\.txt", f.name)
        if not m:
            continue
        num = int(m.group(1))
        kael = m.group(2)  # "K" or ""
        slug = f"{num:04d}{kael}"  # e.g. "0007" or "0007K"
        raw = f.read_bytes()
        # Handle BOM and mixed encodings
        if raw[:2] in (b'\xff\xfe', b'\xfe\xff'):
            content = raw.decode("utf-16")
        elif raw[:3] == b'\xef\xbb\xbf':
            content = raw.decode("utf-8-sig")
        else:
            content = raw.decode("utf-8", errors="replace")
        chapters.append((slug, f.name, content))

    print(f"Found {len(chapters)} chapters.")

    OUTPUT_PATH.parent.mkdir(parents=True, exist_ok=True)

    with zipfile.ZipFile(OUTPUT_PATH, "w") as zf:
        # mimetype must be first and uncompressed
        zf.writestr(
            zipfile.ZipInfo("mimetype"),
            "application/epub+zip",
            compress_type=zipfile.ZIP_STORED,
        )

        zf.writestr("META-INF/container.xml", CONTAINER_XML)
        zf.writestr("OEBPS/content.opf", make_opf(chapters))
        zf.writestr("OEBPS/nav.xhtml", make_nav(chapters))
        zf.writestr("OEBPS/toc.ncx", make_ncx(chapters))
        zf.writestr("OEBPS/style.css", CSS)
        zf.writestr("OEBPS/text/cover.xhtml", COVER_XHTML)

        for slug, fname, content in chapters:
            xhtml = text_to_xhtml(content, slug)
            zf.writestr(f"OEBPS/text/ch{slug}.xhtml", xhtml)

    size_kb = OUTPUT_PATH.stat().st_size // 1024
    print(f"EPUB gerado: {OUTPUT_PATH}")
    print(f"Tamanho: {size_kb} KB")
    print(f"Capítulos incluídos: {len(chapters)} ({chapters[0][0]}–{chapters[-1][0]})")


if __name__ == "__main__":
    main()
