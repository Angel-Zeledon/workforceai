# Archivos que se entienden (plan breve)

Pedido del dueño: "Darle más sentido al sistema de archivos, ahora me es confuso y tampoco puedo crear subcarpetas."

## Qué cambia

1. **Una sola pantalla de Archivos.** `/files` y `/d/[office]` pintan el mismo componente (`components/files/FilesScreen.tsx`,
   nacido de la pantalla de departamento D2, la más trabajada). `/d/<office>?carpeta=` sigue siendo el enlace directo de un
   departamento (con `?doc=` y `?comp=` como hasta ahora); el resto vive en `/files?path=<ruta>&doc=<ruta>`.
   La barra lateral (`FilesNav.tsx`) ordena en lenguaje humano: **Mis departamentos** (cada uno por su nombre; los
   abiertos donde la persona no es miembro aparecen como "solo lectura"), **Compartido con todos**, **Proyectos**,
   **Clientes**, **Escritorios de los agentes** (cada agente por su nombre y su inicial con color, nunca por id).
   `/files` sin ruta muestra esas mismas secciones como portada.
2. **Migas de pan humanas** en todos los niveles ("Contabilidad › Bancos y conciliaciones", "Escritorios de los agentes
   › Tomás Vidal"). La ruta técnica solo aparece en el panel de detalle del archivo ("Ubicación técnica").
3. **Nueva carpeta** (botón + atajo `N`), **Renombrar** y **Mover** (archivos y carpetas; selector de destino en árbol) y
   arrastrar una fila sobre una carpeta. Usa los endpoints existentes: `POST /drive/folders`, `POST /drive/folders/{id}/move`
   (renombrar carpeta = mover al mismo padre con otro nombre), `POST /drive/files/{id}/move`. Validación del nombre en el
   cliente con las mismas reglas que `drive.CleanName` y aviso de duplicado (el backend es idempotente y no lo diría).
   Donde no se puede, los botones quedan deshabilitados con la explicación ("Solo lectura: pide acceso a quien lleva
   Contabilidad").
4. **Adjuntos del chat**: la carpeta `Adjuntos` (o `Attachments`/`Anexos`) de cada departamento y `/Shared/Chat` se
   muestran como "Adjuntos del chat": la vista junta los archivos de las subcarpetas por día y los agrupa con cabeceras
   (Hoy, Esta semana, Este mes, Antes). No se migra nada ni cambia dónde se guardan.
5. **Ayuda y vacío**: una línea por sección ("Lo que pongas aquí lo ven y consultan los agentes de Contabilidad") y el
   indicador "N agentes pueden consultar esto", que sale de `GET /drive/folder-access` (las reglas existentes) y abre
   "Qué agentes la ven".
6. Carpetas internas ocultas en la navegación normal: `/Shared/Skills` (y sus `v<n>`); siguen en Habilidades.

## Backend (mínimo)

`GET /drive/list` añade `access: "edit" | "read"`: lo que la persona puede hacer en la carpeta listada (su nivel en el
drive, `PersonAccess`, combinado con el permiso `files:write` de su rol). No cambia ninguna regla: solo la expone para que
la interfaz no adivine. Sin migración ni rutas nuevas (sección 54 de `08-api.md` documenta el campo).

## Pruebas

- `node --test src/lib/filesLogic.test.mjs`: etiquetas, migas, secciones, carpetas ocultas, agentes por nombre, validación
  de nombres, destinos de mover, agrupación por fecha, textos de solo lectura.
- `go test ./internal/api/ -run 'TestDriveListAccess|TestFolderRename'`: `access` por rol y departamento (miembro, no
  miembro de abierto, sellado = 404), renombrar/mover carpeta respetando permisos y carpetas protegidas.

## Fuera de alcance

Subcarpetas "Puestos" y `FolderAccessSection.tsx` (otro agente); el traductor central de errores (otro agente: aquí los
mensajes son propios en es/en/pt y no muestran códigos).
