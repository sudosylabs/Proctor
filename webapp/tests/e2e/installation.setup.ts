import { expect, request, test } from "@playwright/test";

import { cliSession, createAcademicUnit, createTeacherRole } from "./support/api";
import { bootstrapSecret, randomPassword, saveInstallation, serverURL, unique } from "./support/environment";

// The installation project runs once, first, against the pristine server the
// harness started. Every other journey depends on the Institution and
// administrator it establishes through the hosted /setup page.
test("first-run setup establishes the Institution and first administrator", async ({ page }) => {
  const anonymous = await request.newContext({ baseURL: serverURL });
  const before = await anonymous.get("/api/v1/bootstrap");
  expect(await before.json()).toEqual({ initialized: false });

  await page.goto("/login");
  await expect(page.getByRole("heading", { name: "Setup is required" })).toBeVisible();
  await page.getByRole("link", { name: "Open setup" }).click();
  await expect(page).toHaveURL(/\/setup$/);

  const administrator = {
    username: unique("admin"),
    email: `${unique("admin")}@e2e.proctor.test`,
    password: randomPassword(),
  };
  const institutionName = unique("e2e-institution");
  await page.locator("#bootstrap-secret").fill(bootstrapSecret());
  await page.getByLabel("Institution name").fill(institutionName);
  await page.locator("#institution-display-name").fill("End-to-end University");
  await page.getByLabel("Email address").fill(administrator.email);
  await page.getByLabel("Username").fill(administrator.username);
  await page.locator("#administrator-password").fill(administrator.password);
  const created = page.waitForResponse((response) =>
    response.url() === `${serverURL}/api/v1/bootstrap` && response.request().method() === "POST");
  await page.getByRole("button", { name: "Create installation" }).click();
  expect((await created).status()).toBe(201);
  await expect(page.getByRole("link", { name: "Continue to sign in" })).toBeVisible();

  // Authoritative state: the server is initialized, discovery names the new
  // Institution, and the administrator can authenticate.
  expect(await (await anonymous.get("/api/v1/bootstrap")).json()).toEqual({ initialized: true });
  const discovery = await (await anonymous.get("/api/v1/discovery")).json();
  expect(discovery).toMatchObject({
    initialized: true,
    canonical_origin: serverURL,
    institution: { name: institutionName, display_name: "End-to-end University" },
    capabilities: { local_login: true },
  });
  await anonymous.dispose();

  await page.goto("/setup");
  await expect(page.getByRole("heading", { name: "Setup is complete" })).toBeVisible();
  await expect(page.locator("#bootstrap-secret")).toHaveCount(0);

  const admin = await cliSession(administrator.username, administrator.password);
  const me = await (await admin.get("/api/v1/users/me")).json();
  expect(me).toMatchObject({ username: administrator.username });
  const academicUnitID = await createAcademicUnit(admin);
  const teacherRoleID = await createTeacherRole(admin);
  await admin.post("/api/v1/auth/logout");
  await admin.dispose();

  await saveInstallation({
    institutionID: (discovery as { institution: { id: string } }).institution.id,
    administrator, academicUnitID, teacherRoleID,
  });
});
