import { AnimatePresence, motion } from "motion/react";
import { useState, type FormEvent } from "react";
import { ApiError, useAccess, useExamples } from "../../core/api";
import { Empty, Failure, Loading } from "../../shared/States";
import { date } from "../../shared/format";
import { settle } from "../../theme/motion";
import { SourceRow } from "./SourceRow";

/** "Why can't this person get in?": the verdict, every source of access, and how the ledger got there. */
export function CustomerView() {
  const [user, setUser] = useState<number | null>(null);
  const [typed, setTyped] = useState("");
  const examples = useExamples();
  const access = useAccess(user);

  const submit = (e: FormEvent) => {
    e.preventDefault();
    const n = Number(typed.replace(/^AMB-/i, "").trim());
    if (Number.isInteger(n) && n > 0) setUser(n);
  };

  return (
    <div className="grid gap-10 md:grid-cols-[19rem_1fr]">
      <aside>
        <form onSubmit={submit} className="flex flex-col gap-2">
          <label htmlFor="customer" className="text-sm font-semibold">Customer number</label>
          <div className="flex gap-2">
            <input id="customer" value={typed} onChange={(e) => setTyped(e.target.value)} placeholder="AMB-100001" inputMode="numeric"
              className="w-full rounded-[var(--radius-field)] border border-rule bg-sheet px-3 py-2" />
            <button className="whitespace-nowrap rounded-[var(--radius-field)] bg-action px-4 py-2 font-semibold text-white">Look up</button>
          </div>
        </form>
        <h2 className="mt-8 text-sm font-semibold">Customers from the sandbox</h2>
        {examples.isPending && <Loading label="Loading examples" />}
        {examples.isError && <p className="mt-2 text-sm text-muted">{(examples.error as Error).message}</p>}
        <ul className="mt-2">
          {examples.data?.map((x) => (
            <li key={x.user}>
              <button onClick={() => { setUser(x.user); setTyped(String(x.user)); }} aria-pressed={user === x.user}
                className={`flex w-full items-baseline justify-between gap-3 border-b border-rule px-1 py-2 text-left text-sm hover:bg-sheet ${user === x.user ? "font-semibold" : ""}`}>
                <span>{x.label}</span><span className="text-muted">{x.user}</span>
              </button>
            </li>
          ))}
        </ul>
      </aside>

      <section aria-live="polite">
        {user === null && (
          <Empty title="Look someone up">
            Enter the number from the ticket, or pick a customer on the left. You will see whether the gateway lets them
            in, every subscription, pass or campus seat it considered, and each write that put the ledger where it is.
          </Empty>
        )}
        {user !== null && access.isPending && <Loading label="Checking access" />}
        {access.isError && <Failure message={access.error instanceof ApiError ? access.error.message : "Access check failed."} retry={() => access.refetch()} />}
        <AnimatePresence mode="wait">
          {access.data && (
            <motion.div key={access.data.user} initial={{ opacity: 0, y: 8 }} animate={{ opacity: 1, y: 0 }} exit={{ opacity: 0 }} transition={settle}>
              <Verdict granted={access.data.granted} until={access.data.reasons.find((r) => r.grants)?.until} user={access.data.user} at={access.data.at} />
              {access.data.reasons.length === 0
                ? <Empty title="No purchase and no campus seat">The providers have sent nothing for this customer. If they say they paid, the support agent reads the provider before anything else.</Empty>
                : <ol className="mt-6 border-t border-rule">{access.data.reasons.map((r) => <SourceRow key={r.source} reason={r} />)}</ol>}
            </motion.div>
          )}
        </AnimatePresence>
      </section>
    </div>
  );
}

function Verdict({ granted, until, user, at }: { granted: boolean; until?: string; user: number; at: string }) {
  return (
    <div className={`rounded-[var(--radius-panel)] px-5 py-4 ${granted ? "bg-open-soft" : "bg-shut-soft"}`}>
      <p className="text-sm text-muted">Customer {user}, checked {date(at)}</p>
      <p className={`mt-1 text-2xl font-semibold leading-tight ${granted ? "text-open" : "text-shut"}`}>
        {granted ? `In until ${until ? date(until) : "further notice"}` : "Kept out"}
      </p>
    </div>
  );
}
