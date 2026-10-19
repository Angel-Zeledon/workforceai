"""Render program of the render image (fixed: no user code runs here).

Protocol (no host mounts, no network):
  stdin  : a tar stream with `job.json` ({kind: pdf|image, pages, dpi, max_side, max_bytes})
           and `in/input` (the file bytes).
  stdout : a tar stream with `meta.json` ({status, code, message, total_pages,
           images:[{name, page}]}) and `out/<name>` (PNG or JPEG).

PDF pages are rasterized with poppler's pdftoppm at a DPI bounded so the long side
never exceeds max_side (a hostile MediaBox cannot make a gigapixel page); images
are decoded with Pillow behind a pixel cap (decompression bombs are refused),
auto-rotated, downscaled and re-encoded, which drops every piece of metadata.
The runner re-validates everything this program returns.
"""
import io
import json
import os
import re
import subprocess
import sys
import tarfile
import time

from PIL import Image, ImageOps

# Scratch dir: /work in the docker mode; the local mode (RENDER_EXEC=local) gives each job its own dir.
W = os.environ.get("RENDER_WORK") or "/work"
MAX_PIXELS = 40_000_000
Image.MAX_IMAGE_PIXELS = MAX_PIXELS


class Fail(Exception):
    def __init__(self, code, message):
        super().__init__(message)
        self.code, self.message = code, message


def read_input():
    os.makedirs(os.path.join(W, "in"), exist_ok=True)
    os.makedirs(os.path.join(W, "out"), exist_ok=True)
    job = None
    with tarfile.open(fileobj=sys.stdin.buffer, mode="r|") as tf:
        for m in tf:
            if not m.isreg():
                continue
            src = tf.extractfile(m)
            if m.name == "job.json":
                job = json.loads(src.read(64 << 10).decode())
            elif m.name == "in/input":
                with open(os.path.join(W, "in", "input"), "wb") as out:
                    while True:
                        chunk = src.read(1 << 20)
                        if not chunk:
                            break
                        out.write(chunk)
    if not isinstance(job, dict):
        raise Fail("render_failed", "missing job")
    return job


def encode(img, max_side, max_bytes):
    """Downscale to max_side and encode (PNG with transparency, JPEG otherwise) under max_bytes."""
    img = img.copy()
    img.thumbnail((max_side, max_side), Image.LANCZOS)
    alpha = img.mode in ("RGBA", "LA") or (img.mode == "P" and "transparency" in img.info)
    for _ in range(6):
        buf = io.BytesIO()
        if alpha:
            img.convert("RGBA").save(buf, "PNG", optimize=True)
            ext = "png"
        else:
            img.convert("RGB").save(buf, "JPEG", quality=85, optimize=True)
            ext = "jpg"
        data = buf.getvalue()
        if len(data) <= max_bytes:
            return data, ext, img.size
        img.thumbnail((max(64, img.size[0] * 2 // 3), max(64, img.size[1] * 2 // 3)), Image.LANCZOS)
    raise Fail("image_too_large", "the image stays over the size limit")


def pdfinfo(path, page, timeout):
    p = subprocess.run(["pdfinfo", "-f", str(page), "-l", str(page), path], capture_output=True, timeout=timeout)
    err = p.stderr.decode("utf-8", "replace").lower()
    if "password" in err or "encrypt" in err:
        raise Fail("pdf_encrypted", "the PDF is protected with a password")
    if p.returncode != 0:
        raise Fail("pdf_invalid", "the PDF could not be read")
    out = p.stdout.decode("utf-8", "replace")
    m = re.search(r"^Pages:\s+(\d+)", out, re.M)
    total = int(m.group(1)) if m else 0
    m = re.search(r"^Page\s+%d size:\s+([\d.]+) x ([\d.]+) pts" % page, out, re.M)
    size = (float(m.group(1)), float(m.group(2))) if m else None
    return total, size


def render_pdf(job, deadline):
    src = os.path.join(W, "in", "input")
    max_side, max_bytes = int(job["max_side"]), int(job["max_bytes"])
    dpi = int(job.get("dpi") or 110)
    total, _ = pdfinfo(src, 1, 15)
    if total <= 0:
        raise Fail("pdf_invalid", "the PDF declares no pages")
    images = []
    for page in job.get("pages") or [1]:
        if page < 1 or page > total:
            continue
        left = deadline - time.time()
        if left < 3:
            break
        _, size = pdfinfo(src, page, min(10, left))
        eff = dpi
        if size and max(size) > 0:  # bound the raster: long side <= max_side whatever the MediaBox says
            eff = min(dpi, max_side * 72.0 / max(size))
        eff = max(eff, 10)
        base = os.path.join(W, "out", "raw-%d" % page)
        args = ["pdftoppm", "-png", "-r", "%.2f" % eff, "-f", str(page), "-l", str(page), "-singlefile"]
        if not size:
            args += ["-scale-to", str(max_side)]
        p = subprocess.run(args + [src, base], capture_output=True, timeout=max(1, min(25, deadline - time.time())))
        if p.returncode != 0:
            err = p.stderr.decode("utf-8", "replace").lower()
            if "password" in err:
                raise Fail("pdf_encrypted", "the PDF is protected with a password")
            continue
        with Image.open(base + ".png") as im:
            data, ext, (w, h) = encode(im, max_side, max_bytes)
        os.remove(base + ".png")
        name = "page-%d.%s" % (page, ext)
        with open(os.path.join(W, "out", name), "wb") as f:
            f.write(data)
        images.append({"name": name, "page": page})
    if not images:
        raise Fail("pdf_pages_invalid", "none of the requested pages could be rendered")
    return total, images


def render_image(job):
    src = os.path.join(W, "in", "input")
    try:
        with Image.open(src) as im:
            if im.format not in ("PNG", "JPEG", "GIF", "WEBP"):
                raise Fail("image_unsupported", "only PNG, JPEG, GIF and WebP images are prepared")
            if im.size[0] * im.size[1] > MAX_PIXELS:
                raise Fail("image_too_large", "the image has too many pixels")
            im.seek(0)
            im.load()
            img = ImageOps.exif_transpose(im)
            data, ext, _ = encode(img, int(job["max_side"]), int(job["max_bytes"]))
    except Image.DecompressionBombError:
        raise Fail("image_too_large", "the image has too many pixels")
    except Fail:
        raise
    except Exception:
        raise Fail("image_invalid", "the image could not be decoded")
    name = "image." + ext
    with open(os.path.join(W, "out", name), "wb") as f:
        f.write(data)
    return 0, [{"name": name, "page": 0}]


def add_bytes(tf, name, data):
    info = tarfile.TarInfo(name)
    info.size, info.mtime, info.mode = len(data), int(time.time()), 0o600
    tf.addfile(info, io.BytesIO(data))


def main():
    deadline = time.time() + max(5, int(os.environ.get("RENDER_TIMEOUT", "40")))
    meta = {"status": "ok", "code": "", "message": "", "total_pages": 0, "images": []}
    try:
        job = read_input()
        if job.get("kind") == "pdf":
            meta["total_pages"], meta["images"] = render_pdf(job, deadline)
        elif job.get("kind") == "image":
            meta["total_pages"], meta["images"] = render_image(job)
        else:
            raise Fail("render_failed", "unknown kind")
    except Fail as e:
        meta.update(status="failed", code=e.code, message=e.message, images=[])
    except subprocess.TimeoutExpired:
        meta.update(status="failed", code="timeout", message="the renderer took too long", images=[])
    except Exception:
        meta.update(status="failed", code="render_failed", message="the file could not be rendered", images=[])
    with tarfile.open(fileobj=sys.stdout.buffer, mode="w|") as tf:
        add_bytes(tf, "meta.json", json.dumps(meta).encode())
        for img in meta["images"]:
            with open(os.path.join(W, "out", img["name"]), "rb") as f:
                add_bytes(tf, "out/" + img["name"], f.read())
    sys.stdout.buffer.flush()


if __name__ == "__main__":
    main()
