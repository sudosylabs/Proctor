import createClient, { type Middleware } from "openapi-fetch";

import { withCSRFHeader } from "../auth/csrf";
import type { paths } from "./generated/schema";

const csrfMiddleware: Middleware = {
  onRequest({ request }) {
    const cookies = typeof document === "undefined" ? "" : document.cookie;
    return new Request(request, {
      credentials: "same-origin",
      headers: withCSRFHeader(request.method, cookies, request.headers),
    });
  },
};

// Hosted pages always address the installation that served them. There is no
// remote-base option because permitting one would reintroduce issuer mix-up.
export const apiClient = createClient<paths>({
  baseUrl: "",
  credentials: "same-origin",
});

apiClient.use(csrfMiddleware);
