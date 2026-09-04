import { configDefaults, defineConfig } from "vitest/config";

export default defineConfig({
  test: {
    exclude: [...configDefaults.exclude, "tests/browser/**"],
    reporters: process.env.PROCTOR_TEST_REPORT_DIR ? ["default", "junit"] : ["default"],
    outputFile: process.env.PROCTOR_TEST_REPORT_DIR
      ? `${process.env.PROCTOR_TEST_REPORT_DIR}/webapp-unit/junit.xml`
      : undefined,
    coverage: {
      enabled: Boolean(process.env.PROCTOR_TEST_REPORT_DIR),
      provider: "v8",
      reporter: ["text-summary", "json-summary", "lcov", "html"],
      reportsDirectory: process.env.PROCTOR_TEST_REPORT_DIR
        ? `${process.env.PROCTOR_TEST_REPORT_DIR}/webapp-unit/coverage`
        : "./coverage",
      reportOnFailure: true,
      exclude: ["src/api/generated/**", "src/generated/**"],
    },
  },
});
