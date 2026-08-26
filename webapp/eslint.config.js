import eslint from "@eslint/js";
import globals from "globals";
import tseslint from "typescript-eslint";

export default tseslint.config(
  { ignores: ["dist", "src/api/generated"] },
  eslint.configs.recommended,
  ...tseslint.configs.recommended,
  {
    files: [
      "**/*.{js,mjs}",
      "vite.config.ts",
      "vitest.config.ts",
      "playwright.config.ts",
    ],
    languageOptions: {
      globals: globals.node,
    },
  },
  {
    files: ["**/*.{ts,tsx}"],
    languageOptions: {
      ecmaVersion: 2023,
      globals: globals.browser,
    },
    rules: {
      "no-restricted-imports": [
        "error",
        {
          paths: [
            {
              name: "lucide-react",
              message:
                "Import product icons through components/Icon/Icon instead.",
            },
          ],
        },
      ],
    },
  },
  {
    files: ["src/components/Icon/Icon.tsx"],
    rules: {
      "no-restricted-imports": "off",
    },
  },
);
