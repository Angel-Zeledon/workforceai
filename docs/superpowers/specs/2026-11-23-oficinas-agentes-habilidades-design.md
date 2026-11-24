# Oficinas, agentes y habilidades: cada cosa en su sitio (diseño)

Estado: **aprobado por el dueño el 2026-11-23** ("si a las oficinas y eso", sobre la propuesta del chat).
Amplía `docs/architecture/equipos-oficinas.md` (§3, §4, §8).

## 1. Para qué

El dueño: agente, para quién trabaja y habilidades están en la misma pantalla y confunden, aunque significan cosas muy
distintas. Un empleado nuevo debería poder contratarse para una oficina concreta. Puede haber más de una oficina y hay
que poder moverse entre ellas.

Lo que hay hoy: Contratar mezcla Agentes, Habilidades y Plantillas y no pregunta la oficina; el mismo agente sale como
"Oficina General", "Sin oficina" y "Legal" según la pantalla; cinco palabras para lo mismo (Oficina, Departamento,
Equipos, Estructura, Agentes y oficinas); un agente solo puede ser de una oficina; cambiar de oficina solo existe en la
3D y solo cambia el dibujo.

## 2. Vocabulario (uno por cosa, en toda la app)

| Palabra | Pregunta | Nota |
|---|---|---|
| **Oficina** | ¿Dónde se trabaja y quiénes están? | "Departamento" y "Equipo" dejan de usarse para esto |
| **Agente** | ¿Quién es, qué puesto tiene y para quién trabaja? | |
| **Puesto** | ¿De qué se encarga y a quién reporta? | Parte del agente y de la oficina, no pantalla aparte |
| **Habilidad** | ¿Cómo se hace algo aquí? | |
| **Plantilla / equipo listo** | Punto de partida | Dentro de Oficinas: "Empezar con un equipo listo" |

## 3. Pantallas

Menú: **Oficinas** · **Agentes** · **Habilidades** como entradas separadas. "Agentes y oficinas" y "Equipos" salen del
menú. "Departamento" (la entrada de archivos de la empresa) pasa a "Archivos de la oficina" y sigue a la oficina
elegida. Los enlaces viejos (`/org?tab=…`, `/teams`, `/d/…`) siguen llegando a donde corresponde.

- **Oficinas:** lista de oficinas (abierta/privada, responsable, personas, agentes) → página de una oficina: Personas ·
  Agentes · Puestos · Habilidades de la oficina · Archivos · Memoria. Organigrama y reglas de delegación, aquí.
- **Agentes:** filas agrupadas por oficina base, con buscador y filtros: nombre, puesto, oficina base + otras, quién le
  puede pedir, estado. Ficha con cuatro pestañas que no se mezclan:
  **Quién es** · **Dónde y para quién trabaja** · **Qué sabe hacer** (habilidades, con su origen: puesto, oficina o
  propia) · **Permisos**.
- **Habilidades:** la biblioteca. Sin tarjetas de "Agentes" ni "Plantillas". Cada habilidad dice quién la usa.

**Contratar, tres pasos:** 1) ¿Para qué oficina? (la actual elegida) 2) ¿Qué puesto? (los de la oficina o uno del
catálogo, con sus habilidades sugeridas que se pueden quitar) 3) Nombre y quién le puede pedir (la oficina, personas,
toda la empresa). Nadie queda "sin oficina": sin elegir es General, y se dice.

## 4. Moverse entre oficinas

Selector fijo en el encabezado, junto a Lista | Oficina 3D, en todas las pantallas de trabajo:
`[Legal ▾]  Todas mis oficinas · Legal · Ventas · General · Dirección (privada, cerrada)`.

- Cambia a la vez: equipo, conversación de la oficina, En curso, Te necesitan, Listo, archivos de la oficina y la 3D
  (se entra por la puerta: transición corta, sin animación con movimiento reducido).
- Se recuerda por persona y dispositivo. Con una sola oficina no aparece. Las privadas donde la persona no está salen
  cerradas, con "Pedir acceso" si ya existe esa función.
- En "Todas mis oficinas" se ve todo lo que la persona puede ver, con la oficina indicada; al escribir se elige a cuál va.
- La conversación "Toda la oficina" es por oficina: `POST /messages` acepta `office_id`; el reparto solo considera
  agentes de esa oficina; mensaje y pedido guardan `office_id`; el historial se filtra por ella. Lo anterior a este
  cambio queda en General.

## 5. Agentes en varias oficinas

Un agente tiene **una oficina base** (escritorio, presupuesto, jefe, puesto) y puede **trabajar también en** otras. Es el
mismo agente (no se copia): misma persona, mismas habilidades propias, un solo lugar en el límite del plan.

- Pertenecer a otra oficina da a sus miembros poder pedirle trabajo. Lo concede quien lleva la oficina base y lo acepta
  quien lleva la otra (un admin puede las dos cosas). Queda en auditoría y se puede quitar.
- **La memoria y los archivos no cruzan.** Cada pedido lleva la oficina desde la que se hizo. En ese pedido el agente
  solo usa: memoria de la empresa, memoria de **esa** oficina, archivos de **esa** oficina y los compartidos con ella, y
  los del que pide. No usa memoria ni archivos de su oficina base ni de otra, aunque tenga permiso en otro contexto. Lo
  que aprende queda en la memoria de esa oficina. Las habilidades de una oficina solo valen en ella.
- Si no se sabe la oficina de un pedido (automático, anterior), vale la base y nunca otra: ante la duda, menos acceso.
- Los agentes personales siguen igual (son de su persona).
- El costo se anota a la oficina que pidió; el tope del agente sigue siendo uno.
- En la 3D está sentado en su oficina base. En otra, sale en una mesa de visita mientras trabaja para ella; si está
  ocupado en otra, se ve "ocupado en Legal" (a quien no puede ver Legal: "ocupado en otra oficina").

## 6. Formas consideradas

A) **Agente único con oficina base y otras** (elegida). B) Copiar el agente por oficina: duplica costo y límite, y las
copias divergen. C) Agente de toda la empresa sin oficina: es lo de hoy y no dice para quién trabaja ni aísla.

## 7. Datos y compatibilidad

- `agent_offices (org_id, agent_id, office_id, granted_by, accepted_by, created_at)`, RLS y migración de bajada. La base
  sigue saliendo de `agent_access`/departamento (`offices.Model.effective`), que pasa a ser **la única fuente** para
  todas las pantallas.
- `office_id` (nulable) en mensajes y pedidos; nulo = General.
- Nada cambia para una empresa con solo General, ni para los agentes de hoy: siguen en su oficina.

## 8. Cómo se comprueba

- Un pedido desde Ventas a un agente con base Legal no recibe ni memoria, ni archivos, ni notas, ni habilidades, ni
  historial de Legal (pruebas en cada camino: chat, tarea, consulta, reunión, proyecto, ayudantes, búsqueda de archivos).
- Quitar al agente de una oficina corta el acceso al momento, también a pedidos en marcha que aún no empezaron.
- Privadas: quien no es miembro no ve agentes, mensajes ni nombres de pedidos por ninguna vía (lista, eventos, 3D).
- El mismo agente dice la misma oficina en todas las pantallas.
- Enlaces viejos llegan; una empresa con una sola oficina no ve el selector; móvil, teclado, es/en/pt.

## 9. Orden de construcción

1. Motor: varias oficinas, contexto por oficina, contratar con oficina, mensajes por oficina.
2. Menú y palabras. 3. Selector de oficina. 4. Agentes, ficha y Contratar. 5. Página de Oficinas. 6. La 3D.
