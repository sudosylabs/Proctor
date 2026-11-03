import { randomUUID } from "node:crypto";

import { expect, request, type APIRequestContext } from "@playwright/test";

import { loadInstallation, randomPassword, serverURL, unique } from "./environment";
import { claimFrom, mailedLink } from "./mailpit";

export interface Account {
  userID: string;
  username: string;
  email: string;
  password: string;
}

async function json(response: Awaited<ReturnType<APIRequestContext["get"]>>, status: number) {
  // The status and Problem code are enough to diagnose a contract failure;
  // the body never carries a secret this suite did not generate.
  expect(response.status(), `${response.url()} → ${await response.text()}`).toBe(status);
  return (await response.json()) as Record<string, unknown>;
}

// cliSession signs in through the public API as a CLI client and returns a
// bearer-authenticated request context. It is the suite's administrative
// vantage point for arranging fixtures and asserting authoritative state.
export async function cliSession(loginID: string, password: string): Promise<APIRequestContext> {
  const anonymous = await request.newContext({ baseURL: serverURL });
  try {
    const body = await json(await anonymous.post("/api/v1/auth/login", {
      data: {
        login_id: loginID, password, client_type: "cli",
        device_id: `e2e-${randomUUID()}`, device_name: "Proctor end-to-end suite",
      },
    }), 200);
    const tokens = body.tokens as { access_token: string };
    return await request.newContext({
      baseURL: serverURL,
      extraHTTPHeaders: { Authorization: `Bearer ${tokens.access_token}` },
    });
  } finally {
    await anonymous.dispose();
  }
}

let administrator: Promise<APIRequestContext> | undefined;

// administratorSession reuses one Session per worker so the suite stays well
// inside the per-User Session limit.
export function administratorSession(): Promise<APIRequestContext> {
  administrator ??= loadInstallation().then(({ administrator: account }) =>
    cliSession(account.username, account.password));
  return administrator;
}

export async function createAcademicUnit(admin: APIRequestContext): Promise<string> {
  const name = unique("e2e-unit");
  const body = await json(await admin.post("/api/v1/academic-units", {
    data: { name, display_name: `End-to-end ${name}`, description: "Synthetic end-to-end fixture" },
  }), 201);
  return body.id as string;
}

export async function createTeacherRole(admin: APIRequestContext): Promise<string> {
  const name = unique("e2e-teacher");
  const body = await json(await admin.post("/api/v1/roles", {
    data: {
      name, display_name: "End-to-end Teacher", description: "Synthetic end-to-end fixture",
      permissions: ["academic_unit.view"],
    },
  }), 201);
  return body.id as string;
}

export interface IssuedInvitation { id: string; email: string; link: string }

// inviteTeacher issues a real teacher Invitation and returns the link the
// server mailed to the recipient through Mailpit.
export async function inviteTeacher(email = `${unique("invitee")}@e2e.proctor.test`): Promise<IssuedInvitation> {
  const { academicUnitID, teacherRoleID } = await loadInstallation();
  const admin = await administratorSession();
  const body = await json(await admin.post(`/api/v1/academic-units/${academicUnitID}/invitations/teacher`, {
    data: { email, role_id: teacherRoleID },
  }), 201);
  return { id: body.id as string, email, link: await mailedLink(email, "/join") };
}

export async function invitation(id: string): Promise<Record<string, unknown>> {
  return json(await (await administratorSession()).get(`/api/v1/invitations/${id}`), 200);
}

// invitedAccount admits a fresh local User through the public Invitation
// acceptance API, so each journey owns an isolated account.
export async function invitedAccount(): Promise<Account> {
  const issued = await inviteTeacher();
  const username = unique("person");
  const password = randomPassword();
  const anonymous = await request.newContext({ baseURL: serverURL });
  try {
    const body = await json(await anonymous.post("/api/v1/invitations/teacher-academic-unit/accept", {
      data: { claim: claimFrom(issued.link), username, password },
    }), 200);
    return { userID: body.user_id as string, username, email: issued.email, password };
  } finally {
    await anonymous.dispose();
  }
}
