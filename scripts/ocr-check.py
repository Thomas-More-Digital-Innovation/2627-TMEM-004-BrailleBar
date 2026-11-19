"""End-to-end check of file printing, run inside the OCR image so it has Pillow:
docker compose run --rm -v ./scripts:/scripts:ro ocr-service python /scripts/ocr-check.py
"""

import io
import json
import sys
import time
import urllib.request
import uuid
import zipfile

from PIL import Image, ImageDraw, ImageFont

GATEWAY = "http://gateway:8080"
failures = 0


def call(method, path, device, data=None):
    req = urllib.request.Request(GATEWAY + path, data=data, method=method, headers={"X-Device-Id": device})
    if method == "POST" and path.endswith("/status"):
        req.add_header("Content-Type", "application/json")
    with urllib.request.urlopen(req, timeout=30) as res:
        body = res.read()
    return json.loads(body) if body else None


def wait_for_job(device, seconds=60):
    end = time.time() + seconds
    while time.time() < end:
        jobs = call("GET", "/jobs", device)["jobs"]
        if jobs:
            return jobs[0]
        time.sleep(0.5)
    return None


def check(name, data, expected):
    global failures
    device = "ocr-" + uuid.uuid4().hex[:12]
    call("POST", "/documents/file", device, data)
    job = wait_for_job(device)
    if job is None:
        print(f"FAIL {name}: no print job appeared")
        failures += 1
        return
    pages = call("GET", f"/jobs/{job['jobId']}/pages", device)["pages"]
    text = " ".join(line for page in pages for line in page).lower()
    print(f"{'ok  ' if expected in text else 'FAIL'} {name}: {text!r}")
    failures += expected not in text
    call("POST", f"/jobs/{job['jobId']}/status", device, b'{"state":"cancelled","page":0}')


def picture():
    image = Image.new("RGB", (1600, 300), "white")
    font = ImageFont.load_default(size=64)
    ImageDraw.Draw(image).text((40, 100), "hello world from the scanner", fill="black", font=font)
    return image


def as_file(image, kind):
    out = io.BytesIO()
    image.save(out, kind, **({"resolution": 150} if kind == "PDF" else {}))
    return out.getvalue()


def docx():
    ns = "http://schemas.openxmlformats.org/wordprocessingml/2006/main"
    xml = f'<w:document xmlns:w="{ns}"><w:body><w:p><w:r><w:t>hello from word</w:t></w:r></w:p></w:body></w:document>'
    out = io.BytesIO()
    with zipfile.ZipFile(out, "w") as z:
        z.writestr("word/document.xml", xml)
    return out.getvalue()


check("photo", as_file(picture(), "PNG"), "hello world")
check("scanned pdf", as_file(picture(), "PDF"), "hello world")
check("word file", docx(), "hello from word")

device = "ocr-" + uuid.uuid4().hex[:12]
call("POST", "/documents/file", device, as_file(Image.new("RGB", (800, 600), "white"), "PNG"))
blank = wait_for_job(device, seconds=10)
print(f"{'ok  ' if blank is None else 'FAIL'} blank photo: no job created")
failures += blank is not None

sys.exit(1 if failures else 0)
