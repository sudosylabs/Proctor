import { expect, test } from "@playwright/test";

const fixturePath = "/tests/fixtures/theme-presentation.html";

for (const themeCase of [
  {
    name: "system light",
    system: "light",
    preference: "system",
    effective: "light",
    rootTheme: null,
    metadataCount: 2,
    themeColor: "#ffffff",
  },
  {
    name: "system dark",
    system: "dark",
    preference: "system",
    effective: "dark",
    rootTheme: null,
    metadataCount: 2,
    themeColor: "#111111",
  },
  {
    name: "explicit dark on a light system",
    system: "light",
    preference: "dark",
    effective: "dark",
    rootTheme: "dark",
    metadataCount: 1,
    themeColor: "#111111",
  },
  {
    name: "explicit light on a dark system",
    system: "dark",
    preference: "light",
    effective: "light",
    rootTheme: "light",
    metadataCount: 1,
    themeColor: "#ffffff",
  },
] as const) {
  test(`${themeCase.name} keeps tokens, metadata, lockup, and favicon coherent`, async ({
    page,
  }) => {
    await page.emulateMedia({ colorScheme: themeCase.system });
    await page.goto(fixturePath);
    if (themeCase.preference !== "system") {
      await page.evaluate((preference) => {
        window.setThemePresentationPreference(preference);
      }, themeCase.preference);
    }

    if (themeCase.rootTheme === null) {
      await expect(page.locator("html")).not.toHaveAttribute("data-theme");
    } else {
      await expect(page.locator("html")).toHaveAttribute(
        "data-theme",
        themeCase.rootTheme,
      );
    }
    const lockup = page.getByRole("img", { name: "Proctor" });
    await expect(lockup).toHaveAttribute(
      "data-proctor-lockup-color-scheme",
      themeCase.effective,
    );

    const presentation = await page.evaluate(() => ({
      body: getComputedStyle(document.body).backgroundColor,
      canvas: getComputedStyle(document.documentElement).backgroundColor,
      colorScheme: getComputedStyle(document.documentElement).colorScheme,
      metadata: Array.from(
        document.querySelectorAll<HTMLMetaElement>('meta[name="theme-color"]'),
        (meta) => ({ content: meta.content, media: meta.media }),
      ),
      activeFavicons: Array.from(
        document.querySelectorAll<HTMLLinkElement>('link[rel="icon"]'),
      )
        .filter((link) => link.media === "" || matchMedia(link.media).matches)
        .map((link) => new URL(link.href).pathname),
    }));
    expect(presentation.body).toBe(presentation.canvas);
    expect(presentation.colorScheme).toBe(themeCase.effective);
    expect(presentation.metadata).toHaveLength(themeCase.metadataCount);
    if (themeCase.preference === "system") {
      expect(
        presentation.metadata.find(({ media }) =>
          media.includes(themeCase.system),
        )?.content,
      ).toBe(themeCase.themeColor);
    } else {
      expect(presentation.metadata).toEqual([
        { content: themeCase.themeColor, media: "" },
      ]);
    }

    const expectedFavicon =
      themeCase.system === "dark" ? "proctor-mark-white" : "proctor-mark";
    expect(presentation.activeFavicons).toHaveLength(2);
    expect(
      presentation.activeFavicons.every((href) =>
        href.includes(expectedFavicon),
      ),
    ).toBe(true);

    const currentSource = await lockup.evaluate(
      (image: HTMLImageElement) => image.currentSrc,
    );
    if (currentSource.startsWith("data:")) {
      expect(decodeURIComponent(currentSource)).toContain(
        themeCase.effective === "dark"
          ? "fill='#FFFFFF'"
          : "fill='#161616'",
      );
    } else if (themeCase.effective === "dark") {
      expect(currentSource).toContain("proctor-lockup-purple-white");
    } else {
      expect(currentSource).toContain("proctor-lockup");
      expect(currentSource).not.toContain("purple-white");
    }
  });
}
