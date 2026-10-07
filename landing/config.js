/*
 * Configuración de la landing. ÚNICO lugar donde se cambian los CTA.
 * Sin formularios y sin analítica: los botones son enlaces normales.
 */
window.LANDING_CONFIG = {
  // Demo pública (app abierta en modo simulación, sin IA real ni credenciales).
  DEMO_URL: "https://app.workforceai.es",

  // Correo de contacto / acceso anticipado. Vacío = el botón de contacto lleva a la demo (no se publica ningún correo).
  CONTACT_EMAIL: "",

  // Asunto del correo de acceso anticipado, por idioma.
  CONTACT_SUBJECT: {
    es: "Acceso anticipado a AI Workforce OS",
    en: "Early access to AI Workforce OS",
  },

  // URL canónica del sitio (Open Graph / SEO).
  SITE_URL: "https://workforceai.es",
};
