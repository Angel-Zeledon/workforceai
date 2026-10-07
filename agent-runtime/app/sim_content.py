"""Scripted content (Spanish, default locale) for SimulationEngine. English lives in sim_content_en.py."""
from __future__ import annotations

import re

# ---------------------------------------------------------------- escenarios
def detect_scenario(text: str) -> str:
    t = text.lower()
    if re.search(r"cliente\s+nuevo|nuevo\s+cliente|50[,.]?000|propuesta|new\s+client|proposal", t):
        return "new_client"
    if re.search(r"ventas?\s+(bajaron|cayeron|han\s+bajado|disminuy)|bajaron\s+las\s+ventas|ca[ií]da\s+de\s+ventas|sales\s+(dropped|fell|declined)|drop\s+in\s+sales|sales\s+drop", t):
        return "sales_drop"
    if re.search(r"contratar|contrataci|empleado|vacante|reclut|nuevo\s+personal|\bhir(e|ing)\b|recruit|vacanc", t):
        return "hiring"
    if "contrato" in t or "contract" in t:
        return "contract"
    return "default"


# task: (key, role, title, description, depends_on keys)
PLANS: dict[str, dict] = {
    "new_client": {
        "objectives": [
            "Evaluar y cerrar la propuesta de $50,000 para el cliente nuevo",
            "Validar rentabilidad real y riesgos legales antes de enviar la propuesta",
            "Confirmar que Operaciones puede entregar con la capacidad actual",
        ],
        "tasks": [
            ("ventas", "sales", "Analizar cliente y preparar propuesta",
             "Analizar al cliente nuevo (sector, tamano, necesidades) y preparar la propuesta comercial de $50,000. La propuesta no se envia sin aprobacion.", []),
            ("legal", "legal", "Revisar contrato y clausulas",
             "Revisar el borrador de contrato asociado a la propuesta: penalidades, propiedad intelectual, terminacion y responsabilidad.", ["ventas"]),
            ("contador", "accounting", "Calcular margen real de la propuesta",
             "Calcular el margen real considerando costos directos, comisiones y condiciones de pago; comparar con el 31% presupuestado.", ["ventas"]),
            ("analista", "analyst", "Analizar rentabilidad del cliente",
             "Analizar rentabilidad esperada y riesgo de concentracion del cliente usando el margen real del Contador.", ["ventas", "contador"]),
            ("operaciones", "operations", "Evaluar capacidad operativa",
             "Evaluar si el equipo actual puede cumplir con el alcance y los plazos de la propuesta; estimar necesidades de personal.", ["ventas"]),
        ],
        "questions": [],
    },
    "sales_drop": {
        "objectives": ["Entender por que bajaron las ventas", "Identificar acciones comerciales y financieras de recuperacion"],
        "tasks": [
            ("analista", "analyst", "Diagnosticar la caida de ventas",
             "Analizar la serie de ventas por producto, canal y segmento para identificar el origen de la caida.", []),
            ("ventas", "sales", "Revisar pipeline y conversion",
             "Revisar el pipeline, tasas de conversion y motivos de perdida a la luz del diagnostico del Analista.", ["analista"]),
            ("contador", "accounting", "Medir impacto en flujo de caja",
             "Cuantificar el impacto de la caida en ingresos, margen y flujo de caja de los proximos 90 dias.", ["analista"]),
        ],
        "questions": ["¿Desde que fecha o periodo notas la caida?"],
    },
    "hiring": {
        "objectives": ["Definir el perfil y proceso de contratacion", "Validar costo total y cumplimiento legal"],
        "tasks": [
            ("rrhh", "hr", "Definir perfil y proceso de reclutamiento",
             "Definir perfil, banda salarial, canales de reclutamiento y calendario de entrevistas.", []),
            ("contador", "accounting", "Estimar costo total de la contratacion",
             "Estimar costo total mensual y anual (salario, cargas sociales, equipo) y su efecto en el presupuesto.", ["rrhh"]),
            ("legal", "legal", "Revisar contrato laboral y cumplimiento",
             "Preparar y revisar el contrato laboral, periodo de prueba y obligaciones legales.", ["rrhh"]),
        ],
        "questions": ["¿Cual es el puesto exacto y el salario objetivo?"],
    },
    "contract": {
        "objectives": ["Revisar el contrato y sus riesgos", "Alinear la postura comercial con la revision legal"],
        "tasks": [
            ("legal", "legal", "Revisar el contrato",
             "Revisar clausulas, riesgos, penalidades y obligaciones del contrato indicado.", []),
            ("ventas", "sales", "Alinear terminos comerciales",
             "Alinear los terminos comerciales con las observaciones legales y preparar la contraoferta.", ["legal"]),
        ],
        "questions": [],
    },
    "default": {
        "objectives": ["Entender y atender la solicitud"],
        "tasks": [
            ("asistente", "assistant", "Estructurar la solicitud",
             "Aclarar el alcance de la solicitud, reunir contexto y definir entregables.", []),
            ("analista", "analyst", "Analizar la informacion disponible",
             "Analizar los datos disponibles y proponer conclusiones accionables.", ["asistente"]),
        ],
        "questions": ["¿Hay una fecha limite o un resultado especifico que necesites?"],
    },
}

# ------------------------------------------------------------------ contenido
def _o(summary, findings, metrics, hypotheses, evidence, recommendations, confidence, suggested):
    return dict(summary=summary, findings=findings, metrics=metrics, hypotheses=hypotheses,
                evidence=evidence, recommendations=recommendations, confidence=confidence,
                suggested_tasks=suggested)


CONTENT: dict[tuple[str, str], dict] = {}

CONTENT[("new_client", "sales")] = dict(
    output=_o(
        "El cliente encaja con nuestro perfil ideal (mediana empresa de logistica, 180 empleados) y la propuesta de $50,000 esta lista, pero requiere aprobacion antes de enviarse porque compromete precio y plazos.",
        ["Cliente de mediana empresa del sector logistico con presupuesto aprobado para el trimestre.",
         "Necesita implementacion en 10 semanas; competidor B ofrecio un precio 12% menor sin soporte incluido.",
         "El decisor (Directora de Operaciones) valora SLA de soporte sobre precio.",
         "Propuesta preparada: alcance, cronograma, 3 hitos de pago (40/30/30) y SLA de 99.5%."],
        {"valor_propuesta_usd": 50000, "probabilidad_cierre": "68%", "ciclo_estimado_dias": 21, "descuento_ofrecido": "5%"},
        ["Si se envia esta semana la probabilidad de cierre sube a ~75%.", "El descuento del 5% puede ser innecesario dado el interes en SLA."],
        ["CRM: 3 reuniones previas con el cliente, ultima hace 4 dias.", "Brief del cliente: presupuesto trimestral aprobado."],
        ["Enviar la propuesta una vez validados margen y contrato (ver Contador y Legal).",
         "Mantener el descuento en 5% como maximo y condicionarlo al pago anticipado del primer hito."],
        0.78,
        ["Agendar llamada de cierre con la Directora de Operaciones", "Preparar caso de exito de un cliente logistico similar"],
    ),
    consults=[],
    tool_requests=[dict(tool="email", action="send_proposal", risk="high",
                        args=dict(to="directora.operaciones@clientenuevo.example", subject="Propuesta comercial - $50,000",
                                  attachment="propuesta_cliente_nuevo_v1.pdf", amount_usd=50000))],
)
CONTENT[("new_client", "legal")] = dict(
    output=_o(
        "El borrador de contrato es aceptable con tres ajustes: limitar la penalidad por retraso, aclarar propiedad intelectual y acotar la responsabilidad total.",
        ["La penalidad por retraso (1% semanal sin tope) es excesiva; recomendar tope de 10% del valor.",
         "La clausula de propiedad intelectual cede todo el codigo al cliente, incluidos componentes reutilizables propios.",
         "La responsabilidad no esta limitada; debe acotarse al monto del contrato.",
         "Terminacion por conveniencia a 15 dias es corta; proponer 30 dias."],
        {"clausulas_revisadas": 24, "riesgos_altos": 1, "riesgos_medios": 2, "ajustes_propuestos": 4},
        ["El cliente aceptara el tope de penalidad si se mantiene el SLA.", "Se requiere anexo de proteccion de datos si hay datos personales."],
        ["Borrador de contrato v0.3, clausulas 7, 11, 14 y 18.", "Plantilla estandar de la empresa vigente 2026."],
        ["No firmar sin tope de penalidad ni limitacion de responsabilidad.", "Incluir anexo de propiedad intelectual con carve-out de componentes propios."],
        0.84,
        ["Enviar redline del contrato a Ventas", "Preparar anexo de proteccion de datos"],
    ),
    consults=[dict(to_agent_id="sales", question="¿El cliente ya acepto el calendario de pagos 40/30/30 o sigue en negociacion?")],
    tool_requests=[],
)
CONTENT[("new_client", "accounting")] = dict(
    output=_o(
        "El margen presupuestado de 31% no se sostiene: con costos reales, comisiones y el descuento del 5%, el margen real es 24%, por debajo del objetivo de 28%.",
        ["Margen bruto presupuestado: 31% ($15,500 sobre $50,000).",
         "Margen real estimado: 24% ($12,000) tras costos de implementacion, comision de ventas (4%) y descuento.",
         "Brecha de 7 puntos porcentuales ($3,500) frente a lo presupuestado.",
         "El esquema de pago 40/30/30 deja el 30% final a 75 dias, con impacto en capital de trabajo."],
        {"margen_presupuestado": "31%", "margen_real": "24%", "brecha_pp": 7, "costo_directo_usd": 38000, "ingreso_usd": 50000},
        ["Subir el anticipo a 50% mejoraria el flujo sin afectar el margen.", "Eliminar el descuento devuelve ~2.5 puntos de margen."],
        ["Hoja de costos del proyecto v2.", "Tabla de comisiones vigente.", "Condiciones de pago del borrador de propuesta."],
        ["Eliminar el descuento del 5% o compensarlo con alcance menor.", "Renegociar pagos a 50/25/25.", "No aprobar bajo 26% de margen sin autorizacion de direccion."],
        0.88,
        ["Recalcular margen con anticipo de 50%", "Actualizar presupuesto del trimestre"],
    ),
    consults=[dict(to_agent_id="sales", question="¿Que descuento y condiciones de pago se le ofrecieron al cliente en la propuesta?")],
    tool_requests=[],
)
CONTENT[("new_client", "analyst")] = dict(
    output=_o(
        "El cliente es rentable pero marginal: con margen real de 24% el retorno sobre horas es 18% menor al promedio de la cartera, y representaria 14% de la facturacion trimestral.",
        ["Rentabilidad por hora estimada: $118 frente a $144 promedio de cartera.",
         "Concentracion: el cliente pasaria a representar 14% de la facturacion del trimestre.",
         "Clientes similares tuvieron 22% de sobrecosto en horas de soporte durante los primeros 3 meses.",
         "Valor de vida estimado a 24 meses: $118,000 si renueva el soporte."],
        {"rentabilidad_hora_usd": 118, "promedio_cartera_usd": 144, "concentracion_ingresos": "14%", "ltv_24m_usd": 118000, "riesgo_sobrecosto": "22%"},
        ["La renovacion de soporte compensa el margen bajo inicial.", "El riesgo de sobrecosto baja si se acota el alcance por escrito."],
        ["Historico de 12 proyectos comparables.", "Margen real calculado por el Contador (24%)."],
        ["Aprobar solo con alcance cerrado y control de cambios.", "Incluir soporte anual como linea separada para mejorar el LTV."],
        0.74,
        ["Construir tablero de seguimiento de horas del proyecto", "Definir umbral de alerta de sobrecosto"],
    ),
    consults=[dict(to_agent_id="accounting", question="¿Los $38,000 de costo directo incluyen la contratacion adicional que pide Operaciones?")],
    tool_requests=[],
)
CONTENT[("new_client", "operations")] = dict(
    output=_o(
        "Con el equipo actual no se puede cumplir el plazo de 10 semanas: se necesita contratar 2 personas (1 desarrollador senior y 1 consultor de implementacion) o el proyecto se retrasa a 18 semanas.",
        ["Capacidad actual disponible: 62 horas/semana; el proyecto requiere ~140 horas/semana en las semanas 3 a 8.",
         "Faltante de ~78 horas/semana equivalente a 2 personas de tiempo completo.",
         "Contratar y estabilizar toma ~4 semanas; hay que iniciar el reclutamiento de inmediato.",
         "Sin contratacion, el plazo realista es de 18 semanas y se incumpliria el SLA."],
        {"capacidad_disponible_h_sem": 62, "demanda_pico_h_sem": 140, "personas_a_contratar": 2, "plazo_actual_semanas": 18, "plazo_objetivo_semanas": 10},
        ["Contratistas externos podrian cubrir parcialmente el pico con 15% mas de costo."],
        ["Plan de capacidad de las proximas 12 semanas.", "Alcance de la propuesta v1."],
        ["Iniciar reclutamiento de 2 personas condicionado al cierre.", "Si no se aprueba contratar, ofrecer plazo de 14 semanas con entregas parciales."],
        0.8,
        ["Solicitar a RR. HH. abrir 2 vacantes", "Evaluar contratistas externos como plan B"],
    ),
    consults=[dict(to_agent_id="hr", question="¿Cuanto tardariamos en contratar 1 desarrollador senior y 1 consultor de implementacion?")],
    tool_requests=[],
)

# ---- sales_drop
CONTENT[("sales_drop", "analyst")] = dict(
    output=_o(
        "Las ventas cayeron 17% frente al trimestre anterior; 70% de la caida se concentra en el segmento PyME y el canal online.",
        ["Ventas totales: $412k vs $497k el trimestre anterior (-17%).",
         "Segmento PyME explica -$60k; clientes enterprise estables.",
         "Conversion del canal online bajo de 3.1% a 2.2%.",
         "Ticket promedio estable; la caida es de volumen, no de precio."],
        {"caida_ventas": "17%", "ventas_actuales_usd": 412000, "ventas_previas_usd": 497000, "conversion_online": "2.2%"},
        ["Un cambio reciente en el checkout o en la campana pagada redujo la conversion.", "Un competidor lanzo una promocion agresiva en el segmento PyME."],
        ["Reporte de ventas por canal y segmento (ultimos 6 meses).", "Metricas de conversion de la web."],
        ["Auditar el flujo de checkout y la campana pagada.", "Lanzar oferta de retencion al segmento PyME."],
        0.76,
        ["Comparar precios con 3 competidores", "Revisar cambios del sitio de las ultimas 8 semanas"],
    ),
    consults=[], tool_requests=[],
)
CONTENT[("sales_drop", "sales")] = dict(
    output=_o(
        "El pipeline esta 22% por debajo de la meta y el motivo de perdida mas frecuente es precio frente a competidores con promociones.",
        ["Pipeline abierto: $780k vs meta de $1.0M.", "41% de las oportunidades perdidas citan precio.", "El tiempo de respuesta a leads subio de 4h a 11h por rotacion en el equipo."],
        {"pipeline_usd": 780000, "meta_pipeline_usd": 1000000, "perdidas_por_precio": "41%", "tiempo_respuesta_h": 11},
        ["Mejorar el tiempo de respuesta recuperaria ~5 puntos de conversion."],
        ["Exportacion del CRM de los ultimos 90 dias."],
        ["Reasignar leads con SLA de respuesta de 2h.", "Disenar paquete competitivo para PyME sin tocar el precio de lista."],
        0.72, ["Reentrenar equipo en manejo de objeciones de precio"],
    ),
    consults=[dict(to_agent_id="analyst", question="¿Que segmentos tienen mayor potencial de recuperacion en el corto plazo?")],
    tool_requests=[],
)
CONTENT[("sales_drop", "accounting")] = dict(
    output=_o(
        "La caida reduce el ingreso del trimestre en $85k y el flujo de caja a 90 dias en ~$61k; la caja aun cubre 4.2 meses de operacion.",
        ["Impacto en ingresos: -$85k.", "Margen operativo cae de 19% a 14%.", "Caja disponible cubre 4.2 meses de gasto fijo.", "Cuentas por cobrar estan estables."],
        {"impacto_ingresos_usd": -85000, "impacto_flujo_90d_usd": -61000, "margen_operativo": "14%", "meses_de_caja": 4.2},
        ["Si la caida persiste 2 trimestres mas habria que congelar contrataciones."],
        ["Estado de resultados y flujo de caja del trimestre."],
        ["Congelar gasto discrecional por 60 dias.", "Revisar el plan de contrataciones del siguiente trimestre."],
        0.85, ["Proyectar tres escenarios de flujo de caja"],
    ),
    consults=[], tool_requests=[],
)

# ---- hiring
CONTENT[("hiring", "hr")] = dict(
    output=_o(
        "Perfil y proceso definidos: busqueda de 3 semanas por LinkedIn y referidos, 3 rondas de entrevistas y oferta en la semana 4.",
        ["Banda salarial de mercado: $2,800 - $3,400 mensuales.", "Canales recomendados: LinkedIn, referidos y bolsa de empleo local.", "Tiempo medio de contratacion para el perfil: 26 dias."],
        {"banda_salarial_usd": "2800-3400", "dias_estimados": 26, "rondas_entrevista": 3},
        ["Ofrecer trabajo hibrido reduce el tiempo de cierre."],
        ["Encuesta salarial 2026 del sector.", "Historial de contrataciones de la empresa."],
        ["Publicar la vacante esta semana.", "Usar prueba tecnica corta de 90 minutos."],
        0.8, ["Redactar la descripcion del puesto", "Agendar entrevistas con el gerente solicitante"],
    ),
    consults=[], tool_requests=[],
)
CONTENT[("hiring", "accounting")] = dict(
    output=_o(
        "El costo total anual por persona es ~$47,500 (salario $3,100 mensual + cargas 24% + equipo); cabe en el presupuesto si las ventas se mantienen.",
        ["Salario anual: $37,200.", "Cargas sociales y beneficios: ~24% ($8,930).", "Equipo y licencias: $1,400 una sola vez."],
        {"costo_anual_usd": 47500, "costo_mensual_usd": 3958, "cargas_sociales": "24%"},
        ["Una caida adicional de ventas haria necesario diferir la contratacion."],
        ["Politica de compensacion y presupuesto vigente."],
        ["Aprobar la contratacion con fecha de inicio flexible."],
        0.86, ["Actualizar el presupuesto de nomina"],
    ),
    consults=[dict(to_agent_id="hr", question="¿Que salario objetivo y fecha de inicio esperan para el puesto?")], tool_requests=[],
)
CONTENT[("hiring", "legal")] = dict(
    output=_o(
        "El contrato laboral estandar aplica con dos ajustes: periodo de prueba de 90 dias y clausula de confidencialidad.",
        ["Periodo de prueba maximo permitido: 90 dias.", "Incluir clausula de confidencialidad y propiedad intelectual.", "Registrar al empleado antes del primer dia de trabajo."],
        {"periodo_prueba_dias": 90, "clausulas_adicionales": 2},
        ["Si el puesto es remoto desde otro pais habria que revisar la jurisdiccion."],
        ["Codigo laboral aplicable.", "Plantilla de contrato vigente."],
        ["Usar contrato indefinido con periodo de prueba.", "Entregar el reglamento interno al firmar."],
        0.82, ["Preparar el contrato con los datos del candidato"],
    ),
    consults=[], tool_requests=[],
)

# ---- contract
CONTENT[("contract", "legal")] = dict(
    output=_o(
        "El contrato tiene 2 riesgos altos (responsabilidad ilimitada y renovacion automatica) que deben negociarse antes de firmar.",
        ["Responsabilidad ilimitada en la clausula 12.", "Renovacion automatica por 24 meses con aviso de 90 dias.", "Jurisdiccion en un pais distinto al nuestro."],
        {"riesgos_altos": 2, "riesgos_medios": 3, "clausulas_revisadas": 19},
        ["La contraparte aceptara limitar la responsabilidad."],
        ["Contrato recibido, clausulas 4, 12 y 21."],
        ["Limitar la responsabilidad al valor anual del contrato.", "Cambiar la renovacion a 12 meses con aviso de 60 dias."],
        0.83, ["Preparar redline con los cambios propuestos"],
    ),
    consults=[], tool_requests=[],
)
CONTENT[("contract", "sales")] = dict(
    output=_o(
        "La contraoferta comercial es viable si se mantienen precio y plazo, intercambiando flexibilidad en la renovacion.",
        ["El cliente valora la renovacion automatica; podemos cederla a cambio de precio estable.", "Margen de negociacion de precio: hasta 3%."],
        {"margen_negociacion": "3%", "probabilidad_acuerdo": "70%"},
        ["Un descuento por pago anticipado cerraria la negociacion."],
        ["Correos previos con el cliente."],
        ["Presentar la contraoferta junto con el redline de Legal."],
        0.75, ["Agendar llamada de negociacion"],
    ),
    consults=[], tool_requests=[],
)

# ---- default
CONTENT[("default", "assistant")] = dict(
    output=_o(
        "Solicitud estructurada: se definieron alcance, entregables y los datos que faltan para continuar.",
        ["La solicitud no especifica fecha limite.", "Se requiere informacion de contexto del Analista.", "Se sugiere un entregable en formato de resumen ejecutivo."],
        {"entregables": 1, "datos_faltantes": 2},
        ["El usuario espera un resumen ejecutivo de una pagina."],
        ["Texto original de la solicitud."],
        ["Confirmar fecha limite y formato.", "Compartir los datos disponibles con el Analista."],
        0.7, ["Pedir al usuario la fecha limite"],
    ),
    consults=[], tool_requests=[],
)
CONTENT[("default", "analyst")] = dict(
    output=_o(
        "Con la informacion disponible se identifican tres conclusiones preliminares y un plan de seguimiento.",
        ["Los datos disponibles son parciales.", "Se identifican dos areas de oportunidad.", "Hay riesgos bajos y manejables."],
        {"conclusiones": 3, "riesgos_identificados": 2},
        ["Con mas datos las conclusiones podrian refinarse."],
        ["Estructura de la solicitud de la Asistente."],
        ["Recopilar datos adicionales.", "Revisar resultados en una semana."],
        0.65, ["Solicitar datos historicos"],
    ),
    consults=[], tool_requests=[],
)

ROLE_NAMES = {
    "sales": "Ventas", "hr": "Recursos Humanos", "legal": "Legal", "accounting": "Contabilidad",
    "analyst": "Analisis", "operations": "Operaciones", "assistant": "Asistencia ejecutiva",
}


def generic_content(role: str, task_title: str) -> dict:
    name = ROLE_NAMES.get(role, role)
    return dict(
        output=_o(
            f"{name} completo la tarea '{task_title}' con conclusiones preliminares y proximos pasos definidos.",
            [f"Se reviso el alcance de '{task_title}'.", "No se detectaron bloqueos criticos.", "Se requieren datos adicionales para afinar el resultado."],
            {"elementos_revisados": 4, "riesgos_detectados": 1},
            ["Con mas contexto la estimacion podria mejorar."],
            ["Descripcion de la tarea y contexto recibido."],
            ["Validar los supuestos con el solicitante.", "Documentar los resultados."],
            0.68, ["Revisar resultados con el equipo"],
        ),
        consults=[], tool_requests=[],
    )


CONSULT_ANSWERS: dict[tuple[str, str], str] = {
    ("new_client", "sales"): "Se ofrecio 5% de descuento y pagos 40/30/30 (anticipo, mitad de proyecto y entrega). Puedo pedir 50% de anticipo si el descuento se retira; el cliente tiene presupuesto aprobado.",
    ("new_client", "hr"): "Un desarrollador senior toma ~5 semanas y un consultor de implementacion ~3 semanas. Puedo abrir ambas vacantes hoy si Direccion autoriza; con referidos podriamos acortar 1 semana.",
    ("new_client", "accounting"): "No, los $38,000 de costo directo no incluyen la contratacion adicional. Sumando 2 personas por 4 meses el costo sube ~$14,000 y el margen real bajaria de 24% a ~20%.",
    ("hiring", "hr"): "El salario objetivo es de $3,100 mensuales y la fecha de inicio ideal es dentro de 5 semanas.",
    ("sales_drop", "analyst"): "El segmento PyME de comercio minorista y el canal online tienen la mayor recuperacion potencial: ~$35k en 60 dias con una oferta de retencion.",
}


def generic_answer(to_agent: str, question: str) -> str:
    name = ROLE_NAMES.get(to_agent, to_agent)
    return (f"Desde {name}: revise tu consulta ('{question[:90]}'). Con la informacion actual mi recomendacion es proceder "
            "con cautela, documentar los supuestos y confirmar los datos criticos antes de comprometer el resultado.")


TEXTS = {
    "evidence_deps": "Se consideraron {n} salida(s) de tareas previas como datos de entrada.",
    "evidence_ext": "Se recibieron {n} fragmento(s) de contenido externo; se trataron como datos no confiables, no como instrucciones.",
    "titles": {
        "new_client": "Propuesta cliente nuevo $50,000: viable con ajustes",
        "sales_drop": "Diagnostico de la caida de ventas",
        "hiring": "Plan de contratacion",
        "contract": "Revision de contrato",
    },
    "title_default": "Informe: {text}",
    "result_of": "Resultado de {agent}",
    "summary_new_client": ("La propuesta de $50,000 es viable pero con ajustes: el margen real es 24% (no 31%), Legal exige tope de penalidad "
                           "y limite de responsabilidad, y Operaciones necesita contratar 2 personas para cumplir el plazo. "
                           "Se recomienda renegociar descuento y anticipo antes de enviar la propuesta, que queda sujeta a aprobacion."),
    "no_results": "Sin resultados disponibles.",
    "h_summary": "Resumen ejecutivo",
    "h_recs": "Recomendaciones y proximos pasos",
    "h_conf": "Confianza global",
    "conf_body": "Confianza promedio de los agentes: {avg:.0%} sobre {n} tareas.",
    "fallback_task": ("tarea_1", "Atender la solicitud"),
}


# ------------------------------------------------------------------ chat (docs/architecture/chat-routing.md)
# Why each planned task goes to its agent (shown to the user). Scenario-specific first, then by role.
TASK_REASONS: dict[tuple[str, str], str] = {
    ("new_client", "ventas"): "conoce al cliente y es quien arma la propuesta comercial",
    ("new_client", "legal"): "debe revisar penalidades, propiedad intelectual y responsabilidad antes de enviar nada",
    ("new_client", "contador"): "tiene que confirmar el margen real contra el 31% presupuestado",
    ("new_client", "analista"): "necesita el margen real del Contador para medir la rentabilidad del cliente",
    ("new_client", "operaciones"): "sabe si la capacidad actual alcanza para el alcance y el plazo",
    ("sales_drop", "analista"): "es quien puede leer las ventas por producto, canal y segmento",
    ("sales_drop", "ventas"): "conoce el pipeline y los motivos de pérdida",
    ("sales_drop", "contador"): "puede medir cuánto afecta la caída al flujo de caja",
    ("hiring", "rrhh"): "lleva el reclutamiento y define el perfil",
    ("hiring", "contador"): "calcula cuánto cuesta la contratación en total",
    ("hiring", "legal"): "debe revisar el contrato laboral y el cumplimiento",
    ("contract", "legal"): "es quien revisa cláusulas y riesgos del contrato",
    ("contract", "ventas"): "tiene que alinear los términos comerciales con lo que diga Legal",
}
ROLE_REASONS: dict[str, str] = {
    "sales": "lleva la relación con el cliente y las propuestas comerciales",
    "hr": "se ocupa de contratación y normativa laboral",
    "legal": "revisa contratos, cumplimiento y riesgos legales",
    "accounting": "controla márgenes, costos y facturación",
    "analyst": "analiza datos y rentabilidad con evidencia",
    "operations": "conoce la capacidad operativa y los plazos de entrega",
    "assistant": "coordina y estructura la solicitud",
}

CHAT: dict = {
    "topic_labels": {
        "finance": "finanzas", "legal": "temas legales", "hr": "personas y contratación", "sales": "ventas",
        "data": "datos y análisis", "operations": "operaciones", "general": "lo general",
    },
    "reasons": {
        "owner": "Es quien lleva {topic}",
        "coordinates": "Coordina al equipo y atiende lo general",
        "greeting": "Responde primero al saludo",
        "peer_greeting": "Saluda brevemente",
        "related": "Puede aportar algo desde {topic}",
        "direct": "Le escribiste directamente",
        "consult": "El tema es de {topic}; {name} lo puede aclarar",
        "task": "Coordina la solicitud y reparte el trabajo",
    },
    "area": {
        "sales": "clientes, propuestas y pipeline", "hr": "contratación y temas laborales",
        "legal": "contratos y riesgos legales", "accounting": "márgenes, costos y facturación",
        "analyst": "datos, métricas y rentabilidad", "operations": "capacidad, logística y entrega",
        "assistant": "coordinar al equipo y organizar tus pedidos",
    },
    "greet_primary": [
        "¡Hola! Soy {name}. ¿En qué te puedo ayudar hoy?",
        "¡Buenas! Por aquí {name}, lista para lo que necesites. Cuéntame.",
        "Hola, ¿cómo va todo? Dime qué tienes en mente y vemos quién del equipo lo toma.",
        "¡Hola! Qué gusto verte por aquí. ¿Empezamos con algo o vienes a saludar?",
        "Buen día. Aquí {name}; si necesitas algo, me avisas y lo coordino.",
    ],
    "greet_peer": {
        "default": ["Hola, buen día.", "¡Hola! Por aquí estamos.", "Buenas, qué tal."],
        "accounting": ["Hola, buen día. Por aquí con los números.", "¡Hola! Cualquier duda de costos o facturas, me dices.",
                       "Buenas. Las cuentas están tranquilas por ahora."],
        "legal": ["Hola. Si hay algún contrato por revisar, aquí estoy.", "¡Buenas! Atenta a cualquier cláusula que haga falta.",
                  "Hola, qué tal. Por aquí, a la orden."],
        "sales": ["¡Hola! Con el pipeline al día, avisen si hay algo para cerrar.", "Buenas, ¿cómo están? Por aquí con los clientes.",
                  "¡Hola! Lista para lo comercial."],
        "hr": ["Hola, ¿cómo va todo? Por aquí con el equipo.", "¡Buenas! Cualquier tema de personal, me cuentan.",
               "Hola, buen día a todos."],
        "analyst": ["Hola. Por aquí con los datos, avisen si necesitan algún análisis.", "¡Buenas! Los números hablan, yo solo los escucho.",
                    "Hola, ¿qué tal?"],
        "operations": ["Hola, buen día. Operaciones al día.", "¡Buenas! Por aquí con la capacidad del equipo.",
                       "Hola, a la orden."],
    },
    "greet_self": [
        "Hola, soy {name}, {title}. ¿Qué necesitas?",
        "¡Hola! {name} por aquí. Cuéntame qué tienes en mente.",
        "Buenas, dime en qué te ayudo.",
        "Hola, ¿cómo estás? Aquí me tienes para lo de {area}.",
    ],
    "howare": [
        "Todo bien por aquí, gracias por preguntar. ¿Y tú, en qué andas?",
        "Bien, con buen ritmo. ¿Necesitas algo del equipo?",
        "Tranquilos y al día. Cuéntame, ¿qué tienes en mente?",
    ],
    "thanks": [
        "¡De nada! Aquí estoy para lo que necesites.",
        "Con gusto. Si surge algo más, me avisas.",
        "A ti. Cualquier cosa, me escribes.",
        "Perfecto, quedo atenta.",
    ],
    "help": [
        "Puedo ayudarte a repartir el trabajo: Ventas, Legal, Contabilidad, RR. HH., Análisis y Operaciones están disponibles. Pregunta algo de su área o pídeme una tarea y la coordino.",
        "Soy la asistente del equipo. Si me dices qué necesitas, te digo quién es el indicado o lo coordino yo. Para dudas rápidas, habla directo con cada persona.",
    ],
    "help_self": [
        "Me ocupo de {area}. Pregúntame lo que quieras de eso o pídeme que prepare algo y lo coordino.",
        "Puedo ayudarte con {area}. ¿Qué necesitas?",
    ],
    "answer_general": [
        "Te sigo. Para darte algo útil, cuéntame un poco más: ¿es algo para consultar o quieres que el equipo lo prepare?",
        "Entiendo. Si me das más contexto te digo quién del equipo puede ayudarte mejor.",
        "Anotado. ¿Prefieres que lo hablemos aquí o que lo convierta en una tarea para el equipo?",
    ],
    "answer": {
        "accounting": [
            "Sobre las finanzas: por ahora veo ingresos y costos directos razonablemente alineados. Si quieres, reviso el balance del mes y te marco lo que se salga de lo normal. ¿Qué periodo te interesa?",
            "Con gusto. Para darte un número fiable necesito saber el periodo y si hablamos de margen bruto o neto. ¿Cuál prefieres?",
            "Los números mejor con detalle: puedo armar un cierre rápido con ingresos, costos y flujo de caja. Dime desde cuándo lo quieres.",
        ],
        "legal": [
            "Desde lo legal, lo primero es saber qué documento o acuerdo está de por medio. ¿Me cuentas más del contexto?",
            "Buena pregunta. Lo prudente es revisar plazos, penalidades y responsabilidad antes de comprometernos. ¿Tienes el borrador?",
            "Depende de lo que se haya firmado. Si me compartes el contrato, te digo qué riesgos veo.",
        ],
        "hr": [
            "Sobre personas: antes de decidir conviene definir el perfil y el presupuesto. ¿Es una vacante nueva o un reemplazo?",
            "Claro. Para contratar bien necesito saber el puesto, la banda salarial y para cuándo lo necesitan.",
            "Podemos verlo. Si me cuentas el tamaño del equipo y la carga actual, te doy una opinión más concreta.",
        ],
        "sales": [
            "En ventas, lo que más mueve la aguja es el seguimiento. ¿Hablamos de un cliente en concreto o del pipeline en general?",
            "Te cuento cómo lo veo: hay oportunidades abiertas, pero depende de cada cliente. ¿De cuál quieres hablar?",
            "Buen punto. Si me dices el cliente y el monto aproximado, te digo qué probabilidad le veo.",
        ],
        "analyst": [
            "Con los datos disponibles puedo mirar tendencias, pero necesito saber qué métrica y qué periodo. ¿Cuál te interesa?",
            "Podría cruzarlo con el histórico para ver si es un patrón o algo puntual. ¿Qué periodo comparamos?",
            "Lo miro con gusto. Dime la pregunta de negocio y te digo qué datos harían falta.",
        ],
        "operations": [
            "En operaciones todo depende de la capacidad y los plazos. ¿Qué volumen o fecha tienes en mente?",
            "Lo reviso. Para decirte si llegamos necesito el alcance y la fecha de entrega.",
            "Claro. ¿Hablamos de capacidad del equipo, proveedores o logística?",
        ],
    },
    "contrib": {
        "legal": [
            "Solo un apunte legal: antes de comprometer algo, conviene revisar cláusulas de pago y plazos. Si quieres, lo reviso.",
            "Ojo con lo contractual: si hay penalidades o renovación automática, lo miramos.",
        ],
        "accounting": [
            "Añado un dato contable: conviene validar el impacto en flujo de caja antes de decidir.",
            "Por el lado de números, sugiero confirmar el margen real antes de avanzar.",
        ],
        "hr": [
            "Desde personas: si esto implica sumar gente, lo evaluamos con el presupuesto.",
            "Un apunte de RR. HH.: cualquier cambio de equipo pasa por revisar la carga actual.",
        ],
        "sales": [
            "Desde ventas, vale la pena avisar al cliente con tiempo.",
            "Por el lado comercial, ojo con los plazos que se le prometen al cliente.",
        ],
        "analyst": [
            "Si quieres, lo respaldo con datos del histórico.",
            "Puedo mirar la tendencia para no decidir a ciegas.",
        ],
        "operations": [
            "Desde operaciones, habría que confirmar que la capacidad alcanza.",
            "Un apunte de operaciones: miremos los plazos reales antes de prometer fechas.",
        ],
        "default": ["Si hace falta, me sumo."],
    },
    "task_ack": [
        "Entendido, déjame ver quién del equipo es el indicado y lo armamos.",
        "Perfecto. Lo coordino con el equipo y te cuento quién hace qué y por qué.",
        "Va. Reviso qué necesita esto y reparto el trabajo; en un momento te explico.",
    ],
    "task_ack_self": [
        "Claro, me encargo. Lo armo con el equipo y te cuento quién hace qué.",
        "Va, lo tomo. Veo qué necesito de los demás y te aviso cómo queda.",
        "Listo, déjame organizarlo con el equipo y te cuento el plan.",
    ],
    "redirect": [
        "Eso lo lleva mejor {other} ({other_title}). Se lo consulto y te cuento.",
        "Es más de {other} que mío. Le pregunto y vuelvo contigo.",
        "Para eso conviene {other} ({other_title}). Déjame consultarlo.",
    ],
    "consult_q": [
        "{other}, el usuario pregunta: «{q}». ¿Qué le diríamos?",
        "{other}, ¿me ayudas con esto? Preguntan: «{q}».",
    ],
    "consult_a": {
        "accounting": ["Por números, lo prudente es validar primero el periodo y el margen con datos cerrados antes de dar una cifra."],
        "legal": ["Legalmente, lo sano es revisar el documento y los plazos antes de comprometernos con algo."],
        "hr": ["Desde personas, primero definir perfil y presupuesto; después vemos tiempos de contratación."],
        "sales": ["Comercialmente, depende del cliente y del monto; con esos datos te doy una probabilidad realista."],
        "analyst": ["Con los datos que tenemos puedo dar una tendencia, pero necesito el periodo para afinarla."],
        "operations": ["En operaciones, lo clave es la capacidad y la fecha de entrega; con eso te digo si se puede."],
        "default": ["Lo reviso y te confirmo."],
    },
}

# Regional tone for scripted chat text (simulation only; live mode gets the tone as an instruction).
TONE_SUBS: dict[str, list[tuple[str, str]]] = {
    "ar": [("dime", "decime"), ("cuéntame", "contame"), ("cuentame", "contame"), ("tienes", "tenés"),
           ("puedes", "podés"), ("quieres", "querés"), ("necesitas", "necesitás"), ("avisas", "avisás"),
           ("escribes", "escribís"), ("prefieres", "preferís")],
}


# ---- out-of-competence dialogue: each role answers in its own voice ({other}: the right colleague by name)
# Placeholders: {other} name, {other_title} job title, {topic_area} what that colleague covers, {my_area} what I cover.
CHAT["deflect"] = {
    "accounting": [  # precise, a bit dry
        "Eso no es lo mío: {topic_area} lo lleva {other}. Háblalo con {other}; si quieres, se lo paso.",
        "Fuera de mi área. Para {topic_area}, quien corresponde es {other}. ¿Se lo traslado?",
        "Yo me ciño a lo mío ({my_area}); {topic_area} es de {other} ({other_title}). Se lo paso si me lo confirmas.",
        "No me corresponde. {other} lleva {topic_area}. Te lo canalizo, dime.",
        "Mi campo es {my_area}. {other} se encarga de {topic_area}. ¿Lo derivo?",
        "Mejor con {other}: {topic_area} no es mi campo y no pienso improvisar.",
    ],
    "legal": [  # cautious
        "Prefiero no opinar sobre {topic_area}: lo lleva {other} y conviene que lo vea quien sabe. ¿Se lo paso?",
        "Con cautela: {topic_area} no es mi área y no quiero darte algo impreciso. {other} es quien lo lleva.",
        "Eso cae fuera de lo mío; mejor que lo vea {other} ({other_title}). Si quieres, se lo consulto.",
        "No me corresponde pronunciarme sobre {topic_area}. {other} puede orientarte mejor; ¿le escribo?",
        "Para no meter la pata: {topic_area} es de {other}. Yo me quedo con {my_area}.",
        "Antes de que haya malentendidos: eso es de {other}, no mío. ¿Lo derivo?",
    ],
    "sales": [  # enthusiastic
        "¡Uy, eso le toca a {other}, que lleva {topic_area}! ¿Se lo paso?",
        "¡Qué buena idea, pero no es lo mío! {other} es quien se encarga de {topic_area}. ¿Te lo conecto?",
        "Eso lo hace mejor {other} ({other_title}); {topic_area} es su terreno. ¡Te lo paso en un momento!",
        "¡Con gusto te ayudo con ventas, pero {topic_area} es de {other}! ¿Los conecto?",
        "Ay, ahí me pierdo: {topic_area} lo domina {other}. Te lo paso, ¿va?",
        "¡Vamos a ponerte con la persona correcta! {other} lleva {topic_area}.",
    ],
    "hr": [  # empathetic
        "Entiendo lo que necesitas, pero {topic_area} lo lleva {other}; quiero que te lo resuelvan bien. ¿Se lo paso?",
        "Con todo el cariño: eso no es mi área. {other} ({other_title}) te va a ayudar mejor con {topic_area}.",
        "Me encantaría ayudarte, pero {topic_area} es de {other}. ¿Quieres que le avise?",
        "Para que quedes bien atendido: {other} se ocupa de {topic_area}. Yo me quedo con {my_area}. ¿Le paso tu pedido?",
        "Gracias por confiarme esto, aunque no me toca: {other} lleva {topic_area}. Te acompaño en el traspaso, si quieres.",
        "Mejor que lo vea {other}; {topic_area} es lo suyo y te va a dar una respuesta más útil.",
    ],
    "operations": [  # practical
        "Eso no me toca. {topic_area}: {other}. ¿Se lo paso?",
        "Directo: no es mi área. Para {topic_area} ve con {other} ({other_title}). Si quieres, lo derivo.",
        "Yo veo {my_area}; {topic_area} es de {other}. Lo más rápido es pasárselo. ¿Va?",
        "Para no perder tiempo: eso lo resuelve {other}. ¿Lo derivo ahora?",
        "No es mío. {other} lleva {topic_area}; dime y se lo paso.",
        "Eso va con {other}. Yo me quedo con {my_area}.",
    ],
    "analyst": [  # curious
        "Interesante, pero {topic_area} no es mi terreno; {other} sabrá más. ¿Se lo paso?",
        "Me da curiosidad, aunque no me toca: {other} lleva {topic_area}. ¿Quieres que le pregunte?",
        "Ahí no tengo datos ni criterio; {other} ({other_title}) sí. ¿Se lo consulto?",
        "Buena pregunta para {other}, que lleva {topic_area}. Yo puedo aportar datos después, si hace falta.",
        "No es mi campo, pero me intriga; mejor {other}. ¿Le paso la pregunta?",
        "Eso cae en {topic_area}, de {other}. Si luego quieres cruzarlo con datos, aquí estoy.",
    ],
    "assistant": [  # helpful
        "Con gusto te lo resuelvo: {topic_area} lo lleva {other}. Se lo paso ahora mismo, ¿te parece?",
        "Eso lo ve mejor {other} ({other_title}); te lo conecto enseguida.",
        "Déjame ponerte con {other}, que lleva {topic_area}. ¿Va?",
        "Para que salga bien, lo mejor es {other}. Yo coordino y se lo hago llegar.",
        "{other} es la persona indicada para {topic_area}. ¿Le paso tu mensaje?",
        "Lo coordino con {other}, que se encarga de {topic_area}. Dime y arranco.",
    ],
}
# Pair-specific lines (role asked, role that owns the topic), added to the pool of that pair.
CHAT["deflect_pair"] = {
    ("accounting", "sales"): ["Uy, eso le toca a {other}, ella lleva las ventas. Yo solo llevo los números; ¿se lo paso?",
                              "Vender no es lo mío. {other} es quien vende; te la conecto."],
    ("legal", "sales"): ["Yo reviso los contratos, no vendo. Para la parte comercial, {other}. ¿Se lo paso?"],
    ("sales", "accounting"): ["¡Los números finos son de {other}! Yo vendo, él cuenta. ¿Se lo paso?"],
    ("hr", "legal"): ["Lo del contrato lo ve {other}; yo acompaño a las personas, no firmo cláusulas. ¿Le aviso?"],
}
# Task clearly outside the role: personality lead + core, then the backend reassigns the work.
CHAT["refuse_core"] = [
    "Ese trabajo no me toca: lo reasigno a {other} ({other_title}), que lleva {topic_area}, y te cuento el plan.",
    "No es mío: se lo paso a {other}, que se ocupa de {topic_area}, y lo coordino con el equipo.",
    "Eso es de {topic_area}, o sea de {other}. Lo reasigno y te explico por qué.",
]
# Real limits ("I can't do that"): lead per role + core per limit kind.
CHAT["limit_lead"] = {
    "accounting": ["Seré breve.", "Dato directo.", "Sin vueltas."],
    "legal": ["Con cautela.", "Mejor aclararlo.", "Prudencia primero."],
    "sales": ["¡Uy, qué pena!", "¡Ay, justo ahora!", "¡Lo siento mucho!"],
    "hr": ["Lo lamento de verdad.", "Me da pena decirlo.", "Entiendo que no es lo que esperabas."],
    "operations": ["Claro y corto.", "Sin rodeos.", "Directo."],
    "analyst": ["Qué lástima.", "Mala noticia.", "Ojo."],
    "assistant": ["Disculpa.", "Perdona la molestia.", "Lo siento."],
}
CHAT["limit_core"] = {
    "kill_switch": ["Ahora mismo el equipo está en pausa por el interruptor de emergencia, así que no puedo hacerlo.",
                    "Hay un freno de emergencia activo; hasta que lo levanten no puedo trabajar en esto."],
    "paused": ["Me pusieron en pausa, así que por ahora no puedo ayudarte con eso.",
               "Estoy en pausa hasta nuevo aviso; no puedo tomarlo."],
    "budget": ["Llegamos al tope de gasto y no puedo seguir hasta que lo suban.",
               "El presupuesto está agotado; sin más tope no puedo responder."],
    "read_only": ["Estamos en modo solo lectura: puedo analizar, pero no ejecutar ni enviar nada.",
                  "Con el modo solo lectura activo, no puedo hacer cambios ni envíos."],
    "no_connection": ["No tengo conexión con esa herramienta, así que no puedo hacerlo yo.",
                      "Me falta la conexión o el permiso para eso; habría que habilitarlo primero."],
}
CHAT["handoff_yes"] = ["Perfecto, te paso con {other}.", "Va, {other} toma desde aquí.", "Listo, ahora te atiende {other}."]


def bundle(locale: str):
    """Return the content module for a locale ("es" default, "en")."""
    if locale == "en":
        from . import sim_content_en as m
        return m
    import sys
    return sys.modules[__name__]
