# syntax=docker/dockerfile:1
FROM python:3.12.9-slim
ENV PYTHONDONTWRITEBYTECODE=1 PYTHONUNBUFFERED=1
WORKDIR /srv
COPY requirements.txt .
# The app user needs a writable home: CrewAI stores its data under $HOME and, without
# it, failed to import with "Permission denied: '/home/app'" so live mode silently
# fell back to simulation.
RUN pip install --no-cache-dir -r requirements.txt \
 && useradd --system --uid 10001 --create-home --home-dir /home/app app
COPY --chown=app:app app ./app
USER app
EXPOSE 8000
HEALTHCHECK --interval=15s --timeout=5s --start-period=20s --retries=5 \
  CMD python -c "import urllib.request;urllib.request.urlopen('http://127.0.0.1:8000/healthz', timeout=3)" || exit 1
CMD ["uvicorn", "app.main:app", "--host", "0.0.0.0", "--port", "8000"]
