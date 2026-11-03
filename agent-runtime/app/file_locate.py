"""How an agent finds the files a person mentions and turns invoices into exact numbers.

Spec: docs/superpowers/specs/2026-11-03-agentes-ubican-archivos-y-accesos-design.md (parts 1 and 2). The backend does
the work (fs.recent lists what the person uploaded or moved, filtered by the agent's folder access; fs.income_statement
adds up in code); these lines only tell the model the order to look in, when to confirm, what to do without access and
that it never adds up invoices itself. Kept apart from security.py so it merges cleanly.
"""

from __future__ import annotations

import re

# Mentions of files without a path: "the invoices of the month", "el contrato que subi ayer", "los PDF".
_MENTIONS_FILES = re.compile(
    r"\b(archivo|archivos|file|files|arquivo|arquivos|documento|documentos|document|documents|pdf|pdfs|excel|xlsx|"
    r"factura|facturas|invoice|invoices|nota fiscal|notas fiscais|contrato|contratos|contract|contracts|recibo|recibos|"
    r"receipt|receipts|extracto|statement|subi|subio|subido|subidos|subidas|uploaded|puse|deje|carpeta|folder|pasta|"
    r"adjunto|adjuntos|attachment|attachments)\b")

_INVOICES_TO_PNL = re.compile(
    r"\b(factura|facturas|invoice|invoices|nota fiscal|notas fiscais|cfdi|comprobante|comprobantes)\b.*\b("
    r"estado de resultados|estado de perdidas|perdidas y ganancias|p&l|pnl|income statement|profit and loss|"
    r"resultado|resultados|utilidad|utilidades|ganancia|ganancias|demonstracao do resultado|dre|lucro)\b"
    r"|\b(estado de resultados|income statement|profit and loss|p&l|demonstracao do resultado)\b")

DEFAULT_RECENT_DAYS = 14

LOCATE_RULES = (
    "ENCONTRAR LOS ARCHIVOS QUE LA PERSONA MENCIONA (sin ruta, p. ej. \"las facturas del mes\", \"el contrato que subi ayer\"): "
    "busca en este orden y para en cuanto el conjunto este claro: 1) lo adjuntado en el mensaje o en las entradas; "
    f"2) recent {{days?, kind?, path?}}: lo que ESA persona subio, cambio o movio en los ultimos {DEFAULT_RECENT_DAYS} dias, "
    "agrupado por carpeta (kind: pdf | sheet | doc | image | text; amplia days si hace falta); 3) search por palabras y por "
    "significado en las carpetas que puedes ver.",
    "- CONFIRMA SOLO SI HAY DUDA: si el conjunto es claro (p. ej. 23 PDF subidos hoy en una sola carpeta) sigue sin "
    "preguntar. Si hay ambiguedad (dos meses mezclados, dos carpetas candidatas, muy pocos o demasiados archivos para lo "
    "pedido) confirma en UNA linea antes de gastar, p. ej. \"Encontre 23 PDF en /Shared/Facturas/Octubre, subidos hoy. "
    "Uso esos?\": dentro de una tarea, si tienes help_request disponible, usalo (kind=file, options con las carpetas "
    "candidatas); si no, deja la pregunta en summary y no sigas con archivos dudosos.",
    "- SIN ACCESO: si recent dice \"no access\" o read/list/search responde not_granted, NO lo saltes en silencio: di en "
    "que carpeta esta y que no la puedes ver, y ofrece \"Darle acceso a esta carpeta\" (para quien puede darlo: un "
    "administrador o el responsable de tu equipo, en tu panel > Carpetas que puede ver) o \"Pedir acceso\" (para quien "
    "no). Nunca intentes llegar a esa carpeta por otro camino.",
)

PNL_RULES = (
    "FACTURAS -> ESTADO DE RESULTADOS (numeros exactos): NUNCA sumes, restes ni conviertas importes tu mismo. 1) Lee "
    "cada factura (read; una por una o por paginas) y pasala a UNA fila: {file: ruta, page, date: AAAA-MM-DD, number, "
    "issuer, issuer_tax_id, receiver, receiver_tax_id, concept, category (cuenta sugerida: Ventas, Costo de ventas, "
    "Renta, Servicios...), currency, subtotal, taxes, total, type: income|expense} copiando los importes TAL COMO "
    "aparecen. 2) Si una factura no tiene texto (escaneada) y no la pudiste ver como imagen, agregala igual con "
    "status:\"scanned\"; si no se lee, status:\"unreadable\" y reason. Nunca inventes ni completes un importe. Si dudas "
    "de algo, ponlo en doubt.",
    "- 3) Pide income_statement {path: .../Estado de resultados <periodo>.xlsx, company: {name, tax_id} (la empresa para "
    "la que trabajas: emitidas = ingreso, recibidas = gasto), currency, rates? ({\"USD\": 18.2} solo si la persona te dio "
    "el tipo de cambio), invoices: [filas], locale}. El backend calcula todo, revisa duplicados (numero + emisor), "
    "totales que no cuadran, monedas sin tipo de cambio y escaneadas, y escribe el Excel con Detalle, Estado de "
    "resultados y Revisar. Si no sabes el nombre o RFC/NIF de la empresa, preguntalo.",
    "- 4) En summary di cuantas facturas se leyeron, el resultado (utilidad antes de impuestos) y cuantas hay que "
    "revisar, con las cifras EXACTAS que devolvio el backend, y la ruta del Excel.",
)


def wants_files(*texts: str | None) -> bool:
    from .routing import norm  # lazy: routing imports models

    return bool(_MENTIONS_FILES.search(norm(" ".join(t for t in texts if t))))


def wants_income_statement(*texts: str | None) -> bool:
    from .routing import norm

    return bool(_INVOICES_TO_PNL.search(norm(" ".join(t for t in texts if t))))


def locate_lines(ws, request_text: str | None = None, task_text: str | None = None, trim: bool = True) -> list[str]:
    """The finding rules when the task talks about files (always with trim off); the invoice rules when it asks
    for an income statement from invoices and the agent can write."""
    lines: list[str] = []
    if not trim or wants_files(request_text, task_text):
        lines += [*LOCATE_RULES]
    if getattr(ws, "can_write", False) and (not trim or wants_income_statement(request_text, task_text)):
        lines += [*PNL_RULES]
    return lines
