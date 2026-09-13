"""生成 100解压 应用图标（压缩/解压意象：两个压缩盒 + 双向箭头）。
输出: tmp/icon_preview.png（预览）、ICON_256.png、ICON.png、app/ui/images/icon_{64,256}.png
"""
from PIL import Image, ImageDraw
import os
ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))

SS = 1024          # 超采样画布，最后缩到 512
RADIUS = 230       # 圆角（约 22%）

TOP = (59, 154, 224)      # #3B9AE0
BOT = (29, 111, 184)      # #1D6FB8
WHITE = (255, 255, 255)

img = Image.new("RGBA", (SS, SS), (0, 0, 0, 0))
draw = ImageDraw.Draw(img)

# 背景圆角方块 + 垂直渐变
for y in range(SS):
    t = y / SS
    color = tuple(int(a + (b - a) * t) for a, b in zip(TOP, BOT)) + (255,)
    draw.line([(0, y), (SS, y)], fill=color)
mask = Image.new("L", (SS, SS), 0)
ImageDraw.Draw(mask).rounded_rectangle([0, 0, SS - 1, SS - 1], radius=RADIUS, fill=255)
img.putalpha(mask)
draw = ImageDraw.Draw(img)

cx = SS // 2

# 上方压缩盒：盒盖 + 盒身
draw.rounded_rectangle([170, 190, 470, 270], radius=24, fill=WHITE)
draw.rounded_rectangle([188, 262, 452, 500], radius=28, fill=WHITE)
draw.rounded_rectangle([250, 292, 390, 338], radius=16, fill=BOT)
# 下方解压盒
draw.rounded_rectangle([554, 522, 854, 602], radius=24, fill=WHITE)
draw.rounded_rectangle([572, 594, 836, 832], radius=28, fill=WHITE)
draw.rounded_rectangle([634, 624, 774, 670], radius=16, fill=BOT)
# 双向箭头：压缩向上、解压向下
draw.rounded_rectangle([cx - 22, 314, cx + 22, 710], radius=18, fill=(255, 255, 255, 235))
draw.polygon([(cx - 92, 380), (cx + 92, 380), (cx, 292)], fill=WHITE)
draw.polygon([(cx - 92, 644), (cx + 92, 644), (cx, 732)], fill=WHITE)

out512 = img.resize((512, 512), Image.LANCZOS)
os.makedirs(os.path.join(ROOT, "tmp"), exist_ok=True)
out512.save(os.path.join(ROOT, "tmp", "icon_preview.png"))
out512.save(os.path.join(ROOT, "ICON_256.PNG"))
out512.save(os.path.join(ROOT, "ICON.PNG"))
out512.resize((256, 256), Image.LANCZOS).save(os.path.join(ROOT, "app", "ui", "images", "icon_256.png"))
out512.resize((64, 64), Image.LANCZOS).save(os.path.join(ROOT, "app", "ui", "images", "icon_64.png"))
print("icons generated")
