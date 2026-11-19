import { useEffect, useRef, useState } from "preact/hooks";
import { money } from "../lib";

// Charts are plain SVG drawn at their measured width: one hue (the accent)
// for what the chart is about, gray for context, hairline gridlines, and a
// tooltip on hover or keyboard focus. Every value also appears as text
// elsewhere on the page.

/** compact is a short axis or cap label: ₹950, ₹12K, ₹1.4L. */
export function compact(amount: number, currency: string) {
  const v = amount / 100;
  const symbol = !currency || currency.toUpperCase() === "INR" ? "₹" : currency.toUpperCase() + " ";
  const short = (n: number, unit: string) =>
    (n >= 10 ? Math.round(n) : Math.round(n * 10) / 10).toString() + unit;
  if (symbol === "₹" && v >= 100000) return symbol + short(v / 100000, "L");
  if (v >= 1000) return symbol + short(v / 1000, "K");
  return symbol + Math.round(v);
}

// niceMax rounds a maximum up to a round step (1, 1.5, 2, 2.5, 3, 4, 5, 6 or 8
// times a power of ten), so the tallest mark nearly fills the plot.
function niceMax(v: number) {
  if (v <= 0) return 100;
  const p = Math.pow(10, Math.floor(Math.log10(v)));
  return [1, 1.5, 2, 2.5, 3, 4, 5, 6, 8, 10].map((m) => m * p).find((m) => m >= v) ?? 10 * p;
}

function useWidth() {
  const ref = useRef<HTMLDivElement>(null);
  const [width, setWidth] = useState(640);
  useEffect(() => {
    const el = ref.current;
    if (!el || typeof ResizeObserver === "undefined") return;
    const observer = new ResizeObserver(([entry]) =>
      setWidth(Math.max(280, entry.contentRect.width)),
    );
    observer.observe(el);
    return () => observer.disconnect();
  }, []);
  return [ref, width] as const;
}

// A tip right of the middle opens to the left of x, so it stays inside.
type Tip = {
  x: number;
  y: number;
  left?: boolean;
  lines: { value: string; label: string; key?: string }[];
};

function Tooltip({ tip }: { tip: Tip | null }) {
  if (!tip) return null;
  return (
    <div
      class="chart-tip"
      style={{ left: tip.x, top: tip.y, transform: tip.left ? "translateX(-100%)" : undefined }}
      role="status"
    >
      {tip.lines.map((l) => (
        <div class="chart-tip-row" key={l.label}>
          {l.key && <span class={"line-key " + l.key} />}
          <strong>{l.value}</strong>
          <span>{l.label}</span>
        </div>
      ))}
    </div>
  );
}

/**
 * Columns, one per month, oldest first. The focused month wears the
 * accent; picking a column picks its month.
 */
export function MonthColumns({
  months,
  focus,
  currency,
  label,
  pick,
}: {
  months: { month: string; amount: number }[];
  focus: string;
  currency: string;
  label: string;
  pick: (month: string) => void;
}) {
  const [ref, width] = useWidth();
  const [tip, setTip] = useState<Tip | null>(null);
  const height = 220,
    top = 22,
    bottom = 26,
    left = 46,
    right = 4;
  const plot = height - top - bottom;
  const max = niceMax(Math.max(...months.map((m) => m.amount), 0));
  const slot = (width - left - right) / Math.max(1, months.length);
  const bar = Math.min(24, slot * 0.6);
  const y = (v: number) => top + plot - (v / max) * plot;
  const largest = months.reduce((a, b) => (b.amount > a.amount ? b : a), months[0]);
  const name = (m: string, long = false) =>
    new Date(Number(m.slice(0, 4)), Number(m.slice(5, 7)) - 1, 1).toLocaleDateString("en-GB", {
      month: long ? "long" : "short",
      year: long ? "numeric" : undefined,
    });
  const every = slot < 34 ? 2 : 1;
  return (
    <div class="chart" ref={ref} onPointerLeave={() => setTip(null)}>
      <svg width={width} height={height} role="img" aria-label={label}>
        {[0, max / 2, max].map((t) => (
          <g key={t}>
            <line class="grid" x1={left} x2={width - right} y1={y(t)} y2={y(t)} />
            <text class="tick" x={left - 8} y={y(t) + 4} text-anchor="end">
              {compact(t, currency)}
            </text>
          </g>
        ))}
        {months.map((m, i) => {
          const cx = left + slot * i + slot / 2;
          const h = Math.max(0, y(0) - y(m.amount));
          const x = cx - bar / 2;
          const r = Math.min(4, h, bar / 2);
          const d =
            h > 0
              ? `M${x},${y(0)} V${y(m.amount) + r} Q${x},${y(m.amount)} ${x + r},${y(m.amount)} H${x + bar - r} Q${x + bar},${y(m.amount)} ${x + bar},${y(m.amount) + r} V${y(0)} Z`
              : "";
          const show = () =>
            setTip({
              x: cx > width / 2 ? cx - 8 : cx + 8,
              left: cx > width / 2,
              y: Math.max(0, y(m.amount) - 56),
              lines: [{ value: money(m.amount, currency), label: name(m.month, true) }],
            });
          return (
            <g key={m.month} class={"column" + (m.month === focus ? " focus" : "")}>
              {d && <path d={d} />}
              {(m.month === focus || (m === largest && m.amount > 0)) && (
                <text class="cap" x={cx} y={y(m.amount) - 6} text-anchor="middle">
                  {compact(m.amount, currency)}
                </text>
              )}
              {i % every === (months.length - 1) % every && (
                <text class="tick" x={cx} y={height - 8} text-anchor="middle">
                  {name(m.month)}
                </text>
              )}
              <rect
                class="hit"
                x={left + slot * i}
                y={top}
                width={slot}
                height={plot}
                tabIndex={0}
                role="button"
                aria-label={`${name(m.month, true)}: ${money(m.amount, currency)}`}
                onPointerMove={show}
                onFocus={show}
                onBlur={() => setTip(null)}
                onClick={() => pick(m.month)}
                onKeyDown={(e) => (e.key === "Enter" || e.key === " ") && pick(m.month)}
              />
            </g>
          );
        })}
      </svg>
      <Tooltip tip={tip} />
    </div>
  );
}

/**
 * Running totals through a month against the month before: the month in the
 * accent, the one before in gray, with a crosshair that finds the day.
 */
export function RunningTotal({
  daily,
  previous,
  days,
  currency,
  names,
}: {
  daily: number[];
  previous: number[];
  days: number;
  currency: string;
  names: [string, string];
}) {
  const [ref, width] = useWidth();
  const [day, setDay] = useState(0);
  const sum = (a: number[]) => a.reduce<number[]>((out, v) => [...out, (out.at(-1) ?? 0) + v], []);
  const now = sum(daily).slice(0, Math.max(1, days));
  const before = sum(previous);
  const height = 220,
    top = 12,
    bottom = 26,
    left = 46,
    right = 12;
  const plot = height - top - bottom;
  const length = Math.max(daily.length, previous.length);
  const max = niceMax(Math.max(now.at(-1) ?? 0, before.at(-1) ?? 0));
  const x = (d: number) => left + ((d - 1) / Math.max(1, length - 1)) * (width - left - right);
  const y = (v: number) => top + plot - (v / max) * plot;
  const path = (a: number[]) => a.map((v, i) => `${i ? "L" : "M"}${x(i + 1)},${y(v)}`).join(" ");
  const at = (a: number[], d: number) => (d <= a.length ? a[d - 1] : undefined);
  return (
    <div
      class="chart"
      ref={ref}
      onPointerMove={(e) => {
        const box = (e.currentTarget as HTMLElement).getBoundingClientRect();
        const d =
          Math.round(((e.clientX - box.left - left) / (width - left - right)) * (length - 1)) + 1;
        setDay(Math.min(length, Math.max(1, d)));
      }}
      onPointerLeave={() => setDay(0)}
    >
      <svg
        width={width}
        height={height}
        role="img"
        aria-label={`Running total: ${names[0]} ${money(now.at(-1) ?? 0, currency)} by day ${now.length}; ${names[1]} ${money(before.at(-1) ?? 0, currency)}`}
      >
        {[0, max / 2, max].map((t) => (
          <g key={t}>
            <line class="grid" x1={left} x2={width - right} y1={y(t)} y2={y(t)} />
            <text class="tick" x={left - 8} y={y(t) + 4} text-anchor="end">
              {compact(t, currency)}
            </text>
          </g>
        ))}
        {[1, 8, 15, 22, length].map((d) => (
          <text key={d} class="tick" x={x(d)} y={height - 8} text-anchor="middle">
            {d}
          </text>
        ))}
        {before.length > 0 && <path class="line before" d={path(before)} />}
        <path class="line now" d={path(now)} />
        <circle class="end" cx={x(now.length)} cy={y(now.at(-1) ?? 0)} r={4} />
        {day > 0 && <line class="crosshair" x1={x(day)} x2={x(day)} y1={top} y2={top + plot} />}
      </svg>
      <Tooltip
        tip={
          day > 0
            ? {
                x: x(day) > width / 2 ? x(day) - 10 : x(day) + 10,
                left: x(day) > width / 2,
                y: top,
                lines: [
                  { value: `Day ${day}`, label: "" },
                  ...(at(now, day) !== undefined
                    ? [{ value: money(at(now, day)!, currency), label: names[0], key: "now" }]
                    : []),
                  ...(at(before, day) !== undefined
                    ? [{ value: money(at(before, day)!, currency), label: names[1], key: "before" }]
                    : []),
                ],
              }
            : null
        }
      />
    </div>
  );
}
