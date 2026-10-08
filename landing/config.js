/*
 * Landing configuration. The ONLY place where the CTAs are changed.
 * No forms and no analytics: the buttons are plain links.
 */
window.LANDING_CONFIG = {
  // Public demo (open app in simulation mode, no real AI and no credentials).
  DEMO_URL: "https://app.workforceai.es",

  // Contact / early access email. Empty = the contact button points to the demo.
  CONTACT_EMAIL: "info@workforceai.es",

  // Subject of the early access email, per language.
  CONTACT_SUBJECT: {
    es: "Acceso anticipado a AI Workforce OS",
    en: "Early access to AI Workforce OS",
  },

  // Canonical site URL (Open Graph / SEO).
  SITE_URL: "https://workforceai.es",
};
