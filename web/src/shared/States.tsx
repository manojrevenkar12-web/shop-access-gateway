import type { ReactNode } from "react";

export function Loading({ label }: { label: string }) {
  return (
    <div role="status" aria-label={label} className="flex flex-col gap-2 py-4">
      {[0, 1, 2].map((i) => <div key={i} className="h-10 animate-pulse rounded-[var(--radius-field)] bg-rule/60" />)}
    </div>
  );
}

export function Failure({ message, retry }: { message: string; retry?: () => void }) {
  return (
    <div role="alert" className="rounded-[var(--radius-panel)] border-l-4 border-shut bg-shut-soft px-4 py-3 text-sm">
      <p>{message}</p>
      {retry && <button onClick={retry} className="mt-2 font-semibold text-action underline underline-offset-4">Try again</button>}
    </div>
  );
}

export function Empty({ title, children }: { title: string; children?: ReactNode }) {
  return (
    <div className="px-1 py-6 text-sm text-muted">
      <p className="text-base font-semibold text-ink">{title}</p>
      {children && <div className="mt-1 max-w-prose">{children}</div>}
    </div>
  );
}
