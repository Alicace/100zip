import re, pathlib, sys
sys.stdout.reconfigure(encoding="utf-8", errors="replace")
d = pathlib.Path(r"D:\codex\fnnas-unzip\docs")
files = {p.name for p in d.glob("*.md")}
bad = []
ok = 0
for p in sorted(d.glob("*.md")):
    t = p.read_text(encoding="utf-8", errors="replace")
    for m in re.finditer(r"\[([^\]]+)\]\(([^)\s]+)\)", t):
        target = m.group(2)
        if target.startswith(("http://", "https://", "#")):
            continue
        base = target.split("#")[0]
        if not base:
            continue
        if base not in files:
            bad.append(f"{p.name} -> {target}")
        else:
            ok += 1
print(f"内部链接检查：OK {ok} 条，失效 {len(bad)} 条")
for b in bad:
    print("  BAD:", b)
print("文档数:", len(files))
