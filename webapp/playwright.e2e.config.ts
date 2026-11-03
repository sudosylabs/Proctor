import { defineConfig } from "@playwright/test";

// End-to-end journeys against one real `proctor serve` node. `make e2e` owns
// the server, PostgreSQL, and Mailpit lifecycle and supplies these values; the
// suite never mocks an API route and never starts a server itself.
const baseURL = process.env.PROCTOR_E2E_BASE_URL;
const stateDir = process.env.PROCTOR_E2E_STATE_DIR;
if (!baseURL || !stateDir) {
  throw new Error("Run the end-to-end suite through `make e2e`.");
}
const reportDir = process.env.PROCTOR_TEST_REPORT_DIR
  ? `${process.env.PROCTOR_TEST_REPORT_DIR}/e2e`
  : `${stateDir}/report`;

export default defineConfig({
  testDir: "./tests/e2e",
  outputDir: `${reportDir}/results`,
  forbidOnly: Boolean(process.env.CI),
  // A failure is a real defect or nondeterminism to fix, never a retry.
  retries: 0,
  fullyParallel: true,
  // Password hashing is deliberately bounded on the server; two workers keep
  // journeys independent without queueing behind its concurrency limit.
  workers: 2,
  // Real Argon2 hashing and durable mail Jobs make journeys slower than mocks.
  timeout: 120_000,
  expect: { timeout: 15_000 },
  reporter: [
    ["list"],
    ["junit", { outputFile: `${reportDir}/junit.xml` }],
    ["html", { outputFolder: `${reportDir}/html`, open: "never" }],
  ],
  use: {
    baseURL,
    browserName: "chromium",
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
  },
  projects: [
    { name: "installation", testMatch: /installation\.setup\.ts$/ },
    { name: "chromium", dependencies: ["installation"], testMatch: /\.e2e\.ts$/ },
  ],
});
