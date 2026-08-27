import "../../src/styles/reset.css";
import "../../src/styles/tokens.css";
import "../../src/styles/base.css";

import { StrictMode, useLayoutEffect, useState } from "react";
import { createRoot } from "react-dom/client";

import {
  defaultDocumentDescriptor,
  synchronizeDocument,
} from "../../src/app/document";
import { AccessPageShell } from "../../src/components/AccessPageShell/AccessPageShell";
import type { ThemePreference } from "../../src/generated/design-system/themes";
import {
  ProductThemeProvider,
  useResolvedProductTheme,
} from "../../src/theme/ProductTheme";

let setThemePreference: ((preference: ThemePreference) => void) | undefined;

declare global {
  interface Window {
    setThemePresentationPreference(preference: ThemePreference): void;
  }
}

window.setThemePresentationPreference = (preference) => {
  setThemePreference?.(preference);
};

function ThemePresentationFixture() {
  const [preference, setPreference] = useState<ThemePreference>("system");
  const theme = useResolvedProductTheme(preference);
  setThemePreference = setPreference;

  useLayoutEffect(() => {
    synchronizeDocument(
      document,
      {
        ...defaultDocumentDescriptor,
        title: "Theme presentation · Proctor",
      },
      theme,
    );
  }, [theme]);

  return (
    <ProductThemeProvider value={theme}>
      <AccessPageShell skipLabel="Skip to main content" variant="single">
        <h1>Theme presentation</h1>
      </AccessPageShell>
    </ProductThemeProvider>
  );
}

const root = document.getElementById("root");
if (root === null) {
  throw new Error("Theme presentation fixture root is missing");
}
createRoot(root).render(
  <StrictMode>
    <ThemePresentationFixture />
  </StrictMode>,
);
