import { useMutation, useQuery } from "@tanstack/react-query";
import type { Access, Example, HistoryEntry, Quote, Report, Resolution } from "./types";

/** The console talks to the gateway on the same origin (`make serve`), or through Vite's proxy in development. */
const BASE = (import.meta.env.VITE_API_URL as string | undefined) ?? "";

export class ApiError extends Error {}

async function call<T>(path: string, init?: RequestInit): Promise<T> {
  let res: Response;
  try {
    res = await fetch(BASE + path, init);
  } catch {
    throw new ApiError("The gateway could not be reached. Start it with `make serve`, then try again.");
  }
  const body = await res.json().catch(() => ({}));
  if (!res.ok) throw new ApiError((body as { error?: string }).error ?? `The gateway answered ${res.status}.`);
  return body as T;
}

const post = (body: unknown): RequestInit => ({ method: "POST", headers: { "content-type": "application/json" }, body: JSON.stringify(body) });

export const useExamples = () => useQuery({ queryKey: ["examples"], queryFn: () => call<Example[]>("/v1/sandbox/examples"), staleTime: Infinity });

export const useAccess = (user: number | null) =>
  useQuery({ queryKey: ["access", user], enabled: user !== null, queryFn: () => call<Access>(`/v1/access/${user}`) });

export const useHistory = (sub: string | null) =>
  useQuery({ queryKey: ["history", sub], enabled: sub !== null, queryFn: () => call<HistoryEntry[]>(`/v1/subscriptions/${sub}/history`) });

export const useReport = () => useQuery({ queryKey: ["report"], queryFn: () => call<Report>("/v1/report"), staleTime: Infinity });

export const useResolve = () =>
  useMutation({ mutationFn: (t: { id: string; text: string }) => call<Resolution>("/v1/support/tickets", post(t)) });

export const useQuote = () =>
  useMutation({ mutationFn: (q: { plan: string; billing: string; ip_country: string; card_country: string; vat_id: string }) =>
    call<Quote>("/v1/vat/quote", post(q)) });
