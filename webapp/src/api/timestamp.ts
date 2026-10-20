// Proctor sends every timestamp as an RFC 3339 string; responses use UTC.
const wireTimestampPattern =
  /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d{1,9})?(?:Z|[+-]\d{2}:\d{2})$/;

export function isWireTimestamp(value: unknown): value is string {
  return typeof value === "string" && wireTimestampPattern.test(value) &&
    Number.isFinite(Date.parse(value));
}
