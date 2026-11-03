import { expect, test } from "@playwright/test";

import { cliSession, invitedAccount } from "./support/api";
import { currentUser, enterCode, expectSignedIn, signOut, submitCredentials } from "./support/browser";
import { currentTimeStep, nextTimeStep, totp } from "./support/totp";

test("MFA enrollment protects the next login with an authenticator challenge", async ({ page }) => {
  const account = await invitedAccount();
  await submitCredentials(page, account.username, account.password);
  await expectSignedIn(page);

  await page.goto("/account/security");
  await expect(page.getByText("Not enabled", { exact: true })).toBeVisible();
  await page.getByRole("button", { name: "Set up an authenticator", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Add your authenticator", level: 1 })).toBeFocused();
  await expect(page.getByRole("img", { name: "QR code for authenticator setup" })).toBeVisible();
  await page.getByText("Can’t scan the code?", { exact: true }).click();
  const secret = (await page.getByLabel("Setup key", { exact: true }).inputValue()).replace(/\s+/g, "");
  expect(secret).toMatch(/^[A-Z2-7]+=*$/);

  const enrolledStep = currentTimeStep();
  await enterCode(page.getByLabel("Authenticator code"), totp(secret, enrolledStep));
  await page.getByRole("button", { name: "Activate authenticator", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Save your recovery codes", level: 1 })).toBeFocused();
  await page.getByRole("checkbox", { name: "I have saved these recovery codes in a secure place." }).check();
  await page.getByRole("button", { name: "Finish and hide codes" }).click();
  await expect(page.getByText("Enabled", { exact: true })).toBeVisible();

  // Authoritative state: the server reports an enabled factor for this User.
  const status = await (await page.request.get("/api/v1/users/me/mfa")).json();
  expect(status).toMatchObject({ enabled: true, pending: false });
  expect(status.recovery_codes_remaining).toBeGreaterThan(0);

  await signOut(page);
  await submitCredentials(page, account.username, account.password);
  await expect(page.getByRole("heading", { name: "Confirm it’s you" })).toBeVisible();
  const code = page.getByLabel("Authenticator code");
  await expect(code).toBeFocused();
  expect(await currentUser(page)).toEqual({ status: 401 });

  // A wrong code keeps the challenge and creates no Session.
  const wrong = String((Number(totp(secret, nextTimeStep(enrolledStep))) + 1) % 1_000_000).padStart(6, "0");
  await enterCode(code, wrong);
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await expect(code).toHaveAttribute("aria-invalid", "true");
  expect(await currentUser(page)).toEqual({ status: 401 });

  await enterCode(code, totp(secret, nextTimeStep(enrolledStep)));
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await expectSignedIn(page);
  expect(await currentUser(page)).toEqual({ status: 200, username: account.username });

  // Password alone no longer authenticates any client.
  await expect(cliSession(account.username, account.password)).rejects.toThrow(/authentication\.mfa\.required/);
});
