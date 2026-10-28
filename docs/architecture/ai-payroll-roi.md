# Nómina de IA y retorno (B15)

Pone el **costo de IA medido** de cada empleado al lado de una **estimación** de las horas humanas que habría
tomado su trabajo terminado, con la metodología siempre visible. Nunca se presenta como un ahorro medido.

Código: `backend/internal/application/roi.go` (modelo y servicio), `backend/internal/api/roi.go`,
`backend/internal/infrastructure/{memory,postgres}/roi.go`, migración `796_roi_settings` (+ `migrations/down`).
Frontend: `frontend/src/components/cost/RoiPanel.tsx` (tablero, sección Costos), `frontend/src/lib/roiLogic.ts`
(mismo modelo, pruebas en `roiLogic.test.mjs`), demo en `frontend/src/lib/mock/roi-mock.ts`.

## Modelo (transparente y configurable)

```
horas estimadas  = tareas contadas × minutos por tarea ÷ 60
tiempo humano $  = horas estimadas × tarifa por hora de la organización   (solo si la organización la ingresó)
costo IA / hora  = costo de IA medido ÷ horas estimadas
```

- **Se cuentan** las tareas con estado `done` que terminaron dentro de la ventana (7, 30 o 90 días; 1..365 por API).
- **Se excluyen** (y se informan aparte): las que necesitaron rehacerse (`output.review` con `reworks > 0` o veredicto
  distinto de `pass`) o un reintento (`metrics.attempts > 1`), las fallidas y las omitidas (`metrics.skipped`).
- **Minutos por tarea**: el valor del rol del agente si la organización lo definió; si no, el valor por defecto de la
  organización; si la organización nunca guardó su modelo, **30 minutos marcados como supuesto** ("supuesto inicial,
  no un dato"). El "tipo de tarea" es el rol del agente (contador, abogada, ventas...), lo único que todas las tareas
  tienen de forma fiable. 0 minutos en un rol = ese rol no cuenta.
- **Costo de IA**: el libro de uso (`usage`) en la misma ventana, por agente. Es lo único medido del panel.
- **Tarifa por hora**: no hay valor por defecto. Sin tarifa la API devuelve `est_human_cost_usd: null` y la UI muestra
  solo horas ("Sin tarifa: solo horas"). Una tarifa `<= 0` o `null` al guardar la borra.

Cada respuesta de `GET /roi` trae `methodology` (ventana, reglas de exclusión, minutos y su origen, tarifa y su origen,
base del costo) y la UI la pinta junto a las cifras, no detrás de un clic.

## Seguridad y gobierno

- Lectura `metrics:read`; cambiar el modelo `org:manage` (dueño/administrador), auditado como `roi.settings_changed`
  (sin montos, solo qué cambió).
- No inicia trabajo ni cambia autonomía, permisos o topes: el interruptor de emergencia, el modo solo lectura, la pausa
  y los topes de gasto no se ven afectados ni se saltan.
- `roi_settings` con RLS por organización (`tenant_isolation`), igual que `202_rls_policies.sql`.

## Modo demo

El mock incluye un historial de ejemplo de 25 días por empleado (con algunas tareas rehechas y fallidas para que se
vea la exclusión) más las tareas reales de la sesión; la UI lo rotula "Demo: incluye un historial de ejemplo".

## Límites conocidos

- Los minutos por rol son un promedio: una tarea larga y una corta valen lo mismo. Es una estimación gruesa a propósito.
- El costo por agente lee todo el libro de uso de la organización y filtra por fecha en memoria (como el desglose de
  costos existente); para organizaciones muy grandes convendría una consulta por ventana.
- La moneda es USD, la misma del libro de uso.
- La prueba de Postgres (`roi_pg_test.go`) solo corre con `TEST_DATABASE_URL`.
