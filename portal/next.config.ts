import type { NextConfig } from "next";

const config: NextConfig = {
  output: "export",
  basePath: "/portal",
  trailingSlash: true,
  images: { unoptimized: true },
  poweredByHeader: false,
};

export default config;
