import { useEffect, useRef, useState } from "preact/hooks";
import { X } from "lucide-preact";
import type { Mark } from "../types";

// The fields an example can tag. The first four are required.
export const tagFields = [
  { field: "amount", label: "Amount", required: true },
  { field: "merchant", label: "Merchant", required: true },
  { field: "account", label: "Card / account", required: true },
  { field: "date", label: "Date", required: true },
  { field: "currency", label: "Currency", required: false },
  { field: "reference", label: "Reference", required: false },
];

// missingFields lists required fields not yet tagged. With a description,
// the merchant is optional: the email may not name one.
export function missingFields(marks: Mark[], description = "") {
  return tagFields.filter(
    (f) =>
      f.required &&
      !(f.field === "merchant" && description.trim()) &&
      !marks.some((m) => m.field === f.field),
  );
}

// offset is how many characters of the container's text come before
// (node, at): the same UTF-16 offset the server converts back.
function offset(container: Node, node: Node, at: number) {
  const range = document.createRange();
  range.setStart(container, 0);
  range.setEnd(node, at);
  return range.toString().length;
}

// ExampleTagger shows the email text. Select some of it, then press the
// field it is; tagged text is highlighted.
export function ExampleTagger({
  body,
  marks,
  setMarks,
}: {
  body: string;
  marks: Mark[];
  setMarks: (marks: Mark[]) => void;
}) {
  const text = useRef<HTMLDivElement>(null);
  const [selection, setSelection] = useState<{ start: number; end: number } | null>(null);
  // Keep the last selection inside the text, so tapping a field on a phone
  // (which clears the selection) still tags it.
  useEffect(() => {
    const changed = () => {
      const current = document.getSelection();
      const container = text.current;
      if (!current || current.isCollapsed || !current.rangeCount || !container) return;
      const range = current.getRangeAt(0);
      if (!container.contains(range.startContainer) || !container.contains(range.endContainer))
        return;
      let start = offset(container, range.startContainer, range.startOffset),
        end = offset(container, range.endContainer, range.endOffset);
      while (start < end && /\s/.test(body[start])) start++;
      while (end > start && /\s/.test(body[end - 1])) end--;
      setSelection(end > start ? { start, end } : null);
    };
    document.addEventListener("selectionchange", changed);
    return () => document.removeEventListener("selectionchange", changed);
  }, [body]);
  function tag(field: string) {
    if (!selection) return;
    const { start, end } = selection;
    setMarks(
      [
        ...marks.filter((m) => m.field !== field && (m.end <= start || m.start >= end)),
        { field, start, end },
      ].sort((a, b) => a.start - b.start),
    );
    setSelection(null);
    document.getSelection()?.removeAllRanges();
  }
  const segments = [];
  let at = 0;
  for (const m of [...marks].sort((a, b) => a.start - b.start)) {
    if (m.start > at) segments.push(body.slice(at, m.start));
    segments.push(
      <mark class={"tag tag-" + m.field} title={m.field}>
        {body.slice(m.start, m.end)}
      </mark>,
    );
    at = m.end;
  }
  segments.push(body.slice(at));
  return (
    <div class="tagger">
      <p class="hint">
        {selection
          ? `Selected “${body.slice(selection.start, selection.end).slice(0, 60)}”. What is it?`
          : "Select a value in the email, then choose what it is."}
      </p>
      <div class="tag-buttons" role="group" aria-label="Tag the selection">
        {tagFields.map((f) => {
          const mark = marks.find((m) => m.field === f.field);
          return (
            <div class={"tag-button tag-" + f.field + (mark ? " tagged" : "")} key={f.field}>
              <button
                type="button"
                disabled={!selection}
                // Keep the text selected on desktop browsers.
                onMouseDown={(e) => e.preventDefault()}
                onClick={() => tag(f.field)}
              >
                <span class="eyebrow">
                  {f.label}
                  {f.required ? "" : " · optional"}
                </span>
                <span class="tag-value">
                  {mark ? body.slice(mark.start, mark.end) : "Not tagged"}
                </span>
              </button>
              {mark && (
                <button
                  type="button"
                  class="icon-btn"
                  aria-label={`Clear ${f.label}`}
                  onClick={() => setMarks(marks.filter((m) => m !== mark))}
                >
                  <X size={13} />
                </button>
              )}
            </div>
          );
        })}
      </div>
      <div ref={text} class="mono tag-text" aria-label="Email text" tabIndex={0}>
        {segments}
      </div>
    </div>
  );
}
