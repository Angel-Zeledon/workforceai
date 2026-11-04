# Responsive de toda la app: plan de implementación

> **Para agentes:** ejecución nativa (un solo agente, en esta sesión). Pasos con casillas (`- [ ]`).

**Objetivo:** que en teléfono (360-430 px) y tableta (768 px) ninguna pantalla tenga cuadros más grandes de lo necesario, texto cortado o desbordado, barras que se solapen o tablas que rompan la página, sin cambiar el escritorio (1280/1440).

**Arquitectura:** primero los componentes y clases compartidos (encabezado de página, Modal y sus variantes, Btn, tabs, tablas, hoja inferior), que arreglan muchas pantallas a la vez; después lo específico de cada pantalla. Todo con prefijos de Tailwind (`max-sm:`/`sm:`/`md:`) o clases utilitarias en `globals.css` bajo `@media (max-width: 639px)`, así desde `sm` todo queda igual que hoy.

**Stack:** Next.js 15, React 19, Tailwind 3.

**Referencia:** `frontend/src/components/onboarding/WelcomeOverlay.tsx` (commit fbf605a): hoja inferior compacta en teléfono, tarjeta de siempre desde `sm`.

## Restricciones globales

- No cambiar lógica, no quitar funciones, no romper ni duplicar `data-testid` (Playwright va en modo estricto).
- Textos nuevos en es/en/pt (`node scripts/check-i18n.mjs`).
- No tocar backend, runtime, `.env`, cobros ni la landing. Sin commits ni despliegues.
- Escala en teléfono: títulos 17-18 px, cuerpo 13-14 px, secundarios 11.5-12 px. Áreas táctiles ≥ 40 px.
- Diálogos en teléfono: hoja inferior, `max-h` en `dvh`, relleno con `env(safe-area-inset-bottom)`, cerrar siempre visible.

## Puntos que hay que vigilar

1. Oficina en teléfono con una aprobación pendiente: la bandeja de Pendientes, las sugerencias y la barra de escribir no se tapan entre sí, y la bandeja queda debajo del panel del agente cuando este se abre.
2. Placeholder largo en la barra de escribir: sigue siendo de una línea mientras está vacía (antes crecía a tres).
3. Tabs con muchas opciones (Agentes y oficinas, Conexiones): una sola fila que se desplaza, y la última pestaña se puede alcanzar.
4. Tabla de Archivos a 360 px: el nombre se lee entero y no hay columnas aplastadas.
5. Escritorio a 1280/1440: ningún cambio visible (los cambios viven bajo `sm`/`md`).

---

### Tarea 1: utilidades compartidas en `globals.css`

**Archivos:** modificar `frontend/src/app/globals.css`

- [x] `.wf-hscroll`: en teléfono, fila sin salto de línea, desplazamiento horizontal sin barra visible, hijos sin encogerse, difuminado a la derecha como pista.
- [x] `.wf-tabs`: `.wf-hscroll` más pestañas de ≥ 40 px de alto en teléfono.
- [x] `.wf-scroll-x`: contenedor de tabla con `overflow-x:auto` y sombras laterales cuando hay más contenido (gradientes `local`/`scroll`).
- [x] `.wf-sheet`: en teléfono, hoja inferior (esquinas de arriba redondeadas, relleno inferior con área segura).
- [x] Barra de escribir: placeholder de una sola línea (`textarea:placeholder-shown { white-space: nowrap }`).

### Tarea 2: componentes compartidos

**Archivos:** `components/nav/ui2.tsx` (PageHeader, PageSkeleton), `components/ui.tsx` (Btn, Card), `components/templates/Modal.tsx`, `components/projects/shared.tsx` (Modal), `components/connections/shared.tsx` (Modal), `components/evidence/EvidenceDrawer.tsx`, `components/chat/ChatThread.tsx` (altura de ChatInput vacío)

- [x] PageHeader: `px-4 py-3 sm:px-6 sm:py-4`, título `text-[18px] sm:text-xl`, descripción `text-[12.5px] sm:text-[13px]`.
- [x] Btn: `min-h-10 sm:min-h-0` (40 px en teléfono).
- [x] Los tres Modal: hoja inferior en teléfono (`items-end p-0 sm:items-center sm:p-4`), `max-h-[92dvh]`, área segura; relleno `p-4 sm:p-5`.
- [x] ChatInput: con el texto vacío no mide el placeholder (queda en una línea).
- [x] Verificar: `npx tsc --noEmit`.

### Tarea 3: títulos y tabs en todas las pantallas

**Archivos:** cada `<h1>` de página (ApprovalsInbox, ApprovalsMobile, ConnectionsView, ProjectsView, ProjectDetail, BatchView, SecurityCenter, ShopView, SkillsView, SkillDetailView, TemplateBuilder, TemplateLibrary, auth/*) y cada `role="tablist"` con `flex-wrap` (OrgPage, ConnectionsView, ApprovalsInbox, SkillsView, ProjectDetail, BatchView, MeetingsView, RoomView, TemplateGallery, CostBreakdownPanel, viewers, PeopleChatsList)

- [x] `<h1>` `text-xl` → `text-[18px] sm:text-xl`; `text-2xl` → `text-xl sm:text-2xl`.
- [x] Contenedores de tabs: añadir `wf-tabs` (desde `sm` siguen como hoy).

### Tarea 4: oficina en teléfono

**Archivos:** `components/office/OfficeView.tsx`, `components/ApprovalsTray.tsx`, `components/CommandBar.tsx`

- [x] Por debajo de `md`, la bandeja de Pendientes y la barra de escribir van en una sola columna fija abajo (contenedor `flex-col` con `md:contents`), así nunca se solapan; desde `md` quedan como hoy. La columna queda por debajo del panel del agente (z menor que 30).
- [x] Bandeja: ancho completo y lista `max-h-[30dvh]` en teléfono.
- [x] Sugerencias: una fila que se desplaza (`wf-hscroll`) en teléfono; hilo de la oficina `30dvh` en teléfono.

### Tarea 5: pantallas específicas

- [x] Archivos (`drive/DriveView.tsx`): en teléfono solo la columna del nombre, con versión, tamaño y fecha debajo del nombre.
- [x] Proyectos: tarjetas de plantilla (`projects/ProjectsView.tsx` o el componente de la tarjeta) con los botones en su propia fila; editor del borrador (`projects/DraftEditor.tsx`) con la etiqueta encima y el campo a lo ancho.
- [x] Conexiones: tarjetas sin desbordarse por la derecha (`min-w-0`, la insignia se ajusta o se trunca).
- [x] Tienda: cuadrícula de 2 columnas en teléfono con tarjetas compactas.
- [x] Tablas anchas (lotes, costos, auditoría, tokens, bases de datos, uso): envolver en `wf-scroll-x`.
- [x] Las demás pantallas de la lista del encargo: revisar a 360/390/768 y corregir lo que aparezca.

### Tarea 6: verificación y capturas

- [x] Capturas antes/después a 390×844 en `docs/screenshots/responsive-2026-11-04/{antes,despues}/` (al menos 12).
- [x] Revisar 1280 y 1440 en Oficina, Proyectos, Archivos y Conexiones (sin cambios).
- [x] `npx tsc --noEmit`, `npm run test:unit`, `node scripts/check-i18n.mjs`, `npm run build`.
