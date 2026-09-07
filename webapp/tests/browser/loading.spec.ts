import { expect, test, type Locator, type Page } from "@playwright/test";

const discovery = {
  discovery_version: 1,
  canonical_origin: "http://127.0.0.1:5173",
  initialized: true,
  capabilities: { local_login: true, public_registration: false, invitation_admission: true, desktop_authorization: true },
  desktop_authorization: { protocol: "proctor-desktop-authorization", minimum_version: 1, maximum_version: 1 },
  institution: { id: "institution-1", name: "preview", display_name: "Institution access" },
  providers: [],
};

async function box(locator: Locator) {
  const bounds = await locator.boundingBox();
  expect(bounds).not.toBeNull();
  return bounds!;
}

async function noOverflow(page: Page) {
  const geometry = await page.evaluate(() => ({
    width: innerWidth,
    scrollWidth: document.documentElement.scrollWidth,
    overflow: Array.from(document.querySelectorAll("body *")).flatMap((node) => {
      const rect = node.getBoundingClientRect();
      return rect.left < -1 || rect.right > innerWidth + 1
        ? [{ tag: node.tagName, className: node.getAttribute("class"), left: rect.left, right: rect.right }]
        : [];
    }),
  }));
  expect(geometry.scrollWidth <= geometry.width + 1, JSON.stringify(geometry)).toBe(true);
}

for (const colorScheme of ["light", "dark"] as const) {
  for (const width of [320, 390, 768, 1440]) {
    test(`sign-in loading reserves only the methods area at ${width}px in ${colorScheme}`, async ({ page }, testInfo) => {
      await page.setViewportSize({ width, height: 1024 });
      await page.emulateMedia({ colorScheme, reducedMotion: "reduce" });
      let release!: () => void;
      const pending = new Promise<void>((resolve) => { release = resolve; });
      await page.route("**/api/v1/discovery", async (route) => {
        await pending;
        await route.fulfill({ contentType: "application/json", body: JSON.stringify(discovery) });
      });
      try {
        await page.goto("/login");
        await page.evaluate(() => document.fonts.ready);
        const main = page.getByRole("main");
        const loader = main.locator("[data-proctor-loading]");
        const heading = page.getByRole("heading", { name: "Sign in", exact: true });
        const aside = page.getByRole("complementary");
        await expect(loader).toBeVisible();
        await expect(loader).toHaveAttribute("role", "status");
        await expect(loader).toContainText("Checking available sign-in methods…");
        await expect(heading).toBeVisible();
        await expect(aside).toContainText("Your institution controls which sign-in methods are available.");
        await expect(page.getByRole("img", { name: "Proctor" })).toBeVisible();
        const headingBefore = await box(heading);
        const asideBefore = await box(aside);
        const loaderBox = await box(loader);
        const mainBox = await box(main);
        const iconBox = await box(loader.locator("svg"));
        expect(loaderBox.y).toBeGreaterThan(headingBefore.y + headingBefore.height);
        expect(loaderBox.height).toBeCloseTo(320, 1);
        expect(loaderBox.x).toBeGreaterThanOrEqual(mainBox.x);
        expect(loaderBox.x + loaderBox.width).toBeLessThanOrEqual(mainBox.x + mainBox.width + 1);
        expect(Math.abs(iconBox.x + iconBox.width / 2 - loaderBox.x - loaderBox.width / 2)).toBeLessThan(1);
        await noOverflow(page);
        await page.screenshot({ path: testInfo.outputPath(`login-loading-${width}-${colorScheme}.png`), fullPage: true });
        await page.locator(".proctor-skip-link").focus();
        release();
        await expect(page.getByLabel("Email or username")).toBeVisible();
        await expect(loader).toHaveCount(0);
        expect(await box(heading)).toEqual(headingBefore);
        expect(await box(aside)).toEqual(asideBefore);
        await expect(page.locator(".proctor-skip-link")).toBeFocused();
        await noOverflow(page);
      } finally { release(); }
    });
  }

  test(`loading fits inline, compact, and message parents in ${colorScheme}`, async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 });
    await page.emulateMedia({ colorScheme, reducedMotion: "no-preference" });
    await page.goto("/tests/fixtures/loading.html");
    const inline = page.getByTestId("inline-parent").locator("[data-proctor-loading]");
    expect((await box(inline)).width).toBe(16);
    const spinner = inline.locator("svg");
    await expect(spinner).toHaveCSS("animation-duration", "0.96s");
    expect(await spinner.evaluate((node) => getComputedStyle(node).animationName)).not.toBe("none");
    for (const name of ["compact", "message"]) {
      const parent = page.getByTestId(`${name}-parent`);
      const parentBox = await box(parent);
      const child = parent.locator("[data-proctor-loading]");
      const childBox = await box(child);
      expect(childBox.x).toBeGreaterThanOrEqual(parentBox.x);
      expect(childBox.y).toBeGreaterThanOrEqual(parentBox.y);
      expect(childBox.x + childBox.width).toBeLessThanOrEqual(parentBox.x + parentBox.width + 1);
      expect(childBox.y + childBox.height).toBeLessThanOrEqual(parentBox.y + parentBox.height + 1);
      if (name === "message") {
        const labelBox = await box(child.locator("span").last());
        expect(labelBox.x).toBeGreaterThanOrEqual(parentBox.x);
        expect(labelBox.x + labelBox.width).toBeLessThanOrEqual(parentBox.x + parentBox.width + 1);
      }
      const inherited = await parent.evaluate((node) => getComputedStyle(node).color);
      await expect(child.locator("svg")).toHaveCSS("color", inherited);
    }
    await page.emulateMedia({ reducedMotion: "reduce", forcedColors: "active" });
    await expect(spinner).toHaveCSS("animation-name", "none");
    await expect(inline).toContainText("Loading inline…");
    await page.evaluate(() => { document.body.style.zoom = "2"; document.documentElement.dir = "rtl"; });
    await noOverflow(page);
  });
}

test("every button variant preserves its footprint and accessible name while loading", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto("/tests/fixtures/loading.html");
  await page.evaluate(() => document.fonts.ready);
  for (const variant of ["primary", "secondary", "text"]) {
    const button = page.getByTestId(`button-${variant}`);
    const finish = page.getByTestId(`finish-${variant}`);
    const before = await box(button);
    const neighbor = await box(finish);
    await button.click();
    await expect(button).toBeDisabled();
    await expect(button).toHaveAttribute("aria-busy", "true");
    await expect(button).toHaveAccessibleName("Saving your changes and waiting for confirmation…");
    await expect(button.locator("[data-proctor-loading]")).toBeVisible();
    await expect(button.locator("[data-proctor-icon=loading]")).toBeVisible();
    expect(await box(button)).toEqual(before);
    expect(await box(finish)).toEqual(neighbor);
    await expect(button.locator("[aria-hidden=true]").first()).toHaveCSS("visibility", "hidden");
    await expect(button.getByRole("status")).toHaveCount(0);
    await expect(page.getByTestId(`action-${variant}`).locator("output")).toHaveText("1");
    await finish.click();
    await expect(button).toBeEnabled();
    await expect(button).toHaveAccessibleName("Save these account preferences");
    expect(await box(button)).toEqual(before);
  }
});

test("a discovery retry uses the reserved area and leaves focus alone while pending", async ({ page }) => {
  let release!: () => void;
  let requests = 0;
  const pending = new Promise<void>((resolve) => { release = resolve; });
  await page.route("**/api/v1/discovery", async (route) => {
    requests += 1;
    if (requests === 1) return route.fulfill({ status: 503 });
    await pending;
    await route.fulfill({ contentType: "application/json", body: JSON.stringify(discovery) });
  });
  try {
    await page.goto("/login");
    const heading = page.getByRole("heading", { name: "Sign in", exact: true });
    await page.evaluate(() => document.fonts.ready);
    const before = await box(heading);
    await page.getByRole("button", { name: "Try again" }).click();
    await expect(page.getByRole("status")).toContainText("Checking available sign-in methods…");
    await expect(heading).not.toBeFocused();
    expect(await box(heading)).toEqual(before);
    release();
    await expect(page.getByLabel("Email or username")).toBeVisible();
    expect(requests).toBe(2);
  } finally { release(); }
});

for (const route of [
  "/setup",
  "/register",
  "/account/security",
  "/account/reauthenticate?task=security",
  "/account/connect-provider",
  "/authorize/desktop?request=desktop-handle&state=desktop-state#proof=private-browser-proof",
  "/join#token=private-invitation-claim",
  "/authorization/complete",
]) {
  test(`${route} keeps initial loading inside its task region`, async ({ page }, testInfo) => {
    await page.setViewportSize({ width: 390, height: 844 });
    await page.emulateMedia({ colorScheme: "dark", reducedMotion: "reduce" });
    let release!: () => void;
    const pending = new Promise<void>((resolve) => { release = resolve; });
    await page.route("**/api/v1/**", async (request) => {
      await pending;
      await request.fulfill({ status: 503 });
    });
    try {
      await page.goto(route);
      const main = page.getByRole("main");
      const loader = main.locator("[data-proctor-loading]");
      await expect(loader).toBeVisible();
      await expect(page.getByRole("heading", { level: 1 })).toHaveCount(1);
      await expect(page.getByRole("img", { name: "Proctor" })).toBeVisible();
      await expect(page.locator('[aria-live="polite"]')).toHaveCount(1);
      const mainBox = await box(main);
      const loaderBox = await box(loader);
      expect(loaderBox.x).toBeGreaterThanOrEqual(mainBox.x);
      expect(loaderBox.x + loaderBox.width).toBeLessThanOrEqual(mainBox.x + mainBox.width + 1);
      expect(loaderBox.y).toBeGreaterThanOrEqual(mainBox.y);
      expect(loaderBox.y + loaderBox.height).toBeLessThanOrEqual(mainBox.y + mainBox.height + 1);
      await page.screenshot({ path: testInfo.outputPath("pending-task.png"), fullPage: true });
      await page.evaluate(() => { document.body.style.zoom = "2"; document.documentElement.dir = "rtl"; });
      await noOverflow(page);
      release();
      await expect(loader).toHaveCount(0);
    } finally { release(); }
  });
}
