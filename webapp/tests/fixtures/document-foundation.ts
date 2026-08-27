import {
  synchronizeDocument,
  type DocumentDescriptor,
} from "../../src/app/document";
import type { ThemePreference } from "../../src/generated/design-system/themes";
import {
  readSystemColorScheme,
  resolveProductTheme,
} from "../../src/theme/ProductTheme";
import "../../src/styles/reset.css";
import "../../src/styles/tokens.css";
import "../../src/styles/base.css";

const descriptor: DocumentDescriptor = {
  language: "en",
  direction: "ltr",
  title: "Document foundation · Proctor",
};

setTheme("system");

declare global {
  interface Window {
    setDocumentFoundationTheme: (
      themePreference: ThemePreference,
    ) => void;
  }
}

window.setDocumentFoundationTheme = setTheme;

function setTheme(themePreference: ThemePreference) {
  synchronizeDocument(
    document,
    descriptor,
    resolveProductTheme(themePreference, readSystemColorScheme()),
  );
}
