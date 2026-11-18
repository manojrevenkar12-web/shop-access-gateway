import { useReport } from "../../core/api";
import { Empty, Failure, Loading } from "../../shared/States";
import { count } from "../../shared/format";

/** The design study the gateway's shape came from: every design on the same simulated history. */
export function StudyView() {
  const report = useReport();
  if (report.isPending) return <Loading label="Loading the study" />;
  if (report.isError) return <Failure message={(report.error as Error).message} retry={() => report.refetch()} />;
  const r = report.data;
  if (!r.access.length) return <Empty title="No designs in the report">Run `make study` to fill it.</Empty>;
  const max = Math.max(...r.access.map((a) => a.wrong_denials_per_1000));
  return (
    <div>
      <p className="max-w-prose text-sm text-muted">
        Dev world, seed {r.seed}: {count(r.webhook_deliveries)} webhook deliveries and {count(r.access[0].checks)} page
        checks, the same for every design. Each step adds one thing to the step above.
      </p>
      <table className="mt-6 w-full text-sm">
        <caption className="sr-only">Wrong denials and leaked access by design</caption>
        <thead className="text-left text-muted">
          <tr><th className="py-2 font-normal">Design</th><th className="w-[40%] font-normal">Wrong denials per 1,000 checks</th>
            <th className="text-right font-normal">Customers locked out</th><th className="text-right font-normal">Access given away</th><th className="text-right font-normal">Provider calls</th></tr>
        </thead>
        <tbody>
          {r.access.map((a) => (
            <tr key={a.design} className={`border-t border-rule ${a.design === "gateway" ? "font-semibold" : ""}`}>
              <td className="py-2">{a.design.replaceAll("_", " ")}</td>
              <td>
                <div className="flex items-center gap-2">
                  <div className={`h-3 rounded-sm ${a.design === "gateway" ? "bg-open" : a.design === "naive" ? "bg-shut" : "bg-muted/50"}`}
                    style={{ width: `${Math.max(2, (a.wrong_denials_per_1000 / max) * 85)}%` }} />
                  <span>{a.wrong_denials_per_1000.toFixed(2)}</span>
                </div>
              </td>
              <td className="text-right">{count(a.users_wrongly_denied)}</td>
              <td className="text-right">€{count(a.leaked_euros)}</td>
              <td className="text-right">{count(a.provider_api_calls)}</td>
            </tr>
          ))}
        </tbody>
      </table>
      <h2 className="mt-10 text-lg font-semibold">Support inbox</h2>
      <table className="mt-3 w-full text-sm">
        <thead className="text-left text-muted"><tr><th className="py-2 font-normal">Who answers</th><th className="text-right font-normal">Closed without a person</th>
          <th className="text-right font-normal">Sent to a person</th><th className="text-right font-normal">Wrong refunds or grants</th><th className="text-right font-normal">Person-hours</th></tr></thead>
        <tbody>
          {r.support.map((s) => (
            <tr key={s.agent} className="border-t border-rule">
              <td className="py-2">{s.agent}</td><td className="text-right">{count(s.automated_right)}</td><td className="text-right">{count(s.escalated)}</td>
              <td className={`text-right ${s.wrong_money_or_access_actions ? "font-semibold text-shut" : ""}`}>{s.wrong_money_or_access_actions}</td>
              <td className="text-right">{count(s.person_hours)}</td>
            </tr>
          ))}
        </tbody>
      </table>
      <p className="mt-6 text-sm">Release gate: <span className={r.gate.pass ? "font-semibold text-open" : "font-semibold text-shut"}>{r.gate.pass ? "passes" : "fails"}</span></p>
    </div>
  );
}
