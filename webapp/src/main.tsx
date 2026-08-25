import { StrictMode } from "react";
import { createRoot } from "react-dom/client";

import { App } from "./app/App";
import { bootstrapHostedPage } from "./app/bootstrap";
import "./styles/tokens.css";

const root = document.getElementById("root");
if (root === null) {
  throw new Error("Proctor webapp root is missing");
}

createRoot(root).render(
  <StrictMode>
    <App bootstrap={bootstrapHostedPage(window.location, window.history)} />
  </StrictMode>,
);
