FROM python:3.13-slim AS runtime
RUN apt-get update \
 && apt-get install -y --no-install-recommends tesseract-ocr tesseract-ocr-nld tesseract-ocr-eng \
 && rm -rf /var/lib/apt/lists/*
WORKDIR /app
COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt
COPY ocr.py worker.py ./
RUN useradd --no-create-home ocr
USER ocr

FROM runtime AS test
USER root
RUN pip install --no-cache-dir pytest==8.4.1 jsonschema==4.25.0
COPY test_ocr.py .
USER ocr
CMD ["python", "-m", "pytest", "-q", "-p", "no:cacheprovider"]

# last, so a plain build gives the worker
FROM runtime
CMD ["python", "-u", "worker.py"]
