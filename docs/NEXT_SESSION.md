# Próxima sesión

## Actualización 2026-10-27 (rama `feat/api-sources`, sin fusionar a `main`)

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

Pendiente:
1. **Cuenta de Gemini de pago.** La clave está en la capa gratuita: 20 solicitudes por día y por modelo, y Google
   puede usar el contenido para mejorar sus productos. Activar facturación y rotar la clave (se pegó en el chat).
   Luego repetir `scripts/eval-live --live` completo.
2. Probar con Postgres (`TEST_DATABASE_URL`): migraciones 802-804 y los stores de buzones y enlaces.
3. Ver en navegador las pestañas nuevas de Conexiones (buzones, enlaces) y la insignia "Desactivada en v1".
4. `internal/projects` `TestEveryBuiltinTemplateRunsAtMaximumSize` se pasó de 10 min con la máquina cargada: repetir
   en frío para saber si es carga o un problema real.
5. Fusionar `feat/api-sources` a `main` cuando lo anterior esté verde.
6. Backlog que queda (sin integraciones de proveedor): B14 calendario fiscal, B18 "enséñale una vez", B19 voz.

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
