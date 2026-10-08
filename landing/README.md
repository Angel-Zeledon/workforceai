# Landing de AI Workforce OS

Sitio estático autocontenido: HTML + CSS + JS mínimo. Sin build, sin frameworks, sin CDNs (la fuente Inter va en `fonts/`), sin formularios y sin analítica.

## Verla

```bash
npx serve landing          # o: npx --yes serve landing -l 4000
```

También funciona abriendo `landing/index.html` directamente en el navegador. Parámetro `?lang=en` fuerza el idioma.

## Desplegar

Sube el contenido de `landing/` a cualquier hosting estático (Netlify, Vercel, Cloudflare Pages, GitHub Pages, S3, Caddy/Nginx). No hay paso de build; el directorio de publicación es `landing`.

## Dónde cambiar qué

| Qué | Dónde |
|---|---|
| URL de la demo y correo de contacto (CTA) | `config.js` (único lugar; valores `TODO` por reemplazar) |
| Todos los textos, ES y EN | `i18n.js` (diccionario por clave) |
| Estructura y orden de secciones | `index.html` (cada texto es `data-i18n="clave"`) |
| Colores, tipografía, espaciado | `styles.css` (tokens en `:root`, mismos neutros e índigo que la app) |
| Oficina animada del hero (guion, estados, arcos, globos) | `stage.js` (guion fijo; con movimiento reducido muestra un cuadro estático) |
| Sonido y música (apagados por defecto, Web Audio sin archivos) | `sound.js` |
| Capturas | `img/`, regenerables con `scripts/capture.mjs` |

### Idioma
Se detecta del navegador (español por defecto) y el selector ES/EN guarda la preferencia en `localStorage`. El HTML trae el español horneado para SEO y carga sin JS: tras editar textos en español corre `node landing/scripts/prerender.mjs`.

### Capturas
Con la app corriendo (Docker en :3000, o la build mock local):

```bash
node landing/scripts/capture.mjs                                   # http://localhost:3000
CAPTURE_URL=http://localhost:3100 node landing/scripts/capture.mjs # otra URL
```

Requiere Playwright en `e2e/` y `sharp` en `frontend/` (`npm install` en ambos). Genera `office`, `approvals`, `dashboard` y `projects` en webp (1600x1000) y `og.png` (1200x630). Para mock local: `cd frontend && NEXT_PUBLIC_MOCK=true NEXT_DIST_DIR=.next-landing npm run build && NEXT_DIST_DIR=.next-landing npx next start -p 3100`.

## Pendientes antes de publicar
- Reemplazar los `TODO` de `config.js`.
- Poner URLs absolutas en `og:image`/`og:url` cuando se conozca el dominio.
- Mantener la honestidad: la sección "Estado actual" debe reflejar lo que de verdad funciona. No añadir logos, testimonios, métricas, certificaciones ni precios sin evidencia.
