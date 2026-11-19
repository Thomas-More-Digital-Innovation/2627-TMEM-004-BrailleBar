"""Runs real Tesseract, inside the test image:
docker build --target test -t ocr-test backend/ocr-service
docker run --rm -v "${PWD}/contracts:/contracts:ro" ocr-test
"""

import io
import json
import zipfile

import jsonschema
import pypdfium2 as pdfium
import pytest
from PIL import Image, ImageDraw, ImageFont

import ocr
import worker

UPLOAD = {"id": "e1", "type": "document.uploaded", "traceparent": "00-abc-def-01",
          "payload": {"documentId": "d1", "deviceId": "dev1", "source": "pdf", "needsOcr": True}}


def text_of(pages):
    return " ".join(line["text"] for page in pages for line in page["lines"]).lower()


def picture(text="hello world from the scanner"):
    image = Image.new("RGB", (1600, 300), "white")
    ImageDraw.Draw(image).text((40, 100), text, fill="black", font=ImageFont.load_default(size=64))
    return image


def png(image):
    out = io.BytesIO()
    image.save(out, "PNG")
    return out.getvalue()


def docx(*paragraphs, xml=None):
    body = "".join(f'<w:p><w:r><w:t>{p}</w:t></w:r></w:p>' for p in paragraphs)
    xml = xml or f'<w:document xmlns:w="{ocr.W[1:-1]}"><w:body>{body}</w:body></w:document>'
    out = io.BytesIO()
    with zipfile.ZipFile(out, "w") as z:
        z.writestr("word/document.xml", xml)
    return out.getvalue()


def image_pdf(image):
    out = io.BytesIO()
    image.save(out, "PDF", resolution=150)
    return out.getvalue()


def contract(name, event):
    with open("/contracts/events.schema.json") as f:
        schema = json.load(f)
    jsonschema.Draft7Validator({"$ref": f"#/$defs/{name}", "$defs": schema["$defs"]}).validate(event)


def test_photo_is_read_with_positions():
    pages = ocr.read_document(png(picture()), "image/png")
    assert "hello world" in text_of(pages)
    line = pages[0]["lines"][0]
    assert line["box"]["w"] > 0 and len(line["words"]) >= 2 and line["words"][0]["box"]["h"] > 0


def test_scanned_pdf_goes_through_ocr():
    assert "hello world" in text_of(ocr.read_document(image_pdf(picture()), "application/pdf"))


def test_docx_text_is_used_directly():
    pages = ocr.read_document(docx("first paragraph", "second one"), ocr.DOCX)
    assert [line["text"] for line in pages[0]["lines"]] == ["first paragraph", "second one"]


def test_docx_with_entities_is_refused():
    bomb = '<!DOCTYPE x [<!ENTITY a "aaaa">]><x>&a;</x>'
    with pytest.raises(Exception):
        ocr.read_document(docx(xml=bomb), ocr.DOCX)


def test_blank_photo_is_refused():
    with pytest.raises(ocr.Unreadable, match="hard to read"):
        ocr.read_document(png(Image.new("RGB", (800, 600), "white")), "image/png")


def test_low_confidence_is_refused(monkeypatch):
    def poor(image, number, confidences):
        confidences.append(20)
        return ocr._text_page(number, 1, 1, ["maybe text"])

    monkeypatch.setattr(ocr, "_ocr_image", poor)
    with pytest.raises(ocr.Unreadable, match="hard to read"):
        ocr.read_document(png(picture()), "image/png")


def test_too_many_pages_are_refused():
    pdf = pdfium.PdfDocument.new()
    for _ in range(ocr.MAX_PAGES + 1):
        pdf.new_page(100, 100)
    out = io.BytesIO()
    pdf.save(out)
    with pytest.raises(ocr.Unreadable, match="too long"):
        ocr.read_document(out.getvalue(), "application/pdf")


def test_events_follow_the_contract():
    pages = ocr.read_document(png(picture()), "image/png")
    done = worker.completed_event(UPLOAD, pages)
    contract("document.ocr.completed", done)
    assert done["traceparent"] == UPLOAD["traceparent"]
    assert done["id"] == worker.completed_event(UPLOAD, pages)["id"], "a redelivery must give the same event id"

    failed = worker.failed_event(UPLOAD, ocr.HARD_TO_READ)
    contract("print.job.status", failed)
    assert failed["payload"]["state"] == "failed"
