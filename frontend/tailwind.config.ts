import type { Config } from "tailwindcss";

// Todos los colores salen de variables CSS (src/app/globals.css) como triples RGB,
// así que `bg-panel/90`, `border-line/60`, etc. siguen funcionando.
const v = (name: string) => `rgb(var(--${name}) / <alpha-value>)`;

const config: Config = {
  content: ["./src/**/*.{ts,tsx}"],
  theme: {
    extend: {
      colors: {
        bg: v("bg"),
        panel: v("panel"),
        panel2: v("panel2"),
        line: v("line"),
        "line-strong": v("line-strong"),
        ink: v("ink"),
        ink2: v("ink2"),
        mute: v("mute"),
        accent: v("accent"),
        "accent-hover": v("accent-hover"),
        "accent-soft": v("accent-soft"),
        ok: v("ok"),
        warn: v("warn"),
        err: v("err"),
        info: v("info"),
        // Paletas de Tailwind reafinadas: desaturadas y con contraste AA para texto
        amber: { 100: "#f8eed9", 200: "#f0dfb9", 300: "#e3c987", 400: "#d2a24a", 500: "#c58a2c", 600: "#a86208", 700: "#8a5206", 800: "#6e4205" },
        red: { 50: "#fbf0ef", 100: "#f6e0de", 200: "#ecc3bf", 300: "#dd9a94", 400: "#cc7168", 500: "#bd554c", 600: "#a84239", 700: "#8e342d", 800: "#712a25" },
        emerald: { 100: "#dcefe5", 300: "#8cc5a8", 400: "#4fa07b", 500: "#2f7d55", 600: "#276a48", 700: "#1f5a3d" },
        violet: { 100: "#e9e5f6", 500: "#6d5bb5", 600: "#5d4ca0", 700: "#4f4390" },
      },
      fontFamily: {
        sans: ["var(--font-sans)", "Inter", "system-ui", "-apple-system", "Segoe UI", "sans-serif"],
        display: ["var(--font-sans)", "Inter", "system-ui", "-apple-system", "Segoe UI", "sans-serif"],
        mono: ["var(--font-mono)", "ui-monospace", "SFMono-Regular", "Menlo", "Consolas", "monospace"],
      },
      borderRadius: {
        sm: "4px",
        DEFAULT: "6px",
        md: "6px",
        lg: "8px",
        xl: "10px",
        "2xl": "10px",
        "3xl": "12px",
      },
      boxShadow: {
        // sombras suaves y frías; nada de "sombra sólida" estilo juguete
        pop: "0 1px 2px 0 rgb(15 23 42 / 0.05)",
        soft: "0 1px 2px 0 rgb(15 23 42 / 0.04), 0 8px 24px -8px rgb(15 23 42 / 0.14)",
        float: "0 1px 2px 0 rgb(15 23 42 / 0.05), 0 12px 32px -10px rgb(15 23 42 / 0.2)",
      },
    },
  },
  plugins: [],
};
export default config;
