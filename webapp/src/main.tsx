import "./styles/reset.css";
import "./styles/tokens.css";
import "./styles/base.css";

import { StrictMode } from "react";
import { createRoot } from "react-dom/client";

import { App } from "./app/App";
import {
  defaultDocumentDescriptor,
  synchronizeDocument,
} from "./app/document";
import { FatalErrorBoundary } from "./app/FatalErrorBoundary";
import {
  bootstrapHostedPage,
  type HostedPageBootstrap,
} from "./app/HostedRoutes";
import {
  readSystemColorScheme,
  resolveProductTheme,
} from "./theme/ProductTheme";
import { redactedRootErrorOptions } from "./app/rootErrors";

const root = document.getElementById("root");
if (root === null) {
  throw new Error("Proctor webapp root is missing");
}

let bootstrap: HostedPageBootstrap | undefined;
let bootstrapFailed = false;
try {
  synchronizeDocument(
    document,
    defaultDocumentDescriptor,
    resolveProductTheme("system", readSystemColorScheme()),
  );
  bootstrap = bootstrapHostedPage(window.location, window.history);
} catch {
  bootstrapFailed = true;
}

createRoot(root, redactedRootErrorOptions).render(
  <StrictMode>
    <FatalErrorBoundary initialFailure={bootstrapFailed}>
      {bootstrap === undefined ? null : <App bootstrap={bootstrap} />}
    </FatalErrorBoundary>
  </StrictMode>,
);
