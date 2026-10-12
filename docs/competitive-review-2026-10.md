# Revision competitiva 2026-10: que copiar y que mejorar

Fecha: 2026-10-12. Extiende `docs/competitive-analysis.md` (2026-10-06); no repite sus conclusiones (huecos de mercado, pricing sugerido, backlog de 20 mejoras). Aqui el foco es primera entrada, onboarding, activacion y retencion.

Convenciones: [oficial] = leido en la pagina del proveedor en esta sesion; [terceros] = blog/resena/agregador; "no verificado" = no pude confirmarlo. Las busquedas dieron poco sobre el flujo exacto de primer uso de varios productos (Gumloop, n8n, Manus, Relevance, CrewAI, Copilot Studio); en esos casos lo digo y no lo invento. No se probo ningun producto en vivo.

---

## 1. Resumen ejecutivo

1. **El patron ganador de arranque es "4 pasos y trabaja"**: Sintra lo dice literal en su home (contexto del negocio, conectar herramientas, reglas y niveles de aprobacion, dejar trabajar) [oficial: https://sintra.ai]. Lindy promete setup de unos 2 minutos y $50 de credito sin tarjeta [oficial: https://www.lindy.ai/pricing]. Nosotros tenemos mas (oficina viva, tours) pero no tenemos un camino corto y obligatorio hacia el primer resultado.
2. **Nadie combina presencia espacial con trabajo de agentes para un no tecnico.** Los espaciales (Gather, WorkAdventure) son para humanos y cobran por miembro; los de agentes son chats/listas. Nuestra oficina es el diferenciador, pero hoy no esta usada para el momento de mayor impacto: la primera entrada.
3. **Confianza: la tendencia es aprobar "una vez / esta sesion / denegar"** (Copilot Studio, anunciado, no GA confirmado [terceros: https://mc.merill.net/message/RM570434]) y aprobaciones en Slack (Zapier, [terceros: https://reallygoodemails.com/emails/new-ai-agents-are-here-make-your-zaps-smarter/text]). Artisan dice tener "approval gates donde quieras" sin explicar como [oficial: https://www.artisan.co/blog/artisan-launches-ava-2-0-the-first-autonomous-ai-bdr-now-self-serve]. Hay espacio para hacer la aprobacion visible y fisica en la oficina.
4. **Pricing: la senal de moda es bajar la barrera de entrada.** Artisan anuncio bajar la entrada de $2,500 a $250/mes con $300 de credito sin tarjeta (anuncio propio) [oficial: mismo enlace]; Lindy pausa y avisa al agotar creditos en lugar de cobrar extra [oficial: https://www.lindy.ai/pricing]. Nuestra respuesta: demo gratis + tope duro visible + cero creditos opacos (ya en el analisis previo).
5. **Retencion de los competidores es funcional (valor del trabajo), no ludica.** Ninguno de los revisados muestra monedas/rachas ligadas a trabajo terminado [no verificado en los 14 productos revisados; ausencia no probada]. Las monedas son nuestro diferenciador si se atan a metas reales y no a "tiempo en pantalla".
6. **Riesgo directo:** Sintra se vende con el mismo concepto ("AI employees" con nombre) y con cifras de escala (40,000+ emprendedores) [oficial]. Debemos ganar en ver-trabajar, confianza y espanol, no en cantidad de helpers.

---

## 2. Tabla de competidores (solo lo nuevo; la comparativa completa esta en el analisis previo)

| Producto | Primer uso / bienvenida | Lo que hace bien | Precio en una linea | Debilidad |
|---|---|---|---|---|
| **Sintra** | Home propone 4 pasos: contexto, herramientas, reglas/aprobaciones, a trabajar [oficial: https://sintra.ai]; segun una resena, plan -> poblar "Brain AI" con texto, web, PDF [terceros: https://www.toolmage.com/en/tool/sintra/]. Workspaces separan marcas/clientes [terceros: dupple.com/reviews/sintra] | 12 helpers con nombre y rol; prueba social numerica (600K+ cuentas, 100M+ prompts 2025, afirmacion propia) y garantia de reembolso [oficial] | Planes de pago con 14 dias de garantia; cifras exactas del analisis previo: $52-97/mes [oficial: https://sintra.ai/pricing] | Segun una resena de un competidor, no ejecuta en sistemas reales (sesgo posible) [terceros: https://wf.lindy.ai/blog/sintra-ai-review]; plantel inconsistente entre fuentes |
| **Lindy** | Free: $50 de credito, sin tarjeta, ~2 min de setup, valido 7 dias [oficial: https://www.lindy.ai/pricing]. Docs: skills que se crean describiendo el proceso en el chat [oficial: https://docs.lindy.ai/skills/by-lindy/step-by-step-onboarding.md]. "Step by step onboarding" tipo formulario para plantillas que recoge autorizaciones [terceros: zoftwarehub.com/en-bh/products/lindy/zoftware-analysis] | Credito de prueba generoso y limitado en el tiempo (urgencia); pausa y avisa al agotar creditos; onboarding en vivo en Team [oficial] | Team $29.99/mes/usuario por 3,000 creditos; top-up $10 por 1,000; Enterprise a medida [oficial]. Un sitio de comparacion afirma que el free permanente se elimino y la entrada es $49.99 [terceros: hackceleration.com; contradice la pagina oficial; no verificado] | Creditos que no rolan y consumo variable (2 a 2,500 por tarea) [oficial]; quejas de analisis previo |
| **Artisan (Ava 2.0)** | Anuncia autoservicio en menos de 10 min, $300 de credito sin tarjeta, entrada $250/mes (anuncio propio, resumido por tercero) [terceros: https://www.usecarly.com/blog/artisan-ai/ ; pagina oficial solo muestra "Request a demo" y approval gates] | Metafora de empleado (BDR) con una sola funcion muy clara | Intern $250/mes anual, Employee $600/mes anual, resto por cotizacion [terceros: https://www.usecarly.com/blog/artisan-ai/, comprobado por ellos el 2026-10-08] | Un reporte neutral habla de 2-3 meses hasta ver resultados pese al "10 min" [terceros: https://www.enginy.ai/blog/artisan-pricing]; ventas-only |
| **Zapier Agents** | "Get started free"; Copilot ayuda a construir; galeria de plantillas (Lead Enrichment, Support Email Agent, Sales Email Writer, Expense Classifier...) [oficial: https://zapier.com/agents] | Plantillas por tarea concreta; ~100 plantillas segun terceros [terceros: https://growwstacks.com/blog/build-first-ai-agent-zapier/]; aprobaciones en Slack [terceros] | Free 400 actividades, Pro ~$50/mes [terceros: https://www.getapp.com/all-software/a/zapier-agents/] | La pagina de agentes no describe aprobaciones ni guardrails [oficial]; cifras de clientes inconsistentes (3.4M vs 2.2M) en la misma pagina |
| **Salesforce Agentforce** | Salesforce Foundations como entrada gratuita con Agent Builder y creditos Flex [terceros: https://coworker.ai/blog/salesforce-agentforce-pricing ; https://www.eesel.ai/blog/is-salesforce-agentforce-worth-the-cost]; prueba de 30 dias es afirmacion repetida pero sin confirmar | Entrada gratis para construir y probar antes de pagar | $2 por conversacion o Flex Credits (~$500/100k); implementacion por socios $2,000-6,000 por agente segun terceros [terceros: eesel.ai] | Costo real en implementacion; fuentes en conflicto sobre trial |
| **Microsoft Copilot Studio** | Onboarding de plantillas: no encontre fuente fiable (no verificado). Aprobacion humana por herramienta y agente: aprobar / aprobar la sesion / denegar, solicitud en linea en Teams y Copilot [terceros: https://mc.merill.net/message/RM570434]; "AI approvals" en agent flows con accion multistage [oficial: https://www.microsoft.com/en-us/microsoft-copilot/blog/?p=5650] | Modelo mental de aprobacion mas fino del mercado (por herramienta, por sesion) | Ver analisis previo (packs de 25,000 creditos a $200/mes) | Roadmap marca la funcion "In development" con GA objetivo sept-2026: no confirmado que ya este disponible [terceros: mc.merill.net] |
| **Gather** | Segun guias antiguas (2021-2024), "isla tutorial" para moverse, silenciar e interactuar, saltable, luego puerta a un espacio [terceros: https://internship.its.umich.edu/content/how-use-gather-town; flujo posiblemente cambiado]. Plantillas de oficina y +200 objetos [oficial: https://www.gather.town/pricing] | Presencia espacial: ver y oir a los cercanos, estado sincronizado con calendario; guia de onboarding copiable para el equipo [oficial/terceros: https://www.gather.town/guides/onboarding-guide] | Essential $12/miembro/mes; prueba 30 dias sin tarjeta; custom con onboarding [oficial: https://www.gather.town/pricing]. Otro sitio dice $15 desde 2026 (no verificado) | Cobra por miembro; un sitio afirma que quitaron el plan gratis (no verificado); diseñado para humanos en llamada, no para trabajo asincrono de IA |
| **WorkAdventure** | Free sin limite de tiempo para hasta 10 simultaneos (pagina) con 1 mes de prueba premium; mapas con plantillas y editor; un bot de IA incluido [oficial: https://workadventu.re/pricing] | Open source/autoalojable, gamificacion en el free, Customer Success Manager en Business | Business desde 8 EUR/usuario/mes (la pagina oficial); otros listados dicen 5 EUR o $11 (no coinciden) [oficial vs terceros] | Orientado a eventos y oficina remota humana; sin agentes de negocio |
| **AI Town (a16z + Convex)** | Starter kit MIT desplegable con personajes LLM [terceros: https://cryptoslate.com/vc-firm-a16z-releases-open-source-microverse-packed-with-generative-ai-npcs-in-ai-town/] | Demuestra que ver personajes con vida propia atrae | Gratis (open source) | Sin objetivo de negocio ni aprobaciones; actividad 2026 no verificada |
| **Gumloop** | Onboarding: busqueda sin detalle fiable (no verificado). 185+ plantillas en marketplace comunitario (julio 2026) [terceros: https://automationatlas.io/answers/gumloop-pricing-explained-2026/] | Marketplace de plantillas comunitarias | Pro $37/mes desde 20,000 creditos; el free varia entre fuentes (2,000 a 5,000 creditos) [terceros: automationatlas.io; lindy.ai/blog/gumloop-pricing] | Fuentes de precios inconsistentes; ver analisis previo (no lineal) |
| **Relevance AI** | Invent (texto a agente) y Workforce (equipos) lanzados con Serie B de $24M (2025) [terceros: https://itbrief.com.au/story/relevance-ai-raises-24-million-to-boost-ai-workforce-tools]; despliegue "en minutos" con plantillas o marketplace [oficial: https://relevanceai.com/docs]; plantilla estrella BDR [terceros: relevanceai.com/blog, sin fecha] | Una plantilla estrella en lugar de 400 opciones sin guia | Solo Enterprise oficial; ver analisis previo | Flujo actual de primer uso: no verificado |
| **Manus** | 300 creditos diarios gratis ademas de un bono de bienvenida (1,000-1,500 segun la fuente) [terceros: https://fast.io/resources/is-manus-ai-free/] | Credito diario recurrente = motivo para volver | Pro $20/mes por 4,000 creditos [terceros: https://www.nocode.mba/articles/manus-ai-pricing] | Consumo por tarea muy variable (~59 a 900 creditos) [terceros: https://meshworld.in/blog/ai/agents/manus-ai/mastery-guide/] |
| **CrewAI AMP** | Onboarding: no encontre detalle; los tiers incluyen "horas de onboarding" (2 a 20) [terceros: https://www.lindy.ai/blog/crew-ai-pricing, enero 2026, de un competidor] | Vender onboarding asistido como parte del plan | Free 50 ejecuciones, Basic $99, Standard $500, Pro $1,000 (misma fuente; no oficial) | Opaco; es nuestro motor |
| **n8n** | Biblioteca comunitaria de plantillas de agentes [terceros: https://horadecodar.com.br/templates-automacao-n8n-nichos-2026/]; precios en el analisis previo | Plantillas gratuitas de gran volumen | Ver analisis previo | Primer uso orientado a tecnicos; flujo exacto no verificado |

---

## 3. Que copiar

| # | Que copiar | Quien lo hace | Nuestra version concreta | Esfuerzo | Por que gana |
|---|---|---|---|---|---|
| C1 | **Camino de 4 pasos visible ("checklist de activacion")** | Sintra (contexto, herramientas, reglas, trabajar) [oficial: https://sintra.ai] | Checklist persistente en la oficina: 1) Cuentanos tu negocio (Sofia), 2) Conecta una herramienta, 3) Elige cuanta autonomia, 4) Pide tu primera tarea. Cada paso marcado completa una "meta" con monedas | M | Sintra lo hace en una web; nosotros lo hacemos en un mundo donde el paso 4 se ve |
| C2 | **Credito de prueba con reloj** | Lindy ($50, 7 dias, sin tarjeta) [oficial] | Sin tarjeta; "Tu primera semana: X tareas incluidas" con contador visible. Importante: pausa y aviso, nunca cobro sorpresa | S | Copia lo que funciona sin copiar el defecto de creditos opacos |
| C3 | **Una plantilla estrella por segmento, no un catalogo** | Relevance (BDR como plantilla insignia) [terceros] | Tres "paquetes de oficina" de primera tarea: despacho/consultor, tienda online, agencia. Cada uno con una tarea de ejemplo ya redactada en espanol | M | Reduce la decision en el primer minuto; el analisis previo ya proponia paquetes por negocio, aqui se prioriza uno por segmento |
| C4 | **Aprobar "solo esta vez / esta sesion / denegar"** | Copilot Studio (anunciado) [terceros] | Tarjeta de aprobacion con tres botones y "recordar para este destinatario" (ya hay allowlist en el diseno previo) | M | Reduce fatiga de aprobar sin renunciar a control |
| C5 | **Aprobacion en el canal donde ya estas** | Zapier (Slack) [terceros] | Notificacion push/email con boton Aprobar para la bandeja (antes de WhatsApp) | M | Cierra el ciclo cuando el usuario no tiene la oficina abierta |
| C6 | **Credito diario recurrente** | Manus (300/dia) [terceros] | "Visita diaria": 1 mini-meta del dia con premio de monedas (no creditos de computo) | S | Motivo de retorno sin regalar costo de LLM |
| C7 | **Presencia con estado y plantillas de espacio** | Gather (estado, +200 objetos, plantillas) [oficial] | Plantillas de oficina por segmento que se aplican en el onboarding (decoracion inicial acorde al negocio) | M | Convierte la tienda cosmetica en parte del arranque |
| C8 | **Garantia visible y prueba social con numeros propios** | Sintra (garantia de reembolso, cifras de uso) [oficial] | Mostrar solo cifras reales de early access; mientras no existan, usar casos concretos ("tarea terminada en X min") con evidencia, no numeros inflados | S | Sintra se apoya en cifras de escala que no podemos igualar hoy; la transparencia es una alternativa honesta |
| C9 | **Onboarding asistido como parte del plan** | Lindy (live onboarding en Team), CrewAI (horas), Gather (custom onboarding) [oficial/terceros] | Sesion de 20 min por videollamada en espanol para el primer equipo de pago | S | Barato de operar y fuerte en confianza para PYME hispanohablante |

---

## 4. Que mejorar y donde ganar

| # | Oportunidad | Donde fallan los demas | Nuestra jugada | Esfuerzo | Por que gana |
|---|---|---|---|---|---|
| M1 | **Aprobacion fisica en la oficina**: el empleado camina a tu escritorio con la carpeta; al aprobar, la envia | Copilot Studio y Zapier: aprobaciones como mensaje o paso; Artisan no explica el mecanismo [oficial] | Animacion de "entrega en mano" ligada al evento real `approval.requested`; el borrador abre editable, con diff y riesgo (low/medium/high) | M | Convierte el punto mas aburrido en el mas memorable y refuerza "nada sale sin tu OK" |
| M2 | **Primera tarea con exito garantizado** | Artisan: 10 min de setup vs 2-3 meses a resultados [terceros]; Lindy: creditos que se gastan en pruebas fallidas | Tarea de bienvenida de bajo riesgo que no necesita conexiones (ej. investigar y resumir sobre el propio negocio) con resultado en menos de ~2 min en simulacion o live con tope | M | Activacion = ver un reporte propio rapido; sin esto el 3D es decoracion |
| M3 | **Metas y monedas atadas a trabajo terminado y a seguridad** | Competidores no gamifican el valor (no verificado); WorkAdventure gamifica la presencia [oficial] | Monedas por: primera tarea, primer reporte aprobado, conectar con permisos minimos, revisar el centro de seguridad. No por tiempo en pantalla | M | Premia comportamientos que aumentan retencion y confianza a la vez |
| M4 | **Costo visible antes y despues** | Lindy: 2 a 2,500 creditos por tarea [oficial]; Manus 59 a 900 [terceros] | "Esta tarea costara aprox. X-Y" antes y "costo real Z" en el reporte; sin unidad "credito" | M | Responde a la queja numero uno ya documentada |
| M5 | **Equipo inicial por segmento con nombres y voz en espanol** | Sintra: helpers fijos en ingles; roster inconsistente entre fuentes | Contratar 3 de los 7 empleados en el onboarding y desbloquear el resto con metas | S | Menos abrumador y alimenta el progreso |
| M6 | **Camino de upgrade claro** | Gumloop/Lindy: precios variables [terceros] | Pagina de planes con "gastado / estimado / tope" y tope duro por defecto | S | Confianza comercial |

---

## 5. Experiencia de bienvenida: 3 propuestas concretas

Contexto: el usuario ya tiene cuenta (login/registro), una organizacion y entra a la oficina por primera vez. Hoy tenemos tours por pantalla; falta un momento de apertura. Las propuestas son alternativas, se pueden combinar A+C.

### Propuesta A: "Sofia te recibe en la puerta" (recomendada, esfuerzo M)

Idea: Sofia (asistente) camina hasta la entrada, y los demas empleados levantan la vista. Un solo texto, una sola pregunta.

Texto (voz de Sofia, tuteo neutro):
- Linea 1: "Hola, [nombre]. Esta es tu oficina. Aqui trabajamos nosotros y tu decides."
- Linea 2: "Antes de empezar: nada importante sale de aqui sin tu aprobacion."
- Pregunta: "Para que tipo de negocio trabajamos?" con tres botones: "Despacho o consultoria", "Tienda online", "Agencia o servicios" y "Otro".
- Cierre: "Perfecto. Te presento a tu equipo."

Pasos:
1. Camara hace un paneo corto de la oficina vacia a la entrada (2 s, saltable).
2. Sofia aparece y habla con globo de dialogo; los demas empleados cambian a estado "atento" (se reutiliza el estado existente, sin logica nueva de backend).
3. El usuario elige negocio; se siembran 3 empleados y decoracion inicial (ver C7) y se muestra el checklist de activacion (C1).
4. Sofia propone la primera tarea (M2) con un boton "Probar ahora".
5. Se otorgan las primeras monedas al completar el paso 1 del checklist.

Por que: mezcla el 4 pasos de Sintra con presencia espacial al estilo Gather; aqui es el empleado quien saluda, no una isla tutorial [terceros sobre Gather]. Medir: porcentaje que llega a "primera tarea enviada" en la primera sesion (hipotesis, sin cifra base).

### Propuesta B: "Tu primer dia de oficina" (guion en 3 escenas, esfuerzo M)

Idea: la bienvenida es una mini-historia con meta clara, para quien prefiere no escribir.

Texto por escena:
- Escena 1, Recepcion: "Bienvenido. Hoy es el primer dia de tu equipo. Elige a quien quieres conocer primero."
- Escena 2, Escritorio del empleado elegido: "Soy Valeria, de Ventas. Puedo preparar una propuesta, pero no la envio sin que la veas. Quieres ver como lo hago?"
- Escena 3, Bandeja de aprobacion: "Aqui aparecen mis borradores. Aprueba, edita o rechaza. Tu mandas."

Pasos:
1. Eleccion de un empleado entre 3 fichas (nombre, cara, que hace).
2. Ejecuta una tarea de demostracion con datos de ejemplo claramente marcados como tales (en simulacion).
3. El usuario aprueba o rechaza el primer borrador; recibe un reporte y monedas.
4. Se ofrece "Hacerlo con tus datos" que lleva a conectar una herramienta con permisos minimos.

Por que: enseña el ciclo completo (pedir, ver, aprobar, reporte) en menos de 2 minutos. Riesgo: confundir datos de ejemplo con reales; marcarlos siempre y no emitir acciones externas.

### Propuesta C: "Reunion de arranque" (esfuerzo S, la mas barata)

Idea: sin animacion nueva, un modal-reunion antes del tour que reutiliza los tours existentes.

Texto:
- Titulo: "Reunion de arranque, 60 segundos".
- Cuerpo: "Tu equipo esta listo. Dime tres cosas y empezamos: a que se dedica tu negocio, que te quita mas tiempo y que nunca debo hacer sin preguntarte."
- Tres campos cortos con ejemplos y boton "Empezar".

Pasos:
1. Formulario de tres campos (el tercero define reglas de autonomia iniciales).
2. Sofia resume en un globo lo que entendio y lo guarda como memoria de compania.
3. Se lanza el tour de la oficina y se muestra el checklist.

Por que: es el formulario conversacional de Lindy/Relevance en clave de oficina. Menos impacto emocional que A/B pero se puede publicar en dias y sirve como base de memoria para el wizard del analisis previo (item 5 del backlog).

Reglas comunes a las tres: saltable siempre, accesible con teclado, respetar "reducir movimiento", solo una vez por cuenta con boton "Ver bienvenida otra vez" en Ayuda, y nunca un efecto de sonido sin accion del usuario.

---

## 6. Prioridades en 30 dias

Semana 1 (S, rapidas):
- C2/M6: pagina de plan "gastado / estimado / tope" y mensaje de prueba sin tarjeta con contador.
- C6: meta diaria con monedas.
- C8: seccion de prueba social basada en casos reales de early access (sin numeros inflados).

Semana 2 (M):
- Propuesta C (reunion de arranque) como entrega minima de bienvenida.
- C1: checklist de activacion con 4 pasos y monedas por paso.

Semana 3 (M):
- Propuesta A (Sofia en la puerta) construida sobre C.
- M2: primera tarea garantizada (de bajo riesgo).

Semana 4 (M):
- C4: aprobacion "solo esta vez / esta sesion / denegar".
- M1: animacion de entrega en mano para aprobaciones.
- C9: sesion de onboarding de 20 min para los primeros equipos.

Medicion minima (hipotesis a validar, sin cifras base): porcentaje que completa el checklist; tiempo hasta la primera tarea; porcentaje que aprueba al menos un borrador; retorno a los 7 dias. Instrumentar antes de lanzar para no pelearse con los datos despues.

No hacer en estos 30 dias: marketplace, voz, WhatsApp (ver backlog previo).

---

## 7. Notas y limites de esta revision

- Las busquedas no dieron flujos de primer uso fiables para Gumloop, n8n, Manus, Relevance, CrewAI y Copilot Studio; lo marcado "no verificado" no debe usarse en material comercial.
- Hay conflictos de datos que no pude resolver: precio de WorkAdventure (8 EUR en la pagina oficial; 5 EUR y $11 en otras fuentes), precio de Lindy Free (oficial dice $50 gratis; un sitio dice que ya no existe), precio de Gather ($12 oficial; $15 en un sitio), plan de pago de Gumloop. Se prioriza siempre la pagina oficial cuando la lei.
- La ausencia de monedas/rachas en competidores es una observacion sobre lo que encontre, no una prueba negativa.
- Las cifras de Sintra (600K+ cuentas, 100M+ prompts) son afirmaciones de la propia empresa.
- Las funciones de Copilot Studio citadas estaban en roadmap; no confirme disponibilidad general.

---

## 8. Fuentes con URL

Oficiales (leidas en esta sesion):
- Sintra home: https://sintra.ai
- Sintra pricing: https://sintra.ai/pricing (citada del analisis previo)
- Lindy pricing: https://www.lindy.ai/pricing
- Lindy docs, skills: https://docs.lindy.ai/skills/by-lindy/step-by-step-onboarding.md
- Gather pricing: https://www.gather.town/pricing
- Gather guia de onboarding para equipos: https://www.gather.town/guides/onboarding-guide (solo titulo en resultados de busqueda)
- WorkAdventure pricing: https://workadventu.re/pricing
- Zapier Agents: https://zapier.com/agents
- Artisan, anuncio Ava 2.0: https://www.artisan.co/blog/artisan-launches-ava-2-0-the-first-autonomous-ai-bdr-now-self-serve
- Microsoft Copilot blog (AI approvals): https://www.microsoft.com/en-us/microsoft-copilot/blog/?p=5650
- Relevance AI docs: https://relevanceai.com/docs

Terceros:
- Sintra, resenas: https://www.toolmage.com/en/tool/sintra/ ; https://dupple.com/reviews/sintra ; https://wf.lindy.ai/blog/sintra-ai-review (competidor)
- Lindy: https://zoftwarehub.com/en-bh/products/lindy/zoftware-analysis ; https://www.hackceleration.com/labs/compare/lindy-vs-airia
- Artisan: https://www.usecarly.com/blog/artisan-ai/ ; https://www.enginy.ai/blog/artisan-pricing
- Zapier Agents: https://growwstacks.com/blog/build-first-ai-agent-zapier/ ; https://www.getapp.com/all-software/a/zapier-agents/ ; https://reallygoodemails.com/emails/new-ai-agents-are-here-make-your-zaps-smarter/text
- Agentforce: https://coworker.ai/blog/salesforce-agentforce-pricing ; https://www.eesel.ai/blog/is-salesforce-agentforce-worth-the-cost
- Copilot Studio aprobaciones: https://mc.merill.net/message/RM570434 ; https://radar.firstaimovers.com/copilot-studio-human-in-loop-governance-smes-2026.md
- Gather onboarding (guias antiguas): https://internship.its.umich.edu/content/how-use-gather-town ; https://www.costbench.com/software/video-conferencing/gather-town/hidden-costs/
- WorkAdventure: https://subscribed.fyi/workadventure/reviews/ ; https://us.fitgap.com/products/workadventure
- AI Town: https://cryptoslate.com/vc-firm-a16z-releases-open-source-microverse-packed-with-generative-ai-npcs-in-ai-town/ ; https://github.com/a16z-infra/ai-town/
- Gumloop: https://automationatlas.io/answers/gumloop-pricing-explained-2026/ ; https://www.lindy.ai/blog/gumloop-pricing (competidor)
- Relevance AI: https://itbrief.com.au/story/relevance-ai-raises-24-million-to-boost-ai-workforce-tools
- Manus: https://fast.io/resources/is-manus-ai-free/ ; https://www.nocode.mba/articles/manus-ai-pricing ; https://meshworld.in/blog/ai/agents/manus-ai/mastery-guide/
- CrewAI: https://www.lindy.ai/blog/crew-ai-pricing (competidor)
- n8n: https://horadecodar.com.br/templates-automacao-n8n-nichos-2026/
