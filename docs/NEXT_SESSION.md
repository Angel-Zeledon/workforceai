# Próxima sesión

## Actualización 2026-10-28: `feat/api-sources` fusionada a `main` (local, sin push)

Antes de fusionar, todo en verde: `go vet` y `go test ./...` del backend; 863 pruebas del runtime; 226 pruebas
unitarias del frontend, i18n y `npm run build`. Postgres (`TEST_DATABASE_URL`, migraciones 802-804) y la revisión en
navegador de Conexiones quedaron hechos el 27. `TestEveryBuiltinTemplateRunsAtMaximumSize` tarda 21 s en frío: lo
de los 10 min era la carga de la máquina. El build encontró un `<a>` interno en la bandeja de aprobaciones, ya
corregido con `next/link`.

Para el runtime usa `agent-runtime/.venv/Scripts/python -m pytest`: el Python global importa el paquete `tests`
de otro proyecto (`forecasting/ForecastingCore`) y falla al recolectar.

Pendiente:
1. **Gemini en la capa gratuita, por decisión del dueño** (2026-10-28): unas 20 solicitudes por día y por modelo,
   y Google puede usar el contenido para mejorar sus productos. Las evaluaciones en vivo tienen que caber en esa cuota,
   así que hay pocos casos por día. Conviene rotar la clave igual, porque se pegó en el chat.
2. `git push origin main` y redesplegar. En producción: definir `GEMINI_API_KEY` y dejar `VENDOR_INTEGRATIONS`
   vacío (las migraciones 802-804 se aplican solas al arrancar el backend).
3. Backlog que queda (sin integraciones de proveedor): B14 calendario fiscal, B18 "enséñale una vez", B19 voz.

### Lo que entró (2026-10-27)

Decisiones del dueño: **Gemini es el LLM**; **v1 sin integraciones de un proveedor concreto** (Slack, Gmail…),
solo formas genéricas de importar datos.

Hecho y probado de verdad:
- **Gemini** como proveedor (`gemini-3.6-flash`, razonamiento bajo). Evaluación en vivo: 4/6 casos dorados, los dos
  de inyección incluidos, USD 0.025; los 2 fallos fueron la cuota diaria de la capa gratuita.
- **Interruptor v1** `VENDOR_INTEGRATIONS` (apagado por defecto): oculta y rechaza conectores de proveedor y el canal
  de Slack; las conexiones viejas se conservan marcadas "Desactivada en v1".
- **Buzones de datos** (migración 803): URL + token; probado con curl (JSON y CSV llegan a la unidad, 401, token en
  la URL rechazado, 423 en solo lectura).
- **Importar desde un enlace** (migración 804): probado con un CSV público real (versión 1, luego `unchanged`) y
  rechazo de 127.0.0.1 y 169.254.169.254.

---

(Notas del 2026-10-12)

## Qué hay en `main`
- Rediseño corporativo: navegación lateral con rutas, landing `/welcome`, login dividido, guías y animaciones.
- Logo del agentito: favicon, íconos PWA y iOS.
- Historial de chats: general y por agente, enlazados.
- Archivos reales: Excel, Word y PDF, con visor en el panel.
- Tienda con inventario: comprar no equipa; se equipa desde Inventario.
- Sesión con cookie HttpOnly.
- "Permitir siempre" por proyecto, solo para escrituras internas versionadas.
- Memoria a largo plazo: pestaña Memoria, `memory.search` y notas.
- Tu propio LLM: OpenAI, Claude, Kimi o DeepSeek, en Ajustes → Modelo de IA o con `/modelo`.
- Comandos `/` en español e inglés (`/ayuda` los lista).
- Proyectos grandes con "Antes de empezar": subir archivos o usar datos de ejemplo coherentes.
- Aviso claro cuando el proveedor de IA se queda sin saldo.

## Bloqueado: DeepSeek sin saldo
La cuenta de DeepSeek quedó en -0.04 USD ("Insufficient Balance"). Para seguir:
1. Recarga en https://platform.deepseek.com, o configura otra key en Ajustes → Modelo de IA.
2. Pide en la siguiente sesión: **"haz las pruebas en vivo pendientes"**. Son tres:
   - `/resumir`, `/reunion` y `/excel` con el modelo real (costo y calidad).
   - Cierre consolidado de 3 filiales **con datos de ejemplo**: que los agentes calculen números reales.
   - Confirmar que `/healthz` del agent-runtime en producción dice `"mode":"live"`.

## Producción
- Redespliega desde `main`. Si ves el ícono viejo, recarga forzado (Cmd+Shift+R).
- Define `CONNECTIONS_KEK` (`openssl rand -base64 32`) en el backend. Sin ella no se pueden guardar las keys de "tu propio LLM" ni las de conectores.
- Tu contenedor `kb-embeddings` / `assistant-embeddings`, de otro proyecto, consume mucha CPU y RAM de Docker. Apágalo cuando trabajemos aquí.

## Prueba e2e abierta
- `e2e/tests/templates.spec.ts` "UI (Spanish) › duplicate a built-in…": el proyecto arranca (con datos de ejemplo) pero no termina en 10 min en simulación. Las pruebas de plantillas por API sí pasan. Revisar si leer los archivos de entrada alarga la ejecución simulada o si algún nodo queda esperando.

## Pendiente después
- Pulir monedas: revisar los 63 artículos y probar de punta a punta los bonos de proyecto y de calidad.
- Probar los conectores reales (Gmail, Google, LinkedIn, Slack) con cuentas de verdad.
- Búsqueda por significado para la memoria: hecha sin pgvector (migración 786, `docs/architecture/04-memoria.md` §6.1). Falta probar el test de Postgres con `TEST_DATABASE_URL` y activar `EMBEDDINGS_*` en producción.
