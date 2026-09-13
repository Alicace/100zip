import sys, pathlib, re
sys.stdout.reconfigure(encoding="utf-8", errors="replace")
p = pathlib.Path(r"D:\codex\fnnas-unzip\tools\downloads\libarchive-formats.5")
t = p.read_text(encoding="utf-8", errors="replace")
print("bytes:", len(t))
print("---- section headings ----")
for m in re.finditer(r"^\.Sh\s+(.*)$", t, re.M):
    print(" ", m.group(1))
i = t.find("Write")
print("---- context around first 'Write' ----")
print(t[max(0,i-800): i+2500])
