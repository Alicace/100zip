import re, pathlib, sys
sys.stdout.reconfigure(encoding="utf-8", errors="replace")
t = pathlib.Path(r"D:\codex\fnnas-unzip\tools\downloads\7zip-home.html").read_text(encoding="utf-8", errors="replace")
i = t.find("Packing")
print("len:", len(t), "idx:", i)
print(repr(t[max(0,i-1500): i+3000]))
