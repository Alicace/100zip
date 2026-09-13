from pathlib import Path
from PIL import Image, ImageDraw

ROOT = Path(__file__).resolve().parents[1]


def make_icon(size: int) -> Image.Image:
    scale = 4
    n = size * scale
    im = Image.new("RGBA", (n, n), (0, 0, 0, 0))
    d = ImageDraw.Draw(im)
    s = n / 512
    def box(x0, y0, x1, y1, r, fill):
        d.rounded_rectangle(tuple(int(v * s) for v in (x0, y0, x1, y1)), radius=int(r * s), fill=fill)
    # 柔和的蓝色渐变和内阴影，适配飞牛桌面上的立体图标风格
    for y in range(n):
        t = y / max(1, n - 1)
        c = (int(79 - 25 * t), int(169 - 48 * t), int(235 - 34 * t), 255)
        d.line((0, y, n, y), fill=c)
    mask = Image.new("L", (n, n), 0)
    md = ImageDraw.Draw(mask)
    md.rounded_rectangle((int(4*s), int(4*s), int(508*s), int(508*s)), radius=int(104*s), fill=255)
    im.putalpha(mask)
    d = ImageDraw.Draw(im)
    white = (255, 255, 255, 255)
    ink = (47, 122, 191, 255)
    # 高光边缘和底部阴影
    d.rounded_rectangle((int(10*s), int(10*s), int(502*s), int(502*s)), radius=int(100*s), outline=(255,255,255,55), width=max(1, int(5*s)))
    # 简洁的单盒体，避免原图两个盒子叠加造成的拥挤感
    box(101, 218, 421, 422, 42, (218, 235, 248, 150))
    box(96, 210, 416, 414, 42, white)
    box(82, 170, 430, 246, 34, white)
    # 盒体上的向下箭头，表示解压到目标目录
    d.rounded_rectangle((int(236*s), int(238*s), int(276*s), int(330*s)), radius=int(18*s), fill=ink)
    d.polygon([(int(188*s), int(318*s)), (int(256*s), int(386*s)), (int(324*s), int(318*s))], fill=ink)
    # 轻量分隔线，让盒盖更清楚
    d.rounded_rectangle((int(154*s), int(202*s), int(358*s), int(218*s)), radius=int(8*s), fill=(222, 239, 252, 255))
    return im.resize((size, size), Image.Resampling.LANCZOS)


make_icon(512).save(ROOT / "ICON.PNG", optimize=True)
make_icon(512).save(ROOT / "ICON_256.PNG", optimize=True)
make_icon(256).save(ROOT / "app" / "ui" / "images" / "icon_256.png", optimize=True)
make_icon(64).save(ROOT / "app" / "ui" / "images" / "icon_64.png", optimize=True)
