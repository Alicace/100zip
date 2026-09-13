import sys
import pathlib

sys.path.insert(0, r"D:\codex\fnnas-unzip\tools\pylibs")
sys.stdout.reconfigure(encoding="utf-8", errors="replace")

import py7zr  # noqa: E402

src = pathlib.Path(r"D:\codex\fnnas-unzip\tools\downloads\7z2603-extra.7z")
dst = pathlib.Path(r"D:\codex\fnnas-unzip\tools\7zip-extracted")
dst.mkdir(parents=True, exist_ok=True)

with py7zr.SevenZipFile(src, mode="r") as z:
    names = z.getnames()
    print("entries:", len(names))
    z.extractall(path=dst)

print("extracted to", dst)
for p in sorted(dst.rglob("*")):
    if p.is_file():
        print(f"  {p.relative_to(dst)}  {p.stat().st_size}")
