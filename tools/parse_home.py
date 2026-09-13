import re, sys, pathlib, html
sys.stdout.reconfigure(encoding="utf-8", errors="replace")
p = pathlib.Path(r"D:\codex\fnnas-unzip\tools\downloads\7zip-home.html")
t = p.read_text(encoding="utf-8", errors="replace")
tables = re.findall(r"<table.*?</table>", t, re.S | re.I)
target = None
for tb in tables:
    if "Packing" in tb and "Unpacking" in tb:
        target = tb
        break
if not target:
    print("no table found; tables:", len(tables)); sys.exit(1)
rows = re.findall(r"<tr.*?</tr>", target, re.S | re.I)
print(f"rows: {len(rows)}")
out = []
for r in rows:
    cells = re.findall(r"<t[dh][^>]*>(.*?)</t[dh]>", r, re.S | re.I)
    cells = [html.unescape(re.sub(r"<[^>]+>", "", c)).strip() for c in cells]
    if cells:
        out.append(cells)
for c in out:
    print(" | ".join(c))
pathlib.Path(r"D:\codex\fnnas-unzip\refs\evidence\7zip-formats-table.txt").write_text(
    "\n".join(" | ".join(c) for c in out), encoding="utf-8")
