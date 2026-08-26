import {
  synchronizeDocument,
  type DocumentDescriptor,
} from "../../src/app/document";
import "../../src/styles/reset.css";
import "../../src/styles/tokens.css";
import "../../src/styles/base.css";

const descriptor: DocumentDescriptor = {
  language: "en",
  direction: "ltr",
  title: "Document foundation · Proctor",
  themePreference: "system",
};

synchronizeDocument(document, descriptor);

declare global {
  interface Window {
    setDocumentFoundationTheme: (
      themePreference: DocumentDescriptor["themePreference"],
    ) => void;
  }
}

window.setDocumentFoundationTheme = (themePreference) => {
  synchronizeDocument(document, { ...descriptor, themePreference });
};
