import { describe, expect, it } from "vitest";
import {
  availableRunActions,
  parsePinnedSource,
  qualityStatus,
  runRequest,
  statusTone,
} from "./presentation";

describe("automation evidence and commands", () => {
  it("never renders absent, skipped, or unrecognized quality evidence as passing", () => {
    for (const value of [
      undefined,
      null,
      "success",
      {},
      "missing",
      "skipped",
      "unknown",
    ]) {
      expect(qualityStatus(value)).not.toBe("passed");
      expect(statusTone(qualityStatus(value))).not.toBe("positive");
    }
    expect(qualityStatus("passed")).toBe("passed");
    expect(qualityStatus("failed")).toBe("failed");
  });

  it("offers undo only for an applied batch and limits cancel and retry to applicable states", () => {
    expect(availableRunActions({ status: "running" })).toEqual({
      cancel: true,
      retry: false,
      undo: false,
    });
    expect(
      availableRunActions({ status: "failed", batch_id: "batch" }),
    ).toEqual({ cancel: false, retry: true, undo: false });
    expect(
      availableRunActions({ status: "partial", batch_id: "batch" }).undo,
    ).toBe(true);
    expect(availableRunActions({ status: "completed" }).undo).toBe(false);
    expect(
      availableRunActions({ status: "undone", batch_id: "batch" }).undo,
    ).toBe(false);
  });

  it("pins PRD runs to a specific saved revision and preserves source text and retry keys", () => {
    expect(
      runRequest(
        "decompose",
        { mode: "page", pageID: "page-a", revision: "12", text: "" },
        "retry-1",
      ),
    ).toMatchObject({
      source: { page_id: "page-a", revision: 12 },
      idempotency_key: "retry-1",
    });
    expect(() =>
      runRequest(
        "decompose",
        { mode: "page", pageID: "page-a", revision: "", text: "" },
        "key",
      ),
    ).toThrow();
    expect(() =>
      runRequest(
        "decompose",
        { mode: "text", pageID: "", revision: "", text: "  " },
        "key",
      ),
    ).toThrow();
    expect(
      runRequest(
        "schedule",
        { mode: "text", pageID: "", revision: "", text: "ignored" },
        "key",
      ),
    ).not.toHaveProperty("source");
  });

  it("rejects imprecise provider references and retains exact commit and Actions attempt", () => {
    expect(() =>
      parsePinnedSource({
        commit: "abc123",
        pullRequest: "",
        run: "",
        attempt: "",
      }),
    ).toThrow();
    expect(() =>
      parsePinnedSource({
        commit: "",
        pullRequest: "1.5",
        run: "",
        attempt: "",
      }),
    ).toThrow();
    expect(() =>
      parsePinnedSource({ commit: "", pullRequest: "", run: "", attempt: "2" }),
    ).toThrow();
    expect(
      parsePinnedSource({
        commit: "a".repeat(40),
        pullRequest: "7",
        run: "100",
        attempt: "2",
      }),
    ).toEqual({
      commit_sha: "a".repeat(40),
      pull_request: 7,
      run_id: 100,
      run_attempt: 2,
    });
  });

  it("bounds uploaded Chinese PRDs by UTF-8 bytes and retains a stable incremental source key", () => {
    const source = {
      mode: "text" as const,
      pageID: "",
      revision: "",
      text: "需求".repeat(9000),
      key: "prd-portal",
    };
    expect(() => runRequest("decompose", source, "key")).toThrow("UTF-8");
    expect(
      runRequest("decompose", { ...source, text: "客户可查看订单" }, "key")
        .source,
    ).toEqual({ text: "客户可查看订单", key: "prd-portal" });
  });

  it("preserves selected schedule scope and rejects an inverted execution window", () => {
    const source = {
      mode: "text" as const,
      pageID: "",
      revision: "",
      text: "",
    };
    expect(
      runRequest("schedule", source, "retry-key", {
        start: "2026-09-12",
        end: "2026-09-30",
        taskIDs: "a, b\na",
      }),
    ).toMatchObject({
      start_date: "2026-09-12",
      end_date: "2026-09-30",
      task_ids: ["a", "b"],
      idempotency_key: "retry-key",
    });
    expect(() =>
      runRequest("schedule", source, "key", {
        start: "2026-09-30",
        end: "2026-09-12",
        taskIDs: "",
      }),
    ).toThrow();
  });
});
