const day = new Intl.DateTimeFormat("en-GB", { day: "numeric", month: "short", year: "numeric", timeZone: "UTC" });
const stamp = new Intl.DateTimeFormat("en-GB", { day: "numeric", month: "short", hour: "2-digit", minute: "2-digit", timeZone: "UTC" });

export const date = (iso: string) => day.format(new Date(iso));
export const when = (iso: string) => `${stamp.format(new Date(iso))} UTC`;
export const euros = (cents: number) => new Intl.NumberFormat("en-GB", { style: "currency", currency: "EUR" }).format(cents / 100);
export const count = (n: number) => new Intl.NumberFormat("en-GB").format(Math.round(n));

/** Where a ledger write came from, in a support person's words. */
export function origin(source: string): string {
  if (source.startsWith("webhook")) return "provider webhook";
  if (source === "return") return "customer returned from checkout";
  if (source === "reconcile") return "nightly reconcile";
  if (source.startsWith("support")) return `support, ticket ${source.slice(8)}`;
  return source;
}

export const actionLabel: Record<string, string> = {
  sync_grant: "Access opened from the provider's record",
  explain_unpaid: "Told the customer nothing is paid",
  refund_duplicate: "Second charge refunded",
  explain_single_charge: "Told the customer there is one charge",
  refund_withdrawal: "Refunded: withdrawal within 14 days",
  cancel: "Renewal stopped",
  join_campus: "Campus seat taken",
  escalate: "Sent to a person",
};
