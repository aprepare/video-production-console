# 封面转换测试样本

`cover.heic` 是本任务生成的三色矩形图，没有第三方图片内容。实际编码为 HEVC/H.265，900×600，容器品牌为 `heic`，兼容品牌为 `mif1heicmiaf`。测试使用它验证 HEIC 解码、JPEG 输出及 540 像素长边限制；没有 FFmpeg 时仅跳过需要实际解码的子测试。

生成方式（仅重建样本时需要 Python Pillow 与 pillow-heif，Go 测试无需这些依赖）：

```python
from PIL import Image, ImageDraw
import pillow_heif

image = Image.new('RGB', (900, 600), (238, 228, 208))
draw = ImageDraw.Draw(image)
draw.rectangle((0, 0, 299, 599), fill=(180, 40, 32))
draw.rectangle((300, 0, 599, 599), fill=(32, 140, 88))
draw.rectangle((600, 0, 899, 599), fill=(40, 80, 180))
pillow_heif.from_pillow(image).save('cover.heic', quality=75)
```
