/** @type {import('next').NextConfig} */
const nextConfig = {
  output: "standalone",
  reactStrictMode: false,
  transpilePackages: ["three"],
  // Permite un directorio de build aparte (p. ej. `NEXT_DIST_DIR=.next-dev next dev`) sin pisar el de otros procesos.
  ...(process.env.NEXT_DIST_DIR ? { distDir: process.env.NEXT_DIST_DIR } : {}),
};
export default nextConfig;
