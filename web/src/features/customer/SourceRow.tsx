import { AnimatePresence, motion } from "motion/react";
import { useState } from "react";
import { useHistory } from "../../core/api";
import type { Reason } from "../../core/types";
import { Failure, Loading } from "../../shared/States";
import { date, origin, when } from "../../shared/format";
import { open as openT } from "../../theme/motion";

const chip = { open: "bg-open-soft text-open", held: "bg-held-soft text-held", shut: "bg-shut-soft text-shut" };
const tone = (r: Reason): keyof typeof chip => (r.grants ? (r.status === "past_due" ? "held" : "open") : "shut");

/** One source of access, with its ledger history behind a disclosure. */
export function SourceRow({ reason }: { reason: Reason }) {
  const [expanded, setExpanded] = useState(false);
  const isSub = !reason.source.startsWith("campus:");
  const sub = isSub ? reason.source.split(":")[1] : null;
  const history = useHistory(expanded ? sub : null);
  const t = tone(reason);
  return (
    <li className="border-b border-rule py-4">
      <div className="flex flex-wrap items-baseline gap-x-4 gap-y-1">
        <span className={`rounded-[var(--radius-field)] px-2 py-0.5 text-xs font-semibold ${chip[t]}`}>{reason.describe}</span>
        <span className="font-semibold">{reason.source}</span>
        <span className="text-sm text-muted">{reason.grants ? "until" : "ended"} {date(reason.until)}</span>
        {isSub && (
          <button onClick={() => setExpanded((v) => !v)} aria-expanded={expanded} className="ml-auto text-sm font-semibold text-action">
            {expanded ? "Hide history" : "Show history"}
          </button>
        )}
      </div>
      <p className="mt-1 text-sm text-muted">Last written by {origin(reason.event)}</p>
      <AnimatePresence initial={false}>
        {expanded && (
          <motion.div initial={{ height: 0, opacity: 0 }} animate={{ height: "auto", opacity: 1 }} exit={{ height: 0, opacity: 0 }} transition={openT} className="overflow-hidden">
            {history.isPending && <Loading label="Loading history" />}
            {history.isError && <Failure message={(history.error as Error).message} retry={() => history.refetch()} />}
            {history.data && (
              <ol className="mt-3 ml-1 border-l-2 border-rule pl-4">
                {history.data.map((h, i) => (
                  <li key={i} className="relative pb-3 text-sm last:pb-0">
                    <span aria-hidden className="absolute -left-[1.4rem] top-1.5 size-2.5 rounded-full bg-ink" />
                    <span className="font-semibold">{h.status.replace("_", " ")}</span>
                    <span className="text-muted">, period {date(h.period_start)} to {date(h.period_end)}, access until {date(h.until)}</span>
                    <div className="text-muted">{when(h.at)}, {origin(h.source)}</div>
                  </li>
                ))}
              </ol>
            )}
          </motion.div>
        )}
      </AnimatePresence>
    </li>
  );
}
