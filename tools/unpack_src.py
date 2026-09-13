import sys
import tarfile
import pathlib

sys.stdout.reconfigure(encoding="utf-8", errors="replace")

src = pathlib.Path(r"D:\codex\fnnas-unzip\tools\downloads\7z2603-src.tar.xz")
dst = pathlib.Path(r"D:\codex\fnnas-unzip\refs\7zip-src")
dst.mkdir(parents=True, exist_ok=True)

with tarfile.open(src, "r:xz") as tf:
    names = tf.getnames()
    print("entries:", len(names))
    print("top-level:", sorted({n.split("/")[0] for n in names})[:20])
    tf.extractall(dst)

print("extracted to", dst)
for p in sorted(dst.iterdir()):
    print("  ", p.name, "(dir)" if p.is_dir() else p.stat().st_size)
