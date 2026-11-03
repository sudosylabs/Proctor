import { randomBytes } from "node:crypto";
import { readFile, writeFile } from "node:fs/promises";
import path from "node:path";

function required(name: string): string {
  const value = process.env[name];
  if (!value) throw new Error(`${name} is not set; run the suite through make e2e.`);
  return value;
}

export const serverURL = required("PROCTOR_E2E_BASE_URL");
export const mailpitURL = required("PROCTOR_E2E_MAILPIT_URL");
const stateDir = required("PROCTOR_E2E_STATE_DIR");

export function bootstrapSecret(): string {
  return required("PROCTOR_E2E_BOOTSTRAP_SECRET");
}

// Long random passwords satisfy any reasonable policy rule (length, classes,
// breach or identity similarity) without coupling the suite to the policy.
export function randomPassword(): string {
  return `${randomBytes(30).toString("base64url")}-Qz7`;
}

// unique returns a lowercase identifier unique to this run and call.
export function unique(prefix: string): string {
  return `${prefix}-${randomBytes(6).toString("hex")}`;
}

export interface Installation {
  institutionID: string;
  administrator: { username: string; email: string; password: string };
  academicUnitID: string;
  teacherRoleID: string;
}

// The installation project records what it established for dependent tests.
// The file lives in the run's private, disposable state directory.
const installationFile = path.join(stateDir, "installation.json");

export async function saveInstallation(value: Installation): Promise<void> {
  await writeFile(installationFile, `${JSON.stringify(value)}\n`, { mode: 0o600 });
}

export async function loadInstallation(): Promise<Installation> {
  return JSON.parse(await readFile(installationFile, "utf8")) as Installation;
}
