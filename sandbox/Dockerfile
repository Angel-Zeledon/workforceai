# Image in which approved code runs (one disposable container per run).
# Contents are pinned: base image by digest, libraries by exact version.
# Build:  docker build -t wfai-sandbox-python:1 sandbox/
# The runner starts it with: --network none --read-only, a /work tmpfs, a
# non-root user, no capabilities and cpu/memory/pids limits (see
# backend/internal/sandbox/runnerd). Nothing secret is ever baked in or passed.
FROM python:3.12-slim@sha256:05cda9777409a9c3ffddd94a4c476b79f0769a0b4857f0c7ed9226b6800b0d6f

ENV PIP_NO_CACHE_DIR=1 PIP_DISABLE_PIP_VERSION_CHECK=1 PYTHONDONTWRITEBYTECODE=1 PYTHONUNBUFFERED=1

# Data libraries only. No network tools, no compilers, no shell utilities beyond the base image.
RUN pip install \
      numpy==2.1.3 \
      pandas==2.2.3 \
      openpyxl==3.1.5 \
      matplotlib==3.9.2 \
    && pip uninstall -y pip setuptools wheel 2>/dev/null || true \
    && find /usr/local -name '__pycache__' -type d -prune -exec rm -rf {} + \
    && useradd --uid 65532 --no-create-home --shell /usr/sbin/nologin sandbox \
    && mkdir -p /opt/sandbox

COPY entry.py /opt/sandbox/entry.py

# Matplotlib/pandas need a writable home: /work is a tmpfs mounted by the runner.
ENV HOME=/work MPLBACKEND=Agg MPLCONFIGDIR=/work/.mpl
USER 65532:65532
WORKDIR /work
ENTRYPOINT ["python", "-B", "/opt/sandbox/entry.py"]
