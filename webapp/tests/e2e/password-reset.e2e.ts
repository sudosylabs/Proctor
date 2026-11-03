import { expect, test } from "@playwright/test";

import { cliSession, invitedAccount } from "./support/api";
import { currentUser, expectSignedIn, submitCredentials } from "./support/browser";
import { randomPassword } from "./support/environment";
import { mailedLink } from "./support/mailpit";

test("a mailed password-reset link replaces the password and revokes Sessions", async ({ page }) => {
  const account = await invitedAccount();
  const existing = await cliSession(account.username, account.password);
  expect((await existing.get("/api/v1/users/me")).status()).toBe(200);

  await page.goto("/login");
  await page.getByRole("link", { name: "Forgot password?" }).click();
  await expect(page).toHaveURL(/\/account\/forgot-password$/);
  await page.getByLabel("Email address").fill(account.email);
  await page.getByRole("button", { name: "Send reset link" }).click();
  await expect(page.getByRole("heading", { name: "Check your email" })).toBeVisible();

  const link = await mailedLink(account.email, "/account/reset-password");
  await page.goto(link);
  // The bootstrap removes the one-time token from the address before render.
  await expect(page).toHaveURL(/\/account\/reset-password$/);
  const replacement = randomPassword();
  await page.locator("#new-password").fill(replacement);
  await page.locator("#confirm-new-password").fill(replacement);
  await page.getByRole("button", { name: "Set new password" }).click();
  await expect(page.getByRole("heading", { name: "Your password was changed" })).toBeVisible();

  // Authoritative state: the earlier Session is revoked, the old password is
  // refused, and the new one signs in.
  expect((await existing.get("/api/v1/users/me")).status()).toBe(401);
  await existing.dispose();
  await submitCredentials(page, account.username, account.password);
  await expect(page.getByText("The email, username, or password wasn’t accepted.")).toBeVisible();
  await submitCredentials(page, account.username, replacement);
  await expectSignedIn(page);
  expect(await currentUser(page)).toEqual({ status: 200, username: account.username });

  // The link is single-use.
  await page.goto("about:blank");
  await page.goto(link);
  await page.locator("#new-password").fill(randomPassword());
  const another = await page.locator("#new-password").inputValue();
  await page.locator("#confirm-new-password").fill(another);
  await page.getByRole("button", { name: "Set new password" }).click();
  await expect(page.getByRole("heading", { name: "This reset link can’t be used" })).toBeVisible();
});
