import type { ComponentChildren } from "preact";
import { Inbox } from "lucide-preact";

export function Section({
  index,
  title,
  children,
}: {
  index: string;
  title: string;
  children?: ComponentChildren;
}) {
  return (
    <div class="section-head">
      <span class="section-index">{index}</span>
      <h2>{title}</h2>
      <div class="spacer" />
      {children}
    </div>
  );
}
export function ErrorNote({ error }: { error: string }) {
  return error ? (
    <div class="notice error" role="alert">
      {error}
    </div>
  ) : null;
}
export function Field({
  label,
  children,
  hint,
}: {
  label: string;
  children: ComponentChildren;
  hint?: string;
}) {
  return (
    <label class="field">
      <span class="eyebrow">{label}</span>
      {children}
      {hint && <span class="hint">{hint}</span>}
    </label>
  );
}
export function Empty({ title, children }: { title: string; children: ComponentChildren }) {
  return (
    <div class="empty">
      <Inbox size={28} />
      <h3>{title}</h3>
      <p>{children}</p>
    </div>
  );
}
