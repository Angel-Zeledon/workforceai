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


def bundle(locale: str):
    """Return the content module for a locale ("es" default, "en")."""
    if locale == "en":
        from . import sim_content_en as m
        return m
    import sys
    return sys.modules[__name__]
