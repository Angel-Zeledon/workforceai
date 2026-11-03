# Permisos de aprobación por agente, por acción y por proyecto

Fecha: 2026-11-03. Estado: aprobado por el dueño en el chat ("si hazlo").

## Por qué

El dueño no quiere aceptar cada aprobación: quiere decidir de antemano qué necesita su OK, por agente o por tipo de
trabajo. Hoy el motor existe (`docs/architecture/06-permisos-autonomia.md`: 4 niveles `suggest`, `approve_each`,
`rules`, `autonomous`; reglas allow/deny por herramienta con condiciones; gobierno de la organización en
`/policy/rules`; doble aprobación; "permitir siempre" por proyecto en `StandingApprovals`; escalera de confianza en
`docs/architecture/trust-ladder.md`), pero las reglas solo se cambian por API y la autonomía casi no se entiende en
pantalla.

## Lo que se construye

1. **Tres niveles en palabras simples**, en el panel de cada agente (sección "Permisos"):
   - **Me pregunta todo** (`approve_each`).
   - **Solo lo importante** (por defecto, `rules` con un conjunto de reglas de fábrica): hace solo lo interno (leer,
     buscar, escribir borradores, crear y editar archivos internos, analizar); pregunta antes de lo que sale de la
     empresa o no tiene vuelta (enviar correos o mensajes, publicar, pagar o facturar, borrar, contratos y propuestas).
   - **Trabaja solo** (`autonomous`): no pregunta nada salvo el piso de seguridad.
   `suggest` ("Solo sugiere") sigue existiendo como opción avanzada. El techo de autonomía del rol y la escalera de
   confianza se respetan: si el rol no permite un nivel, la opción aparece deshabilitada con el motivo.
2. **Excepciones por tipo de acción**, debajo del nivel: una lista de categorías comprensibles (enviar correos y
   mensajes, publicar, pagar y facturar, borrar archivos, contratos y propuestas, gastos con monto) que se traducen a
   las herramientas reales del agente. Cada una: **Preguntar siempre**, **Nunca preguntar** o **Preguntar si…** (más de
   N por día, monto mayor a X, destinatario nuevo). Se guardan como reglas del agente en el motor existente.
3. **Por proyecto y por lote:** "En este proyecto/lote no preguntar para [categoría]" (p. ej. enviar el correo a los
   clientes del lote): se aprueba una vez para todo el lote. Extiende los permisos permanentes por proyecto que ya
   existen (§10 de `06-permisos-autonomia.md`) a los lotes del diseño `2026-11-03-proyectos-por-cliente-y-ayuda-design.md`
   y los hace visibles y revocables en la vista del proyecto y del lote.
4. **Piso de seguridad:** pagos, contratos y propuestas, y borrar sin vuelta preguntan siempre, también en "Trabaja
   solo" (es el invariante de §3 de `06-permisos-autonomia.md`). Solo el **dueño de la cuenta** puede quitarlo para un
   agente y una categoría concretos, confirmando "Entiendo el riesgo"; queda en la auditoría y se ve marcado en el
   panel. Los límites de presupuesto y de ritmo siguen activos siempre.
5. **Aprender de las decisiones:** en Pendientes (la bandeja de aprobaciones), junto a Aprobar: **"Aprobar y no volver
   a preguntar por esto"**. Crea la excepción para ese agente y esa categoría (o ese proyecto/lote si la aprobación
   viene de uno), con un resumen de lo que se creó y un enlace para deshacerla.
6. **Por defecto de la empresa:** Ajustes → Aprobaciones: nivel por defecto de los agentes nuevos y una vista de todas
   las excepciones activas por agente (quién y cuándo las creó), con revocar.
7. **Quién puede cambiar qué:** administradores y el responsable del equipo del agente cambian niveles y excepciones;
   el piso de seguridad solo el dueño. Todo cambio queda en la auditoría.

## Metas, no metas, anti-metas

- **Metas:** dejar de aprobar lo rutinario sin perder el control de lo delicado; entenderlo sin leer documentación.
- **No metas:** cambiar el motor de decisión ni sus invariantes; reglas por persona (E3/E5).
- **Anti-metas:** que un agente envíe, pague o borre algo sin que nadie lo haya decidido; que el usuario no sepa por
  qué algo le pidió aprobación (cada aprobación dice qué regla la pidió y cómo evitarla la próxima vez).

## Restricciones

- Reutilizar el motor de `06-permisos-autonomia.md` (§8: dos capas, gana la más estricta), `autonomy_rules`,
  `/policy/rules`, `StandingApprovals` y la bandeja de aprobaciones; sin un segundo motor.
- Organizaciones existentes: su comportamiento no cambia hasta que alguien toque los permisos (los agentes en `rules`
  sin reglas siguen pidiendo aprobación como hoy). "Solo lo importante" con reglas de fábrica aplica a agentes nuevos
  y a quien lo elija.
- Textos es/en/pt, estilo de la app, pruebas en backend y frontend.

## A criterio de quien construye

El mapeo exacto de categorías a herramientas, la forma de guardar las reglas de fábrica, el diseño visual de la
sección dentro del estilo de la app.
