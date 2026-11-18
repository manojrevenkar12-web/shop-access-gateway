import { useState } from "react";
import { useQuote } from "../../core/api";
import { Failure } from "../../shared/States";
import { euros } from "../../shared/format";

const countries = ["", "DE", "AT", "CH", "FR", "NL", "ES", "IT", "PL", "GB", "US"];
const plans = [["monthly", "Monthly"], ["yearly", "Yearly"], ["exam_pass", "Exam pass"]];

/** What a checkout charges, from the evidence the payment page has. */
export function VatView() {
  const [f, setF] = useState({ plan: "monthly", billing: "AT", ip_country: "AT", card_country: "DE", vat_id: "" });
  const quote = useQuote();
  const set = (k: keyof typeof f) => (e: { target: { value: string } }) => setF({ ...f, [k]: e.target.value });
  const q = quote.data;
  const field = "mt-1 w-full rounded-[var(--radius-field)] border border-rule bg-sheet px-3 py-2";
  return (
    <div className="grid gap-10 md:grid-cols-[22rem_1fr]">
      <form onSubmit={(e) => { e.preventDefault(); quote.mutate(f); }} className="flex flex-col gap-3">
        <label className="text-sm font-semibold">Plan
          <select value={f.plan} onChange={set("plan")} className={field}>{plans.map(([v, l]) => <option key={v} value={v}>{l}</option>)}</select>
        </label>
        {([["billing", "Billing address country"], ["ip_country", "Country of the IP address"], ["card_country", "Card issued in"]] as const).map(([k, l]) => (
          <label key={k} className="text-sm font-semibold">{l}
            <select value={f[k]} onChange={set(k)} className={field}>{countries.map((c) => <option key={c} value={c}>{c || "unknown"}</option>)}</select>
          </label>
        ))}
        <label className="text-sm font-semibold">VAT number (businesses)
          <input value={f.vat_id} onChange={set("vat_id")} placeholder="ATU12345678" className={field} />
        </label>
        <button className="mt-2 rounded-[var(--radius-field)] bg-action px-4 py-2 font-semibold text-white">Price the checkout</button>
      </form>
      <div aria-live="polite">
        {quote.isError && <Failure message={(quote.error as Error).message} />}
        {!q && !quote.isError && (
          <p className="max-w-prose pt-6 text-sm text-muted">
            VAT on digital services follows where the buyer lives. The gateway needs two pieces of evidence that agree;
            when they do not, it prices on the billing address and asks the customer to confirm their country.
          </p>
        )}
        {q && (
          <div className="pt-6">
            <p className="text-2xl font-semibold">{euros(q.gross_cents)}</p>
            <p className="text-sm text-muted">{euros(q.net_cents)} net + {euros(q.vat_cents)} VAT{q.reverse_charge ? " (reverse charge)" : ` at ${q.rate_percent}%`}, taxed in {q.country}</p>
            <p className={`mt-4 max-w-prose rounded-[var(--radius-panel)] px-4 py-3 text-sm ${q.basis.includes("review") ? "bg-held-soft" : "bg-sheet"}`}>{q.basis.charAt(0).toUpperCase() + q.basis.slice(1)}.</p>
          </div>
        )}
      </div>
    </div>
  );
}
