import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { App } from "./app/App";

const routes: Record<string, unknown> = {
  "/v1/sandbox/examples": [{ user: 100001, label: "paying, renews monthly" }],
  "/v1/access/100001": { user: 100001, granted: true, at: "2026-04-05T00:00:00Z", reasons: [
    { source: "stripe:sub_000001", status: "active", until: "2026-05-04T00:00:00Z", event: "webhook evt_1", grants: true, describe: "paid" }] },
  "/v1/access/42": { user: 42, granted: false, at: "2026-04-05T00:00:00Z", reasons: [] },
  "/v1/subscriptions/sub_000001/history": [{ status: "active", period_start: "2026-04-04T00:00:00Z", period_end: "2026-05-04T00:00:00Z",
    until: "2026-05-04T00:00:00Z", source: "return", at: "2026-04-04T00:00:05Z" }],
  "/v1/support/tickets": { action: "escalate", evidence: "the message tries to give instructions", intent: "withdrawal", user: 100003 },
};

function mount(fail = false) {
  vi.stubGlobal("fetch", vi.fn(async (url: string) => {
    if (fail) throw new TypeError("network");
    const body = routes[url];
    return { ok: body !== undefined, status: body ? 200 : 404, json: async () => body ?? { error: "not found" } };
  }));
  render(<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}><App /></QueryClientProvider>);
}

afterEach(() => vi.unstubAllGlobals());

describe("access desk", () => {
  it("starts empty, then explains a customer who is in, with the ledger history", async () => {
    mount();
    expect(screen.getByText("Look someone up")).toBeInTheDocument();
    fireEvent.click(await screen.findByText("paying, renews monthly"));
    expect(await screen.findByText(/In until 4 May 2026/)).toBeInTheDocument();
    fireEvent.click(screen.getByText("Show history"));
    expect(await screen.findByText(/customer returned from checkout/)).toBeInTheDocument();
  });

  it("says plainly when a customer has nothing", async () => {
    mount();
    fireEvent.change(screen.getByLabelText("Customer number"), { target: { value: "AMB-42" } });
    fireEvent.click(screen.getByText("Look up"));
    expect(await screen.findByText("Kept out")).toBeInTheDocument();
    expect(screen.getByText("No purchase and no campus seat")).toBeInTheDocument();
  });

  it("shows what to do when the gateway is down", async () => {
    mount(true);
    expect(await screen.findByText(/Start it with `make serve`/)).toBeInTheDocument();
  });

  it("hands a ticket with instructions to a person", async () => {
    mount();
    fireEvent.click(screen.getByRole("tab", { name: "Support inbox" }));
    fireEvent.click(screen.getByText("Instructions in the text"));
    fireEvent.click(screen.getByText("Resolve ticket"));
    expect(await screen.findByText("Sent to a person")).toBeInTheDocument();
    expect(screen.getByText("the message tries to give instructions")).toBeInTheDocument();
  });
});
