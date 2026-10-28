#!/usr/bin/env python3
"""Generate the app icon (pure Python, no PIL): rounded-rect blue gradient
background, white display with a green "connected" dot.

Outputs assets/icon.iconset/*.png (for `iconutil` on macOS),
assets/icon_512.png and assets/icon_256.png (README / Linux).
Run:  python3 tools/gen_icon.py
"""

import os
import struct
import subprocess
import sys
import zlib

SIZE = 512
OUT_DIR = os.path.join(os.path.dirname(os.path.dirname(os.path.abspath(__file__))),
                       "assets")


def lerp(a, b, t):
    return tuple(int(a[i] + (b[i] - a[i]) * t) for i in range(3))


def in_rounded_rect(x, y, cx, cy, w, h, r):
    dx = max(abs(x - cx) - (w / 2 - r), 0)
    dy = max(abs(y - cy) - (h / 2 - r), 0)
    return dx * dx + dy * dy <= r * r


def render(size):
    """RGBA bytes for one size."""
    s = size / SIZE
    px = bytearray()
    for y in range(size):
        for x in range(size):
            nx, ny = x / s, y / s
            r, g, b, a = 0, 0, 0, 0
            # background: rounded square 512x512 r=110, vertical gradient
            if in_rounded_rect(nx, ny, 256, 256, 512, 512, 110):
                c = lerp((10, 132, 255), (0, 80, 200), ny / SIZE)
                r, g, b, a = *c, 255
            # display: white rounded rect
            if in_rounded_rect(nx, ny, 256, 232, 372, 252, 36):
                r, g, b, a = 255, 255, 255, 255
            # stand
            if in_rounded_rect(nx, ny, 256, 408, 44, 60, 10) \
                    or in_rounded_rect(nx, ny, 256, 444, 150, 26, 12):
                r, g, b, a = 235, 239, 245, 255
            # green "connected" dot on the display
            dx, dy = nx - 340, ny - 300
            if dx * dx + dy * dy <= 34 * 34:
                r, g, b, a = 46, 204, 113, 255
            px += bytes((r, g, b, a))
    return bytes(px)


def write_png(path, size, rgba):
    raw = b"".join(b"\x00" + rgba[y * size * 4:(y + 1) * size * 4]
                   for y in range(size))
    def chunk(typ, data):
        c = struct.pack(">I", len(data)) + typ + data
        return c + struct.pack(">I", zlib.crc32(typ + data) & 0xFFFFFFFF)
    ihdr = struct.pack(">IIBBBBB", size, size, 8, 6, 0, 0, 0)
    png = (b"\x89PNG\r\n\x1a\n" + chunk(b"IHDR", ihdr)
           + chunk(b"IDAT", zlib.compress(raw, 9)) + chunk(b"IEND", b""))
    with open(path, "wb") as f:
        f.write(png)


def main():
    iconset = os.path.join(OUT_DIR, "icon.iconset")
    os.makedirs(iconset, exist_ok=True)
    for name, size in (("icon_16x16", 16), ("icon_32x32", 32),
                       ("icon_128x128", 128), ("icon_256x256", 256),
                       ("icon_512x512", 512)):
        rgba = render(size)
        write_png(os.path.join(iconset, f"{name}.png"), size, rgba)
        write_png(os.path.join(iconset, f"{name}@2x.png"), size * 2, rgba)
    rgba = render(512)
    write_png(os.path.join(OUT_DIR, "icon_512.png"), 512, rgba)
    write_png(os.path.join(OUT_DIR, "icon_256.png"), 256, render(256))
    if sys.platform == "darwin":
        subprocess.run(["iconutil", "-c", "icns", iconset,
                        "-o", os.path.join(OUT_DIR, "icon.icns")], check=True)
    print("icon written to", OUT_DIR)


if __name__ == "__main__":
    main()
