# Recursos del laboratorio de animación (`/office-lab`, experimental)

Solo datos (modelos 3D); nada de lo descargado se ejecuta. Todo CC0 (dominio público): no exige atribución, se deja anotada igualmente.

## quaternius-characters/

- **Origen:** "Ultimate Animated Character Pack" de Quaternius — https://quaternius.com/packs/ultimatedanimatedcharacter.html
  (descarga pública en Google Drive enlazada desde esa página, carpeta `glTF`).
- **Autor:** Quaternius (Tomás Laulhé), https://quaternius.com
- **Licencia:** CC0 1.0 Universal — https://creativecommons.org/publicdomain/zero/1.0/ (ver `License.txt` del paquete, copiado aquí).
- **Archivos:** `Suit_Male.glb` (el agente), `Casual2_Female.glb` (el compañero), `OldClassy_Female.glb` (la jefa).
- **Cambios:** convertidos de `.gltf` con buffer base64 incrustado a `.glb` binario y reducidos a los clips usados
  (Idle, Walk, Walk_Carry, SitDown, StandUp, Victory, PickUp); el resto de clips (Death, Jump, Roll, SwordSlash…) se quitaron.
  ~2 MB -> ~0,8 MB cada uno.

## kenney-furniture/

- **Origen:** "Furniture Kit" (2.0) de Kenney — https://kenney.nl/assets/furniture-kit (carpeta `Models/GLTF format`).
- **Autor:** Kenney (www.kenney.nl)
- **Licencia:** CC0 1.0 Universal — http://creativecommons.org/publicdomain/zero/1.0/ (ver `License.txt` del paquete, copiado aquí).
- **Archivos:** 16 `.glb` sin texturas (escritorio, silla de escritorio, monitor, teclado, ratón, portátil, mesa redonda,
  sillas, estantería, plantas, lámparas, alfombra, perchero, papelera, sofá…), sin modificar. Los colores se cambian al
  cargarlos (paleta sobria) en `PanelB.tsx`.

## Reutilizado del repo

- `/office/env/room_512.hdr` (reflejos del panel B): ya estaba en `public/office`, ver `public/office/CREDITS.md`.
