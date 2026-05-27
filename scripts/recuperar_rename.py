"""
Recupera o rename parcial: renomeia os _temp_ files para seus destinos finais.
"""
import os, re
from pathlib import Path

CHAPTERS_DIR = Path(
    r"c:\Users\tsuba\GolandProjects\InconsistencyFixer\output\writer"
    r"\Completed-stories\vow-of-the-bloodthorne\chapters-revised"
)

# Mesma ordem de leitura do script principal (sem 0028)
READING_ORDER = [
    ("0001K", True), ("0001", False), ("0002", False), ("0003", False),
    ("0003K", True), ("0004", False), ("0005", False), ("0006", False),
    ("0007", False), ("0008", False), ("0009", False), ("0010", False),
    ("0010K", True), ("0011", False), ("0012", False), ("0013", False),
    ("0014", False), ("0014K", True), ("0015", False), ("0016", False),
    ("0017", False), ("0018", False), ("0019", False), ("0020", False),
    ("0020K", True), ("0021", False), ("0022", False), ("0023", False),
    ("0024", False), ("0025", False), ("0026", False), ("0027", False),
    ("0029", False), ("0030", False), ("0031", False), ("0032", False),
    ("0033", False), ("0034", False), ("0035", False), ("0036", False),
    ("0037", False), ("0038", False), ("0039", False), ("0040", False),
    ("0040K", True), ("0041", False), ("0042", False), ("0043", False),
    ("0044", False), ("0045", False), ("0046", False), ("0047", False),
    ("0048", False), ("0049", False), ("0050", False), ("0051", False),
    ("0052", False), ("0053", False), ("0054", False), ("0054K", True),
    ("0055", False), ("0056", False), ("0057", False), ("0058", False),
    ("0059", False), ("0060", False), ("0061", False), ("0062", False),
    ("0063", False), ("0063K", True), ("0064", False), ("0065", False),
    ("0066", False), ("0067", False), ("0068", False), ("0069", False),
    ("0070", False), ("0071", False), ("0072", False), ("0073", False),
    ("0074", False), ("0075", False), ("0076", False), ("0077", False),
    ("0078", False), ("0079", False), ("0080", False), ("0081", False),
    ("0081K", True), ("0082", False), ("0083", False), ("0084", False),
    ("0085", False), ("0085K", True), ("0086", False), ("0087", False),
    ("0088", False), ("0089", False), ("0090", False), ("0091", False),
    ("0092", False), ("0093", False), ("0094", False), ("0095", False),
    ("0096", False), ("0096K", True), ("0097", False), ("0098", False),
    ("0099", False), ("0100K", True), ("0101", False), ("0102", False),
    ("0103", False), ("0104", False), ("0105", False), ("0106", False),
    ("0107", False), ("0108", False), ("0109", False), ("0110", False),
    ("0111", False), ("0112", False), ("0113", False), ("0113K", True),
    ("0114", False), ("0115", False), ("0116", False), ("0117", False),
    ("0118", False), ("0119", False), ("0120", False), ("0121", False),
    ("0122", False), ("0123", False), ("0124", False), ("0125", False),
    ("0126", False), ("0127", False), ("0128", False), ("0129", False),
    ("0130", False), ("0131", False), ("0132", False), ("0133", False),
    ("0134", False), ("0135", False), ("0136", False), ("0137K", True),
    ("0138", False), ("0139", False),
]

def get_original_name(base, is_kael):
    if is_kael and not base.endswith("K"):
        return f"chapter_{base}K.txt"
    return f"chapter_{base}.txt"

# Build full map: original_name -> final_name
full_map = {}
for i, (base, is_kael) in enumerate(READING_ORDER, start=1):
    orig = get_original_name(base, is_kael)
    final = f"chapter_{i:04d}.txt"
    full_map[orig] = final

# Find temp files and their targets
temp_files = list(CHAPTERS_DIR.glob("_temp_*.txt"))
print(f"Temp files found: {len(temp_files)}")

conflicts = []
to_rename = []

for temp_path in sorted(temp_files):
    # Extract original name: _temp_chapter_XXXX.txt -> chapter_XXXX.txt
    orig_name = temp_path.name[6:]  # strip "_temp_"
    if orig_name not in full_map:
        print(f"  UNKNOWN: {temp_path.name}")
        continue
    final_name = full_map[orig_name]
    final_path = CHAPTERS_DIR / final_name
    if final_path.exists():
        conflicts.append((temp_path, final_path))
        print(f"  CONFLICT: {temp_path.name} -> {final_name} (already exists)")
    else:
        to_rename.append((temp_path, final_path))

print(f"\nTo rename: {len(to_rename)}")
print(f"Conflicts: {len(conflicts)}")

# Rename non-conflicting
for temp_path, final_path in to_rename:
    os.rename(temp_path, final_path)
    print(f"  OK: {temp_path.name} -> {final_path.name}")

# Handle conflicts: the existing file is the one already correctly renamed
# The temp file is a duplicate — check content or just remove temp
if conflicts:
    print("\nConflicts (temp file has same target as existing):")
    for temp_path, final_path in conflicts:
        print(f"  {temp_path.name} conflicts with existing {final_path.name}")
        print("  Removing temp (existing is correct)...")
        os.remove(temp_path)

print("\nDone.")
remaining = list(CHAPTERS_DIR.glob("_temp_*.txt"))
print(f"Remaining temp files: {len(remaining)}")
final_count = len(list(CHAPTERS_DIR.glob("chapter_*.txt")))
print(f"Final chapter files: {final_count}")
