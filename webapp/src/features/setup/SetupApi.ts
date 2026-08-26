import { apiClient } from "../../api/client";
import { readProblemValue } from "../../api/problem";

export type SetupStatus =
  | { kind: "loading" }
  | { kind: "ready" }
  | { kind: "complete" }
  | { kind: "failure" };

export interface SetupSubmission {
  bootstrapSecret: string;
  institutionName: string;
  institutionDisplayName: string;
  institutionDescription: string;
  administratorEmail: string;
  administratorUsername: string;
  administratorDisplayName: string;
  password: string;
}

export type SetupSubmissionResult =
  | { kind: "complete" }
  | { kind: "problem"; code?: string }
  | { kind: "failure" };

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

export function resolveInstallationStatus(value: unknown): SetupStatus {
  if (!isRecord(value) || typeof value.initialized !== "boolean") {
    return { kind: "failure" };
  }
  return { kind: value.initialized ? "complete" : "ready" };
}

export async function requestInstallationStatus(): Promise<SetupStatus> {
  try {
    const { data } = await apiClient.GET("/api/v1/bootstrap");
    return resolveInstallationStatus(data);
  } catch {
    return { kind: "failure" };
  }
}

export async function submitInstallation(
  submission: SetupSubmission,
): Promise<SetupSubmissionResult> {
  try {
    const description = submission.institutionDescription.trim();
    const administratorDisplayName =
      submission.administratorDisplayName.trim();
    const { error, response } = await apiClient.POST("/api/v1/bootstrap", {
      body: {
        bootstrap_secret: submission.bootstrapSecret,
        institution: {
          name: submission.institutionName.trim(),
          display_name: submission.institutionDisplayName.trim(),
          ...(description === "" ? {} : { description }),
        },
        administrator: {
          email: submission.administratorEmail.trim(),
          username: submission.administratorUsername.trim(),
          ...(administratorDisplayName === ""
            ? {}
            : { display_name: administratorDisplayName }),
        },
        password: submission.password,
      },
    });

    if (response.ok) {
      return { kind: "complete" };
    }
    return { kind: "problem", code: readProblemValue(error)?.code };
  } catch {
    return { kind: "failure" };
  }
}
