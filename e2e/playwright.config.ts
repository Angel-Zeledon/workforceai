import { defineConfig, devices } from "@playwright/test";

const baseURL = process.env.FRONTEND_URL ?? "http://localhost:3000";

export default defineConfig({
  testDir: "./tests",
  timeout: 120_000,
  expect: { timeout: 15_000 },
  fullyParallel: false,
  workers: 1,
  retries: process.env.CI ? 1 : 0,
  reporter: process.env.CI ? [["list"], ["html", { open: "never" }]] : "list",
  use: {
    baseURL,
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
    // The first-run welcome covers the office until it is dismissed; every browser starts as if it had been seen
    // (auth off, so the key is the anonymous one; see WelcomeOverlay.tsx). The animated 3D office is off by default
    // (it uses a lot of the device); these suites drive it, so every browser starts with it on (lib/preferences.ts).
    storageState: { cookies: [], origins: [{ origin: new URL(baseURL).origin, localStorage: [
      { name: "wf.welcome.v1.anon", value: "1" },
      { name: "aiwos.preferences.v1", value: JSON.stringify({ office3d: true }) },
    ] }] },
  },
  projects: [{ name: "chromium", use: { ...devices["Desktop Chrome"] } }],
});
