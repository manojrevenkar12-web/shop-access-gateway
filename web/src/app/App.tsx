import { useState } from "react";
import { CustomerView } from "../features/customer/CustomerView";
import { InboxView } from "../features/inbox/InboxView";
import { StudyView } from "../features/study/StudyView";
import { VatView } from "../features/vat/VatView";

const tabs = [
  { id: "customer", label: "Customer", view: CustomerView },
  { id: "inbox", label: "Support inbox", view: InboxView },
  { id: "vat", label: "Checkout VAT", view: VatView },
  { id: "study", label: "Design study", view: StudyView },
] as const;

export function App() {
  const [tab, setTab] = useState<(typeof tabs)[number]["id"]>("customer");
  const View = tabs.find((t) => t.id === tab)!.view;
  return (
    <div className="mx-auto max-w-[var(--width-page)] px-6 pb-16">
      <header className="flex flex-wrap items-end justify-between gap-4 border-b border-rule pt-8 pb-3">
        <div>
          <h1 className="text-xl font-semibold leading-none">Access desk</h1>
          <p className="mt-1 text-sm text-muted">Who gets in, why, and what support did about it. Sandbox data.</p>
        </div>
        <nav aria-label="Sections" className="flex gap-1" role="tablist">
          {tabs.map((t) => (
            <button key={t.id} role="tab" aria-selected={tab === t.id} onClick={() => setTab(t.id)}
              className={`rounded-[var(--radius-field)] px-3 py-1.5 text-sm ${tab === t.id ? "bg-ink text-white" : "text-muted hover:text-ink"}`}>
              {t.label}
            </button>
          ))}
        </nav>
      </header>
      <main className="pt-8"><View /></main>
    </div>
  );
}
