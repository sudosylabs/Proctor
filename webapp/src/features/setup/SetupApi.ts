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
  | { kind: "bootstrap_denied" }
  | { kind: "password_rejected" }
  | { kind: "rate_limited" }
  | { kind: "unavailable" };

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
    const { data, error, response } = await apiClient.POST("/api/v1/bootstrap", {
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

    if (response.status === 201 && isRecord(data)) {
      return { kind: "complete" };
    }
    switch (readProblemValue(error)?.code) {
      case "installation.already_initialized":
        return { kind: "complete" };
      case "installation.bootstrap_denied":
        return { kind: "bootstrap_denied" };
      case "authentication.password.invalid":
        return { kind: "password_rejected" };
      case "authentication.rate_limited":
        return { kind: "rate_limited" };
      default:
        return { kind: "unavailable" };
    }
  } catch {
    return { kind: "unavailable" };
  }
}
