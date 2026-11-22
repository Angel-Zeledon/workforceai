# F3. Guardar un departamento como plantilla y llevarlo a otra empresa: plan

Fecha: 2026-11-21. Rama aislada; migración reservada **826**; sección **50** de `08-api.md`.

## Objetivo

Un contador o consultor monta su oficina tipo y la replica en cada cliente: exporta un departamento (o la empresa) a
un archivo JSON versionado y lo importa en otra organización con la misma vista previa e instalación de los equipos
listos (F2). La confianza no viaja: los agentes empiezan en periodo de prueba y las habilidades esperan revisión.

## Diseño

- **Formato** `aiw.office-template`, `format_version: 1`. Es el modelo de un equipo F2 (`agents[] {role, does}`,
  `department`, `name`, `for`, `solves`) extendido con lo propio: por persona nombre, instrucciones, creatividad, tono,
  responsabilidades, etiquetas y habilidades asignadas; habilidades con `SKILL.md` y archivos de texto de la versión
  activa; plantillas de trabajo propias (G2) con los agentes de sus pasos escritos como **rol**; programaciones;
  objetivos activos; departamentos por nombre.
- **Lista blanca**: el archivo se construye campo a campo desde tipos propios (`internal/officetemplates`), nunca
  copiando estructuras internas. Nunca salen ids, conexiones, credenciales, memoria, drive, personas, clientes,
  auditoría, chats, autonomía ni rutas. Textos libres con forma de secreto o correo se omiten al exportar.
- **Importar** = validación estricta (`DisallowUnknownFields`, tamaño máximo 3 MB, topes por lista, rol existente,
  tono y creatividad válidos, referencias internas coherentes, textos por `CleanInstructions` y
  `sanitize.ScoreInjection`, habilidades por `skills.Build`) y se guarda en la organización (`office_templates`,
  migración 826, RLS) sin duplicar el mismo contenido. Aparece en "Equipos → Tus plantillas".
- **Vista previa e instalación**: el mismo `application.PreviewTeam` de F2 (ahora admite el mismo rol dos veces en un
  departamento) por cada departamento de la plantilla; cada uno va al departamento con el mismo nombre que la persona
  gestiona o al destino elegido (General por defecto). Instalar reutiliza `HireForRole` (con el nombre del archivo si
  está libre), los topes de oficina y plan, el cerrojo por organización y `AssignAgent`; luego perfil, habilidades
  (`in_review`, asignadas), plantillas de trabajo, programaciones (desactivadas) y objetivos (activos solo si caben;
  si no, archivados). Idempotente: una segunda instalación no contrata ni crea nada repetido.
- **Permisos**: exportar un departamento = gestionarlo (admin o responsable) y verlo; un privado solo sus miembros.
  Exportar la empresa = admin, y deja fuera los privados de los que no es miembro. Importar e instalar =
  `agents:write` (como F2), instalar en un departamento exige gestionarlo.

## Pasos (TDD)

1. `officetemplates`: tipos, `Parse` estricto, `Fingerprint`, almacén en memoria + pruebas.
2. `application`: `PreviewTeam` con roles repetidos, `HireForRoleNamed`; `skills.ImportPackage`/`ActivePackage`;
   `goals.Import` + pruebas.
3. Postgres + migración 826 + rollback.
4. API `api/office_templates.go` + pruebas (lista blanca, ida y vuelta entre dos organizaciones, idempotencia,
   archivo enorme o malicioso, campos desconocidos, autonomía reiniciada, habilidades no activas, sellado, permisos).
5. Frontend: API + lógica pura con `node --test`, "Guardar como plantilla" (departamento y Ajustes), "Importar
   plantilla" y "Tus plantillas" en Equipos, i18n es/en/pt.
6. `docs/architecture/office-templates.md` y sección 50 de `08-api.md`.
