# Chats en orden: un chat por "con quién hablas" y cada trabajo es un pedido

Fecha: 2026-11-03. Estado: aprobado por el dueño en el chat ("hazle si").

## Por qué

El dueño se enredó en Chats y teme que a sus usuarios les pase peor. Hoy un mismo trabajo está repartido en tres
lugares con tres nombres (solicitud / informe / conversación del trabajo); "Ver conversación del trabajo" cambia todo
el chat a otra conversación; la barra lateral mezcla "Conversaciones" (General + agentes), "Personas" y "Conversaciones
anteriores" (las conversaciones internas de cada pedido); y las tarjetas del chat tienen tres botones que llevan a
pantallas distintas (Informes, Proyectos, otro chat).

## Regla única

**Un chat por "con quién hablas"; cada trabajo es un "pedido" que se abre al costado, sin salir del chat.**

## Lo que se construye

1. **Barra lateral en tres grupos** (en `frontend/src/components/chats/ChatsView.tsx` y vecinos):
   - **Agentes:** "Toda la oficina" (el General de hoy) y un chat por agente (lo de hoy, con el nombre nuevo).
   - **Personas:** lo que hoy muestra `PeopleChatsList` (compañeros y canales).
   - **Pedidos:** cada trabajo pedido, con estado (en curso, necesita tu ayuda, listo, falló), quién lo hace y cuándo.
     Reemplaza "Conversaciones anteriores"; archivar y buscar siguen funcionando.
2. **Panel del pedido al costado** (drawer a la derecha del chat, con cerrar y Esc; en móvil, pantalla completa con
   volver). Pestañas: **Resumen** (qué se pidió, estado, quién, costo, fechas), **Pasos** (tareas con agente y estado),
   **Archivos** (los que produjo), **Informe** (el informe final, hoy en un modal) y **Cómo lo trabajaron** (la
   conversación interna de hoy, en solo lectura). Si el pedido es parte de un proyecto, un enlace "Abrir proyecto".
   La URL refleja el pedido abierto (`/chats/...?pedido=<id>`) para que Atrás del navegador lo cierre.
3. **Una sola tarjeta por pedido en el chat:** estado + "Abrir pedido" (abre el panel); se quitan la etiqueta
   "Solicitud/Request" y los botones sueltos "Ver informe" / "Ver proyecto" / "Ver cómo lo trabajaron" (quedan dentro
   del panel). Si el pedido necesita ayuda o aprobación, la tarjeta lo dice y lleva a Pendientes.
4. **Un solo nombre: "pedido"** (en/pt: "request"/"pedido") en todos los textos de la app que hoy dicen solicitud,
   trabajo o conversación del trabajo, incluido el mensaje del bot "Puedes seguir el avance en Solicitudes" si viene
   del frontend (si lo escribe el backend o el runtime, cambiar el texto allí también).
5. **El buscador de arriba** busca en los tres grupos a la vez (agentes, personas, pedidos).

## Metas, no metas, anti-metas

- **Metas:** que nadie se pierda; todo lo de un trabajo en un solo lugar; volver siempre a donde se estaba.
- **No metas:** cambiar cómo se guardan los chats o los pedidos en el backend (si hace falta un endpoint pequeño para el
  panel, se agrega); rediseñar Proyectos.
- **Anti-metas:** perder conversaciones existentes; romper los enlaces viejos (`/chats/<agente>`, abrir una
  conversación de trabajo por id debe abrir su pedido en el panel).

## Restricciones

- Reutilizar `useChats`, `useStore` (requests, reports, tasks), el visor de archivos y el modal de informe existentes.
- Los estados "necesita tu ayuda" vienen del trabajo en curso de Proyectos (diseño
  `2026-11-03-proyectos-por-cliente-y-ayuda-design.md`): si todavía no existe en el código, el panel y la lista los
  muestran cuando aparezcan (dejar el hueco preparado, sin inventar datos).
- Textos es/en/pt; estilo de la app; pruebas unitarias de la lógica nueva (agrupar, estados, URL).
