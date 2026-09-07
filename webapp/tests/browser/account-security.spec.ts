import { readFile } from "node:fs/promises";
import { expect, test, type Page } from "@playwright/test";
import jsQR from "jsqr";
import { PNG } from "pngjs";

const baseContext = {
  enabled: false, pending: false, recovery_codes_remaining: 0,
  service_enabled: true, mfa_recovery_required: false,
  authentication_method: "password", authentication_strength: "single_factor",
  recently_authenticated: true,
};
const setupKey = "JBSWY3DPEHPK3PXP";
const provisioningURI = `otpauth://totp/Proctor:preview?secret=${setupKey}&issuer=Proctor`;
const recoveryCodes = ["ABCD-EFGH-IJKL", "MNOP-QRST-UVWX", "2345-6789-ABCD", "EFGH-IJKL-MNOP"];

async function mockSecurity(page: Page, context = baseContext) {
  const state = {
    context: { ...context }, requests: [] as Array<{ path: string; method: string; body: unknown }>,
    lostActivation: false, signedOut: false,
  };
  await page.route("**/api/v1/**", async (route) => {
    const request = route.request();
    const path = new URL(request.url()).pathname;
    const method = request.method();
    state.requests.push({ path, method, body: method === "POST" && request.postData() ? request.postDataJSON() : undefined });
    async function json(value: unknown, status = 200) { await route.fulfill({ status, contentType: "application/json", body: JSON.stringify(value) }); }
    async function problem(code: string, status = 403) { await route.fulfill({ status, contentType: "application/problem+json", body: JSON.stringify({ type: "about:blank", title: "Private server detail", status, code }) }); }
    if (path === "/api/v1/users/me/mfa" && method === "GET") {
      if (state.signedOut) return problem("authentication.required", 401);
      return json(state.context);
    }
    if (path === "/api/v1/users/me/mfa/setup") {
      if (!state.context.recently_authenticated) return problem("authentication.reauthentication_required");
      state.context.pending = true;
      return json({ secret: setupKey, provisioning_uri: provisioningURI, expires_at: Date.now() + 600_000 }, 201);
    }
    if (path === "/api/v1/users/me/mfa/activate") {
      if (request.postDataJSON().code !== "123456") return problem("authentication.mfa.invalid_code");
      state.context.enabled = true; state.context.pending = false;
      state.context.mfa_recovery_required = false;
      state.context.recovery_codes_remaining = recoveryCodes.length;
      state.context.authentication_strength = "multi_factor";
      return json(state.lostActivation ? {} : { recovery_codes: recoveryCodes });
    }
    if (path === "/api/v1/users/me/mfa/challenge") {
      state.context.authentication_strength = "multi_factor";
      return json({ id: "current-session", client_type: "web" });
    }
    if (path === "/api/v1/users/me/mfa/recovery-codes/regenerate") {
      if (!state.context.recently_authenticated) return problem("authentication.reauthentication_required");
      if (state.context.authentication_strength !== "multi_factor") return problem("authentication.strong_required");
      state.context.recovery_codes_remaining = recoveryCodes.length;
      return json({ recovery_codes: recoveryCodes });
    }
    if (path === "/api/v1/users/me/mfa/disable") {
      state.context.enabled = false; state.context.recovery_codes_remaining = 0;
      await route.fulfill({ status: 204 }); return;
    }
    if (path === "/api/v1/auth/reauthenticate/password") {
      if (request.postDataJSON().password !== "current-password") return problem("authentication.invalid_credentials", 401);
      state.context.recently_authenticated = true;
      return json({ id: "current-session", client_type: "web", reauthenticated_at: Date.now() });
    }
    if (path === "/api/v1/auth/reauthenticate/external") return json({ redirect_url: "https://sso.example/reauthenticate" });
    if (path === "/api/v1/auth/logout") { state.signedOut = true; await route.fulfill({ status: 204 }); return; }
    if (path === "/api/v1/users/me") return json({ id: "user", username: "person", display_name: "Person" });
    return problem("unexpected.test.request", 500);
  });
  return state;
}

function mutations(state: Awaited<ReturnType<typeof mockSecurity>>, suffix: string) {
  return state.requests.filter(({ path, method }) => method === "POST" && path.endsWith(suffix));
}

async function activate(page: Page) {
  await page.getByRole("button", { name: "Set up an authenticator", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Add your authenticator", level: 1 })).toBeFocused();
  await page.getByLabel("Authenticator code", { exact: false }).fill("123456");
  await page.getByRole("button", { name: "Activate authenticator", exact: true }).click();
}

async function expectScannableSetup(page: Page) {
  const qr = page.getByRole("img", { name: "QR code for authenticator setup" });
  await expect(qr).toBeVisible();
  const png = PNG.sync.read(await qr.screenshot());
  const decoded = jsQR(new Uint8ClampedArray(png.data), png.width, png.height);
  expect(decoded?.data).toBe(provisioningURI);
}

for (const colorScheme of ["light", "dark"] as const) {
  for (const width of [320, 1440]) {
    test(`QR setup and OTP entry remain usable at ${width}px in ${colorScheme}`, async ({ page }, testInfo) => {
      await page.setViewportSize({ width, height: 1000 });
      await page.emulateMedia({ colorScheme, reducedMotion: "reduce" });
      await mockSecurity(page);
      const external: string[] = [];
      page.on("request", (request) => {
        if (!request.url().startsWith("http://127.0.0.1:5173/")) external.push(request.url());
      });
      await page.goto("/account/security");
      await page.getByRole("button", { name: "Set up an authenticator", exact: true }).click();
      await expectScannableSetup(page);
      await expect(page.getByLabel("Setup key", { exact: true })).not.toBeVisible();
      const manual = page.getByText("Can’t scan the code?", { exact: true });
      await manual.focus();
      await page.keyboard.press("Enter");
      await expect(page.getByLabel("Setup key", { exact: true })).toHaveValue(setupKey);
      await expect(page.getByLabel("Setup key", { exact: true })).toBeVisible();
      await page.getByLabel("Authenticator code").fill("012345");
      await expect(page.locator("[data-proctor-otp-slot]")).toHaveText(["0", "1", "2", "3", "4", "5"]);
      await page.screenshot({ path: testInfo.outputPath(`mfa-setup-${width}-${colorScheme}.png`), fullPage: true });
      await page.evaluate(() => { document.body.style.zoom = "2"; document.documentElement.dir = "rtl"; });
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth + 1)).toBe(true);
      await expect(page.getByLabel("Authenticator code")).toHaveAttribute("dir", "ltr");
      await expectScannableSetup(page);
      await page.emulateMedia({ forcedColors: "active" });
      await expectScannableSetup(page);
      const focus = await page.locator("[data-proctor-otp-slot][data-active]").evaluate((slot) => ({
        style: getComputedStyle(slot).outlineStyle, width: getComputedStyle(slot).outlineWidth,
      }));
      expect(focus).toEqual({ style: "solid", width: "3px" });
      expect(external).toEqual([]);
    });
  }
}

test("OTP supports leading zeroes, editing, paste, validation and explicit submission", async ({ page }) => {
  const state = await mockSecurity(page);
  await page.goto("/account/security");
  await page.getByRole("button", { name: "Set up an authenticator", exact: true }).click();
  const input = page.getByLabel("Authenticator code");
  await expect(input).toHaveAttribute("type", "text");
  await expect(input).toHaveAttribute("inputmode", "numeric");
  await expect(input).toHaveAttribute("autocomplete", "one-time-code");
  await input.pressSequentially("012");
  await page.getByRole("button", { name: "Activate authenticator" }).click();
  await expect(input).toBeFocused();
  await expect(input).toHaveAttribute("aria-invalid", "true");
  await expect(page.getByText("Enter all six digits of your authenticator code.")).toBeVisible();
  expect(mutations(state, "/activate")).toHaveLength(0);
  await input.fill("");
  await input.pressSequentially("012345");
  expect(mutations(state, "/activate")).toHaveLength(0);
  await input.press("Backspace");
  await expect(input).toHaveValue("01234");
  await input.press("ArrowLeft");
  await input.press("9");
  await expect(input).toHaveValue("01239");
  await input.press("ControlOrMeta+A");
  await input.press("Backspace");
  await input.pressSequentially("ABC");
  await expect(input).toHaveValue("");
  await input.evaluate((element) => {
    const data = new DataTransfer();
    data.setData("text/plain", "123 456");
    const event = new ClipboardEvent("paste", { bubbles: true, cancelable: true });
    // Firefox drops clipboardData supplied to an untrusted event constructor.
    Object.defineProperty(event, "clipboardData", { value: data });
    element.dispatchEvent(event);
  });
  await expect(input).toHaveValue("123456");
  await expect(page.locator("[data-proctor-otp-slot]")).toHaveText(["1", "2", "3", "4", "5", "6"]);
  expect(mutations(state, "/activate")).toHaveLength(0);
  await input.press("Enter");
  await expect(page.getByRole("heading", { name: "Save your recovery codes" })).toBeVisible();
  expect(mutations(state, "/activate")[0]?.body).toEqual({ code: "123456" });
  await expect(page.getByRole("img", { name: "QR code for authenticator setup" })).toHaveCount(0);
});

test("switching proof methods clears values, restores focus and preserves recovery codes", async ({ page }) => {
  const state = await mockSecurity(page, { ...baseContext, enabled: true });
  await page.goto("/account/security");
  await page.getByRole("button", { name: "Verify this session", exact: true }).click();
  await page.getByLabel("Authenticator code").fill("012345");
  await page.getByRole("button", { name: "Use a recovery code instead" }).click();
  const recovery = page.getByRole("textbox", { name: "Recovery code", exact: true });
  await expect(recovery).toBeFocused();
  await expect(recovery).toHaveValue("");
  await recovery.fill("ABCD-EFGH-IJKL");
  await page.getByRole("button", { name: "Use an authenticator code" }).click();
  await expect(page.getByLabel("Authenticator code")).toBeFocused();
  await expect(page.getByLabel("Authenticator code")).toHaveValue("");
  await page.getByRole("button", { name: "Verify code", exact: true }).click();
  await expect(page.getByLabel("Authenticator code")).toBeFocused();
  expect(mutations(state, "/challenge")).toHaveLength(0);
  await page.getByRole("button", { name: "Use a recovery code instead" }).click();
  await recovery.fill("ABCD-EFGH-IJKL");
  await recovery.press("Enter");
  await expect(page.getByRole("heading", { name: "Account security", exact: true })).toBeVisible();
  expect(mutations(state, "/challenge")[0]?.body).toEqual({ code: "ABCD-EFGH-IJKL" });
});

test("production QR and OTP work under the server Content Security Policy", async ({ page }) => {
  const serverSource = await readFile("../server/webui/webui.go", "utf8");
  const policy = serverSource.match(/header\.Set\("Content-Security-Policy", "([^"]+)"\)/)?.[1];
  expect(policy).toBeDefined();
  await mockSecurity(page);
  await page.route("**/account/security", async (route) => route.fulfill({
    contentType: "text/html", body: await readFile("dist/index.html"),
    headers: { "Content-Security-Policy": policy! },
  }));
  await page.route("**/assets/**", async (route) => {
    const path = new URL(route.request().url()).pathname;
    expect(path).toMatch(/^\/assets\/[A-Za-z0-9_.-]+$/);
    const type = path.endsWith(".js") ? "text/javascript" : path.endsWith(".css") ? "text/css"
      : path.endsWith(".woff2") ? "font/woff2" : path.endsWith(".svg") ? "image/svg+xml" : "image/png";
    await route.fulfill({ contentType: type, body: await readFile(`dist${path}`) });
  });
  const errors: string[] = [];
  page.on("pageerror", (error) => errors.push(error.message));
  page.on("console", (entry) => { if (entry.type() === "error") errors.push(entry.text()); });
  await page.goto("/account/security");
  await page.getByRole("button", { name: "Set up an authenticator", exact: true }).click();
  // QR decoding is covered separately: Playwright's WebKit screenshots inject an inline style.
  await expect(page.getByRole("img", { name: "QR code for authenticator setup" })).toBeVisible();
  await page.getByLabel("Authenticator code").pressSequentially("123456");
  await expect(page.locator("[data-proctor-otp-slot]")).toHaveText(["1", "2", "3", "4", "5", "6"]);
  await page.getByRole("button", { name: "Activate authenticator", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Save your recovery codes" })).toBeVisible();
  expect(errors).toEqual([]);
});

test("enrollment shows codes once and requires acknowledgement before hiding them", async ({ page }) => {
  const state = await mockSecurity(page);
  const logs: string[] = [];
  page.on("console", (entry) => logs.push(entry.text()));
  await page.goto("/account/security#unexpected=secret");
  await expect(page.getByRole("heading", { name: "Account security", level: 1 })).toBeVisible();
  expect(state.requests.filter(({ method }) => method === "POST")).toHaveLength(0);
  await activate(page);
  await expect(page.getByRole("heading", { name: "Save your recovery codes", level: 1 })).toBeFocused();
  const finish = page.getByRole("button", { name: "Finish and hide codes" });
  await expect(finish).toBeDisabled();
  await expect(page.getByRole("link", { name: "Back to sign-in status" })).toHaveCount(0);
  const downloadPromise = page.waitForEvent("download");
  await page.getByRole("button", { name: "Download recovery codes" }).click();
  const download = await downloadPromise;
  expect(download.suggestedFilename()).toBe("proctor-recovery-codes.txt");
  expect(await readFile((await download.path())!, "utf8")).toBe(`${recoveryCodes.join("\n")}\n`);
  await expect(finish).toBeDisabled();
  const stored = await page.evaluate(() => ({ local: { ...localStorage }, session: { ...sessionStorage } }));
  for (const secret of [setupKey, ...recoveryCodes]) {
    expect(JSON.stringify(stored)).not.toContain(secret);
    expect(page.url()).not.toContain(secret);
    expect(logs.join("\n")).not.toContain(secret);
  }
  await page.getByRole("checkbox", { name: "I have saved these recovery codes in a secure place." }).check();
  await finish.click();
  await expect(page.getByRole("heading", { name: "Account security", level: 1 })).toBeVisible();
  await expect(page.getByText(recoveryCodes[0], { exact: true })).toHaveCount(0);
  expect(mutations(state, "/activate")).toHaveLength(1);
  await page.reload();
  await expect(page.getByRole("heading", { name: "Account security", level: 1 })).toBeVisible();
  await expect(page.getByText(recoveryCodes[0], { exact: true })).toHaveCount(0);
});

test("lost activation confirmation reads status and requires explicit challenge and regeneration", async ({ page }) => {
  const state = await mockSecurity(page);
  state.lostActivation = true;
  await page.goto("/account/security");
  await activate(page);
  await expect(page.getByRole("heading", { name: "Check whether the change completed" })).toBeVisible();
  await page.getByRole("button", { name: "Check current status" }).click();
  await expect(page.getByText("Enabled", { exact: true })).toBeVisible();
  expect(mutations(state, "/activate")).toHaveLength(1);
  expect(mutations(state, "/regenerate")).toHaveLength(0);
  await page.getByRole("button", { name: "Verify this session", exact: true }).click();
  await page.getByLabel("Authenticator code").fill("123456");
  await page.getByRole("button", { name: "Verify code", exact: true }).click();
  expect(mutations(state, "/regenerate")).toHaveLength(0);
  await page.getByRole("button", { name: "Generate new recovery codes", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Replace your recovery codes?" })).toBeVisible();
  expect(mutations(state, "/regenerate")).toHaveLength(0);
  await page.getByRole("button", { name: "Replace recovery codes", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Save your recovery codes" })).toBeVisible();
  expect(mutations(state, "/regenerate")).toHaveLength(1);
});

test("disable and replacement require an explicit destructive confirmation", async ({ page }) => {
  const state = await mockSecurity(page, { ...baseContext, enabled: true, authentication_strength: "multi_factor" });
  await page.goto("/account/security");
  await page.getByRole("button", { name: "Disable or replace authenticator" }).click();
  await expect(page.getByText("Your account will have no local MFA after this action.", { exact: false })).toBeVisible();
  expect(mutations(state, "/disable")).toHaveLength(0);
  await page.getByRole("button", { name: "Disable authenticator", exact: true }).click();
  await expect(page.getByRole("button", { name: "Set up an authenticator", exact: true })).toBeVisible();
  expect(mutations(state, "/disable")).toHaveLength(1);
  expect(mutations(state, "/setup")).toHaveLength(0);
});

test("fresh password proof and MFA challenge return without replaying the security action", async ({ page }) => {
  const state = await mockSecurity(page, { ...baseContext, enabled: true, recently_authenticated: false });
  await page.goto("/account/security");
  await page.getByRole("button", { name: "Generate new recovery codes", exact: true }).click();
  await page.getByRole("button", { name: "Replace recovery codes", exact: true }).click();
  await page.getByRole("link", { name: "Confirm your sign-in", exact: true }).click();
  await expect(page).toHaveURL(/\/account\/reauthenticate\?task=security$/);
  await page.getByLabel("Current password", { exact: false }).fill("wrong-password");
  await page.getByRole("button", { name: "Confirm password" }).click();
  await expect(page.getByText("That password could not be verified. Try again.")).toBeVisible();
  await expect(page.getByLabel("Current password", { exact: false })).toHaveValue("");
  await page.getByLabel("Current password", { exact: false }).fill("current-password");
  await page.getByRole("button", { name: "Confirm password" }).click();
  await page.getByRole("button", { name: "Use a recovery code instead" }).click();
  await page.getByRole("textbox", { name: "Recovery code", exact: true }).fill("ABCD-EFGH");
  await page.getByRole("button", { name: "Verify code" }).click();
  await expect(page.getByRole("heading", { name: "Your proof is ready" })).toBeVisible();
  await page.getByRole("link", { name: "Return to your task" }).click();
  await expect(page.getByRole("heading", { name: "Account security", level: 1 })).toBeVisible();
  expect(mutations(state, "/regenerate")).toHaveLength(1);
  expect(mutations(state, "/login")).toHaveLength(0);
  expect(mutations(state, "/challenge")[0]?.body).toEqual({ code: "ABCD-EFGH" });
  await expect(page.getByText("Private server detail")).toHaveCount(0);
});

test("external fresh proof uses the original provider and an explicit closed task", async ({ page }) => {
  const state = await mockSecurity(page, { ...baseContext, authentication_method: "oidc", recently_authenticated: false });
  await page.route("https://sso.example/reauthenticate", (route) => route.fulfill({ contentType: "text/html", body: "<h1>Provider proof</h1>" }));
  await page.goto("/account/reauthenticate?task=connect-provider&return_to=https://attacker.example#external_login=failed");
  await expect(page.getByText("Your provider sign-in could not be confirmed.", { exact: false })).toBeVisible();
  await expect(page).toHaveURL(/\/account\/reauthenticate\?task=connect-provider$/);
  expect(mutations(state, "/external")).toHaveLength(0);
  await expect(page.getByLabel("Current password", { exact: false })).toHaveCount(0);
  await page.getByRole("button", { name: "Continue with your original provider" }).click();
  await expect(page.getByRole("heading", { name: "Provider proof" })).toBeVisible();
  expect(mutations(state, "/external")[0]?.body).toEqual({ task: "connect-provider" });
});

test("recovery restriction remains visible with management disabled and offers only sign out", async ({ page }) => {
  const state = await mockSecurity(page, { ...baseContext, service_enabled: false, mfa_recovery_required: true });
  await page.goto("/account/security");
  await expect(page.getByText("Your institution requires you to set up a new authenticator", { exact: false })).toBeVisible();
  await expect(page.getByText("Authenticator management is currently disabled", { exact: false })).toBeVisible();
  await expect(page.getByRole("button", { name: "Set up an authenticator", exact: true })).toHaveCount(0);
  await expect(page.getByRole("link", { name: "Back to sign-in status" })).toHaveCount(0);
  expect(state.requests.some(({ path }) => path === "/api/v1/users/me")).toBe(false);
  await page.goto("/account/reauthenticate?task=connect-provider");
  await expect(page.getByRole("link", { name: "Restore account access" })).toHaveAttribute("href", "/account/security");
  expect(state.requests.filter(({ method }) => method === "POST")).toHaveLength(0);
});

test("pending enrollment restarts explicitly and invalid codes keep the setup key available", async ({ page }) => {
  const state = await mockSecurity(page, { ...baseContext, pending: true });
  await page.goto("/account/security");
  await expect(page.getByText("Restarting replaces its setup key", { exact: false })).toBeVisible();
  expect(mutations(state, "/setup")).toHaveLength(0);
  await page.getByRole("button", { name: "Restart authenticator setup" }).click();
  await page.getByLabel("Authenticator code", { exact: false }).fill("000000");
  await page.getByRole("button", { name: "Activate authenticator" }).click();
  await expect(page.getByText("That code could not be verified.", { exact: false })).toBeVisible();
  await expect(page.getByLabel("Setup key", { exact: true })).toHaveValue(setupKey);
  await expect(page.getByLabel("Authenticator code", { exact: false })).toHaveValue("");
  expect(mutations(state, "/setup")).toHaveLength(1);
});

test("a response arriving after page exit cannot restore one-time codes on history restoration", async ({ page }) => {
  const state = await mockSecurity(page);
  let release!: () => void;
  let observed!: () => void;
  const waiting = new Promise<void>((resolve) => { release = resolve; });
  const requested = new Promise<void>((resolve) => { observed = resolve; });
  await page.route("**/api/v1/users/me/mfa/activate", async (route) => {
    observed();
    await waiting;
    state.context.enabled = true; state.context.pending = false;
    await route.fulfill({ contentType: "application/json", body: JSON.stringify({ recovery_codes: recoveryCodes }) });
  });
  await page.goto("/account/security");
  await page.getByRole("button", { name: "Set up an authenticator", exact: true }).click();
  await page.getByLabel("Authenticator code", { exact: false }).fill("123456");
  await page.getByRole("button", { name: "Activate authenticator" }).click();
  await requested;
  await page.evaluate(() => { window.dispatchEvent(new PageTransitionEvent("pagehide", { persisted: true })); });
  const response = page.waitForResponse("**/api/v1/users/me/mfa/activate");
  release();
  await response;
  await page.evaluate(() => { window.dispatchEvent(new PageTransitionEvent("pageshow", { persisted: true })); });
  await expect(page.getByText("Enabled", { exact: true })).toBeVisible();
  await expect(page.getByText(recoveryCodes[0], { exact: true })).toHaveCount(0);
  await expect(page.getByLabel("Setup key", { exact: true })).toHaveCount(0);
});

test("a missing Session exposes sign in and a read failure can be retried", async ({ page }) => {
  const state = await mockSecurity(page);
  state.signedOut = true;
  await page.goto("/account/security");
  await expect(page.getByRole("heading", { name: "Sign in to manage your security" })).toBeVisible();
  await expect(page.getByRole("link", { name: "Sign in", exact: true })).toHaveAttribute("href", "/login");
  state.signedOut = false;
  await page.route("**/api/v1/users/me/mfa", async (route) => {
    await page.unroute("**/api/v1/users/me/mfa");
    await route.fulfill({ status: 503 });
  });
  await page.reload();
  await expect(page.getByRole("heading", { name: "We couldn’t check your security" })).toBeVisible();
  await page.getByRole("button", { name: "Check current status" }).click();
  await expect(page.getByRole("heading", { name: "Account security", exact: true })).toBeFocused();
});

test("restricted password login goes directly to reenrollment", async ({ page }) => {
  const state = await mockSecurity(page, { ...baseContext, mfa_recovery_required: true });
  await page.route("**/api/v1/discovery", (route) => route.fulfill({ contentType: "application/json", body: JSON.stringify({
    discovery_version: 1, canonical_origin: "http://127.0.0.1:5173", initialized: true,
    capabilities: { local_login: true, public_registration: false, invitation_admission: false, desktop_authorization: true },
    desktop_authorization: { protocol: "proctor-desktop-authorization", minimum_version: 1, maximum_version: 1 },
    institution: { id: "institution", name: "institution", display_name: "Institution" }, providers: [],
  }) }));
  await page.route("**/api/v1/auth/login", (route) => route.fulfill({ contentType: "application/json", body: JSON.stringify({ session: { id: "restricted", client_type: "web", mfa_recovery_required: true } }) }));
  await page.goto("/login");
  await page.getByLabel("Email or username").fill("person");
  await page.locator("#password").fill("current-password");
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await expect(page).toHaveURL(/\/account\/security$/);
  await expect(page.getByText("Your institution requires you to set up a new authenticator", { exact: false })).toBeVisible();
  expect(state.requests.some(({ path }) => path === "/api/v1/users/me")).toBe(false);
});

test("sign-in status offers the bounded security destination for ordinary and restricted Sessions", async ({ page }) => {
  const state = await mockSecurity(page);
  await page.goto("/authorization/complete");
  await expect(page.getByRole("link", { name: "Account security" })).toHaveAttribute("href", "/account/security");
  state.context.mfa_recovery_required = true;
  await page.route("**/api/v1/users/me", (route) => route.fulfill({ status: 401, contentType: "application/problem+json", body: JSON.stringify({
    type: "about:blank", title: "Private recovery detail", status: 401, code: "authentication.invalid_token",
  }) }));
  await page.reload();
  await expect(page.getByRole("heading", { name: "Restore account access" })).toBeVisible();
  await expect(page.getByRole("link", { name: "Restore account access" })).toHaveAttribute("href", "/account/security");
  await expect(page.getByText("Private recovery detail")).toHaveCount(0);
});

test("fresh proof remains usable with expanded copy, dark mode, and 200 percent zoom", async ({ browserName, page }, testInfo) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.emulateMedia({ colorScheme: "dark", reducedMotion: "reduce" });
  await mockSecurity(page, { ...baseContext, recently_authenticated: false });
  await page.goto("/account/reauthenticate?task=security");
  await expect(page.getByRole("heading", { name: "Confirm your sign-in" })).toBeVisible();
  await page.keyboard.press(browserName === "webkit" ? "Alt+Tab" : "Tab");
  await page.keyboard.press("Enter");
  await expect(page.getByRole("main")).toBeFocused();
  await page.getByRole("heading", { level: 1 }).evaluate((heading) => {
    heading.textContent = `${heading.textContent} — ${heading.textContent} — ${heading.textContent}`;
  });
  await page.evaluate(() => { document.body.style.zoom = "2"; });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth + 1)).toBe(true);
  await page.screenshot({ path: testInfo.outputPath("reauthentication-expanded-dark.png"), fullPage: true });
  await page.locator("#current-password").fill("current-password");
  await page.getByRole("button", { name: "Confirm password" }).click();
  await expect(page.getByRole("heading", { name: "Your proof is ready" })).toBeVisible();
  await expect(page.getByRole("link", { name: "Return to your task" })).toHaveAttribute("href", "/account/security");
});

for (const viewport of [{ width: 390, height: 844 }, { width: 768, height: 1024 }, { width: 1440, height: 1024 }]) {
  for (const colorScheme of ["light", "dark"] as const) {
    test(`security lifecycle fits ${viewport.width}px ${colorScheme} with keyboard and zoom`, async ({ browserName, page }, testInfo) => {
      await page.setViewportSize(viewport);
      await page.emulateMedia({ colorScheme, reducedMotion: "reduce" });
      await mockSecurity(page);
      await page.goto("/account/security");
      await expect(page.getByRole("heading", { name: "Account security", level: 1 })).not.toBeFocused();
      await page.keyboard.press(browserName === "webkit" ? "Alt+Tab" : "Tab");
      await expect(page.getByRole("link", { name: "Skip to account security" })).toBeFocused();
      await page.keyboard.press("Enter");
      await expect(page.getByRole("main")).toBeFocused();
      await activate(page);
      await expect(page.getByRole("heading", { level: 1 })).toHaveCount(1);
      await page.screenshot({ path: testInfo.outputPath(`security-codes-${viewport.width}-${colorScheme}.png`), fullPage: true });
      await page.evaluate(() => { document.body.style.zoom = "2"; });
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth + 1)).toBe(true);
      await page.getByRole("checkbox").focus();
      await page.keyboard.press("Space");
      await page.keyboard.press(browserName === "webkit" ? "Alt+Tab" : "Tab");
      await expect(page.getByRole("button", { name: "Finish and hide codes" })).toBeFocused();
      await page.emulateMedia({ forcedColors: "active" });
      await expect(page.getByRole("button", { name: "Finish and hide codes" })).toBeVisible();
    });
  }
}
