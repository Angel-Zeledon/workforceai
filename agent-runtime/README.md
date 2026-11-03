# agent-runtime

Runtime Python (FastAPI + CrewAI) del AI Workforce OS. Recibe una tarea + contexto del backend Go y devuelve un resultado estructurado y/o `tool_requests`. **Nunca ejecuta herramientas ni llama sistemas externos.** Contrato: `docs/SPEC.md`.

## Endpoints
`POST /v1/plan`, `POST /v1/run-task`, `POST /v1/consult`, `POST /v1/synthesize`, `POST /v1/estimate` (rango de costo por tarea antes de ejecutar; no llama al LLM; usa las mismas tarifas que el costo real), `GET /healthz` (`{status, mode: "simulation"|"live"}`).

## Motores (`app/engine.py`: interfaz `AgentEngine`)
- `SimulationEngine`: guionado, latencias simuladas 2-6s (`SIM_LATENCY_SCALE=0` las desactiva), costos/tokens plausibles. Se usa si `SIMULATION=true` o si no hay ningun proveedor configurado. Ignora la politica de proveedores (no sale ningun dato) y no agrega campos al contrato.
- `CrewAIEngine`: un Agent/Task/Crew de CrewAI por rol, salida validada contra `StructuredOutput`, con **seleccion de proveedor y respaldo automatico** (`app/providers.py`).

## Proveedores de modelo, respaldo y politica de datos
Proveedores: `deepseek`, `anthropic` y `custom` (un endpoint compatible con OpenAI: regional, vLLM/Ollama local, pasarela propia). Basta con **al menos uno** configurado.

| Variable | Descripcion |
|---|---|
| `DEEPSEEK_API_KEY`, `DEEPSEEK_MODEL` | DeepSeek (def. `deepseek/deepseek-chat`) |
| `ANTHROPIC_API_KEY`, `ANTHROPIC_MODEL` | Anthropic (def. `anthropic/claude-sonnet-5-5`) |
| `CUSTOM_LLM_BASE_URL` (obligatoria), `CUSTOM_LLM_API_KEY` (opcional), `CUSTOM_LLM_MODEL` | Endpoint compatible OpenAI. El modelo se pasa como `openai/<nombre>` |
| `CUSTOM_PRICE_IN_PER_M`, `CUSTOM_PRICE_OUT_PER_M` | USD por millon de tokens del endpoint custom (sin ellas se usa la tarifa Sonnet, conservadora). Un modelo local puede ser `0` |
| `MODEL` | Heredada: `proveedor/modelo` aplica a ese proveedor; un nombre sin prefijo aplica al primario |
| `MODEL_PROVIDER_ORDER` | Orden global de respaldo, separado por comas. Def. `deepseek,anthropic,custom` (el orden historico; para que Anthropic sea primario: `anthropic,deepseek`) |
| `MODEL_PROVIDER_ORDER_<ROL>` | Orden para un rol/tarea: el `role` del agente en MAYUSCULAS (`MODEL_PROVIDER_ORDER_LEGAL`), o `PLAN`, `CONSULT`, `SYNTHESIZE` |
| `ALLOWED_PROVIDERS` | Techo del operador: solo estos proveedores pueden recibir datos (p. ej. `anthropic,custom` excluye DeepSeek para todo el despliegue) |
| `PROVIDER_RETRIES` (1), `PROVIDER_RETRY_BACKOFF_S` (1.0), `PROVIDER_TIMEOUT_S` (120) | Reintentos en el mismo proveedor ante timeout/5xx de red antes de pasar al siguiente |
| `PROVIDER_RATE_LIMIT_RETRIES` (2), `PROVIDER_MAX_RETRY_WAIT_S` (60), `PROVIDER_MAX_RETRY_WAIT_S_CHAT` (8) | Limite de tasa (429): espera lo que pide el proveedor (`retryDelay` de Gemini / `Retry-After`) si cabe en el tope de esa clase de llamada (route/chat: 8 s, el backend les da 30 s); si no, HTTP 429 `provider_rate_limited` con `retry_after_s`. Es la UNICA capa de reintento: el SDK va con `max_retries=0` y `CREW_MAX_RETRY_LIMIT` es 0 por defecto |
| `PROVIDER_DEADLINE_S_CHAT` (25) | Tiempo TOTAL de una llamada interactiva (route/chat), reintentos incluidos: cada solicitud lleva como timeout lo que queda y no se empieza un reintento que no alcance a terminar. El backend abandona a los 30 s (`CHAT_TIMEOUT`); despues de eso la respuesta se tira pero se cobra |
| `PROVIDER_OVERLOAD_RETRIES` (2), `PROVIDER_OVERLOAD_BACKOFF_S` (2.0) | Proveedor saturado (Gemini 503 "high demand", Anthropic 529): backoff exponencial 2 s, 4 s, acotado igual |
| `LLM_RPM_<PROVEEDOR>` (p. ej. `LLM_RPM_GEMINI=8`) | Ritmo local: como mucho N llamadas por minuto a ese proveedor (capas gratuitas). Si una llamada tendria que esperar mas que su tope, falla con `provider_rate_limited` sin enviar nada |
| `THINKING_HEADROOM_<PROVEEDOR>` (Gemini: 1024) | Tokens extra en `max_tokens` para el razonamiento oculto que el tope cuenta (Gemini). El guardia de presupuesto los cuenta; si solo ellos superan el tope de la llamada, se omiten |
| `PYTHON_DOTENV_DISABLED` (true) | El runtime NO lee archivos `.env` (CrewAI llama `load_dotenv()` al importarse y encontraba el `.env` del repo) |
| `PRICE_IN_PER_M`, `PRICE_OUT_PER_M` | Fuerza una tarifa para TODOS los proveedores (sobrescribe la tabla) |

**Orden de intentos** de una llamada: `MODEL_PROVIDER_ORDER_<ROL>` -> `preferred_providers` de la solicitud -> `MODEL_PROVIDER_ORDER`; cada fuente solo agrega los que faltan, asi que todo proveedor configurado y permitido es respaldo. Un error transitorio se reintenta en el mismo proveedor; cualquier fallo despues de eso pasa al siguiente. La respuesta indica quien contesto.

**Politica de datos (se aplica ANTES de enviar).** Lista efectiva = `ALLOWED_PROVIDERS` (env) interseccion `allowed_providers` (solicitud). Un proveedor excluido ni se llama ni se usa como respaldo. La solicitud nunca puede ampliar el techo del operador.

### Campos opcionales del contrato (retrocompatibles)
- Solicitudes (`/v1/plan`, `/v1/run-task`, `/v1/consult`, `/v1/synthesize`): `allowed_providers: string[] | null` (ids `deepseek|anthropic|custom`; `null`/ausente = sin restriccion de solicitud; `[]` = ninguno, falla) y `preferred_providers: string[] | null`. **El backend Go aun no los envia**: debe mapear el ajuste por organizacion (p. ej. "excluir DeepSeek") a `allowed_providers` en cada llamada.
- Respuestas (solo modo live; se omiten en simulacion): `provider` y `model` (tambien `usage.provider`, `usage.model`) y `usage.failed_providers` (proveedores que fallaron antes del que respondio). `usage.cost_usd` usa la tarifa del proveedor que respondio. `/healthz` informa el proveedor primario (`deepseek|anthropic|custom|simulation`).

### Errores estables (nunca silenciosos)
El detalle es un objeto `{code, message, providers_tried, allowed_providers, configured_providers}` (sin secretos; los mensajes del proveedor pasan por `redact_secrets`):
- HTTP 422 `no_allowed_provider`: ningun proveedor configurado esta permitido; no se envio nada.
- HTTP 502 `all_providers_failed`: todos los permitidos fallaron (`providers_tried` los lista).
- HTTP 429 `provider_rate_limited` (+ cabecera `Retry-After` y `retry_after_s`): todos los intentados siguen limitando la tasa tras los reintentos. Transitorio: el backend lo reintenta.
- Cuando intentos fallidos ya consumieron tokens, el detalle trae `spent_usd`; `/v1/route` que cae a las reglas devuelve igualmente su `usage` (source `rules`) para que el backend lo cobre.
- Otros fallos del motor siguen siendo HTTP 502 con `detail` de texto (`engine error: ...`).

## Seguridad
La redaccion de secretos (`app/redact.py`) sigue aplicandose a todo lo que entra al prompt y a lo que sale; la politica de proveedores se suma a ella.
`external_content`, outputs de dependencias y memoria se insertan solo en el prompt de usuario, dentro de bloques `DATOS NO CONFIABLES` con instruccion de no obedecerlos; el system prompt se arma solo con persona/responsabilidades/herramientas del backend (`app/security.py`).

## Uso
```
python -m venv .venv && .venv\Scripts\pip install -r requirements-dev.txt
.venv\Scripts\python -m pytest
.venv\Scripts\uvicorn app.main:app --port 8000
docker build -t agent-runtime . && docker run -p 8000:8000 agent-runtime
```
