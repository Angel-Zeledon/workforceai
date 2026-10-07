# Analisis competitivo - AI Workforce OS

Fecha de la investigacion: 2026-10-06. Metodo: WebFetch de paginas oficiales de precios y WebSearch para resenas y noticias. Las cifras de precios cambian a menudo; verifique antes de usarlas en material comercial.

Convenciones de confianza:
- **[oficial]**: leido en la pagina del proveedor durante esta investigacion.
- **[terceros]**: tomado de blogs, agregadores de resenas o comparativas. Pueden ser parciales (varios son de competidores directos: 11x.ai, Lindy, Zapier escriben "reviews" de rivales).
- **[no verificado]**: no pude confirmarlo con una fuente fiable o es inferencia mia.

Estado actual de nuestro producto (README, `docs/SPEC.md`, `docs/architecture/`): oficina 3D (React Three Fiber) con 7 agentes seed (Valeria/Ventas, Marcos/RRHH, Elena/Legal, Tomas/Contabilidad, Nadia/Analisis, Ivan/Operaciones, Sofia/Asistente); backend Go como fuente de verdad; runtime CrewAI reemplazable; modo simulacion; aprobaciones con riesgo low/medium/high; auditoria, presupuesto (`BUDGET_USD`), profundidad de delegacion maxima 5. **Hoy: sin auth, org fija `demo`, sin herramientas reales, sin memoria vectorial, sin canales, sin voz, sin movil, sin marketplace.** Todo lo demas (RBAC, 4 niveles de autonomia, vault, Tool Gateway, WhatsApp, Slack, CRM) esta disenado pero en Fases 2-7.

---

## 1. Tabla comparativa

### 1.1 Plataformas de agentes / empleados de IA

| Producto | Propuesta de valor | Publico | Precio (publico) | Modelo de agentes | Aprobaciones / autonomia | UX distintiva | Debilidades / quejas |
|---|---|---|---|---|---|---|---|
| **Lindy** | Asistentes "no-code" que ejecutan trabajo (email, agenda, reuniones, ventas) desde lenguaje natural | Solopreneurs, equipos pequenos, ops/ventas | Free 7 dias con $50 en creditos; Team desde $29.99/mes por 3,000 creditos (hasta ~$9,999/mes); recarga $10 por 1,000 creditos; Enterprise a medida [oficial: https://www.lindy.ai/pricing] | Agentes individuales y "swarms" multiagente; 40+ skills; 1,500+ integraciones y MCP; computer use; Slack con contexto persistente. Memoria: no detallada en fuentes [no verificado] | "Confirm before sending" por Slack o web; limitar autonomia en acciones de alto impacto [terceros: https://automationatlas.io/answers/how-to-build-a-lindy-ai-assistant-2026/] | 100+ plantillas, creacion por conversacion, notas de reunion | G2 4.9 (171 resenas) pero Trustpilot 1.7: sobrecostos por creditos, loops fallidos que consumen creditos, emails a destinatario equivocado, tokens OAuth que no persisten, cobros tras cancelar, soporte lento [terceros: https://hackceleration.com/lindy] |
| **Relevance AI** | "AI workforce": construir equipos de agentes (Invent genera agentes desde texto; BDR "Bosh") | Equipos GTM/ops, agencias | Pagina oficial solo muestra Enterprise "talk to sales" [oficial: https://relevanceai.com/pricing]; terceros: Free 200 acciones, Pro $19, Team $234, Business $599 [terceros: https://www.salesforge.ai/blog/relevance-ai-reviews] | Multi-agente visual, 400+ plantillas en marketplace, 2,000+ integraciones, agentes de llamadas/reuniones | SSO, RBAC, audit logs en Enterprise [oficial] | Marketplace de agentes clonables | G2 4.3 (~21 resenas): agentes que se congelan, creditos "desaparecen" sin log de gasto, timeouts solo en plan caro, docs desactualizadas [terceros: https://www.g2.com/products/relevance-ai/reviews] |
| **Sintra** | 12 "AI helpers" con roles (social, soporte, email, ventas, SEO...) + "Brain AI" con contexto de negocio | No tecnicos, microempresas | $97/mes (1 mes), $59 (3 meses), $52/mes (12 meses); 250 creditos/mes; helper individual $39/mes; 100+ idiomas [oficial: https://sintra.ai/pricing] | Roles fijos, Brain AI como memoria compartida, 1000+ integraciones declaradas (pagina de precios dice "15+" en el plan) [oficial, contradictorio] | Mayormente redacta borradores; no ejecuta en sistemas reales [terceros: https://tekpon.com/software/sintra/reviews] | Metafora "equipo de empleados", muy simple de entender | Trustpilot: perfil marcado por resenas incentivadas; cambio a creditos que corta el servicio al llegar a cero; reembolsos rechazados fuera de 14 dias; calidad decreciente [terceros: https://uk.trustpilot.com/review/sintra.ai?page=10] |
| **Marblism** | 7 "empleados IA" (Eva asistente, Sonny social, Stan leads, Penny SEO, Rachel recepcionista, Walter web, Linda legal) | Fundadores solos, negocios de servicios | Un plan: $24/mes anual o $44/mes mensual; 50 horas de trabajo IA/mes [oficial: https://www.marblism.com/pricing] | Roles fijos con nombre; recepcionista por voz 100+ idiomas | Revision humana diaria necesaria segun resenas [terceros] | Empleados con nombre y cara; "horas de trabajo" como unidad (mas intuitiva que creditos) | Cada agente pide 30-45 min/dia de revision; faltan integraciones CRM; recepcionista suena robotica [terceros: https://mrktcorrect.com/blog/marblism-review]. Trustpilot con ~800 resenas mayormente positivas [terceros] |
| **Zapier Agents** | Agentes autonomos sobre 8,000+ apps; Copilot unificado; MCP | Operaciones, no tecnicos | Plataforma: Free 100 tareas, Pro desde $19.99, Team desde $69 (anual) [oficial: https://zapier.com/pricing]. Agents como add-on separado: Free 400 actividades, Pro $50/mes por 1,500 actividades [terceros: https://zapier.com/l/agents-pricing] | Agente = objetivo + fuentes + herramientas + guardrails; delegacion de subtareas | Checkpoints de aprobacion humana configurables; MCP con audit log y restricciones por app [terceros] | Mayor catalogo de integraciones del mercado | Costo escala rapido, quejas de cobros inesperados y soporte en la plataforma general; Agents sin resenas suficientes en mid-2026 [terceros: https://www.capterra.com/reviews/130182/zapier] |
| **n8n** | Automatizacion con nodos de IA/agentes, self-host gratis | Tecnicos, devs, ops | Starter 20 EUR/mes (2.5K ejecuciones), Pro 50 EUR, Business 667 EUR, Enterprise a medida; cobra por ejecucion completa, no por paso; Community Edition gratis (~206K estrellas) [oficial: https://n8n.io/pricing] | Workflows deterministas con nodos agente (LangChain); plantillas de agentes | Nodos de aprobacion manuales; sin modelo de autonomia propio [no verificado] | Editor de grafo, control total y self-host | Curva de aprendizaje, sobrecarga de operar self-host, caidas bajo carga [terceros: https://beginnersinai.org/n8n-review/] |
| **Gumloop** | Agentes y workflows no-code con "Company Brain", MCP, 35+ modelos | Equipos de ops/GTM, growth | Pro desde $37/mes con 20,000 creditos; sin free tier (14 dias de prueba); 1 credito = $0.005; fee de orquestacion 8% (16% con BYOK); Enterprise a medida [terceros: https://www.jetadmin.io/blog/gumloop-pricing/] | Agentes ilimitados, BYOK, Skill Sync con GitHub | No verificado | Constructor visual | Precio no lineal (overage ~2.7x mas caro que el incluido), sin rollover [terceros] |
| **Dust** | "Multiplayer AI": agentes compartidos y gobernados sobre datos de la empresa (Slack, Drive, Notion, M365...) | Equipos de conocimiento, mid-market/enterprise (Paris) | Free 500 creditos de por vida; Pro 24 EUR/usuario/mes anual (8,000 creditos); Max 120 EUR (40,000); Enterprise [oficial: https://dust.tt/pricing] | Agentes con instrucciones, conocimiento acotado y herramientas; multi-agente; 20+ modelos | SCIM, admin; HITL no detallado [no verificado] | Agentes llamables desde Slack, web, extension, CLI y API | Pocas resenas independientes encontradas [no verificado]; por seat+creditos |
| **CrewAI Enterprise (AMP)** | Plataforma build+runtime sobre el framework CrewAI | Empresas con equipos tecnicos | Basic gratis (50 ejecuciones/mes), Enterprise a medida; ejecuciones extra ~$0.50 [oficial: https://www.crewai.com/pricing; terceros] | Crews/flows de agentes por rol, tracing, guardrails, HITL | RBAC, SSO, PII redaction en Enterprise [oficial] | Editor visual + copiloto | Precio opaco, telemetria/privacidad cuestionada por algunos [terceros]. **Nota: es nuestro motor; es proveedor, no solo rival.** |
| **Microsoft Copilot Studio** | Constructor de agentes dentro de M365/Power Platform; agentes autonomos; Agent 365 como plano de control | Empresas Microsoft | Packs de 25,000 creditos a $200/mes o pago por uso, requiere Azure [oficial: https://www.microsoft.com/en-us/microsoft-copilot/microsoft-copilot-studio] | Agentes + flujos Power Automate, orquestacion multiagente | Aprobacion humana por herramienta (toggle por agente; aprobar/aprobar sesion/denegar), anunciada 2026-09-01, aun en desarrollo hacia GA [terceros: https://mc.merill.net/message/RM570434] | Integracion nativa con Office/Teams | Medidor de creditos impredecible (1/2/5/10 segun tipo), agentes deshabilitados al pasar 125% del cupo, licenciamiento complejo, analitica debil [terceros: https://visualstudiomagazine.com/articles/2026/08/07/copilot-credit-complaints-keep-coming-too-expensive-to-use.aspx] |
| **Salesforce Agentforce** | Agentes sobre datos y flujos de Salesforce (servicio, ventas) | Enterprise con Salesforce | Flex Credits $500 por 100k (20 creditos por accion estandar); $2 por conversacion; add-on $125/usuario/mes; licencia $5/usuario + creditos [oficial: https://www.salesforce.com/agentforce/pricing/] | Topics/acciones sobre CRM, Data Cloud | Guardrails del Trust Layer [no verificado en detalle] | Dentro del CRM | Complejidad, costo total (Data Cloud, consultoria), baja adopcion a produccion segun un analisis (8% de elegibles) y cifras como "77% de fracaso" o "$13,600/usuario" provienen de blogs de competidores y **no estan verificadas** [terceros: https://www.salesforceben.com/where-are-we-really-at-with-agentforce-adoption/] |
| **Manus** | Agente general autonomo (investigacion, slides, apps web, navegador) | Prosumidores, creadores | Free 300 creditos; Standard $20 (4,000); Customizable $40 (8,000); Extended $200 (40,000); 300 creditos diarios gratis [terceros: https://www.nocode.mba/articles/manus-ai-pricing]. Compra por Meta (dic. 2025) bloqueada por China el 2026-04-27; independiente desde 2026-09-01 [terceros: https://spectrumailab.com/blog/manus-ai-pricing-after-blocked-meta-acquisition-2026] | Un agente generalista con sandbox y navegador; integraciones Slack/WhatsApp/Telegram | Poco control intermedio | Tareas largas "fire and forget" | Trustpilot ~1/5: creditos consumidos aun si falla (500-900 por tarea compleja), sin ver costo antes, creditos borrados, reembolsos automaticos [terceros: https://www.trustpilot.com/review/manus.im?page=2] |
| **Devin (Cognition)** | Ingeniero de software autonomo | Equipos de ingenieria | Free, Pro $20, Max $200, Teams $80 + $40/usuario, Enterprise; cuotas diarias/semanales [oficial: https://devin.ai/pricing]. Antes cobraba por ACU (~$2.25) [terceros] | Un agente por sesion en su sandbox; Fusion (jun-2026) enruta entre dos modelos | PR como punto de revision | Entorno completo del agente visible | Exito 15-30% en tareas variadas segun pruebas independientes; se atasca en loops [terceros: https://codegen.com/blog/ai-tools/devin]; benchmarks de Fusion son del proveedor |
| **Claude Cowork / Claude Code (Anthropic)** | Cowork: agente de escritorio para trabajo de conocimiento (archivos, plugins, tareas programadas); Claude Code: agente de programacion con subagentes y Agent Teams | Profesionales y equipos; devs | Incluido en planes: Pro $20, Max desde $100, Team $20-25/asiento, Enterprise $20/asiento + uso [oficial: https://claude.com/pricing] | Cowork: plugins por area (ventas, finanzas, legal, RRHH...), tareas programadas (feb-2026), computer use (mar), GA macOS/Windows (9-abr), web/movil en beta (7-jul) [terceros: https://fast.io/resources/claude-cowork-features-overview/]. Claude Code Agent Teams (5-feb-2026): agentes con contexto propio y lista de tareas compartida [terceros: https://timewell.jp/en/columns/claude-code-agent-teams-guide]. Managed Agents para agentes en la nube con multiagente [terceros: https://www.claude.com/blog/code-w-claude-sf-2026-sf] | Modos de permiso (preguntar / auto-aceptar / saltar) en Claude Code [terceros] | Acceso directo a archivos locales y a un modelo de primer nivel | Limites de uso (sesion de 5 h + tope semanal; Cowork consume 5-20x chat), tareas programadas solo con la app abierta y el equipo despierto, permisos y salida no verificada [terceros: https://go9x.com/blog/claude-cowork-review] |
| **Genspark** | "Super agent" todo-en-uno (slides, hojas, docs, codigo, video, llamadas) | Prosumidores | Free ~100 creditos/dia; Plus $24.99 (10,000 creditos); Pro $249.99 (125,000) [terceros: https://www.eesel.ai/blog/genspark-ai-pricing]. Chat/imagen sin costo de creditos solo hasta 2026-12-31 [terceros] | Agente generalista que produce artefactos; llamadas telefonicas con IA | Minima | Salida de documentos/artefactos | Trustpilot 3.5/5 (310 resenas): creditos descontados en fallos, costo no mostrado antes, soporte automatizado, bugs [terceros: https://ca.trustpilot.com/review/genspark.ai?page=8] |

### 1.2 Oficinas / ciudades virtuales de agentes

| Proyecto | Que es | Estado | Diferencia con nosotros |
|---|---|---|---|
| **Claw3D** | Oficina 3D open source para ver agentes en tiempo real; standups, revision de PRs, constructor de oficina, aprobaciones y edicion de config de agentes. MIT, ~2.3k estrellas, 625 forks [oficial: https://github.com/iamlukethedev/claw3d] | Activo; conecta a OpenClaw, Hermes o proveedores HTTP | Es **visualizador** de runtimes ajenos, orientado a codigo/devs; no es producto de negocio con RBAC, presupuesto, plantillas ni espanol. Es el rival conceptual mas cercano y valida la categoria |
| **Pixel Agents / Pixel Office / Claude Office / Cubicles** | Oficinas pixel-art que visualizan sesiones de Claude Code u OpenClaw [terceros: https://github.com/zkblk/pixel-agents, https://github.com/lacedupairs-code/pixel-office] | Proyectos pequenos, muchos recientes | Dashboards de monitoreo, no plataforma de trabajo |
| **Generative Agents (Stanford/Google) / AI Town (a16z)** | Investigacion/juguete de simulacion social con agentes LLM; ~20.6k estrellas el repo de Generative Agents [terceros: https://the-decoder.com/ai-town-lets-you-build-your-own-gpt-based-ai-civilization/] | Referencia academica; AI Town es kit open source | Agentes con vida propia, sin objetivo de negocio ni aprobaciones |
| **ChatDev / MetaGPT** | Empresas de software simuladas con roles (CEO, PM, dev). MetaGPT supera ~69k estrellas; su producto comercial MGX se renombro **Atoms** (2026-01-13) y se enfoca en apps con backend, pagos y SEO [terceros: https://aiwiki.ai/wiki/metagpt; https://www.letsdatascience.com/news/deepwisdom-launches-atoms-to-commercialize-code-0bc62ff7] | Activo (DeepWisdom, Shenzhen) | Solo dominio de software; MGX llego a ~500k usuarios registrados en un mes y $1M ARR (cifras de terceros, no verificadas) lo que muestra apetito por "equipo de IA" visible |

### 1.3 Espanol / LatAm

La investigacion **no encontro** un producto LatAm que combine "empleados IA multirol + oficina visual + aprobaciones". El espacio esta dominado por chatbots/agentes de **atencion al cliente por WhatsApp**:

| Producto | Pais | Precio de entrada | Nota |
|---|---|---|---|
| Treble.ai | Colombia | ~USD 399/mes | Campanas y flujos WhatsApp, agentes IA, HubSpot/Salesforce [terceros: https://www.mediasource.mx/herramientas-ia/marketing/treble-ai] |
| Botmaker | Argentina | ~USD 149/mes (otros listados: cotizacion) | Voz/video, grandes cuentas [terceros: https://tecnochat.com/blog/ia/6-plataformas-de-agente-ia-para-whatsapp-en-latam-2026-comparativa-honesta] |
| Aivo | Argentina | desde USD 99 | Integracion CRM (Zendesk/Salesforce) [terceros: idem] |
| Chattigo | Argentina | desde USD 49 | VTEX/Tiendanube [terceros: idem] |
| Weni | Brasil | ~R$999/mes | Multi-canal [terceros: idem] |
| TecnoChat | Colombia | free (100 conv/mes) | Es el sitio que publica la comparativa; sesgo posible |
| Kommo | global | desde USD 20/usuario/mes | CRM de mensajeria con agente IA [terceros: https://duotach.com/blog/mejores-chatbots-whatsapp-mexico] |
| Zenvia | Brasil | cotizacion | Omnicanal enterprise [terceros] |
| Chat Evolution, Dalia, SyncManager, Missyera | CL/varios | servicios a medida (Dalia desde ~$600 setup) | Agencias, no producto [terceros: https://dalia.dev/servicios-web-para-pymes/agentes-virtuales-para-empresas-y-pymes/] |

Estos productos son cara-al-cliente (agente que responde al cliente final). Nosotros somos cara-al-dueno (empleados internos que trabajan y piden permiso). Son complementarios mas que rivales directos, salvo en WhatsApp (Fase 5). Sintra y Marblism soportan "100+ idiomas" pero su UI y posicionamiento son en ingles [oficial]. El espanol nativo (tono, modismos por pais, moneda, facturacion fiscal local) sigue sin estar cubierto por ningun jugador de agentes multirol que encontre. [Conclusion mia, no verificada exhaustivamente.]

---

## 2. Que hacen mejor que nosotros y que podemos copiar o adaptar

Honestidad primero: hoy **no ejecutamos nada real**. Todo competidor serio conecta con Gmail/Calendar/CRM; nosotros solo simulamos. Esa es la brecha numero uno y esta en F4-F6.

| Area | Quien lo hace bien | Que copiar / adaptar | Fase |
|---|---|---|---|
| Plantillas y arranque en minutos | Lindy (100+ plantillas), Relevance (400+), n8n (plantillas de agentes) | Galeria de "paquetes de oficina" por tipo de negocio (despacho contable, tienda online, agencia, clinica) que siembran agentes, memoria, workflows y reglas de autonomia | F2 (onboarding de org) + F3 |
| Onboarding conversacional | Lindy y Relevance (Invent): se describe el objetivo y se genera el agente | Wizard "cuentanos de tu negocio" que genera memoria de compania y propone que agentes contratar; Sofia conduce la entrevista | F2/F3 |
| Confirmacion antes de enviar, en el canal donde ya estas | Lindy (Slack/web), Copilot Studio (aprobar / aprobar por sesion / denegar) | Aprobacion con edicion del borrador (ya previsto) + opcion "aprobar para esta sesion/este destinatario" + botones en Slack/WhatsApp. Ya disenado: aprobar en Slack en F5 | F4-F5 |
| Marketplace de agentes/plantillas | Relevance AI | Marketplace de plantillas de workflow y puestos (ya listado en F7); empezar con curado propio | F7 (curado desde F3) |
| Unidad de cobro comprensible | Marblism ("horas de trabajo"), Genspark ("sparkpages") | Medir y mostrar costo en "$ estimados por solicitud" y "horas de empleado"; evitar "creditos" opacos | F2 (presupuesto) |
| Integraciones masivas | Zapier (8,000+), Lindy (1,500+), n8n | No competir en cantidad: exponer **MCP** como cliente (consumir) y como servidor y/o puente a Zapier/n8n para cobertura larga cola, manteniendo el Tool Gateway y las aprobaciones | F6-F7 |
| Multicanal / agente en tu chat | Dust (Slack, web, extension, CLI, API), Manus (Slack/WhatsApp/Telegram), Lindy (Slack) | Pedir y aprobar por WhatsApp/Telegram/Slack ademas de la oficina | F5 |
| Movil | Cowork web/movil en beta (jul-2026), Marblism (app iOS) | PWA con bandeja de aprobaciones push y comando de voz; la oficina 3D no hace falta en movil | F5 (PWA) |
| Voz | Marblism (Rachel), Lindy (llamadas ~$0.19/min, terceros), Genspark (llamadas), Relevance (agentes de llamadas) | "Recepcionista" en espanol: entrada por voz a Sofia primero (dictar peticion), llamadas salientes mas tarde | F5+ |
| Tareas programadas y proactividad | Cowork (tareas programadas), Lindy, Zapier | Triggers schedule/evento ya previstos en motor de workflows; agregar "briefing de las 8am" de Sofia | F3 |
| Gobernanza centralizada | Copilot Studio Agent 365, Agentforce Trust Layer | Panel de seguridad y kill-switch (F4), export de auditoria (F2) | F2/F4 |
| Equipos de agentes que se coordinan solos | Claude Code Agent Teams (lista de tareas compartida) | Ya lo tenemos en concepto (delegaciones, `consult`); copiar la **lista de tareas compartida visible** y permitir que el usuario reasigne | F3 |
| Comparacion antes de gastar | Ninguno lo hace bien (queja general) | Ver seccion 4 | F2 |
| Observabilidad del gasto | Gumloop desglosa componentes de costo; Relevance **no** tiene log de gasto (queja) | Desglose por agente/tarea en el dashboard (ya tenemos `cost_usd`) | F2 |

---

## 3. Donde somos unicos y como apalancarlo

1. **Oficina 3D como interfaz de supervision real.** Los demas muestran listas/logs/chats. Solo Claw3D y proyectos pixel exploran la idea y estan orientados a devs. Nuestra ventaja: toda animacion deriva de eventos reales (`agent.state_changed`, `message.sent`), por lo que la oficina **es** el panel de auditoria legible para un no tecnico. Apalancar: "ve quien esta bloqueado/esperando tu aprobacion de un vistazo", demo viral (clips de 20 s de agentes colaborando), modo "TV de oficina" para pantalla.
   - Riesgo honesto: la 3D puede ser gimmick si el dashboard y movil no son de primera clase. Mantener ambos modos (ya existe toggle) y medir si los usuarios usan la 3D tras la semana 2. [Hipotesis a validar.]
2. **Aprobaciones como producto, no como ajuste.** Competidores las tienen pero como opcion (Lindy, Zapier) o aun en desarrollo (Copilot Studio: anunciada 2026-09-01, sin GA). Nosotros: Go decide, 4 niveles de autonomia, `args_hash`, maker-checker, invariante de que ciertas acciones nunca se saltan, allowlist de destinos. Apalancar: mensaje "tu empleado IA nunca envia nada que no hayas visto", con "modo borrador" y diff visible.
3. **Espanol nativo y foco hispanohablante.** Nadie en agentes multirol lo tiene como eje (seccion 1.3). Apalancar: nombres/tono/moneda locales (MXN, COP, ARS, CLP), formatos fiscales (CFDI, facturas electronicas) como plantillas, WhatsApp primero, FTS en espanol (ya en F3), soporte y docs en espanol, precios en moneda local o USD claros.
4. **Proyectos paralelos (varias solicitudes/clientes a la vez) con memoria aislada por cliente.** Los asistentes tipo Sintra/Marblism son un chat por helper; Manus/Devin son una sesion por tarea. Nuestro modelo: Request -> plan con dependencias -> ejecucion paralela -> reporte consolidado, con aislamiento de memoria por `customer_id`. Apalancar: "tu oficina trabaja en 5 clientes a la vez sin mezclar datos" (para despachos, agencias, consultores).
5. **Credenciales fuera del modelo (arquitectura).** El runtime no ve secretos; Go ejecuta herramientas. Es una afirmacion de producto verificable y de interes dado el contexto de mercado (seccion 4).
6. **Motor reemplazable (CrewAI hoy).** Permite cambiar de modelo/motor sin tocar contratos; protege margenes.

Lo que NO es unico: multi-agente por roles (Sintra, Marblism, Dust, CrewAI), plantillas, aprobaciones basicas.

---

## 4. Huecos del mercado y quejas recurrentes que podemos resolver

| Queja recurrente (evidencia) | Quien | Como la resolvemos | Estado |
|---|---|---|---|
| **Costos impredecibles / creditos opacos.** Cobran aun si falla; no muestran costo antes | Manus, Genspark, Lindy, Sintra, Relevance, Copilot Studio, Gumloop (precio no lineal) | Estimacion de costo **antes** de ejecutar (el `plan` ya existe: mostrar costo estimado por tarea y total), `budget_cap_usd` por solicitud (ya propuesto en SPEC cambio 8), tope duro que pausa y avisa, reembolso automatico de ejecuciones fallidas por culpa del sistema (decision de negocio), desglose por agente | F1.5/F2 |
| **Loops y fallos que gastan dinero** | Lindy, Manus, Devin, Relevance (congelamientos) | Profundidad maxima de delegacion (ya 5), limite de reintentos, deteccion de loops, timeout por tarea incluido en todos los planes | F2 |
| **Acciones equivocadas (email al destinatario incorrecto)** | Lindy (resenas) | Allowlist de destinatarios, destinatario nuevo = aprobacion con resaltado, `args_hash` | F4 |
| **Credenciales / tokens OAuth que se caen o se exponen** | Lindy (tokens no persisten); encuestas generales de seguridad de agentes [terceros: https://www.avepoint.com/blog/manage/state-of-ai-2026-report] | Vault cifrado, scopes minimos, refresh en Go, alertas de reconexion, pagina "que puede hacer cada empleado" con revocacion en un clic | F4 |
| **Falta de confianza / "no se que hizo"** | Encuestas: 79% ha tenido que revertir una accion de un agente, 42% reporta perdida de ingresos [terceros: https://letsdatascience.com/news/enterprises-struggle-deploying-agentic-ai-at-scale-2666914d, no verificado de forma independiente]; Gartner prevé >40% de proyectos agenticos cancelados para 2027 [terceros: https://theregister.com/2025/10/01/gartner_ai_agents] | Auditoria con cadena de hash, timeline por solicitud, "deshacer/compensar" cuando la herramienta lo permita, reporte final con evidencia y confianza (ya en `StructuredOutput`) | F2-F4 |
| **Revision humana constante (Marblism: 30-45 min/dia por agente)** | Marblism | Autonomia gradual: sugerir -> aprobar cada -> reglas -> autonomo; sugerencia automatica de reglas ("llevas 20 aprobaciones iguales, quieres una regla?") | F3-F6 |
| **Soporte y cobros (cancelacion dificil, reembolsos)** | Lindy, Sintra, Manus, Relevance, Zapier | Cancelacion self-serve, mensual sin permanencia, pruebas sin tarjeta; soporte humano en espanol como diferenciador | negocio |
| **Complejidad de implementacion enterprise** | Agentforce, Copilot Studio | Quedarnos en SMB/mid-market con onboarding guiado | estrategia |
| **Hueco: empleados IA con ejecucion real + supervision visible, en espanol, para PYME** | Ninguno | Nuestro nicho | F4-F6 |
| **Hueco: cumplimiento regional** (datos en LatAm, facturacion local, WhatsApp Business) | Los globales no lo cubren | Plantillas fiscales, region de datos (F7) | F5-F7 |

Honestidad sobre el riesgo: varias de estas promesas (reembolso por fallos, costo estimado) pueden chocar con margenes; la estimacion previa en LLMs es inherentemente imprecisa, mostrar rango y no cifra exacta.

---

## 5. Backlog priorizado (20 mejoras)

Impacto/Esfuerzo: A=alto, M=medio, B=bajo. Fases segun `docs/architecture/09-roadmap-fases.md`. "F1.5" = trabajo posible antes de F2 sobre el codigo actual (simulacion), sin romper el contrato.

| # | Mejora | Impacto | Esfuerzo | Fase | Notas |
|---|---|---|---|---|---|
| 1 | Estimacion de costo previa a ejecutar (rango por tarea y total) + confirmacion si supera umbral | A | B | F1.5 / F2 | Usa el plan; `budget_cap_usd` ya propuesto |
| 2 | Tope duro de presupuesto por solicitud y por agente, con pausa y aviso (nunca "agente muerto" sin explicacion) | A | M | F2 | Reserva/conciliacion ya en F2 |
| 3 | Desglose de gasto por agente/tarea/herramienta en dashboard | M | B | F2 | Ya existe `cost_usd` por agente |
| 4 | Onboarding de org con paquetes por tipo de negocio (agentes, memoria, reglas sembradas) | A | M | F2 | Alineado con "onboarding de org" |
| 5 | Wizard conversacional "cuentanos tu negocio" que escribe memoria de compania | A | M | F3 | Depende de memoria por scopes |
| 6 | Aprobacion con edicion de borrador, "aprobar para esta sesion" y diff | A | M | F4 | Modo borrador ya previsto |
| 7 | Tool Gateway + vault + email read/draft/send (primer canal real) | A | A | F4 | Cierra la brecha critica vs Lindy |
| 8 | Panel de seguridad y kill-switch por herramienta, pagina "que puede hacer cada empleado" | A | M | F4 | Mensaje de confianza |
| 9 | Plantillas de workflow (nuevo cliente, cobranza, propuesta, cierre de mes) con galeria | A | M | F3 | Base del futuro marketplace |
| 10 | Briefing diario y tareas programadas de Sofia ("resumen 8am") | M | B | F3 | Triggers schedule |
| 11 | Autonomia gradual con sugerencia automatica de reglas | A | M | F3-F6 | Reduce la fatiga de aprobar |
| 12 | PWA movil: bandeja de aprobaciones, push, peticion por voz (dictado) | A | M | F5 | Sin 3D en movil |
| 13 | Aprobaciones y comandos desde Slack y WhatsApp/Telegram | A | A | F5 | WhatsApp: plantillas y consentimiento |
| 14 | Plantillas fiscales/locales por pais (MX, CO, AR, CL, ES) y tono regional por agente | A | M | F3-F6 | Diferenciador de espanol |
| 15 | Conectores CRM/Calendar/Drive y puente MCP/Zapier/n8n para cola larga | A | A | F6 | MCP evita escribir 1,000 conectores |
| 16 | Aislamiento de memoria por cliente visible en UI ("este empleado trabaja para el cliente X") | M | M | F3 | Test de propiedad ya previsto |
| 17 | Evaluacion de agentes con casos dorados y calidad visible por empleado (puntaje) | M | A | F7 | Contrarresta "agente que pasa evals y falla" |
| 18 | Marketplace de plantillas/puestos (curado, luego comunitario) y API publica | M | A | F7 | Primero curado propio desde #9 |
| 19 | Voz: recepcionista telefonica en espanol (entrante) | M | A | F7+ | Costo por minuto alto; validar demanda antes |
| 20 | "Modo TV"/clips compartibles de la oficina para marketing (exportar replay) | M | B | F2-F3 | Aprovecha el activo 3D; replay con `seq` (F2) |

Orden sugerido: 1-3 (confianza en costos) -> 4/5/9 (activacion) -> 7/6/8 (valor real y confianza) -> 12/13 (donde esta el usuario) -> 14/15 (profundidad).

---

## 6. Estrategia de posicionamiento y pricing sugerida

### Posicionamiento (hipotesis)
- **Frase**: "Tu oficina de empleados IA que trabaja en espanol, la ves trabajar y no hace nada importante sin tu OK."
- **Segmento inicial**: PYMEs y profesionales hispanohablantes con 2-30 personas (despachos, agencias, consultores, comercio con ventas B2B), donde el dueno es el cuello de botella. Evitar enterprise al inicio (Agentforce/Copilot dominan y tienen ciclos largos).
- **Contra quien**: no contra Zapier/n8n en integraciones; si contra Sintra/Marblism (misma metafora, pero ellos redactan y piden mucha revision, nosotros ejecutamos con control), y contra Lindy en confianza y en espanol. Contra chatbots WhatsApp LatAm: complementarios (cara-al-dueno vs cara-al-cliente).
- **Pruebas de confianza como marketing**: auditoria exportable, "0 envios sin aprobacion" (criterio de salida de F4), credenciales fuera del modelo.
- **Orden de entrada**: demo en simulacion gratis (ya funciona sin clave) como gancho; luego email real (F4) y WhatsApp (F5).
- **Riesgos**: depender de CrewAI y de un proveedor de LLM (margen); la 3D puede costar rendimiento en equipos modestos; la tarea de integrar es larga (F4-F6) y ahi estan los rivales hoy.

### Pricing (propuesta a validar; ninguna cifra esta probada con clientes)
Principios: previsible, sin creditos opacos, con tope duro, mensual sin permanencia.

| Plan | Precio sugerido (USD/mes) | Incluye |
|---|---|---|
| **Demo** | 0 | Simulacion ilimitada, 1 usuario, sin integraciones reales |
| **Starter** | ~29-39 | 1 oficina, 3 empleados, email + calendario, presupuesto de IA incluido ~$10, aprobaciones, 1 usuario |
| **Team** | ~99-149 | 7 empleados, hasta 5 usuarios con roles, WhatsApp/Slack, memoria por cliente, presupuesto incluido ~$40 |
| **Business** | ~299-399 | Todos los empleados, CRM/ERP, workflows ilimitados, auditoria exportable, SSO ligero, presupuesto incluido ~$150, soporte prioritario |
| **Enterprise** | a medida | SSO SAML/SCIM, region de datos, SLA, API |

- **Modelo**: precio base por oficina (no por asiento, para evitar el castigo de Dust/Copilot) + presupuesto de IA incluido, con **consumo adicional a costo + margen visible** y tope duro configurable. Mostrar siempre "gastado / estimado / tope".
- **Por que no creditos**: es la queja numero uno (Manus, Genspark, Lindy, Sintra, Copilot, Gumloop). Alternativa: unidad "$ de trabajo IA" transparente.
- **Referencias de mercado**: Sintra $52-97, Marblism $24-44, Lindy desde $29.99, Dust 24 EUR/usuario, Zapier Agents $50, Gumloop $37, Treble ~$399. Nuestro Starter queda en el rango de Sintra/Marblism; Business compite con Treble pero cubre mas funciones.
- **Margen**: **no verificado.** Falta medir el costo real de LLM por solicitud en modo live antes de fijar presupuestos incluidos. Primer paso: instrumentar `usage.cost_usd` (ya existe en el contrato) en 20-50 solicitudes reales y calcular percentiles.
- **Precios locales**: facturar en USD con opcion MXN/COP/ARS/CLP; en Argentina considerar inestabilidad cambiaria.
- **Reglas anti-queja**: cancelacion self-serve, prueba sin tarjeta, aviso a 80% de presupuesto, reembolso por ejecuciones fallidas atribuibles al sistema.

---

## Apendice: limites de esta investigacion
- WebFetch no pudo leer Manus (pagina de precios) ni Genspark (403); sus precios son de terceros.
- Dust, Lindy y Marblism: no hay una fuente independiente de peso para volumen de usuarios; las cifras de usuarios/ARR de la propia empresa no se verificaron.
- No se probo ningun producto de forma practica; las debilidades vienen de resenas publicadas (Trustpilot/G2/blogs), que tienden a sobrerrepresentar experiencias negativas (Trustpilot) o positivas (listados de afiliados).
- Estadisticas de adopcion y fallos de Agentforce y encuestas generales: provienen de articulos secundarios; tratarlas como orientativas.
- Fuentes de LatAm: la mayoria son comparativas publicadas por proveedores del mismo sector (sesgo).
- Las funciones de Copilot Studio "aprobacion humana por herramienta" estaban en desarrollo/no GA a 2026-09-01 segun el aviso citado.
