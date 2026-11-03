"""agent-runtime package.

The runtime takes its configuration ONLY from the process environment (docker compose / the operator). CrewAI
(crewai/llm.py) and litellm call ``dotenv.load_dotenv()`` at import time; python-dotenv then walks UP from the
library's own directory and, in a checkout, finds the repository's ``.env``. Live finding (2026-11-02): a runtime
started with only GEMINI_API_KEY ended up with the developer's DEEPSEEK_API_KEY / ANTHROPIC_API_KEY (silent extra
failover providers, a different /healthz) and SIMULATION=true (per-request checks flipped) after the first live
call. ``PYTHON_DOTENV_DISABLED`` turns that off before anything imports those libraries; an operator who really
wants it can set PYTHON_DOTENV_DISABLED=false.
"""
import os

os.environ.setdefault("PYTHON_DOTENV_DISABLED", "true")
