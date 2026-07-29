"""Generate the two Play Store graphics that Play will not let you publish without.

Both derive from assets that already ship, so they cannot drift from the product:
the app icon, and the chat wallpaper the app draws behind every conversation.
Change either and re-run this; the store assets follow.

    python3 market/tools/make_play_graphics.py

Writes market/googleplay/graphics/icon-512.png and
market/googleplay/graphics/feature-graphic-1024x500.png.

Needs rsvg-convert (brew install librsvg) to rasterise the wallpaper. Without it
the feature graphic still builds, just without the pattern layer.
"""

import os
import shutil
import subprocess
import sys
import tempfile

from PIL import Image, ImageChops, ImageDraw, ImageFilter, ImageFont

HERE = os.path.dirname(os.path.abspath(__file__))
MARKET = os.path.dirname(HERE)
MOBILE = os.path.dirname(MARKET)
REPO = os.path.dirname(MOBILE)

ICON = os.path.join(
    MOBILE, "ios/Runner/Assets.xcassets/AppIcon.appiconset/Icon-App-1024x1024@1x.png"
)
PATTERN = os.path.join(REPO, "web/public/chat-pattern-light.svg")
OUT = os.path.join(MARKET, "googleplay/graphics")

# Pulled from the icon rather than hard-coded, so there is one source of truth.
def brand_colour(icon):
    return icon.convert("RGB").getpixel((5, 5))


def font(px, weight="bold"):
    """Avenir Next, then Helvetica Neue, then Arial.

    Ordered by how the wordmark actually looks: Avenir's geometric caps suit a
    single short brand word far better than Arial's, and Arial is only here so
    the script still runs on a machine without the macOS font set.
    """
    candidates = {
        "bold": (
            ("/System/Library/Fonts/Avenir Next.ttc", 0),
            ("/System/Library/Fonts/HelveticaNeue.ttc", 1),
            ("/System/Library/Fonts/Supplemental/Arial Bold.ttf", 0),
            ("/usr/share/fonts/truetype/dejavu/DejaVuSans-Bold.ttf", 0),
        ),
        "regular": (
            ("/System/Library/Fonts/HelveticaNeue.ttc", 0),
            ("/System/Library/Fonts/Supplemental/Arial.ttf", 0),
            ("/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf", 0),
        ),
    }[weight]
    for path, index in candidates:
        try:
            return ImageFont.truetype(path, px, index=index)
        except OSError:
            continue
    return ImageFont.load_default()


def logo_mark(icon, size):
    """The white mark, lifted off its purple tile.

    The shipped icon is a rounded square: white artwork on brand purple. For a
    graphic that already has its own background, pasting the whole tile puts a
    box in the middle of the composition — which is exactly what made the first
    version of this look like a slide template. Luminance doubles as the alpha
    channel here because the artwork is pure white and the background is not.
    """
    rgb = icon.convert("RGB").resize((size, size), Image.LANCZOS)
    r, g, b = rgb.split()
    # min(r,g,b), not luminance. The tile's purple runs 64-75 on its lowest
    # channel while the artwork is pure white at 255, so the darkest channel
    # separates them cleanly and keeps the anti-aliased edges. Luminance does
    # not: the tile's own gradient reaches 125 there, above the white-ish
    # threshold, which left a visible square of background behind the mark.
    darkest = ImageChops.darker(ImageChops.darker(r, g), b)
    cut = 90
    alpha = darkest.point(lambda v: 0 if v <= cut else int((v - cut) * 255 / (255 - cut)))
    mark = Image.new("RGBA", (size, size), (255, 255, 255, 0))
    mark.putalpha(alpha)
    return mark


def gradient(size, stops):
    """A diagonal multi-stop wash. Diagonal because a purely vertical one reads
    as a header bar rather than as artwork."""
    w, h = size
    base = Image.new("RGB", (w, h))
    draw = ImageDraw.Draw(base)
    span = w + h
    for i in range(span):
        t = i / span
        for j in range(len(stops) - 1):
            a, b = j / (len(stops) - 1), (j + 1) / (len(stops) - 1)
            if a <= t <= b:
                local = (t - a) / (b - a)
                c1, c2 = stops[j], stops[j + 1]
                colour = tuple(int(c1[k] + (c2[k] - c1[k]) * local) for k in range(3))
                break
        else:
            colour = stops[-1]
        draw.line([(i, 0), (0, i)], fill=colour)
    return base


def pattern_layer(size, boost=3.6, tile_px=340):
    """The app's own chat wallpaper, tiled. Returns None if rsvg-convert is absent."""
    if not (shutil.which("rsvg-convert") and os.path.exists(PATTERN)):
        return None
    with tempfile.TemporaryDirectory() as tmp:
        png = os.path.join(tmp, "tile.png")
        subprocess.run(
            ["rsvg-convert", "-w", str(tile_px), "-h", str(tile_px), PATTERN, "-o", png],
            check=True,
        )
        tile = Image.open(png).convert("RGBA")

    w, h = size
    layer = Image.new("RGBA", size, (255, 255, 255, 0))
    for y in range(0, h + tile_px, tile_px):
        for x in range(0, w + tile_px, tile_px):
            layer.paste(tile, (x, y), tile)

    # Recolour to white and BOOST it. The wallpaper is drawn for a near-white
    # chat background at stroke-opacity 0.13, so it rasterises with a peak alpha
    # of about 62 — against a saturated purple field that is invisible. Scaling
    # it down, as the first version did, made it invisible twice over.
    alpha = layer.getchannel("A").point(lambda v: min(255, int(v * boost)))
    white = Image.new("RGBA", size, (255, 255, 255, 0))
    white.putalpha(alpha)
    return white


def glow(size, centre, radius, colour, strength=150):
    layer = Image.new("RGBA", size, (0, 0, 0, 0))
    d = ImageDraw.Draw(layer)
    cx, cy = centre
    d.ellipse(
        [cx - radius, cy - radius, cx + radius, cy + radius],
        fill=colour + (strength,),
    )
    return layer.filter(ImageFilter.GaussianBlur(radius * 0.55))


def text_with_shadow(img, xy, text, fnt, fill, shadow=(20, 4, 60), offset=(0, 4), blur=8):
    """Draw once blurred and dark, then again sharp on top. A flat white word on
    a purple field has no weight; this gives it somewhere to sit."""
    shadow_layer = Image.new("RGBA", img.size, (0, 0, 0, 0))
    ImageDraw.Draw(shadow_layer).text(
        (xy[0] + offset[0], xy[1] + offset[1]), text, font=fnt, fill=shadow + (170,)
    )
    img.alpha_composite(shadow_layer.filter(ImageFilter.GaussianBlur(blur)))
    ImageDraw.Draw(img).text(xy, text, font=fnt, fill=fill)


def write_icon(icon):
    """512×512, no alpha — Play rejects an icon with a transparent channel."""
    out = Image.new("RGB", (512, 512), brand_colour(icon))
    out.paste(icon.convert("RGB").resize((512, 512), Image.LANCZOS), (0, 0))
    path = os.path.join(OUT, "icon-512.png")
    out.save(path, "PNG", optimize=True)
    return path


def write_feature_graphic(icon):
    w, h = 1024, 500
    base = brand_colour(icon)

    img = gradient(
        (w, h),
        [
            (46, 20, 105),      # deep indigo, bottom-left
            (92, 45, 200),
            base,               # the brand purple, through the middle
            (150, 74, 232),
            (188, 96, 226),     # violet, top-right
        ],
    ).convert("RGBA")

    # Light behind the mark and a cooler one opposite, so the field has depth
    # rather than being a flat ramp.
    img.alpha_composite(glow((w, h), (250, 250), 210, (170, 120, 255), 120))
    img.alpha_composite(glow((w, h), (900, 90), 190, (120, 180, 255), 70))

    pattern = pattern_layer((w, h))
    if pattern is not None:
        img.alpha_composite(pattern)

    # The mark, no tile behind it, with its own soft halo.
    mark_px = 268
    mx, my = 96, (h - mark_px) // 2
    img.alpha_composite(
        glow((w, h), (mx + mark_px // 2, my + mark_px // 2), 150, (255, 255, 255), 60)
    )
    mark = logo_mark(icon, mark_px)
    shadow = Image.new("RGBA", (w, h), (0, 0, 0, 0))
    shadow.paste(mark, (mx + 4, my + 10), mark)
    img.alpha_composite(shadow.filter(ImageFilter.GaussianBlur(14)))
    img.alpha_composite(Image.new("RGBA", (w, h), (0, 0, 0, 0)))
    img.paste(mark, (mx, my), mark)

    text_with_shadow(img, (430, 150), "Pheme", font(112), (255, 255, 255))

    tagline = "Encrypted messaging, on your own server"
    left, right_margin = 436, 56
    size = 34
    measure = ImageDraw.Draw(img)
    while size > 14 and measure.textlength(tagline, font=font(size, "regular")) > w - left - right_margin:
        size -= 1
    text_with_shadow(
        img,
        (left, 300),
        tagline,
        font(size, "regular"),
        (238, 228, 255),
        offset=(0, 3),
        blur=6,
    )

    path = os.path.join(OUT, "feature-graphic-1024x500.png")
    img.convert("RGB").save(path, "PNG", optimize=True)
    return path


def main():
    if not os.path.exists(ICON):
        sys.exit(f"app icon not found at {ICON}")
    os.makedirs(OUT, exist_ok=True)
    icon = Image.open(ICON)
    for path in (write_icon(icon), write_feature_graphic(icon)):
        print("wrote", os.path.relpath(path, MOBILE))
    if not shutil.which("rsvg-convert"):
        print("note: rsvg-convert missing, so the wallpaper layer was skipped")


if __name__ == "__main__":
    main()
