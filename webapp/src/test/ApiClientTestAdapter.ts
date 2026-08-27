export interface ApiResultOptions {
  data?: unknown;
  problemCode?: string;
  problemValue?: unknown;
}

export function apiResult(
  status: number,
  options: ApiResultOptions = {},
): never {
  const error =
    options.problemValue ??
    (options.problemCode === undefined
      ? undefined
      : {
          type: "/problems/test",
          title: "Test problem",
          status,
          code: options.problemCode,
        });
  return {
    data: options.data,
    error,
    response: new Response(null, { status }),
  } as never;
}
