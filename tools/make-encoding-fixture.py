"""生成编码测试样本：GBK 文件名、无 UTF-8 标志位的 ZIP。

用途：验证 Linux 版 7-Zip 是否忽略 -mcp、以及解压时是否保留原始字节。
运行：python tools/make-encoding-fixture.py <输出目录>
"""
import os
import struct
import sys
import zlib


def build_zip(dest_path: str, raw_name: bytes, content: bytes) -> None:
    """手工构造一个最小 ZIP（store 方法、version-made-by=0、不设 UTF-8 标志）。"""
    crc = zlib.crc32(content) & 0xFFFFFFFF

    # Local file header
    local = struct.pack(
        "<IHHHHHIIIHH",
        0x04034B50,   # signature
        20,           # version needed
        0x0000,       # flags（关键：不设 bit 11 = 无 UTF-8 标志）
        0,            # method = store
        0, 0,         # time, date
        crc,
        len(content),
        len(content),
        len(raw_name),
        0,            # extra length
    ) + raw_name + content

    # Central directory
    central = struct.pack(
        "<IHHHHHHIIIHHHHHII",
        0x02014B50,
        0,            # version made by = 0 (DOS/FAT) —— 关键
        20,
        0x0000,
        0,
        0, 0,
        crc,
        len(content),
        len(content),
        len(raw_name),
        0,            # extra field length
        0,            # file comment length
        0,            # disk number start
        0,            # internal file attributes
        0,            # external file attributes
        0,            # relative offset of local header
    ) + raw_name

    eocd = struct.pack(
        "<IHHHHIIH",
        0x06054B50,
        0, 0,
        1,
        1,
        len(central),
        len(local),
        0,
    )

    with open(dest_path, "wb") as f:
        f.write(local + central + eocd)


def main() -> int:
    out_dir = sys.argv[1] if len(sys.argv) > 1 else "."
    os.makedirs(out_dir, exist_ok=True)

    cases = {
        # 简体中文 GBK："测试文件.txt"
        "A_gbk.zip": ("测试文件.txt".encode("gbk"), "hello-gbk\n".encode()),
        # 繁体 Big5："測試檔案.txt"
        "B_big5.zip": ("測試檔案.txt".encode("big5"), "hello-big5\n".encode()),
        # 日文 Shift-JIS："テスト.txt"
        "C_sjis.zip": ("テスト.txt".encode("shift_jis"), "hello-sjis\n".encode()),
        # UTF-8 编码但同样不设标志位（对照组）
        "D_utf8_noflag.zip": ("测试文件.txt".encode("utf-8"), "hello-utf8\n".encode()),
    }
    for name, (raw, content) in cases.items():
        path = os.path.join(out_dir, name)
        build_zip(path, raw, content)
        print(f"{name}: 原始文件名字节 = {raw.hex(' ')}  ({len(raw)} bytes)  -> {path}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
