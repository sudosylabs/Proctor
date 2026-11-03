import { expect, test } from "@playwright/test";

import { invitedAccount } from "./support/api";
import { currentUser, expectSignedIn, signOut, submitCredentials } from "./support/browser";
import { randomPassword } from "./support/environment";

test("local login creates a cookie Session and sign out revokes it", async ({ page }) => {
  const account = await invitedAccount();
  expect(await currentUser(page)).toEqual({ status: 401 });

  await submitCredentials(page, account.username, randomPassword());
  await expect(page.getByText("The email, username, or password wasn’t accepted.")).toBeVisible();
  expect(await currentUser(page)).toEqual({ status: 401 });

  await submitCredentials(page, account.email, account.password);
  await expectSignedIn(page);
  expect(await currentUser(page)).toEqual({ status: 200, username: account.username });

  await signOut(page);
  expect(await currentUser(page)).toEqual({ status: 401 });
  await page.goto("/authorization/complete");
  await expect(page.getByRole("heading", { name: "You’re not signed in" })).toBeVisible();
});
