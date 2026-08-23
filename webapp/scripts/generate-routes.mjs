import { readFile, writeFile } from "node:fs/promises";

const sourceURL = new URL("../../server/webui/hosted_routes.json", import.meta.url);
const outputURL = new URL("../src/app/routes.ts", import.meta.url);
const routes = JSON.parse(await readFile(sourceURL, "utf8"));

if (
  !Array.isArray(routes) ||
  routes.length === 0 ||
  new Set(routes).size !== routes.length ||
  routes.some(
    (route) =>
      typeof route !== "string" ||
      route === "/" ||
      !route.startsWith("/") ||
      route.startsWith("/assets/") ||
      route.endsWith("/"),
  )
) {
  throw new Error("hosted route catalog is invalid");
}

const generated = `// Generated from server/webui/hosted_routes.json. Do not edit.
export const hostedRoutes = ${JSON.stringify(routes, null, 2)} as const;

export type HostedRoute = (typeof hostedRoutes)[number];

export function isHostedRoute(pathname: string): pathname is HostedRoute {
  return hostedRoutes.some((route) => route === pathname);
}
`;

await writeFile(outputURL, generated);
