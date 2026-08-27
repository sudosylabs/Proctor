import { describe, expect, it } from "vitest";

import { resolveProductTheme } from "./ProductTheme";

describe("product theme resolution", () => {
  it.each([
    {
      preference: "system",
      system: "light",
      effective: "light",
      rootTheme: undefined,
      metadataCount: 2,
    },
    {
      preference: "system",
      system: "dark",
      effective: "dark",
      rootTheme: undefined,
      metadataCount: 2,
    },
    {
      preference: "dark",
      system: "light",
      effective: "dark",
      rootTheme: "dark",
      metadataCount: 1,
    },
    {
      preference: "light",
      system: "dark",
      effective: "light",
      rootTheme: "light",
      metadataCount: 1,
    },
  ] as const)(
    "resolves $preference against system $system",
    ({ effective, metadataCount, preference, rootTheme, system }) => {
      const resolved = resolveProductTheme(preference, system);

      expect(resolved.effectiveTheme.id).toBe(effective);
      expect(resolved.effectiveTheme.colorScheme).toBe(effective);
      expect(resolved.rootTheme).toBe(rootTheme);
      expect(resolved.themeColorMetadata).toHaveLength(metadataCount);
      if (preference === "system") {
        expect(resolved.themeColorMetadata.map(({ media }) => media)).toEqual([
          "(prefers-color-scheme: light)",
          "(prefers-color-scheme: dark)",
        ]);
      } else {
        expect(resolved.themeColorMetadata[0]?.media).toBeUndefined();
        expect(resolved.themeColorMetadata[0]?.content).toBe(
          resolved.effectiveTheme.themeColor,
        );
      }
    },
  );
});
