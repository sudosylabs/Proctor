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
  desktop_authorization: {
    protocol: "proctor-desktop-authorization",
    minimum_version: 1,
    maximum_version: 1,
  },
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

async function mockCurrentUser(page: Page) {
  await page.route("**/api/v1/users/me", async (route) => {
    await route.fulfill({
      contentType: "application/json",
      body: JSON.stringify({
        id: "user-1",
        username: "student.one",
        display_name: "Student One",
      }),
    });
  });
}

async function mockProviders(page: Page) {
  await page.route("**/api/v1/auth/providers", async (route) => {
    await route.fulfill({
      contentType: "application/json",
      body: JSON.stringify(defaultDiscovery.providers),
    });
  });
}

async function mockDesktopAuthorization(
  page: Page,
  { currentSession = true }: { currentSession?: boolean } = {},
) {
  let authenticated = false;
  let bindings = 0;
  const context = () => ({
    state: authenticated ? "authenticated" : "bound",
    ...(authenticated
      ? {
          account: {
            id: "user-1",
            username: "student.one",
            display_name: "Student One",
          },
        }
      : {}),
    device_name: "Exam laptop",
    expires_at: Date.now() + 300_000,
    local_login_enabled: !authenticated,
    external_providers: authenticated ? [] : defaultDiscovery.providers,
  });

  await page.route(
    "**/api/v1/auth/desktop/authorizations/bind",
    async (route) => {
      bindings += 1;
      expect(await route.request().postDataJSON()).toEqual({
        handle: "desktop-handle",
        browser_proof: "private-browser-proof",
        state: "desktop-state",
      });
      await route.fulfill({ status: 204 });
    },
  );
  await page.route(
    "**/api/v1/auth/desktop/authorizations/context",
    async (route) => {
      await route.fulfill({
        contentType: "application/json",
        body: JSON.stringify(context()),
      });
    },
  );
  await page.route(
    "**/api/v1/auth/desktop/authorizations/authenticate/session",
    async (route) => {
      if (!currentSession) {
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
        return;
      }
      authenticated = true;
      await route.fulfill({
        contentType: "application/json",
        body: JSON.stringify(context()),
      });
    },
  );
  return {
    authenticate() {
      authenticated = true;
    },
    reset() {
      authenticated = false;
    },
    bindingCount() {
      return bindings;
    },
  };
}

async function submissionGeometry(page: Page, firstControl: string) {
  return page.evaluate((selector) => {
    const control = document.querySelector<HTMLElement>(selector)!;
    const button = document.querySelector<HTMLButtonElement>(
      'button[type="submit"]',
    )!;
    const feedback = document.querySelector<HTMLElement>(
      "[data-proctor-form-feedback]",
    )!;
    const documentTop = (element: HTMLElement) =>
      element.getBoundingClientRect().top + window.scrollY;

    return {
      buttonTop: documentTop(button),
      controlTop: documentTop(control),
      feedbackHeight: feedback.getBoundingClientRect().height,
    };
  }, firstControl);
}

async function expectPersistentSubmissionFailure(
  page: Page,
  expectedMessage: string,
  before: Awaited<ReturnType<typeof submissionGeometry>>,
  firstControl: string,
) {
  const feedback = page.locator("[data-proctor-form-feedback]");
  await expect(feedback).toHaveAttribute("role", "status");
  await expect(feedback).toHaveAttribute("aria-live", "polite");
  await expect(feedback).toContainText(expectedMessage);
  await expect(
    feedback.locator('[data-proctor-notice-tone="danger"]'),
  ).toBeVisible();
  await expect(feedback).not.toBeFocused();

  const after = await submissionGeometry(page, firstControl);
  expect(after.controlTop).toBe(before.controlTop);
  expect(after.buttonTop).toBe(before.buttonTop);
  expect(after.feedbackHeight).toBe(before.feedbackHeight);
  expect(
    await feedback.evaluate((element) => {
      const button = element.parentElement?.querySelector('button[type="submit"]');
      return (
        button !== null &&
        (button.compareDocumentPosition(element) &
          Node.DOCUMENT_POSITION_FOLLOWING) !==
          0
      );
    }),
  ).toBe(true);
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
      first_name: "Ada",
      last_name: "Okafor",
      username: "student.one",
      password: "private-registration-password",
    });
    await route.fulfill({ status: 202 });
  });

  await page.goto("/register#unexpected=private");
  await expect(page).toHaveURL(`${canonicalOrigin}/register`);
  await expect(page).toHaveTitle("Create an account · Proctor");
  await expect(page.getByText("Northbridge Institute")).toBeVisible();

  await page.getByRole("button", { name: "Create account" }).click();
  await expect(page.getByLabel("First name")).toBeFocused();

  await page.getByLabel("First name").fill("Ada");
  await page.getByLabel("Last name").fill("Okafor");
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
    page.locator('[data-proctor-notice-tone="warning"]'),
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

test("login failure stays beside the action without moving the credentials", async ({
  page,
}) => {
  await mockDiscovery(page, { ...defaultDiscovery, providers: [] });
  await page.route("**/api/v1/auth/login", async (route) => {
    await route.fulfill({
      status: 401,
      contentType: "application/problem+json",
      body: JSON.stringify({
        type: "/problems/invalid-credentials",
        title: "Credentials were not accepted",
        status: 401,
        code: "authentication.invalid_credentials",
      }),
    });
  });
  await page.goto("/login");
  await page.getByLabel("Email or username").fill("student@example.edu");
  await page.locator("#password").fill("private-password");

  const before = await submissionGeometry(page, "#login-id");
  await page.getByRole("button", { name: "Sign in" }).click();

  await expectPersistentSubmissionFailure(
    page,
    "The email, username, or password wasn’t accepted.",
    before,
    "#login-id",
  );
});

test("setup failure stays below the action without moving the atomic form", async ({
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
    await route.fulfill({
      status: 503,
      contentType: "application/problem+json",
      body: JSON.stringify({
        type: "/problems/installation-unavailable",
        title: "Installation unavailable",
        status: 503,
        code: "installation.unavailable",
      }),
    });
  });
  await page.goto("/setup");
  await page.locator("#bootstrap-secret").fill("operator-secret");
  await page.getByLabel("Institution name").fill("northbridge-university");
  await page.locator("#institution-display-name").fill("Northbridge University");
  await page.getByLabel("Email address").fill("initial-admin@example.edu");
  await page.getByLabel("Username").fill("initial-admin");
  await page.locator("#administrator-password").fill("private-password");

  const before = await submissionGeometry(page, "#bootstrap-secret");
  await page.getByRole("button", { name: "Create installation" }).click();

  await expectPersistentSubmissionFailure(
    page,
    "Proctor couldn’t create the installation. No setup changes were applied. Review the details and try again.",
    before,
    "#bootstrap-secret",
  );
});

test("registration failure stays below the action without moving account input", async ({
  page,
}) => {
  await mockDiscovery(page);
  await page.route("**/api/v1/auth/register", async (route) => {
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
  });
  await page.goto("/register");
  await page.getByLabel("First name").fill("Ada");
  await page.getByLabel("Last name").fill("Okafor");
  await page.getByLabel("Email address").fill("student.one@example.edu");
  await page.getByLabel("Username").fill("student.one");
  await page.locator("#registration-password").fill("private-password");
  await page
    .getByLabel(
      "I understand that registration does not grant institutional access.",
    )
    .check();

  const before = await submissionGeometry(page, "#registration-first-name");
  await page.getByRole("button", { name: "Create account" }).click();

  await expectPersistentSubmissionFailure(
    page,
    "Proctor couldn’t accept your registration request. Try again.",
    before,
    "#registration-first-name",
  );
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
      body: JSON.stringify({
        session: { id: "session-1", client_type: "web" },
      }),
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

test("password recovery keeps its accepted response generic", async ({ page }) => {
  await page.route("**/api/v1/auth/password-reset/request", async (route) => {
    expect(await route.request().postDataJSON()).toEqual({
      email: "student.one@example.edu",
    });
    await route.fulfill({ status: 202 });
  });
  await page.goto("/account/forgot-password");

  await expect(page).toHaveTitle("Reset your password · Proctor");
  await expect(
    page.getByRole("heading", { level: 1, name: "Reset your password" }),
  ).toBeVisible();
  await expect(
    page.getByRole("heading", { level: 2, name: "Request reset link" }),
  ).toBeVisible();
  await page.getByLabel("Email address").fill("student.one@example.edu");
  await page.getByRole("button", { name: "Send reset link" }).click();

  await expect(
    page.getByRole("heading", { level: 1, name: "Check your email" }),
  ).toBeFocused();
  await expect(page.getByRole("status")).toContainText("Check your email");
  await expect(page.getByRole("heading", { level: 1 })).toHaveCount(1);
  await expect(page.getByText("Request accepted")).toBeVisible();
  await expect(page.getByRole("link", { name: "Return to sign in" })).toBeVisible();
  await expect(page.locator("body")).not.toContainText("student.one@example.edu");
});

test("password reset consumes only the sanitized fragment credential", async ({
  page,
}) => {
  await page.route("**/api/v1/auth/password-reset/complete", async (route) => {
    expect(await route.request().postDataJSON()).toEqual({
      token: "private-reset-token",
      password: "new-private-password",
    });
    await route.fulfill({ status: 204 });
  });
  await page.goto("/account/reset-password#token=private-reset-token");

  await expect(page).toHaveURL(`${canonicalOrigin}/account/reset-password`);
  await expect(page).toHaveTitle("Set a new password · Proctor");
  await page.locator("#new-password").fill("new-private-password");
  await page.locator("#confirm-new-password").fill("new-private-password");
  await page.getByRole("button", { name: "Set new password" }).click();

  await expect(
    page.getByRole("heading", { name: "Your password was changed" }),
  ).toBeFocused();
  await expect(page.getByRole("status")).toContainText(
    "Your password was changed",
  );
  await expect(page.locator("body")).not.toContainText("private-reset-token");
  await expect(page.locator("body")).not.toContainText("new-private-password");
});

test("password reset announces an unusable link returned after submission", async ({
  page,
}) => {
  await page.route("**/api/v1/auth/password-reset/complete", async (route) => {
    await route.fulfill({
      status: 400,
      contentType: "application/problem+json",
      body: JSON.stringify({
        type: "/problems/account-token-invalid",
        title: "Reset link unavailable",
        status: 400,
        code: "authentication.account_token.invalid",
      }),
    });
  });
  await page.goto("/account/reset-password#token=private-reset-token");
  await page.locator("#new-password").fill("new-private-password");
  await page.locator("#confirm-new-password").fill("new-private-password");

  await page.getByRole("button", { name: "Set new password" }).click();

  const heading = page.getByRole("heading", {
    level: 1,
    name: "This reset link can’t be used",
  });
  await expect(heading).toBeFocused();
  await expect(page.getByRole("status")).toContainText(
    "This reset link can’t be used",
  );
  await expect(page.locator("body")).not.toContainText("private-reset-token");
  await expect(page.locator("body")).not.toContainText("new-private-password");
});

test("Invitation acceptance exchanges the claim before account creation", async ({
  page,
}) => {
  await mockDiscovery(page);
  await page.route("**/api/v1/auth/browser/invitations", async (route) => {
    expect(await route.request().postDataJSON()).toEqual({
      claim: "private-invitation-claim",
    });
    await route.fulfill({
      status: 201,
      contentType: "application/json",
      body: JSON.stringify({
        handle: "private-browser-handle",
        purpose: "student_class",
        requirement: "account",
        expires_at: Date.now() + 300_000,
      }),
    });
  });
  await page.route("**/api/v1/auth/browser/invitations/accept", async (route) => {
    expect(await route.request().postDataJSON()).toEqual({
      handle: "private-browser-handle",
      first_name: "Ada",
      last_name: "Okafor",
      username: "ada.okafor",
      password: "private-invitation-password",
    });
    await route.fulfill({
      contentType: "application/json",
      body: JSON.stringify({
        invitation_id: "invitation-1",
        user_id: "user-1",
        replayed: false,
      }),
    });
  });
  await page.goto("/join#token=private-invitation-claim");

  await expect(page).toHaveURL(`${canonicalOrigin}/join`);
  await expect(page.getByText("Join Northbridge Institute")).toBeVisible();
  const firstNameBox = await page
    .locator("#invitation-first-name")
    .boundingBox();
  const lastNameBox = await page.locator("#invitation-last-name").boundingBox();
  expect(firstNameBox).not.toBeNull();
  expect(lastNameBox).not.toBeNull();
  expect(lastNameBox!.y).toBeGreaterThanOrEqual(
    firstNameBox!.y + firstNameBox!.height,
  );
  await page.getByLabel("First name (optional)").fill("Ada");
  await page.getByLabel("Last name (optional)").fill("Okafor");
  await page.getByLabel("Username").fill("ada.okafor");
  await page.locator("#invitation-password").fill("private-invitation-password");
  await page.getByRole("button", { name: "Accept invitation" }).click();

  await expect(
    page.getByRole("heading", { name: "Your Invitation is accepted" }),
  ).toBeFocused();
  await expect(page.getByRole("status")).toContainText(
    "Your Invitation is accepted",
  );
  await expect(page.getByRole("button", { name: "Accept invitation" })).toHaveCount(0);
  await expect(page.locator("body")).not.toContainText("private-invitation-claim");
  await expect(page.locator("body")).not.toContainText("private-browser-handle");
  await expect(page.locator("body")).not.toContainText(
    "private-invitation-password",
  );
});

test("Session Invitation preserves its handle while warning about new-tab sign in", async ({
  page,
}) => {
  await mockDiscovery(page);
  await page.route("**/api/v1/auth/browser/invitations", async (route) => {
    await route.fulfill({
      status: 201,
      contentType: "application/json",
      body: JSON.stringify({
        handle: "private-browser-handle",
        purpose: "student_class",
        requirement: "session",
        expires_at: Date.now() + 300_000,
      }),
    });
  });
  await page.route(
    "**/api/v1/auth/browser/invitations/accept-session",
    async (route) => {
      expect(await route.request().postDataJSON()).toEqual({
        handle: "private-browser-handle",
      });
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
    },
  );
  await page.goto("/join#token=private-invitation-claim");

  await page.getByRole("button", { name: "Accept invitation" }).click();

  const signInLink = page.getByRole("link", { name: "Open sign in" });
  await expect(signInLink).toBeVisible();
  await expect(signInLink).toHaveAttribute("target", "_blank");
  await expect(signInLink).toHaveAttribute("rel", "noopener noreferrer");
  await expect(signInLink).toHaveAccessibleDescription(
    "This browser needs an active Session. Open sign in in another tab, then try acceptance again here.",
  );
});

test("Desktop authorization approves the exact sanitized request", async ({
  page,
}) => {
  await mockDiscovery(page);
  const desktop = await mockDesktopAuthorization(page);
  await mockCurrentUser(page);
  await page.route(
    "**/api/v1/auth/desktop/authorizations/approve",
    async (route) => {
      expect(await route.request().postDataJSON()).toEqual({ state: "desktop-state" });
      await route.fulfill({
        contentType: "application/json",
        body: JSON.stringify({
          redirect_url: `${canonicalOrigin}/authorization/complete`,
          expires_at: Date.now() + 60_000,
        }),
      });
    },
  );
  await page.goto(
    "/authorize/desktop?request=desktop-handle&state=desktop-state#proof=private-browser-proof",
  );

  await expect(page).toHaveURL(
    `${canonicalOrigin}/authorize/desktop?state=desktop-state`,
  );
  await expect(
    page.getByRole("heading", { name: "Continue in Proctor Desktop" }),
  ).toBeVisible();
  expect(desktop.bindingCount()).toBe(1);
  await expect(page.getByText("Northbridge Institute")).toBeVisible();
  await expect(page.getByText("student.one")).toHaveAttribute(
    "translate",
    "no",
  );
  await page.getByRole("button", { name: "Continue to desktop" }).click();

  await expect(page).toHaveURL(`${canonicalOrigin}/authorization/complete`);
  await expect(
    page.getByRole("heading", { name: "You’re signed in" }),
  ).toBeVisible();
  await expect(page.locator("body")).not.toContainText("private-browser-proof");
});

test("Desktop cancellation announces and focuses the replacement task", async ({
  page,
}) => {
  await mockDiscovery(page);
  await mockDesktopAuthorization(page);
  await page.route(
    "**/api/v1/auth/desktop/authorizations/cancel",
    async (route) => {
      expect(await route.request().postDataJSON()).toEqual({ state: "desktop-state" });
      await route.fulfill({ status: 204 });
    },
  );
  await page.goto(
    "/authorize/desktop?request=desktop-handle&state=desktop-state#proof=private-browser-proof",
  );

  await page.getByRole("button", { name: "Cancel request" }).click();

  const heading = page.getByRole("heading", {
    level: 1,
    name: "The request was cancelled",
  });
  await expect(heading).toBeFocused();
  await expect(page.getByRole("status")).toContainText(
    "The request was cancelled",
  );
  await expect(
    page.getByRole("button", { name: "Continue to desktop" }),
  ).toHaveCount(0);
});

test("Desktop local authentication remains in the same tab and creates no browser Session", async ({
  page,
}) => {
  await mockDiscovery(page);
  const desktop = await mockDesktopAuthorization(page, {
    currentSession: false,
  });
  await page.route(
    "**/api/v1/auth/desktop/authorizations/authenticate/password",
    async (route) => {
      expect(await route.request().postDataJSON()).toEqual({
        login_id: "student.one",
        password: "private-password",
      });
      desktop.authenticate();
      await route.fulfill({
        contentType: "application/json",
        body: JSON.stringify({
          state: "authenticated",
          account: {
            id: "user-1",
            username: "student.one",
            display_name: "Student One",
          },
          device_name: "Exam laptop",
          expires_at: Date.now() + 300_000,
          local_login_enabled: false,
          external_providers: [],
        }),
      });
    },
  );
  await page.route("**/api/v1/auth/login", async (route) => {
    await route.fulfill({
      status: 500,
    });
  });
  await page.goto(
    "/authorize/desktop?request=desktop-handle&state=desktop-state#proof=private-browser-proof",
  );

  await expect(
    page.getByRole("heading", { name: "Sign in to continue in Proctor Desktop" }),
  ).toBeVisible();
  await page.getByLabel("Email or username").fill("student.one");
  await page.locator("#desktop-password").fill("private-password");
  await page.getByRole("button", { name: "Sign in" }).click();

  await expect(
    page.getByRole("heading", { name: "Continue in Proctor Desktop" }),
  ).toBeVisible();
  expect(page.url()).toBe(`${canonicalOrigin}/authorize/desktop?state=desktop-state`);
});

test("Desktop provider authentication leaves and returns in the same tab", async ({
  page,
}) => {
  await mockDiscovery(page);
  const desktop = await mockDesktopAuthorization(page, {
    currentSession: false,
  });
  await page.route(
    "**/api/v1/auth/desktop/authorizations/authenticate/providers/university-oidc/login?state=desktop-state",
    async (route) => {
      expect(route.request().method()).toBe("GET");
      desktop.authenticate();
      await route.fulfill({
        contentType: "text/html",
        body: `<script>location.replace("/authorize/desktop?state=desktop-state")</script>`,
      });
    },
  );
  await page.goto(
    "/authorize/desktop?request=desktop-handle&state=desktop-state#proof=private-browser-proof",
  );

  await page
    .getByRole("button", { name: "Continue with University SSO" })
    .click();

  await expect(page).toHaveURL(
    `${canonicalOrigin}/authorize/desktop?state=desktop-state`,
  );
  await expect(
    page.getByRole("heading", { name: "Continue in Proctor Desktop" }),
  ).toBeVisible();
});

test("Desktop confirmation can return to account selection", async ({ page }) => {
  await mockDiscovery(page);
  const desktop = await mockDesktopAuthorization(page);
  await page.route(
    "**/api/v1/auth/desktop/authorizations/account/reset",
    async (route) => {
      desktop.reset();
      await route.fulfill({ status: 204 });
    },
  );
  await page.goto(
    "/authorize/desktop?request=desktop-handle&state=desktop-state#proof=private-browser-proof",
  );

  await page.getByRole("button", { name: "Use another account" }).click();

  await expect(
    page.getByRole("heading", { name: "Sign in to continue in Proctor Desktop" }),
  ).toBeVisible();
  await expect(page.getByLabel("Email or username")).toBeVisible();
});

test("Desktop authentication reports an active Exam Session lock safely", async ({
  page,
}) => {
  await mockDiscovery(page);
  await mockDesktopAuthorization(page, { currentSession: false });
  await page.route(
    "**/api/v1/auth/desktop/authorizations/authenticate/password",
    async (route) => {
      await route.fulfill({
        status: 409,
        contentType: "application/problem+json",
        body: JSON.stringify({
          type: "/problems/desktop-authorization-account-session-locked",
          title: "Account unavailable",
          status: 409,
          code: "authentication.desktop_authorization.account_session_locked",
        }),
      });
    },
  );
  await page.goto(
    "/authorize/desktop?request=desktop-handle&state=desktop-state#proof=private-browser-proof",
  );
  await page.getByLabel("Email or username").fill("student.one");
  await page.locator("#desktop-password").fill("private-password");
  await page.getByRole("button", { name: "Sign in" }).click();

  await expect(
    page.getByRole("heading", {
      name: "This account is already active in an Exam",
    }),
  ).toBeFocused();
  await expect(page.locator("body")).not.toContainText("private-password");
});

test("Desktop rejection announces and focuses the unusable request", async ({
  page,
}) => {
  await mockDiscovery(page);
  await mockDesktopAuthorization(page);
  await page.route(
    "**/api/v1/auth/desktop/authorizations/cancel",
    async (route) => {
      await route.fulfill({
        status: 409,
        contentType: "application/problem+json",
        body: JSON.stringify({
          type: "/problems/desktop-authorization-invalid",
          title: "Desktop request unavailable",
          status: 409,
          code: "authentication.desktop_authorization.invalid",
        }),
      });
    },
  );
  await page.goto(
    "/authorize/desktop?request=desktop-handle&state=desktop-state#proof=private-browser-proof",
  );

  await page.getByRole("button", { name: "Cancel request" }).click();

  const heading = page.getByRole("heading", {
    level: 1,
    name: "This Desktop request can’t be used",
  });
  await expect(heading).toBeFocused();
  await expect(page.getByRole("status")).toContainText(
    "This Desktop request can’t be used",
  );
  await expect(
    page.getByRole("link", { name: "Return to sign in" }),
  ).toBeVisible();
});

test("provider connection requires an explicit provider selection action", async ({
  page,
}) => {
  await mockCurrentUser(page);
  await mockProviders(page);
  await page.route(
    "**/api/v1/authentication-methods/providers/university-oidc/connect",
    async (route) => {
      expect(await route.request().postDataJSON()).toEqual({
        return_to: "/authorization/complete",
      });
      await route.fulfill({
        status: 201,
        contentType: "application/json",
        body: JSON.stringify({
          redirect_url: `${canonicalOrigin}/authorization/complete`,
          expires_at: Date.now() + 300_000,
        }),
      });
    },
  );
  await page.goto("/account/connect-provider");

  await expect(page).toHaveTitle("Connect a provider · Proctor");
  await expect(page.getByText("University SSO", { exact: true })).toBeVisible();
  await expect(page.getByText("OpenID Connect")).toHaveAttribute(
    "translate",
    "no",
  );
  await page.getByRole("button", { name: "Connect University SSO" }).click();

  await expect(page).toHaveURL(`${canonicalOrigin}/authorization/complete`);
  await expect(
    page.getByRole("heading", { name: "You’re signed in" }),
  ).toBeVisible();
});

for (const pageCase of [
  {
    route: "/account/reset-password",
    heading: "This reset link can’t be used",
  },
  { route: "/join", heading: "This Invitation can’t be used" },
  {
    route: "/authorize/desktop",
    heading: "This desktop request can’t be used",
  },
] as const) {
  test(`${pageCase.route} rejects a missing one-time credential`, async ({
    page,
  }) => {
    await page.goto(pageCase.route);

    await expect(
      page.getByRole("heading", { level: 1, name: pageCase.heading }),
    ).toBeVisible();
    await expect(page.getByRole("link", { name: "Return to sign in" })).toBeVisible();
  });
}

test("provider connection presents a signed-out state without shifting into a chooser", async ({
  page,
}) => {
  await page.route("**/api/v1/users/me", async (route) => {
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
  await page.goto("/account/connect-provider");

  await expect(
    page.getByRole("heading", {
      level: 1,
      name: "Sign in to connect a provider",
    }),
  ).toBeVisible();
  await expect(page.getByRole("link", { name: "Sign in" })).toHaveAttribute(
    "href",
    "/login",
  );
  await expect(page.getByRole("radio")).toHaveCount(0);
});

test("provider retry announces and focuses the resulting task", async ({
  page,
}) => {
  let providerAttempt = 0;
  await mockCurrentUser(page);
  await page.route("**/api/v1/auth/providers", async (route) => {
    providerAttempt += 1;
    if (providerAttempt === 1) {
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
      contentType: "application/json",
      body: JSON.stringify(defaultDiscovery.providers),
    });
  });
  await page.goto("/account/connect-provider");

  const unavailableHeading = page.getByRole("heading", {
    level: 1,
    name: "Provider connection is unavailable",
  });
  await expect(unavailableHeading).toBeVisible();
  await expect(unavailableHeading).not.toBeFocused();
  await page.getByRole("button", { name: "Try again" }).click();

  await expect(
    page.getByRole("heading", {
      level: 1,
      name: "Add another sign-in method",
    }),
  ).toBeFocused();
  await expect(page.getByRole("status").first()).toContainText(
    "Connect a provider",
  );
  expect(providerAttempt).toBe(2);
});

for (const pageCase of [
  { route: "/account/forgot-password", heading: "Reset your password" },
  {
    route: "/account/reset-password#token=private-reset-token",
    heading: "Choose a new password",
  },
  {
    route: "/join#token=private-invitation-claim",
    heading: "Join Northbridge Institute",
  },
  {
    route:
      "/authorize/desktop?request=desktop-handle&state=desktop-state#proof=private-browser-proof",
    heading: "Continue in Proctor Desktop",
  },
  {
    route: "/account/connect-provider",
    heading: "Add another sign-in method",
  },
] as const) {
  test(`${pageCase.route} follows dark mode and stays one-dimensional when narrow`, async ({
    page,
  }) => {
    await page.setViewportSize({ width: 390, height: 844 });
    await page.emulateMedia({ colorScheme: "dark", reducedMotion: "reduce" });

    if (pageCase.route.startsWith("/join")) {
      await mockDiscovery(page);
      await page.route("**/api/v1/auth/browser/invitations", async (route) => {
        await route.fulfill({
          status: 201,
          contentType: "application/json",
          body: JSON.stringify({
            handle: "private-browser-handle",
            purpose: "student_class",
            requirement: "account",
            expires_at: Date.now() + 300_000,
          }),
        });
      });
    } else if (pageCase.route.startsWith("/authorize/desktop")) {
      await mockDiscovery(page);
      await mockDesktopAuthorization(page);
    } else if (pageCase.route === "/account/connect-provider") {
      await mockCurrentUser(page);
      await mockProviders(page);
    }

    await page.goto(pageCase.route);
    await expect(
      page.getByRole("heading", { level: 1, name: pageCase.heading }),
    ).toBeVisible();
    await expect(page.locator("aside")).toHaveCount(0);
    await expect(page.locator("main ol")).toHaveCount(0);
    await expect(page.locator("[data-proctor-notice]").first()).toBeVisible();
    await expect(page.locator("[data-proctor-evidence-note]")).toHaveCount(0);

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
      () =>
        document.documentElement.scrollWidth -
        document.documentElement.clientWidth,
    );
    expect(overflow).toBeLessThanOrEqual(1);
  });
}

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
      noticeRule: getComputedStyle(
        document.querySelector<HTMLElement>("[data-proctor-notice]")!,
      ).borderInlineStartColor,
      noticeBackground: getComputedStyle(
        document.querySelector<HTMLElement>("[data-proctor-notice]")!,
      ).backgroundColor,
      noticeRadius: getComputedStyle(
        document.querySelector<HTMLElement>("[data-proctor-notice]")!,
      ).borderRadius,
      warningToken: getComputedStyle(document.documentElement)
        .getPropertyValue("--proctor-color-state-warning")
        .trim(),
    }));
    expect(colors.body).toBe(colors.canvas);
    expect(colors.primary).not.toBe(colors.canvas);
    expect(colors.primaryText).toBe(
      colorToRGB(colors.onPrimary),
    );
    expect(colors.noticeRule).toBe(colorToRGB(colors.warningToken));
    expect(colors.noticeBackground).toBe("rgba(0, 0, 0, 0)");
    expect(colors.noticeRadius).toBe("0px");
  });
}

test("shared notices and password disclosure controls keep their contracts", async ({
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

  await expect(page.locator("[data-proctor-notice]")).toHaveCount(2);
  await expect(
    page.locator('[data-proctor-notice-tone="information"]'),
  ).toBeVisible();
  await expect(
    page.locator('[data-proctor-notice-tone="warning"]'),
  ).toBeVisible();

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
  await expect(
    page.locator('[data-proctor-notice-tone="information"]'),
  ).toBeVisible();
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
    "registration-first-name",
    "registration-last-name",
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

  const firstNameBox = await page.locator("#registration-first-name").boundingBox();
  const lastNameBox = await page.locator("#registration-last-name").boundingBox();
  expect(firstNameBox).not.toBeNull();
  expect(lastNameBox).not.toBeNull();
  expect(Math.abs(firstNameBox!.y - lastNameBox!.y)).toBeLessThanOrEqual(1);

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

    if (pageCase.route === "/register") {
      const firstNameBox = await page
        .locator("#registration-first-name")
        .boundingBox();
      const lastNameBox = await page
        .locator("#registration-last-name")
        .boundingBox();
      expect(firstNameBox).not.toBeNull();
      expect(lastNameBox).not.toBeNull();
      expect(lastNameBox!.y).toBeGreaterThanOrEqual(
        firstNameBox!.y + firstNameBox!.height,
      );
    }

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
