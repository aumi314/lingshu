"""重新生成首页用的宋体子集（Noto Serif SC，OFL 1.1）。首页文案改动后运行。

用法（在项目根目录）：python3 -I scripts/subset-font.py
从 Google Fonts 按"只含这些字"的方式下载，结果放在 web/src/assets/fonts/，随前端一起打包，
线上不依赖外部字体服务。
"""
import pathlib
import re
import urllib.parse
import urllib.request

WEB = pathlib.Path(__file__).resolve().parent.parent / "web" / "src"
SOURCES = [WEB / "pages" / "Welcome.tsx", WEB / "components" / "Meridian.tsx"]
OUT = WEB / "assets" / "fonts"
UA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0 Safari/537.36"

src = "".join(p.read_text(encoding="utf-8") for p in SOURCES)
# 注释里的字不会显示在页面上，去掉以免字体变大
src = re.sub(r"/\*.*?\*/", "", src, flags=re.S)
src = re.sub(r"(^|\s)//.*", "", src)
cjk = sorted(set(re.findall(r"[　-〿一-鿿＀-￯—·]", src)))
text = "".join(cjk) + "".join(chr(c) for c in range(32, 127))
print(f"{len(cjk)} 个汉字和全角符号")

for weight in (500, 900):
    css_url = "https://fonts.googleapis.com/css2?family=Noto+Serif+SC:wght@%d&text=%s" % (weight, urllib.parse.quote(text))
    css = urllib.request.urlopen(urllib.request.Request(css_url, headers={"User-Agent": UA}), timeout=30).read().decode()
    font_url = re.search(r"url\((https://[^)]+)\)", css).group(1)
    data = urllib.request.urlopen(urllib.request.Request(font_url, headers={"User-Agent": UA}), timeout=60).read()
    out = OUT / f"lingshu-serif-{weight}.woff2"
    out.write_bytes(data)
    print(f"{out.relative_to(WEB.parent.parent)}  {len(data) // 1024} KB")
