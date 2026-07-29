"""Generate the two Play Store graphics that Play will not let you publish without.

Both are derived from the shipped app icon, so they cannot drift from it: change
the icon, run this, and the store assets follow.

    python3 market/tools/make_play_graphics.py

Writes market/googleplay/graphics/icon-512.png and
market/googleplay/graphics/feature-graphic-1024x500.png.
"""

import os
import sys

from PIL import Image, ImageDraw, ImageFont

HERE = os.path.dirname(os.path.abspath(__file__))
MOBILE = os.path.dirname(os.path.dirname(HERE))
ICON = os.path.join(
    MOBILE, "ios/Runner/Assets.xcassets/AppIcon.appiconset/Icon-App-1024x1024@1x.png"
)
OUT = os.path.join(os.path.dirname(HERE), "googleplay/graphics")

# The brand purple, read from the icon's own corner rather than hard-coded, so
# there is one source of truth for it.
def brand_colour(icon):
    return icon.convert("RGB").getpixel((5, 5))


def font(px, bold=True):
    for path in (
        "/System/Library/Fonts/Supplemental/Arial Bold.ttf",
        "/System/Library/Fonts/Helvetica.ttc",
        "/usr/share/fonts/truetype/dejavu/DejaVuSans-Bold.ttf",
    ):
        try:
            return ImageFont.truetype(path, px)
        except OSError:
            continue
    return ImageFont.load_default()


def write_icon(icon):
    """512×512, no alpha. Play rejects an icon with a transparent channel."""
    out = Image.new("RGB", (512, 512), brand_colour(icon))
    out.paste(icon.convert("RGB").resize((512, 512), Image.LANCZOS), (0, 0))
    path = os.path.join(OUT, "icon-512.png")
    out.save(path, "PNG", optimize=True)
    return path


def write_feature_graphic(icon):
    """1024×500. Shown at the top of the listing, and cropped hard on some
    surfaces — so everything that matters sits in the middle band."""
    w, h = 1024, 500
    base = brand_colour(icon)
    img = Image.new("RGB", (w, h), base)
    draw = ImageDraw.Draw(img)

    # A soft vertical wash, so it does not read as a flat rectangle.
    for y in range(h):
        t = y / h
        draw.line(
            [(0, y), (w, y)],
            fill=tuple(int(c * (1 - 0.35 * t)) for c in base),
        )

    mark = icon.convert("RGBA").resize((300, 300), Image.LANCZOS)
    img.paste(mark, (78, (h - 300) // 2), mark)

    title_f = font(96)
    draw.text((430, 168), "Pheme", font=title_f, fill=(255, 255, 255))

    # Shrink the tagline until it fits the space left of the right margin, rather
    # than trusting a hard-coded size — the first version ran off the edge.
    tagline = "Encrypted messaging, on your own server"
    left, right_margin = 436, 60
    size = 34
    while size > 14:
        f = font(size)
        if draw.textlength(tagline, font=f) <= w - left - right_margin:
            break
        size -= 1
    draw.text((left, 292), tagline, font=font(size), fill=(240, 232, 255))

    path = os.path.join(OUT, "feature-graphic-1024x500.png")
    img.save(path, "PNG", optimize=True)
    return path


def main():
    if not os.path.exists(ICON):
        sys.exit(f"app icon not found at {ICON}")
    os.makedirs(OUT, exist_ok=True)
    icon = Image.open(ICON)
    for path in (write_icon(icon), write_feature_graphic(icon)):
        print("wrote", os.path.relpath(path, MOBILE))


if __name__ == "__main__":
    main()
