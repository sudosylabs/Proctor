import { expect, test } from "@playwright/test";

const fixturePath = "/tests/fixtures/document-foundation.html";

const acceptanceViewports = [
  { name: "compact", width: 320, height: 568 },
  { name: "tablet", width: 768, height: 1024 },
  { name: "desktop", width: 1440, height: 900 },
] as const;

test("the production login entry keeps its root fully themed and initialized", async ({
  page,
}) => {
  await page.route("**/api/v1/discovery", async (route) => {
    await route.fulfill({
      contentType: "application/json",
      body: JSON.stringify({
        discovery_version: 1,
        canonical_origin: "http://127.0.0.1:5173",
        initialized: true,
        capabilities: {
          local_login: true,
          public_registration: false,
          invitation_admission: true,
          desktop_authorization: true,
        },
        desktop_authorization: {
          protocol: "proctor-desktop-authorization",
          minimum_version: 1,
          maximum_version: 1,
        },
        providers: [],
      }),
    });
  });
  await page.goto("/login");

  await expect(page.locator("html")).toHaveAttribute("lang", "en");
  await expect(page.locator("html")).toHaveAttribute("dir", "ltr");
  await expect(page).toHaveTitle("Sign in · Proctor");
  const state = await page.evaluate(() => {
    const root = document.querySelector<HTMLElement>("#root");
    if (root === null) {
      throw new Error("Production root is missing");
    }
    return {
      bodyBackground: getComputedStyle(document.body).backgroundColor,
      rootBackground: getComputedStyle(root).backgroundColor,
      rootHeight: root.getBoundingClientRect().height,
      viewportHeight: window.innerHeight,
      themeMetadataCount: document.querySelectorAll(
        'meta[name="theme-color"]',
      ).length,
    };
  });
  expect(state.bodyBackground).toBe(state.rootBackground);
  expect(state.rootHeight).toBeGreaterThanOrEqual(state.viewportHeight);
  expect(state.themeMetadataCount).toBe(2);
});

for (const viewport of acceptanceViewports) {
  test(`${viewport.name} canvas remains full-height and one-dimensional at 200% zoom`, async ({
    page,
  }) => {
    await page.setViewportSize(viewport);
    await page.goto(fixturePath);

    await expect(page.locator("html")).toHaveAttribute("lang", "en");
    await expect(page.locator("html")).toHaveAttribute("dir", "ltr");
    await expect(page).toHaveTitle("Document foundation · Proctor");

    const foundation = await page.evaluate(() => {
      const rootStyle = getComputedStyle(document.documentElement);
      const bodyStyle = getComputedStyle(document.body);
      const root = document.querySelector<HTMLElement>("#root");
      const media = document.querySelector<SVGElement>('[data-testid="media"]');
      const pre = document.querySelector<HTMLElement>('[data-testid="preformatted"]');
      const table = document.querySelector<HTMLTableElement>("table");
      const input = document.querySelector<HTMLInputElement>("input");
      const headings = Array.from(
        document.querySelectorAll<HTMLHeadingElement>("h1, h2, h3, h4, h5, h6"),
      );
      const proseLink = document.querySelector<HTMLAnchorElement>("main a");
      const longText = document.querySelector<HTMLElement>('[data-testid="long-text"]');
      const small = document.querySelector<HTMLElement>("small");
      const strong = document.querySelector<HTMLElement>("strong");
      const paragraph = document.querySelector<HTMLElement>("p");
      if (
        root === null ||
        media === null ||
        pre === null ||
        table === null ||
        input === null ||
        headings.length !== 6 ||
        proseLink === null ||
        longText === null ||
        small === null ||
        strong === null ||
        paragraph === null
      ) {
        throw new Error("Foundation fixture is incomplete");
      }
      return {
        background: bodyStyle.backgroundColor,
        canvas: rootStyle.backgroundColor,
        foreground: bodyStyle.color,
        rootForeground: getComputedStyle(root).color,
        bodyHeight: document.body.getBoundingClientRect().height,
        rootHeight: root.getBoundingClientRect().height,
        mediaWidth: media.getBoundingClientRect().width,
        mainWidth: media.parentElement?.getBoundingClientRect().width ?? 0,
        preOwnsOverflow: pre.scrollWidth > pre.clientWidth,
        tableCollapse: getComputedStyle(table).borderCollapse,
        controlFont: getComputedStyle(input).fontFamily,
        bodyFont: bodyStyle.fontFamily,
        headingSizes: headings.map((heading) =>
          Number.parseFloat(getComputedStyle(heading).fontSize),
        ),
        linkDecoration: getComputedStyle(proseLink).textDecorationLine,
        longTextFits: longText.scrollWidth <= longText.clientWidth,
        smallSize: Number.parseFloat(getComputedStyle(small).fontSize),
        bodySize: Number.parseFloat(bodyStyle.fontSize),
        strongWeight: getComputedStyle(strong).fontWeight,
        monoFont: getComputedStyle(pre).fontFamily,
        selectionBackground: getComputedStyle(paragraph, "::selection")
          .backgroundColor,
      };
    });

    expect(foundation.background).toBe(foundation.canvas);
    expect(foundation.foreground).toBe(foundation.rootForeground);
    expect(foundation.bodyHeight).toBeGreaterThanOrEqual(viewport.height);
    expect(foundation.rootHeight).toBeGreaterThanOrEqual(viewport.height);
    expect(foundation.mediaWidth).toBeLessThanOrEqual(foundation.mainWidth);
    expect(foundation.preOwnsOverflow).toBe(true);
    expect(foundation.tableCollapse).toBe("collapse");
    expect(foundation.controlFont).toBe(foundation.bodyFont);
    expect(foundation.headingSizes[0]).toBeGreaterThan(foundation.headingSizes[1] ?? 0);
    expect(foundation.headingSizes[1]).toBeGreaterThan(foundation.headingSizes[2] ?? 0);
    expect(foundation.headingSizes[2]).toBe(foundation.headingSizes[3]);
    expect(foundation.headingSizes[3]).toBeGreaterThan(foundation.headingSizes[4] ?? 0);
    expect(foundation.headingSizes[4]).toBe(foundation.headingSizes[5]);
    expect(foundation.linkDecoration).toContain("underline");
    expect(foundation.longTextFits).toBe(true);
    expect(foundation.smallSize).toBeLessThan(foundation.bodySize);
    expect(foundation.strongWeight).toBe("700");
    expect(foundation.monoFont).toContain("IBM Plex Mono");
    expect(foundation.selectionBackground).not.toBe(foundation.background);

    await page.evaluate(() => {
      document.body.style.zoom = "2";
    });
    const overflow = await page.evaluate(() => {
      const viewportWidth = document.documentElement.clientWidth;
      return {
        amount: document.documentElement.scrollWidth - viewportWidth,
        offenders: Array.from(document.querySelectorAll<HTMLElement>("body *"))
          .map((element) => ({
            element: element.tagName.toLowerCase(),
            id: element.id,
            right: element.getBoundingClientRect().right,
            containedOverflow: element.closest("pre") !== null,
          }))
          .filter(
            ({ containedOverflow, right }) =>
              !containedOverflow && right > viewportWidth + 1,
          ),
      };
    });
    expect(overflow.amount, JSON.stringify(overflow.offenders)).toBeLessThanOrEqual(1);
  });
}

test("system and explicit themes synchronize the canvas and browser metadata", async ({
  page,
}) => {
  await page.emulateMedia({ colorScheme: "dark" });
  await page.goto(fixturePath);

  const system = await page.evaluate(() => ({
    background: getComputedStyle(document.body).backgroundColor,
    canvas: getComputedStyle(document.documentElement).backgroundColor,
    colorScheme: getComputedStyle(document.documentElement).colorScheme,
    theme: document.documentElement.getAttribute("data-theme"),
    metadata: Array.from(
      document.querySelectorAll<HTMLMetaElement>('meta[name="theme-color"]'),
      (meta) => ({ content: meta.content, media: meta.media }),
    ),
  }));
  expect(system.background).toBe(system.canvas);
  expect(system.colorScheme).toContain("dark");
  expect(system.theme).toBeNull();
  expect(system.metadata).toHaveLength(2);

  await page.evaluate(() => window.setDocumentFoundationTheme("light"));
  const explicit = await page.evaluate(() => ({
    background: getComputedStyle(document.body).backgroundColor,
    canvas: getComputedStyle(document.documentElement).backgroundColor,
    colorScheme: getComputedStyle(document.documentElement).colorScheme,
    theme: document.documentElement.dataset.theme,
    metadata: Array.from(
      document.querySelectorAll<HTMLMetaElement>('meta[name="theme-color"]'),
      (meta) => ({ content: meta.content, media: meta.media }),
    ),
  }));
  expect(explicit.background).toBe(explicit.canvas);
  expect(explicit.colorScheme).toContain("light");
  expect(explicit.theme).toBe("light");
  expect(explicit.metadata).toHaveLength(1);
  expect(explicit.metadata[0]?.media).toBe("");
});

test("keyboard traversal reveals the skip link and visible focus treatment", async ({
  browserName,
  page,
}) => {
  await page.goto(fixturePath);
  const skipLink = page.getByRole("link", { name: "Skip to main content" });
  const initialBox = await skipLink.boundingBox();
  expect(initialBox).not.toBeNull();
  expect(initialBox?.y).toBeLessThan(0);

  await page.keyboard.press(browserName === "webkit" ? "Alt+Tab" : "Tab");
  await expect(skipLink).toBeFocused();
  const focusedBox = await skipLink.boundingBox();
  expect(focusedBox).not.toBeNull();
  expect(focusedBox?.x).toBeGreaterThanOrEqual(0);
  expect(focusedBox?.y).toBeGreaterThanOrEqual(0);
  await expect(skipLink).toHaveCSS("outline-style", "solid");
  await expect(skipLink).toHaveCSS("outline-width", "3px");
  await expect(skipLink).toHaveCSS("outline-offset", "2px");

  await page.keyboard.press("Enter");
  await expect(page.locator("#main-content")).toBeFocused();
  await page.keyboard.press(browserName === "webkit" ? "Alt+Tab" : "Tab");
  const proseLink = page.getByRole("link", { name: "semantic link" });
  await expect(proseLink).toBeFocused();
  await expect(proseLink).toHaveCSS("outline-width", "3px");
});

test("reduced motion retains final content without smooth document scrolling", async ({
  page,
}) => {
  await page.emulateMedia({ reducedMotion: "reduce" });
  await page.goto(fixturePath);

  const motion = await page.evaluate(() => {
    const root = getComputedStyle(document.documentElement);
    return {
      slowDuration: root.getPropertyValue("--proctor-duration-slow").trim(),
      scrollBehavior: root.scrollBehavior,
      disclosureAvailable: document.querySelector("details p")?.textContent,
    };
  });
  expect(motion.slowDuration).toBe("0.01ms");
  expect(motion.scrollBehavior).toBe("auto");
  expect(motion.disclosureAvailable).toContain("Disclosure content");
});

test("forced colors retains an outlined focus target and named action", async ({
  browserName,
  page,
}) => {
  await page.emulateMedia({ forcedColors: "active" });
  await page.goto(fixturePath);
  await page.keyboard.press(browserName === "webkit" ? "Alt+Tab" : "Tab");

  const skipLink = page.getByRole("link", { name: "Skip to main content" });
  await expect(skipLink).toBeFocused();
  await expect(skipLink).toHaveCSS("outline-style", "solid");
  await expect(skipLink).toHaveCSS("outline-width", "3px");
  await expect(page.getByRole("button", { name: "Native action" })).toBeVisible();
});

test("fatal render failures expose bounded recovery without exception detail", async ({
  page,
}) => {
  await page.goto("/tests/fixtures/fatal-boundary.html");

  await expect(page.getByRole("heading", { name: "Proctor could not load" })).toBeVisible();
  await expect(page.getByText("Reload this page to try again.")).toBeVisible();
  await expect(page.getByRole("button", { name: "Reload page" })).toBeVisible();
  await expect(page.getByText("Fixture render failed")).toHaveCount(0);
});
