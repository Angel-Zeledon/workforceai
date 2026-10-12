---
name: api-documentation
description: "Documenta un endpoint o una API: propósito, parámetros, respuestas, errores y un ejemplo de uso con datos ficticios. Úsala al publicar o actualizar documentación para desarrolladores."
metadata:
  category: engineering
  builtin: "true"
---
# Documentación de API

## Cuándo usarla
Para explicar a otros desarrolladores cómo usar un endpoint o un conjunto de operaciones.

## Pasos
1. Describe el propósito del endpoint y cuándo conviene usarlo.
2. Tabula los parámetros: tipo, si son obligatorios y valores permitidos.
3. Documenta las respuestas de éxito y los códigos de error con su causa.
4. Añade un ejemplo de petición y de respuesta con datos ficticios.

## Criterios de calidad
- Nombres, tipos y rutas coinciden exactamente con la especificación de la tarea.
- Cada error indica cómo resolverlo desde el lado del cliente.
- Los ejemplos usan datos ficticios y no incluyen credenciales reales.

## Límites
Deja la documentación como borrador para revisión del equipo de API. No inventes campos, límites ni comportamientos que no estén especificados.
