# Render image: PDF pages and images -> PNG/JPEG pixels for vision models
# (docs/architecture/agent-vision-pdf.md). One disposable container per job,
# started by the sandbox runner with the same isolation as code runs:
# --network none --read-only, /work tmpfs, non-root, no capabilities,
# cpu/memory/pids/time limits (backend/internal/sandbox/runnerd/render.go).
# There is no user code here: the program is fixed (render.py).
#
# Pinned: base image by digest, poppler-utils by exact Debian version, Pillow by
# exact version. Build:  docker build -t wfai-sandbox-render:1 sandbox/render
# Enable on the runner:  SANDBOX_RENDER_IMAGE=wfai-sandbox-render:1
FROM python:3.12-slim-bookworm@sha256:34386ef0cb081344d7ec1c103ba398e6e9f64e9ab3a1509accc92a4e24a07258

# poppler from bookworm-security (bump both pins together when Debian ships a new security update;
# a stale pin fails the build loudly instead of installing something else).
ARG POPPLER_VERSION=22.12.0-2+deb12u3
ENV PIP_NO_CACHE_DIR=1 PIP_DISABLE_PIP_VERSION_CHECK=1 PYTHONDONTWRITEBYTECODE=1 PYTHONUNBUFFERED=1

RUN set -e \
    && apt-get update \
    && apt-get install -y --no-install-recommends "poppler-utils=${POPPLER_VERSION}" "libpoppler126=${POPPLER_VERSION}" \
    && rm -rf /var/lib/apt/lists/* \
    && pip install pillow==12.3.0 \
    && python -c "import PIL; assert PIL.__version__ == '12.3.0'" \
    && (pip uninstall -y pip setuptools wheel 2>/dev/null || true) \
    && find /usr/local -name '__pycache__' -type d -prune -exec rm -rf {} + \
    && useradd --uid 65532 --no-create-home --shell /usr/sbin/nologin sandbox \
    && mkdir -p /opt/render

COPY render.py /opt/render/render.py

ENV HOME=/work
USER 65532:65532
WORKDIR /work
ENTRYPOINT ["python", "-B", "/opt/render/render.py"]
