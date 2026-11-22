# F8: probar una habilidad con un caso antes de activarla

Plan corto. Detalle final en `docs/architecture/skills.md` (sección "Probar una habilidad").

## Decisión central

La prueba **no pasa por el Tool Gateway**: el probador (`skills.Tester`) llama al runtime con `Tools: []` y atiende él
mismo una única petición permitida, `skill.load` de la habilidad probada (texto de la versión probada, sea borrador,
en revisión o activa). Cualquier otra petición de herramienta del agente se descarta y se anota como "bloqueada" (solo
`herramienta.acción`, nunca argumentos). Así "sin efectos" se cumple por construcción en Go: no existe ruta de código
desde la prueba hasta un conector, el drive o una aprobación. No se usa el ensayo (B9) porque este ejecuta lecturas
reales y registra "habría hecho"; aquí no hace falta nada de eso y la garantía es más fuerte.

## Pasos (TDD, commits pequeños `feat(skills): ...`)

1. Migración 829 (`skill_test_cases`, `skill_test_runs`, RLS) + bajada + tienda en memoria y Postgres.
2. `skills.Tester`: casos (límites), estimación, ejecución sin efectos, comparación lado a lado con la versión activa,
   historial. Presupuesto vía orquestador (`ReserveAgentCall` / `RecordAgentCall`, con tope por agente y organización).
3. API sección 53 de `08-api.md`: casos CRUD, candidatos, estimación, ejecución, historial. Permisos: leer = `skills:read`,
   guardar casos = `skills:write`, probar (gasta presupuesto) = `skills:review`; departamento privado sellado; el agente
   debe ser usable por la persona.
4. Pruebas Go: sin efectos, presupuesto, borrador sin activar, permisos, sellado, aislamiento de organizaciones.
5. Frontend: panel "Probar con un caso" en la revisión, etiqueta "Sin probar / Probada el …", i18n es/en/pt.
6. Documentación.
