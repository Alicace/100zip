"""7-Zip 26.03 中文文件名编码行为实测（Windows 构建，与 Linux 构建共用同一套 Zip handler 源码）.

目的：裁决 `7zz x -mcp=936` 是否对**解压**生效，并测清**压缩**侧的中文名生成策略。
"""

import os
import re
import shutil
import struct
import subprocess
import sys
import zlib
import pathlib

sys.stdout.reconfigure(encoding="utf-8", errors="replace")

SEVENZA = r"D:\codex\fnnas-unzip\tools\7zip-extracted\x64\7za.exe"
ROOT = pathlib.Path(r"D:\codex\fnnas-unzip\refs\evidence\codepage")
SAMPLE = "中文测试.txt"
SAMPLE2 = "日本語テスト.txt"          # GBK 不可表示，只能 UTF-8
CONTENT = b"hello fnos\n"

LFH = 0x04034B50
CDH = 0x02014B50
EOCD = 0x06054B50
IZ_UNICODE_NAME = 0x7075


def build_iz_unicode_extra(raw_name: bytes, utf8_name: str) -> bytes:
    """Info-ZIP Unicode Path Extra Field (0x7075): ver(1)+crc32(raw name)+utf8 name."""
    payload = b"\x01" + struct.pack("<I", zlib.crc32(raw_name) & 0xFFFFFFFF) + utf8_name.encode("utf-8")
    return struct.pack("<HH", IZ_UNICODE_NAME, len(payload)) + payload


def make_zip(path, raw_name: bytes, flags=0, host_os=0, extra=b"") -> None:
    crc = zlib.crc32(CONTENT) & 0xFFFFFFFF
    size = len(CONTENT)
    dos_time, dos_date = 0x0000, 0x5A21  # 2025-01-01 00:00:00

    lfh = struct.pack(
        "<IHHHHHIIIHH", LFH, 20, flags, 0, dos_time, dos_date, crc, size, size,
        len(raw_name), len(extra),
    ) + raw_name + extra + CONTENT

    cdh = struct.pack(
        "<IHHHHHHIIIHHHHHII", CDH, (host_os << 8) | 20, 20, flags, 0, dos_time, dos_date,
        crc, size, size, len(raw_name), len(extra), 0, 0, 0, 0, 0,
    ) + raw_name + extra

    eocd = struct.pack("<IHHHHIIH", EOCD, 0, 0, 1, 1, len(cdh), len(lfh), 0)
    path.write_bytes(lfh + cdh + eocd)


def inspect_zip(path) -> list:
    """解析中央目录，返回 [(raw_name_bytes, flags, host_os, has_7075)]."""
    data = pathlib.Path(path).read_bytes()
    out = []
    for m in re.finditer(re.escape(struct.pack("<I", CDH)), data):
        off = m.start()
        (
            _sig, _vmb, _vn, flags, _meth, _t, _d, _crc, _cs, _us,
            nlen, elen, clen, _dstart, _iattr, _eattr, _lho,
        ) = struct.unpack_from("<IHHHHHHIIIHHHHHII", data, off)
        name = data[off + 46: off + 46 + nlen]
        extra = data[off + 46 + nlen: off + 46 + nlen + elen]
        has = struct.pack("<H", IZ_UNICODE_NAME) in extra
        out.append((name, flags, _vmb >> 8, has))
    return out


def run(args, cwd=None):
    p = subprocess.run([SEVENZA] + args, capture_output=True, cwd=cwd)
    return p.returncode, p.stdout.decode("utf-8", "replace"), p.stderr.decode("utf-8", "replace")


def safe_extract(zip_path, out_dir, extra_args):
    if out_dir.exists():
        shutil.rmtree(out_dir)
    out_dir.mkdir(parents=True)
    rc, so, se = run(["x", "-y", f"-o{out_dir}", *extra_args, str(zip_path)])
    files = sorted(p.name for p in out_dir.iterdir())
    return rc, files, (se.strip().splitlines() or [""])[-1]


def main():
    if ROOT.exists():
        shutil.rmtree(ROOT)
    ROOT.mkdir(parents=True)
    zips = ROOT / "zips"
    zips.mkdir()

    gbk = SAMPLE.encode("gbk")
    cases = {
        "A-gbk-dos.zip": dict(raw_name=gbk, flags=0, host_os=0),
        "B-gbk-unix.zip": dict(raw_name=gbk, flags=0, host_os=3),
        "C-utf8-flag.zip": dict(raw_name=SAMPLE.encode("utf-8"), flags=0x800, host_os=3),
        "D-gbk-plus-unicodefield.zip": dict(
            raw_name=gbk, flags=0, host_os=0, extra=build_iz_unicode_extra(gbk, SAMPLE)
        ),
    }
    for name, kw in cases.items():
        make_zip(zips / name, **kw)

    print("=" * 78)
    print("PART 1  解压：-mcp=936 是否生效")
    print("=" * 78)
    print(f"{'case':30} {'args':12} {'rc':>3}  extracted-name")
    for name in cases:
        for args, label in (([], "(none)"), (["-mcp=936"], "-mcp=936")):
            out = ROOT / f"out_{name}_{label.strip('()')}".replace("=", "")
            rc, files, err = safe_extract(zips / name, out, args)
            shown = files[0] if files else "<none>"
            mark = "OK " if shown == SAMPLE else "BAD"
            print(f"{name:30} {label:12} {rc:>3}  {mark} {shown}  {('| ' + err) if rc else ''}")

    print()
    print("=" * 78)
    print("PART 2  压缩：生成的中文名编码与 UTF-8 标志")
    print("=" * 78)
    src = ROOT / "src"
    src.mkdir()
    (src / SAMPLE).write_text("a", encoding="utf-8")
    (src / SAMPLE2).write_text("b", encoding="utf-8")
    combos = {
        "default": [],
        "mcl": ["-mcl"],
        "mcu": ["-mcu"],
        "mcp936": ["-mcp=936"],
        "mcl+mcp936": ["-mcl", "-mcp=936"],
        "mcp936+mcl": ["-mcp=936", "-mcl"],
    }
    for label, extra in combos.items():
        outzip = ROOT / f"make_{label.replace('+', '_')}.zip"
        if outzip.exists():
            outzip.unlink()
        rc, so, se = run(["a", "-tzip", *extra, str(outzip), SAMPLE, SAMPLE2], cwd=str(src))
        info = inspect_zip(outzip) if outzip.exists() else []
        desc = []
        for raw, flags, host, has7075 in info:
            utf8_flag = bool(flags & 0x800)
            try:
                as_gbk = raw.decode("gbk")
            except UnicodeDecodeError:
                as_gbk = "<not gbk>"
            desc.append(f"utf8flag={int(utf8_flag)} host={host} gbk_decode={as_gbk!r}")
        print(f"{label:12} rc={rc}  " + (" || ".join(desc) if desc else f"FAILED {se.strip()[:80]}"))

    print()
    print("=" * 78)
    print("PART 3  -mcp 在不同命令下的接受度")
    print("=" * 78)
    for cmd in (["i", str(zips / "A-gbk-dos.zip")], ["l", "-mcp=936", str(zips / "A-gbk-dos.zip")]):
        rc, so, se = run(cmd)
        first = (so.strip().splitlines() or [""])[0][:90]
        print(f"7za {cmd[0]:3} -mcp: rc={rc}  {first}  {se.strip()[:60]}")

    print()
    print("证据目录:", ROOT)


if __name__ == "__main__":
    main()
