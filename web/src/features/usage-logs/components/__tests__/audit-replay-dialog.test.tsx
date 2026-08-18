import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, test, vi } from "vitest";

import { summarizeReplayBody } from "../../lib/audit-replay";
import {
  AuditReplayDialog,
  ReplayPreviewDetails,
} from "../dialogs/audit-replay-dialog";

const { executeReplay, previewReplay } = vi.hoisted(() => ({
  executeReplay: vi.fn(),
  previewReplay: vi.fn(),
}));

vi.mock("../../api", () => ({
  executeBodyAuditReplay: executeReplay,
  previewBodyAuditReplay: previewReplay,
}));

describe("audit replay preview", () => {
  test("renders explicit unavailability instead of inventing client-level replay", () => {
    const markup = renderToStaticMarkup(
      createElement(ReplayPreviewDetails, {
        preview: {
          mode: "client_level",
          available: false,
          unavailable_reason: "original_client_request_not_captured",
        },
      }),
    );

    expect(markup).toContain("This replay mode is unavailable");
    expect(markup).toContain("original_client_request_not_captured");
  });

  test("shows target changes, body summary and exact replay risk", () => {
    const markup = renderToStaticMarkup(
      createElement(ReplayPreviewDetails, {
        preview: {
          mode: "exact_upstream",
          available: true,
          historical_target: "https://old.example/v1/chat/completions",
          target: "https://current.example/v1/chat/completions",
          body: { model: "example-model", messages: [{ role: "user" }] },
          differences: [
            { path: "target", change: "rewritten_to_current_channel_base_url" },
          ],
          risks: [
            "bypasses_gateway_conversion_and_billing",
            "may_incur_provider_cost",
          ],
        },
      }),
    );

    expect(markup).toContain("current.example");
    expect(markup).toContain("example-model");
    expect(markup).toContain("bypasses_gateway_conversion_and_billing");
    expect(markup).toContain("may_incur_provider_cost");
  });

  test("bounds large preview bodies", () => {
    expect(summarizeReplayBody("x".repeat(100), 12)).toBe("xxxxxxxxxxxx\n…");
  });

  test("previews first and sends only after the explicit confirmation click", async () => {
    previewReplay.mockImplementation(
      async (_requestId: string, _attemptId: number, mode: string) =>
        mode === "exact_upstream"
          ? {
              mode,
              available: true,
              confirmation_token: "one-time-token",
              expires_at: Date.now() + 60_000,
              risks: ["may_incur_provider_cost"],
            }
          : {
              mode,
              available: false,
              unavailable_reason: "original_client_request_not_captured",
            },
    );
    executeReplay.mockResolvedValue({
      state: "completed",
      replay_trace_id: 42,
      replay_request_id: "replay-request-id",
      response_status: 200,
      idempotent_replay: false,
    });

    render(
      <AuditReplayDialog
        open
        onOpenChange={() => undefined}
        requestId="original-request-id"
        attemptId={7}
      />,
    );

    await screen.findByText("Replay risks");
    expect(previewReplay).toHaveBeenCalledTimes(2);
    expect(executeReplay).not.toHaveBeenCalled();

    await userEvent.click(
      screen.getByRole("button", {
        name: "Confirm and send real upstream request",
      }),
    );

    await waitFor(() => {
      expect(executeReplay).toHaveBeenCalledWith(
        "original-request-id",
        "one-time-token",
      );
    });
    expect(await screen.findByText("replay-request-id")).toBeInTheDocument();
  });
});
