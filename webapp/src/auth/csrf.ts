export const csrfCookieName = "PROCTOR_CSRF";
export const csrfHeaderName = "X-Proctor-CSRF-Token";

const safeMethods = new Set(["GET", "HEAD", "OPTIONS", "TRACE"]);

export function readCookie(cookieHeader: string, name: string): string | undefined {
  for (const item of cookieHeader.split(";")) {
    const [rawName, ...rawValue] = item.trim().split("=");
    if (rawName === name) {
      const value = rawValue.join("=");
      try {
        return decodeURIComponent(value);
      } catch {
        return undefined;
      }
    }
  }
  return undefined;
}

export function withCSRFHeader(
  method: string,
  cookieHeader: string,
  input?: HeadersInit,
): Headers {
  const headers = new Headers(input);
  if (safeMethods.has(method.toUpperCase()) || headers.has(csrfHeaderName)) {
    return headers;
  }
  const token = readCookie(cookieHeader, csrfCookieName);
  if (token !== undefined && token !== "") {
    headers.set(csrfHeaderName, token);
  }
  return headers;
}
