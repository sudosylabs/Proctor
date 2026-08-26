import { expect, test, type Page } from "@playwright/test";

const canonicalOrigin = "http://127.0.0.1:5173";

const defaultDiscovery = {
  discovery_version: 1,
  canonical_origin: canonicalOrigin,
  initialized: true,
  capabilities: {
    local_login: true,
    public_registration: true,
    invitation_admission: true,
    desktop_authorization: true,
  },
  desktop_authorization: {},
  institution: {
    id: "institution-1",
    name: "northbridge",
    display_name: "Northbridge Institute",
  },
  providers: [
    {
      id: "university-oidc",
      display_name: "University SSO",
      type: "oidc",
    },
  ],
};

async function mockDiscovery(
  page: Page,
  discovery: Record<string, unknown> = defaultDiscovery,
) {
  await page.route("**/api/v1/discovery", async (route) => {
    await route.fulfill({
      contentType: "application/json",
      body: JSON.stringify(discovery),
    });
  });
}

async function mockSetupStatus(page: Page, initialized = false) {
  await page.route("**/api/v1/bootstrap", async (route) => {
    if (route.request().method() !== "GET") {
      await route.fallback();
      return;
    }
    await route.fulfill({
      contentType: "application/json",
      body: JSON.stringify({ initialized }),
    });
  });
}

for (const colorScheme of ["light", "dark"] as const) {
  test(`${colorScheme} presentation selects the governed favicon and lockup`, async ({
    page,
  }) => {
    await page.emulateMedia({ colorScheme });
    await mockDiscovery(page);
    await page.goto("/login");

    const activeFavicons = await page.evaluate(() =>
      Array.from(
        document.querySelectorAll<HTMLLinkElement>('link[rel="icon"]'),
      )
        .filter((link) => link.media === "" || matchMedia(link.media).matches)
        .map((link) => new URL(link.href).pathname),
    );
    const expectedMark =
      colorScheme === "dark" ? "proctor-mark-white" : "proctor-mark";
    expect(activeFavicons).toHaveLength(2);
    expect(activeFavicons.every((href) => href.includes(expectedMark))).toBe(
      true,
    );
    expect(activeFavicons.some((href) => href.includes("black"))).toBe(false);

    const lockup = page.getByRole("img", { name: "Proctor" });
    await expect(lockup).toBeVisible();
    expect(
      await lockup.evaluate(
        (image) => image.closest("header")?.children.length,
      ),
    ).toBe(1);
    const currentSource = await lockup.evaluate(
      (image: HTMLImageElement) => image.currentSrc,
    );
    if (currentSource.startsWith("data:")) {
      const selectedLockup = decodeURIComponent(currentSource);
      expect(selectedLockup).toContain("fill='#5C00AA'");
      expect(selectedLockup).toContain(
        colorScheme === "dark" ? "fill='#FFFFFF'" : "fill='#161616'",
      );
    } else {
      expect(new URL(currentSource).pathname).toContain(
        colorScheme === "dark"
          ? "proctor-lockup-purple-white"
          : "proctor-lockup",
      );
    }
  });
}

test("setup validates and atomically submits the installation", async ({
  page,
}) => {
  await page.route("**/api/v1/bootstrap", async (route) => {
    if (route.request().method() === "GET") {
      await route.fulfill({
        contentType: "application/json",
        body: JSON.stringify({ initialized: false }),
      });
      return;
    }
    expect(await route.request().postDataJSON()).toEqual({
      bootstrap_secret: "operator-secret",
      institution: {
        name: "northbridge-university",
        display_name: "Northbridge University",
        description: "Examination services",
      },
      administrator: {
        email: "initial-admin@example.edu",
        username: "initial-admin",
        display_name: "Initial Administrator",
      },
      password: "private-bootstrap-password",
    });
    await route.fulfill({
      status: 201,
      contentType: "application/json",
      body: JSON.stringify({ state: {} }),
    });
  });

  await page.goto("/setup#unexpected=private");
  await expect(page).toHaveURL(`${canonicalOrigin}/setup`);
  await expect(page).toHaveTitle("Set up Proctor · Proctor");
  await expect(
    page.getByRole("heading", {
      level: 1,
      name: "Establish this installation",
    }),
  ).toBeVisible();

  await page.getByRole("button", { name: "Create installation" }).click();
  await expect(page.locator("#bootstrap-secret")).toBeFocused();
  await expect(page.getByText("Enter the one-time bootstrap secret.")).toBeVisible();

  await page.locator("#bootstrap-secret").fill("operator-secret");
  await page.getByLabel("Institution name").fill("northbridge-university");
  await page.locator("#institution-display-name").fill("Northbridge University");
  await page.getByLabel("Description (optional)").fill("Examination services");
  await page.getByLabel("Email address").fill("initial-admin@example.edu");
  await page.getByLabel("Username").fill("initial-admin");
  await page
    .getByLabel("Display name (optional)")
    .fill("Initial Administrator");
  await page.locator("#administrator-password").fill("private-bootstrap-password");
  await page.getByRole("button", { name: "Create installation" }).click();

  await expect(
    page.getByRole("heading", { level: 2, name: "Setup is complete" }),
  ).toBeVisible();
  await expect(page.getByRole("link", { name: "Continue to sign in" })).toHaveAttribute(
    "href",
    "/login",
  );
  await expect(page.locator("body")).not.toContainText("operator-secret");
  await expect(page.locator("body")).not.toContainText("private-bootstrap-password");
});

test("setup refuses to replace an initialized installation", async ({ page }) => {
  await mockSetupStatus(page, true);
  await page.goto("/setup");

  await expect(
    page.getByRole("heading", { level: 2, name: "Setup is complete" }),
  ).toBeVisible();
  await expect(page.getByLabel("Bootstrap secret")).toHaveCount(0);
});

test("public registration submits only account input and enters verification", async ({
  page,
}) => {
  await mockDiscovery(page);
  await page.route("**/api/v1/auth/register", async (route) => {
    expect(await route.request().postDataJSON()).toEqual({
      email: "student.one@example.edu",
      username: "student.one",
      password: "private-registration-password",
    });
    await route.fulfill({ status: 202 });
  });

  await page.goto("/register#unexpected=private");
  await expect(page).toHaveURL(`${canonicalOrigin}/register`);
  await expect(page).toHaveTitle("Create an account · Proctor");
  await expect(page.getByText("Northbridge Institute")).toBeVisible();

  await page.getByLabel("Email address").fill("student.one@example.edu");
  await page.getByLabel("Username").fill("student.one");
  await page.locator("#registration-password").fill("private-registration-password");
  await page.getByRole("button", { name: "Create account" }).click();
  await expect(
    page.getByLabel(
      "I understand that registration does not grant institutional access.",
    ),
  ).toBeFocused();

  await page
    .getByLabel(
      "I understand that registration does not grant institutional access.",
    )
    .check();
  await page.getByRole("button", { name: "Create account" }).click();

  await expect(
    page.getByRole("heading", { level: 1, name: "Check your email" }),
  ).toBeVisible();
  await expect(page.getByText("Registration accepted")).toBeVisible();
  await expect(page.locator("body")).not.toContainText(
    "private-registration-password",
  );
});

test("registration explains Invitation admission without accepting account input", async ({
  page,
}) => {
  await mockDiscovery(page, {
    ...defaultDiscovery,
    capabilities: {
      ...defaultDiscovery.capabilities,
      public_registration: false,
    },
  });
  await page.goto("/register");

  await expect(
    page.getByRole("heading", { level: 1, name: "An Invitation is required" }),
  ).toBeVisible();
  await expect(page.getByLabel("Email address")).toHaveCount(0);
});

test("login presents the admitted methods and sanitizes provider failure state", async ({
  page,
}) => {
  await mockDiscovery(page);
  await page.goto("/login#external_login=failed");

  await expect(page).toHaveURL(`${canonicalOrigin}/login`);
  await expect(page).toHaveTitle("Sign in · Proctor");
  await expect(page.getByRole("heading", { level: 1, name: "Sign in" })).toBeVisible();
  await expect(page.getByText("Northbridge Institute")).toBeVisible();
  await expect(page.getByLabel("Email or username")).toBeVisible();
  await expect(page.locator("#password")).toHaveAttribute(
    "autocomplete",
    "current-password",
  );
  await expect(
    page.getByText(
      "External sign-in wasn’t completed. Choose a method to try again.",
    ),
  ).toBeVisible();
  await expect(
    page.locator('[data-proctor-icon="information"]'),
  ).toBeVisible();
  await expect(
    page.getByRole("button", { name: "Continue with University SSO" }),
  ).toBeVisible();
  await expect(page.getByRole("link", { name: "Create an account" })).toBeVisible();
});

test("local validation focuses the first invalid field", async ({ page }) => {
  await mockDiscovery(page, { ...defaultDiscovery, providers: [] });
  await page.goto("/login");
  await page.getByRole("button", { name: "Sign in" }).click();

  await expect(page.getByLabel("Email or username")).toBeFocused();
  await expect(page.getByText("Enter your email or username.")).toBeVisible();
  await expect(page.getByText("Enter your password.")).toBeVisible();
});

test("local login enters MFA without placing credentials in the URL", async ({ page }) => {
  await mockDiscovery(page, { ...defaultDiscovery, providers: [] });
  await page.route("**/api/v1/auth/login", async (route) => {
    await route.fulfill({
      status: 401,
      contentType: "application/problem+json",
      body: JSON.stringify({
        type: "/problems/mfa-required",
        title: "MFA required",
        status: 401,
        code: "authentication.mfa.required",
      }),
    });
  });
  await page.goto("/login");
  await page.getByLabel("Email or username").fill("student@example.edu");
  await page.locator("#password").fill("private-password");
  await page.getByRole("button", { name: "Sign in" }).click();

  await expect(page.getByLabel("Authentication code")).toBeFocused();
  await expect(page.locator("#mfa-code")).toHaveAttribute("required", "");
  await expect(
    page.locator('label[for="mfa-code"] [data-required-indicator]'),
  ).toHaveText("*");
  await expect(page).toHaveURL(`${canonicalOrigin}/login`);
  await expect(page.locator("body")).not.toContainText("private-password");
});

test("successful local login replaces the form with Session confirmation", async ({
  page,
}) => {
  await mockDiscovery(page, { ...defaultDiscovery, providers: [] });
  await page.route("**/api/v1/auth/login", async (route) => {
    expect(await route.request().postDataJSON()).toEqual({
      login_id: "student@example.edu",
      password: "private-password",
      client_type: "web",
    });
    await route.fulfill({
      contentType: "application/json",
      body: JSON.stringify({ session: {} }),
    });
  });
  await page.route("**/api/v1/users/me", async (route) => {
    await route.fulfill({
      contentType: "application/json",
      body: JSON.stringify({
        id: "user-1",
        username: "student",
        display_name: "Student",
      }),
    });
  });
  await page.goto("/login");
  await page.getByLabel("Email or username").fill("student@example.edu");
  await page.locator("#password").fill("private-password");
  await page.getByRole("button", { name: "Sign in" }).click();

  await expect(page).toHaveURL(`${canonicalOrigin}/authorization/complete`);
  await expect(
    page.getByRole("heading", { level: 1, name: "You’re signed in" }),
  ).toBeVisible();
  await expect(page.getByText("Session confirmed")).toBeVisible();
  await expect(page.locator("body")).not.toContainText("Student");
});

test("Session confirmation distinguishes 401 from a retryable failure", async ({
  page,
}) => {
  let attempt = 0;
  await page.route("**/api/v1/users/me", async (route) => {
    attempt += 1;
    if (attempt === 1) {
      await route.fulfill({
        status: 500,
        contentType: "application/problem+json",
        body: JSON.stringify({
          type: "/problems/internal",
          title: "Internal failure",
          status: 500,
          code: "authentication.internal",
        }),
      });
      return;
    }
    await route.fulfill({
      status: 401,
      contentType: "application/problem+json",
      body: JSON.stringify({
        type: "/problems/authentication-required",
        title: "Authentication required",
        status: 401,
        code: "authentication.required",
      }),
    });
  });
  await page.goto("/authorization/complete#private-state");

  await expect(page).toHaveURL(`${canonicalOrigin}/authorization/complete`);
  await expect(
    page.getByRole("heading", { name: "We couldn’t check your sign-in" }),
  ).toBeVisible();
  await page.getByRole("button", { name: "Try again" }).click();
  await expect(
    page.getByRole("heading", { name: "You’re not signed in" }),
  ).toBeVisible();
  await expect(page.getByRole("link", { name: "Sign in" })).toHaveAttribute(
    "href",
    "/login",
  );
  expect(attempt).toBe(2);
});

for (const colorScheme of ["light", "dark"] as const) {
  test(`login follows the ${colorScheme} system theme`, async ({ page }) => {
    await page.emulateMedia({ colorScheme });
    await mockDiscovery(page, { ...defaultDiscovery, providers: [] });
    await page.goto("/login#external_login=failed");
    await expect(page.locator('button[type="submit"]')).toBeVisible();

    const colors = await page.evaluate(() => ({
      canvas: getComputedStyle(document.documentElement).backgroundColor,
      body: getComputedStyle(document.body).backgroundColor,
      primary: getComputedStyle(
        document.querySelector<HTMLButtonElement>('button[type="submit"]')!,
      ).backgroundColor,
      primaryText: getComputedStyle(
        document.querySelector<HTMLButtonElement>('button[type="submit"]')!,
      ).color,
      onPrimary: getComputedStyle(document.documentElement)
        .getPropertyValue("--proctor-color-action-on-primary")
        .trim(),
      information: getComputedStyle(
        document.querySelector<SVGElement>(
          '[data-proctor-icon="information"]',
        )!,
      ).color,
      informationToken: getComputedStyle(document.documentElement)
        .getPropertyValue("--proctor-color-state-info")
        .trim(),
    }));
    expect(colors.body).toBe(colors.canvas);
    expect(colors.primary).not.toBe(colors.canvas);
    expect(colors.primaryText).toBe(
      colorToRGB(colors.onPrimary),
    );
    expect(colors.information).toBe(colorToRGB(colors.informationToken));
  });
}

test("the owned icon vocabulary preserves visible labels and disclosure state", async ({
  page,
}) => {
  await mockDiscovery(page, { ...defaultDiscovery, providers: [] });
  await page.goto("/login");

  const loginPassword = page.locator("#password");
  const loginPasswordToggle = page.locator('button[aria-controls="password"]');
  await expect(loginPasswordToggle).toHaveAccessibleName("Show password");
  await expect(loginPasswordToggle).toHaveAttribute("title", "Show password");
  await expect(loginPasswordToggle).toHaveText("");
  await expect(loginPassword).toHaveAttribute("type", "password");
  await loginPasswordToggle.click();
  await expect(loginPasswordToggle).toHaveAccessibleName("Hide password");
  await expect(loginPasswordToggle).toHaveAttribute("title", "Hide password");
  await expect(loginPassword).toHaveAttribute("type", "text");

  await mockSetupStatus(page);
  await page.goto("/setup");

  await expect(page.locator('[data-proctor-icon="information"]')).toBeVisible();
  await expect(page.locator('[data-proctor-icon="warning"]')).toBeVisible();

  const secretToggle = page.locator('button[aria-controls="bootstrap-secret"]');
  await expect(secretToggle).toHaveAccessibleName("Show bootstrap secret");
  await expect(secretToggle).toHaveAttribute("title", "Show bootstrap secret");
  await expect(secretToggle).toHaveText("");
  await expect(
    secretToggle.locator('[data-proctor-icon="showPassword"]'),
  ).toBeVisible();
  await secretToggle.click();
  await expect(secretToggle).toHaveAccessibleName("Hide bootstrap secret");
  await expect(
    secretToggle.locator('[data-proctor-icon="hidePassword"]'),
  ).toBeVisible();

  await mockDiscovery(page);
  await page.goto("/register");
  await expect(page.locator('[data-proctor-icon="mail"]')).toBeVisible();
  const passwordToggle = page.locator(
    'button[aria-controls="registration-password"]',
  );
  await expect(passwordToggle).toHaveAccessibleName("Show password");
  await expect(passwordToggle).toHaveAttribute("title", "Show password");
  await expect(passwordToggle).toHaveText("");
  await expect(
    passwordToggle.locator('[data-proctor-icon="showPassword"]'),
  ).toBeVisible();
  await passwordToggle.click();
  await expect(passwordToggle).toHaveAccessibleName("Hide password");
  await expect(
    passwordToggle.locator('[data-proctor-icon="hidePassword"]'),
  ).toBeVisible();
});

test("required controls are marked and compound controls stay optically aligned", async ({
  page,
}) => {
  await mockDiscovery(page, { ...defaultDiscovery, providers: [] });
  await page.goto("/login");
  for (const id of ["login-id", "password"]) {
    await expect(page.locator(`#${id}`)).toHaveAttribute("required", "");
    await expect(
      page.locator(`label[for="${id}"] [data-required-indicator]`),
    ).toHaveText("*");
  }

  await mockSetupStatus(page);
  await page.goto("/setup");

  for (const id of [
    "bootstrap-secret",
    "institution-name",
    "institution-display-name",
    "administrator-email",
    "administrator-username",
    "administrator-password",
  ]) {
    await expect(page.locator(`#${id}`)).toHaveAttribute("required", "");
    await expect(
      page.locator(`label[for="${id}"] [data-required-indicator]`),
    ).toHaveText("*");
  }
  await expect(page.locator("#institution-description")).not.toHaveAttribute(
    "required",
    "",
  );

  const passwordAlignment = await page.evaluate(() => {
    const input = document.querySelector<HTMLInputElement>(
      "#administrator-password",
    )!;
    const toggle = document.querySelector<HTMLButtonElement>(
      'button[aria-controls="administrator-password"]',
    )!;
    const inputBox = input.getBoundingClientRect();
    const toggleBox = toggle.getBoundingClientRect();
    return Math.abs(
      inputBox.top + inputBox.height / 2 -
        (toggleBox.top + toggleBox.height / 2),
    );
  });
  expect(passwordAlignment).toBeLessThanOrEqual(1);

  await mockDiscovery(page);
  await page.goto("/register");
  for (const id of [
    "registration-email",
    "registration-username",
    "registration-password",
    "registration-acknowledgment",
  ]) {
    await expect(page.locator(`#${id}`)).toHaveAttribute("required", "");
    await expect(
      page.locator(`label[for="${id}"] [data-required-indicator]`),
    ).toHaveText("*");
  }

  const checkboxAlignment = await page.evaluate(() => {
    const checkbox = document.querySelector<HTMLInputElement>(
      "#registration-acknowledgment",
    )!;
    const copy = checkbox.parentElement!.querySelector<HTMLElement>("span")!;
    const checkboxBox = checkbox.getBoundingClientRect();
    const copyBox = copy.getBoundingClientRect();
    const lineHeight = Number.parseFloat(getComputedStyle(copy).lineHeight);
    return Math.abs(
      checkboxBox.top + checkboxBox.height / 2 -
        (copyBox.top + lineHeight / 2),
    );
  });
  expect(checkboxAlignment).toBeLessThanOrEqual(2);
});

for (const pageCase of [
  { route: "/setup", heading: "Establish this installation" },
  { route: "/register", heading: "Create your account" },
] as const) {
  test(`${pageCase.route} follows dark mode and stays one-dimensional when narrow`, async ({
    page,
  }) => {
    await page.setViewportSize({ width: 390, height: 844 });
    await page.emulateMedia({ colorScheme: "dark", reducedMotion: "reduce" });
    if (pageCase.route === "/setup") {
      await mockSetupStatus(page);
    } else {
      await mockDiscovery(page);
    }
    await page.goto(pageCase.route);
    await expect(
      page.getByRole("heading", { level: 1, name: pageCase.heading }),
    ).toBeVisible();

    const theme = await page.evaluate(() => ({
      canvas: getComputedStyle(document.documentElement).backgroundColor,
      body: getComputedStyle(document.body).backgroundColor,
      colorScheme: getComputedStyle(document.documentElement).colorScheme,
    }));
    expect(theme.body).toBe(theme.canvas);
    expect(theme.colorScheme).toBe("dark");

    await page.evaluate(() => {
      document.body.style.zoom = "2";
    });
    const overflow = await page.evaluate(
      () => document.documentElement.scrollWidth - document.documentElement.clientWidth,
    );
    expect(overflow).toBeLessThanOrEqual(1);
  });
}

test("the access shell remains keyboard reachable and one-dimensional at zoom", async ({
  browserName,
  page,
}) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await mockDiscovery(page);
  await page.goto("/login");

  await page.keyboard.press(browserName === "webkit" ? "Alt+Tab" : "Tab");
  const skipLink = page.getByRole("link", { name: "Skip to sign in" });
  await expect(skipLink).toBeFocused();
  await page.keyboard.press("Enter");
  await expect(page.locator("#main-content")).toBeFocused();

  await page.evaluate(() => {
    document.body.style.zoom = "2";
  });
  const overflow = await page.evaluate(
    () => document.documentElement.scrollWidth - document.documentElement.clientWidth,
  );
  expect(overflow).toBeLessThanOrEqual(1);
});

function colorToRGB(color: string): string {
  const value = color.replace("#", "");
  const channels = [0, 2, 4].map((index) =>
    Number.parseInt(value.slice(index, index + 2), 16),
  );
  return `rgb(${channels.join(", ")})`;
}
