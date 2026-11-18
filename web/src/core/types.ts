export interface Reason { source: string; status: string; until: string; event: string; grants: boolean; describe: string }
export interface Access { user: number; granted: boolean; at: string; reasons: Reason[] }
export interface HistoryEntry { status: string; period_start: string; period_end: string; until: string; source: string; at: string }
export interface Example { user: number; label: string }
export interface Resolution { action: string; evidence: string; intent: string; user: number }
export interface Quote { country: string; rate_percent: number; reverse_charge: boolean; net_cents: number; vat_cents: number; gross_cents: number; basis: string }

export interface AccessResult {
  design: string; checks: number; wrong_denials: number; wrong_denials_per_1000: number; users_wrongly_denied: number;
  wrong_grants: number; leaked_euros: number; provider_api_calls: number; first_use_denied: number;
}
export interface SupportResult { agent: string; tickets: number; automated_right: number; escalated: number; wrong_money_or_access_actions: number; person_hours: number }
export interface Report { world: string; seed: number; webhook_deliveries: number; tickets: number; access: AccessResult[]; support: SupportResult[]; gate: { pass: boolean; checks: string[] } }
