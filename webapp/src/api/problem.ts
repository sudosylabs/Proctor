export interface ProblemDetails {
  type: string;
  title: string;
  status: number;
  detail?: string;
  instance?: string;
  code?: string;
}

export async function readProblem(response: Response): Promise<ProblemDetails | undefined> {
  const contentType = response.headers.get("content-type")?.split(";", 1)[0]?.trim();
  if (contentType !== "application/problem+json") {
    return undefined;
  }
  let value: unknown;
  try {
    value = await response.clone().json();
  } catch {
    return undefined;
  }
  if (!isRecord(value) || typeof value.type !== "string" || typeof value.title !== "string" || typeof value.status !== "number") {
    return undefined;
  }
  return {
    type: value.type,
    title: value.title,
    status: value.status,
    ...(typeof value.detail === "string" ? { detail: value.detail } : {}),
    ...(typeof value.instance === "string" ? { instance: value.instance } : {}),
    ...(typeof value.code === "string" ? { code: value.code } : {}),
  };
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}
