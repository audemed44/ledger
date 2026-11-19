import { useEffect, useState } from "preact/hooks";
import { api } from "../api";
import type { Category, CategoryRule } from "../types";
import { ErrorNote, Field } from "./ui";

// Categories lists your categories, to add, rename or remove, and the rules
// that fill them. Rules are made from a transaction's actions.
export function Categories({ version, saved }: { version: number; saved: () => void }) {
  const [categories, setCategories] = useState<Category[]>([]),
    [rules, setRules] = useState<CategoryRule[]>([]),
    [name, setName] = useState(""),
    [renaming, setRenaming] = useState<Category | null>(null),
    [error, setError] = useState("");
  function load() {
    Promise.all([api<Category[]>("categories"), api<CategoryRule[]>("category-rules")])
      .then(([c, r]) => {
        setCategories(c);
        setRules(r);
      })
      .catch((e) => setError(e.message));
  }
  useEffect(load, [version]);
  async function run(path: string, body: unknown, method?: "DELETE" | "PUT") {
    try {
      await api(path, body, method);
      setError("");
      load();
      saved();
      return true;
    } catch (e) {
      setError((e as Error).message);
      return false;
    }
  }
  return (
    <section class="statement-section">
      <div class="section-head">
        <span class="section-index">06</span>
        <h2>Categories</h2>
      </div>
      <p class="hint">
        What money went on. Open a transaction’s actions to put it in a category by hand, or to save
        a rule: “when the description contains this, put it in that”. Rules reach past and future
        transactions; the longest matching text wins, and a category set by hand always stays.
      </p>
      <ErrorNote error={error} />
      <form
        class="category-add"
        onSubmit={async (e) => {
          e.preventDefault();
          if (await run("categories", { name })) setName("");
        }}
      >
        <Field label="New category">
          <input value={name} onInput={(e) => setName(e.currentTarget.value)} maxLength={40} />
        </Field>
        <button class="btn" type="submit" disabled={!name.trim()}>
          Add
        </button>
      </form>
      <div class="category-list">
        {categories.map((c) =>
          renaming?.id === c.id ? (
            <form
              class="trigger"
              key={c.id}
              onSubmit={async (e) => {
                e.preventDefault();
                if (await run(`categories/${c.id}`, { name: renaming.name }, "PUT"))
                  setRenaming(null);
              }}
            >
              <input
                aria-label={`New name for ${c.name}`}
                value={renaming.name}
                maxLength={40}
                onInput={(e) => setRenaming({ ...renaming, name: e.currentTarget.value })}
              />
              <span class="actions">
                <button class="btn" type="submit">
                  Save
                </button>
                <button class="btn" type="button" onClick={() => setRenaming(null)}>
                  Cancel
                </button>
              </span>
            </form>
          ) : (
            <div class="trigger" key={c.id}>
              <span>{c.name}</span>
              <span class="actions">
                <button class="btn" onClick={() => setRenaming(c)}>
                  Rename
                </button>
                <button
                  class="btn"
                  onClick={() => {
                    if (
                      confirm(
                        `Remove “${c.name}”? Its rules go too, and its transactions become uncategorised.`,
                      )
                    )
                      run(`categories/${c.id}`, undefined, "DELETE");
                  }}
                >
                  Remove
                </button>
              </span>
            </div>
          ),
        )}
      </div>
      <h3 class="eyebrow rules-head">Category rules</h3>
      {rules.length ? (
        rules.map((r) => (
          <div class="trigger" key={r.id}>
            <span>
              Contains “{r.contains}”{r.issuer ? ` · ${r.issuer}` : ""} →{" "}
              <strong>{r.category}</strong>
            </span>
            <button class="btn" onClick={() => run(`category-rules/${r.id}`, undefined, "DELETE")}>
              Remove
            </button>
          </div>
        ))
      ) : (
        <p class="hint">No rules yet.</p>
      )}
    </section>
  );
}
