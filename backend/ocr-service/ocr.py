"""Turns an uploaded file into pages of text, in the shape of the document.ocr.completed contract."""

import io
import zipfile

import pypdfium2 as pdfium
import pytesseract
from defusedxml import ElementTree
from PIL import Image, ImageOps

LANG = "nld+eng"
DPI = 300
MAX_PAGES = 50
MAX_DOCX_XML_BYTES = 20 << 20
MIN_TEXT_CHARS = 20  # fewer on a PDF page: a scan, maybe with a stray page number in its text layer
MIN_CONFIDENCE = 50  # mean Tesseract word confidence (0-100); a guess until tested on real photos
TESSERACT_TIMEOUT = 120  # seconds per page

DOCX = "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
W = "{http://schemas.openxmlformats.org/wordprocessingml/2006/main}"
NO_BOX = {"x": 0, "y": 0, "w": 0, "h": 0}  # text from a file has no position; Layout only uses the text

HARD_TO_READ = "This scan is hard to read. Please try again with more light and the whole page in view"
NO_TEXT = "No text was found in this file"
TOO_LONG = f"This file is too long, the limit is {MAX_PAGES} pages"


class Unreadable(Exception):
    """The file itself is the problem: retrying won't help and the user should be told why."""


def read_document(data: bytes, content_type: str) -> list[dict]:
    """Text that is already in the file is used as it is, because it is perfect; only real scans go through OCR."""
    confidences: list[int] = []
    if content_type == DOCX:
        pages = [_read_docx(data)]
    elif content_type == "application/pdf":
        pages = _read_pdf(data, confidences)
    else:
        image = ImageOps.exif_transpose(Image.open(io.BytesIO(data)))  # phones store the rotation separately
        pages = [_ocr_image(image, 1, confidences)]

    if not any(line["text"].strip() for page in pages for line in page["lines"]):
        raise Unreadable(HARD_TO_READ if content_type.startswith("image/") else NO_TEXT)
    # A blind reader can't spot garbage on paper, so a bad scan is refused instead of printed.
    if confidences and sum(confidences) / len(confidences) < MIN_CONFIDENCE:
        raise Unreadable(HARD_TO_READ)
    return pages


def _read_pdf(data: bytes, confidences: list[int]) -> list[dict]:
    pdf = pdfium.PdfDocument(data)
    if len(pdf) > MAX_PAGES:
        raise Unreadable(TOO_LONG)
    pages = []
    for number in range(1, len(pdf) + 1):
        page = pdf[number - 1]
        text = page.get_textpage().get_text_bounded()
        if len(text.strip()) >= MIN_TEXT_CHARS:
            width, height = page.get_size()
            pages.append(_text_page(number, width, height, text.splitlines()))
        else:
            pages.append(_ocr_image(page.render(scale=DPI / 72).to_pil(), number, confidences))
    return pages


def _read_docx(data: bytes) -> dict:
    with zipfile.ZipFile(io.BytesIO(data)) as z:
        if z.getinfo("word/document.xml").file_size > MAX_DOCX_XML_BYTES:
            raise Unreadable(TOO_LONG)
        root = ElementTree.fromstring(z.read("word/document.xml"), forbid_dtd=True)
    paragraphs = ["".join(t.text or "" for t in p.iter(f"{W}t")) for p in root.iter(f"{W}p")]
    # Word only decides page breaks when it displays a file, so this is one long page; Layout makes braille pages.
    return _text_page(1, 0, 0, paragraphs)


def _text_page(number: int, width: float, height: float, lines: list[str]) -> dict:
    return {
        "page": number,
        "width": width,
        "height": height,
        "lines": [
            {"text": line.strip(), "box": NO_BOX, "words": [{"text": w, "box": NO_BOX} for w in line.split()]}
            for line in lines
        ],
    }


def _ocr_image(image: Image.Image, number: int, confidences: list[int]) -> dict:
    data = pytesseract.image_to_data(
        image.convert("L"), lang=LANG, output_type=pytesseract.Output.DICT, timeout=TESSERACT_TIMEOUT
    )
    grouped: dict[tuple, list[dict]] = {}  # insertion order is Tesseract's reading order
    for i, text in enumerate(data["text"]):
        confidence = int(float(data["conf"][i]))
        if not text.strip() or confidence < 0:
            continue
        confidences.append(confidence)
        key = (data["block_num"][i], data["par_num"][i], data["line_num"][i])
        box = {"x": data["left"][i], "y": data["top"][i], "w": data["width"][i], "h": data["height"][i]}
        grouped.setdefault(key, []).append({"text": text.strip(), "box": box})

    lines = []
    for words in grouped.values():
        left = min(w["box"]["x"] for w in words)
        top = min(w["box"]["y"] for w in words)
        right = max(w["box"]["x"] + w["box"]["w"] for w in words)
        bottom = max(w["box"]["y"] + w["box"]["h"] for w in words)
        lines.append({
            "text": " ".join(w["text"] for w in words),
            "box": {"x": left, "y": top, "w": right - left, "h": bottom - top},
            "words": words,
        })
    return {"page": number, "width": image.width, "height": image.height, "lines": lines}
