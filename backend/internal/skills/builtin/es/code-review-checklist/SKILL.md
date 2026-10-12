---
name: code-review-checklist
description: "Genera una lista de revisión de código: corrección, pruebas, seguridad, legibilidad y rendimiento, adaptada al cambio. Úsala al revisar un pull request antes de aprobarlo."
metadata:
  category: engineering
  builtin: "true"
---
# Lista de revisión de código

## Cuándo usarla
Para revisar un cambio de código de forma ordenada antes de aprobarlo.

## Pasos
1. Resume el cambio en una frase y verifica que coincide con su descripción.
2. Revisa la corrección: casos límite, errores controlados y efectos secundarios.
3. Revisa las pruebas, la seguridad (entradas, permisos, secretos) y el rendimiento evidente.
4. Revisa la legibilidad y la coherencia con el estilo del proyecto, marcando cada punto como bloqueante o sugerencia.

## Criterios de calidad
- Cada comentario indica el archivo o la línea y explica el porqué.
- Los bloqueos se distinguen claramente de las mejoras opcionales.
- No se pide reescribir código que cumple los requisitos sin una razón técnica.

## Límites
Deja los comentarios como borrador para que la persona revisora los publique. No apruebes ni fusiones cambios por tu cuenta.
