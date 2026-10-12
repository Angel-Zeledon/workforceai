# Próxima sesión (dejado el 2026-10-12)

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
- Búsqueda por significado (pgvector) para la memoria.
