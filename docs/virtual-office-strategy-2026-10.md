# Oficinas virtuales: lideres, diferenciadores y como pasar de minijuego con IA a oficina corporativa completa

Fecha: 2026-10-19. Complementa `competitive-analysis.md` (plataformas de agentes) y `competitive-review-2026-10.md` (onboarding). Aqui el foco son las **oficinas virtuales para humanos** y lo que le falta a nuestro producto para ser una oficina corporativa de verdad.

Confianza: casi todas las fuentes son blogs de comparacion, varios escritos por competidores (SoWork, Roam). Los precios chocan entre fuentes. Tratar todo como orientativo y verificar en la pagina de cada proveedor antes de usarlo comercialmente. No pude verificar Microsoft Mesh ni la seguridad de Virbela.

## 1. El mercado hoy

| Producto | Idea | Precio orientativo | Lo que mejor hace | Debilidad |
|---|---|---|---|---|
| Gather | Mapas 2D pixel-art con proximidad | ~12 USD/miembro/mes, sin plan gratis permanente (una fuente) | Personalizacion de mapas, cultura de equipo | SSO en la hoja de ruta de Gather 2.0 segun un comparador (sesgado) |
| Kumospace | Salas espaciales pulidas | Gratis hasta 5; ~16 USD/usuario | Mejor acabado visual segun una resena; SSO en Enterprise | Orientado a eventos y reuniones |
| Sococo | Plano de oficina con audio/video | ~13.5-15 USD/asiento, minimo 10 | Presencia clara, integra con Zoom/Webex | Minimos por contrato, estetica corporativa fria |
| Teamflow | Burbujas con apps embebidas | 15-30 USD/usuario | Pizarra y apps dentro del espacio | Caro |
| Branch, oVice, Roam, SoWork | Presencia ligera / sin mapa | 6-10 USD (poco verificado) | Precio, sencillez | Poca profundidad |
| Microsoft Teams, Zoom Workplace, Webex | Suite corporativa | Incluido en licencias | Cumplimiento, SSO, integraciones, base instalada | No son espaciales |

Lectura: el mercado esta partido en dos. Los espaciales (Gather, Kumo, Sococo) ganan en **presencia y cultura** pero pierden ante IT por seguridad y administracion. Las suites (Teams, Zoom) ganan en **gobierno y compras** pero no tienen "lugar". Nadie une las dos cosas con **trabajo real hecho por agentes**.

Hay dos movimientos nuevos:
- **Agentes dentro de la oficina**: SoWork y Roam anuncian oficinas donde humanos y agentes comparten espacio (afirmacion propia, sin verificar). Proyectos abiertos (Agent Virtual Office, CLAW3D, agent-office) solo **visualizan** agentes de codigo; no son un lugar de trabajo.
- **Gobierno de agentes corporativos**: OpenAI Frontier (feb 2026), Copilot Studio y Agentforce venden "AI coworkers" con control y auditoria, sin espacio ni presencia.

## 2. Que exige una empresa para comprar (lo que les falta a casi todos los espaciales)

Segun las fuentes de compras e IT: SSO/SAML, provision SCIM, logs de auditoria exportables, APIs, SLA de disponibilidad, SOC 2 / ISO 27001, integracion con calendario, Slack/Teams y videollamada, y analitica de uso (solo ~7% de las organizaciones dice usar bien sus datos de ocupacion segun una guia citando a JLL).

## 3. Donde estamos hoy (segun el repo)

Ya tenemos, y pocos competidores tienen: aprobaciones con riesgo, auditoria, presupuesto y costes por agente, roles y permisos, conectores con vault, memoria de largo plazo, drive con visores y edicion por chat, proyectos con plantillas, skills, centro de seguridad, tu-propio-LLM, ES/EN/PT, PWA.

Nos falta, en terminos de oficina corporativa:
- **Humanos en el espacio**: presencia multijugador, voz/video por proximidad, chat entre personas, estados. Hoy la oficina es de un usuario con sus agentes.
- **Identidad empresarial**: SSO/SAML/OIDC, SCIM, MFA obligatorio, dominios verificados.
- **Reuniones y calendario**: salas de reunion, agenda, que un agente asista y deje acta (`/reunion` existe como comando; falta el espacio y la integracion).
- **Cumplimiento**: SOC 2 / ISO, retencion y exportacion de datos, residencia, despliegue dedicado.
- **Administracion**: consola de admin, politicas por equipo, facturacion por centro de coste, informes de uso y ROI.
- **Estructura organizativa**: departamentos, jerarquias, delegacion, horarios y turnos de agentes.
- **Costes**: hoy una pregunta trivial puede costar 0.1-0.3 USD (medido en esta sesion, ver abajo). Un corporativo exige costes previsibles.

## 4. Diferenciador propuesto

> **La primera oficina donde la mitad de la plantilla son agentes con responsabilidad, y todo es visible, aprobable y auditable.**

Tres cosas que ningun lider junta: (1) lugar y presencia, (2) trabajo real de agentes con aprobaciones, (3) gobierno corporativo (costes, auditoria, permisos). El minijuego (monedas, tienda, personalizacion) pasa de producto a **capa de retencion** sobre el trabajo, no al reves.

## 5. Hoja de ruta: de minijuego a oficina corporativa

**Fase 1 - Confianza y coste (4-6 semanas).** Sin esto no se vende.
1. Reducir el coste por tarea: medir los ~76k tokens de entrada y ~39k de salida por llamada, limitar la salida, saltarse el planificador en pedidos simples, cortar antes de cruzar el tope.
2. SSO (SAML/OIDC) y MFA; dominios verificados; invitaciones masivas.
3. Consola de admin: politicas, limites por equipo/agente, exportar auditoria.
4. Informe de uso y coste por departamento.

**Fase 2 - Oficina de personas (6-10 semanas).**
5. Presencia multijugador en la oficina (avatares de personas junto a agentes), estados y "no molestar".
6. Chat entre personas y con agentes en el mismo hilo; menciones.
7. Salas de reunion con video/voz (integrar un proveedor, no construirlo) y acta automatica por agente.
8. Calendario (Google/Microsoft) y notificaciones (Slack/Teams).

**Fase 3 - Organizacion (8-12 semanas).**
9. Departamentos, jerarquia y delegacion entre agentes y personas.
10. Roles de agente con descripcion de puesto, horario y presupuesto propios.
11. Plantillas por industria (contable, legal, ventas) listas para empezar.
12. SCIM, retencion/exportacion de datos, SLA.

**Fase 4 - Cumplimiento y escala.**
13. SOC 2 Tipo I luego II; despliegue dedicado o privado; residencia de datos.
14. Marketplace de agentes y skills verificados.
15. API publica y webhooks.

## 6. Riesgos

- Construir voz/video propios: no. Integrar.
- Competir en "oficina social" contra Gather/Kumo: perdemos; competimos en **trabajo gobernado**.
- Promesas de ahorro sin medir: publicar solo costes y tiempos medidos.
- Coste de LLM: es el riesgo comercial numero uno hoy (ver Fase 1).

## 7. Datos medidos en esta sesion (DeepSeek, 2026-10-19)

37 llamadas: ejecutar tarea 26 (76k tokens in / 39k out de media, 1.81 USD), planificar 9 (82k / 42k, 0.67 USD), consultar 2. Total unos 2.5 USD. Las tareas simples se pausaron al tope y lo superaron.

## 8. Lo mejor de cada uno, en un solo producto

Cada diferenciador de la seccion 1, con la forma de tenerlo y el orden. "Tenemos" = ya existe en el repo; el resto es trabajo nuevo.

| Viene de | Diferenciador | Nuestra version | Estado | Fase |
|---|---|---|---|---|
| Gather | Mapas personalizables y cultura | Oficina 3D editable, tienda, monedas | Tenemos | - |
| Gather | Presencia por proximidad (ver y oir a los cercanos) | Avatares de personas junto a agentes, audio/video por cercania (proveedor externo) | Falta | 2 |
| Kumospace | Acabado visual pulido, salas para eventos | Pulido 3D, salas tematicas, modo evento | Parcial | 2-3 |
| Kumospace / oVice | SSO en planes de entrada | SSO desde el plan de equipo, no solo Enterprise | Falta | 1 |
| Sococo | Plano claro de quien esta donde y en que | Panel de presencia: persona o agente, estado, tarea actual | Parcial (agentes) | 2 |
| Sococo | Se integra con Zoom/Webex | Reuniones con Zoom/Meet/Teams y acta automatica | Falta | 2 |
| Teamflow | Apps y pizarra dentro del espacio | Documentos, hojas y tableros dentro de la oficina (workspace) | Parcial | 2-3 |
| Branch / Roam | Precio bajo, entrada sencilla | Plan gratis con tope duro y camino de 4 pasos | Falta | 1 |
| Teams / Zoom | Cumplimiento, auditoria, integraciones | Auditoria, roles, conectores | Parcial; falta SOC 2, SCIM | 1-4 |
| Copilot Studio | Aprobar una vez / sesion / denegar | Tarjeta de aprobacion con tres opciones | Falta | 1 |
| Zapier | Aprobaciones en Slack | Aprobar desde Slack/Teams/email | Falta | 2 |
| Lindy / Manus | Credito de prueba y vuelta diaria | Prueba con tope visible, meta diaria con monedas | Parcial (monedas) | 1 |
| OpenAI Frontier / Agentforce | Gobierno de agentes a escala | Presupuesto, auditoria, permisos por agente | Tenemos | - |
| Open source (CLAW3D, etc.) | Ver agentes trabajar en vivo | Oficina en vivo con trabajo real | Tenemos | - |

**Lo que nadie tiene y ya tenemos o casi:** ver trabajar a los agentes en un lugar, aprobar con la mano, coste y auditoria por agente, espanol y portugues. Eso es el nucleo; lo de arriba lo rodea.

**Lo que hay que decidir para no morir de alcance:**
1. No construir voz/video: integrar.
2. Ganar primero en "trabajo gobernado" (fases 1 y 3); la presencia de personas (fase 2) llega despues, porque sin coste controlado y SSO no se vende a empresas.
3. Un competidor se juzga por un solo eje: el nuestro es "cuanto trabajo real sale con control". Medirlo y publicarlo.

## Fuentes

- [15 Best virtual workspace software for 2026 - Guideflow](https://www.guideflow.com/blog/virtual-workspace-software)
- [Gather Alternatives 2026 - SoWork](https://www.sowork.com/blog/best-gather-town-alternatives-remote-teams)
- [Best Sococo Alternatives - SoWork](https://www.sowork.com/blog/best-sococo-alternatives-remote-team-presence-awareness)
- [Best Virtual Office Software 2026 - Tinyfloor](https://www.tinyfloor.com/guides/best-virtual-office-software)
- [8 Best Virtual Office Tools - DeskBreak](https://www.deskbreak.app/best-remote-work-tools/virtual-office-tools)
- [Top 10 Gather Alternatives - G2](https://www.g2.com/products/gather-town-gather/competitors/alternatives)
- [Virtual office platforms - Product Hunt](https://www.producthunt.com/categories/virtual-office)
- [Compare Gather vs Roam - CB Insights](https://www.cbinsights.com/compare/gather-presence-vs-roam-5)
- [AI-Powered Virtual Offices 2026 - SoWork](https://www.sowork.com/blog/ai-powered-virtual-offices-what-to-know-2026)
- [Best AI Agent Workspace for Humans and Agents - SoWork](https://www.sowork.com/blog/ai-agent-management-platform)
- [Zoom AI office suite and avatars - TechCrunch](https://techcrunch.com/2026/03/10/zoom-launches-an-ai-powered-office-suite-says-ai-avatars-for-meetings-are-coming-soon/)
- [agent-virtual-office - GitHub](https://github.com/KbWen/agent-virtual-office)
- [CLAW3D](https://www.claw3d.ai/)
- [agent-office - GitHub](https://github.com/harishkotra/agent-office)
- [Enterprise Workplace Management for 5,000+ employees - YaRooms](https://www.yarooms.com/guides/enterprise-workplace-management)
- [Calendly IT solutions (SSO/SCIM/logs)](https://calendly.com/solutions/it.html)
