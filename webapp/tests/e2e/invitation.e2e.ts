import { expect, test } from "@playwright/test";

import { invitation, inviteTeacher } from "./support/api";
import { currentUser, expectSignedIn, submitCredentials } from "./support/browser";
import { randomPassword, serverURL, unique } from "./support/environment";

test("a mailed Invitation link admits a new local account", async ({ page }) => {
  const issued = await inviteTeacher();
  expect(issued.link.startsWith(`${serverURL}/join#token=`)).toBe(true);
  expect(await invitation(issued.id)).not.toHaveProperty("accepted_user_id");

  await page.goto(issued.link);
  await expect(page).toHaveURL(`${serverURL}/join`);
  await expect(page.getByRole("heading", { name: "Create your invited account" })).toBeVisible();
  const username = unique("joined");
  const password = randomPassword();
  await page.getByLabel("Username").fill(username);
  await page.locator("#invitation-password").fill(password);
  await page.getByRole("button", { name: "Accept invitation" }).click();
  await expect(page.getByRole("heading", { name: "Your Invitation is accepted" })).toBeFocused();

  // Authoritative state: the Invitation names the User it created.
  const accepted = await invitation(issued.id);
  expect(accepted.accepted_user_id).toEqual(expect.any(String));

  // The claim is consumed. Leave the page first: the sanitized /join URL
  // differs from the link only by its fragment, which would not reload.
  await page.goto("about:blank");
  await page.goto(issued.link);
  await expect(page.getByRole("heading", { name: "This Invitation can’t be used" })).toBeVisible();

  await submitCredentials(page, issued.email, password);
  await expectSignedIn(page);
  expect(await currentUser(page)).toEqual({ status: 200, username });
  const me = await (await page.request.get("/api/v1/users/me")).json();
  expect(me.id).toBe(accepted.accepted_user_id);
});
