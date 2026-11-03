---
name: business-continuity-plan
description: "Elabora un plan de continuidad del negocio: procesos críticos, tiempos máximos de recuperación, alternativas y responsables. Úsala para preparar la operación ante cortes, desastres o fallas de proveedores."
metadata:
  category: operations
  builtin: "true"
---

# Plan de continuidad del negocio

## Cuándo usarla
Para preparar la operación frente a interrupciones graves, como caídas de sistemas, pérdida de instalaciones, falla de un proveedor único o ausencia de personal clave, y saber qué hacer, en qué orden y quién lo hace.

## Pasos
1. Lista los procesos del negocio y prioriza los críticos según el impacto de su interrupción en clientes, ingresos, obligaciones legales y seguridad.
2. Para cada proceso crítico define el tiempo objetivo de recuperación (RTO), es decir, cuánto puede estar detenido, y la pérdida máxima de datos aceptable (RPO).
3. Identifica las amenazas plausibles, como falla de sistemas, proveedor único, instalaciones, personal clave o incidentes de seguridad, y estima su probabilidad.
4. Define una alternativa para cada proceso crítico: procedimiento manual, proveedor alterno, sitio o equipo de respaldo, o personal cruzado capacitado.
5. Asigna un responsable y un suplente por proceso, identificados por cargo. No incluyas datos personales en el documento.
6. Establece cómo y con qué frecuencia se prueba el plan, y entrega el documento (usa `templates/plan_continuidad.md`).

## Criterios de calidad
- Cada proceso crítico tiene RTO, RPO, alternativa y responsable con suplente.
- Las alternativas son realistas con los recursos disponibles y están descritas paso a paso.
- El plan indica cuándo y cómo se prueba.
- Las dependencias de proveedores únicos aparecen como riesgo explícito.

## Límites
Es un borrador para validar con la dirección y con los dueños de cada proceso. No contrata proveedores, no compra equipos, no activa recursos de respaldo y no asigna presupuesto. Los tiempos y alternativas que no estén en la información proporcionada se marcan como por confirmar.
