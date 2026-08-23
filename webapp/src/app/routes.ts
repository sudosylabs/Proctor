// Generated from server/webui/hosted_routes.json. Do not edit.
export const hostedRoutes = [
  "/setup",
  "/login",
  "/register",
  "/authorize/desktop",
  "/join",
  "/account/forgot-password",
  "/account/reset-password",
  "/account/verify-email",
  "/account/connect-provider",
  "/authorization/complete"
] as const;

export type HostedRoute = (typeof hostedRoutes)[number];

export function isHostedRoute(pathname: string): pathname is HostedRoute {
  return hostedRoutes.some((route) => route === pathname);
}
