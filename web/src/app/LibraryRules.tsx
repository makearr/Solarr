import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useParams } from "@tanstack/react-router";
import { useEffect, useState } from "react";

import { api, ok } from "@/api/client";
import type { components } from "@/api/schema";
import { Button } from "@/ui/Button";

import { Card } from "./SettingsCard";

type LibraryRule = components["schemas"]["LibraryRule"];
type Field = components["schemas"]["RuleCondition"]["field"];

const FIELDS: readonly [Field, string, string][] = [
  ["genre", "Genre", "Science Fiction"],
  ["language", "Original language", "ja"],
  ["country", "From country", "JP"],
  ["anilist", "Known to AniList", ""],
];

const field = "rounded-md border border-border-subtle bg-canvas px-2 py-1 text-sm";

/** The rules a library suggests itself by when something new is added (ADR-56, plan 10-libraries L5): a work matching
 *  every condition of any rule is suggested here, the rule matching most winning among libraries. */
export function LibraryRules() {
  const { categoryId } = useParams({ strict: false }) as { categoryId: string };
  const id = Number(categoryId);
  const queryClient = useQueryClient();
  const rules = useQuery({ queryKey: ["library-rules", id], queryFn: () => ok(api.GET("/categories/{id}/rules", { params: { path: { id } } })) });
  const [draft, setDraft] = useState<LibraryRule[]>();
  useEffect(() => {
    if (rules.data) setDraft(rules.data.rules);
  }, [rules.data]);
  const save = useMutation({
    mutationFn: (next: LibraryRule[] | null) => ok(api.PUT("/categories/{id}/rules", { params: { path: { id } }, body: { rules: next } })),
    onSuccess: (data) => {
      queryClient.setQueryData(["library-rules", id], data);
      setDraft(data.rules);
    },
  });
  if (!rules.data || !draft) return <p className="p-6 text-sm text-ink-muted">Loading…</p>;
  const setRule = (index: number, rule: LibraryRule) => setDraft(draft.map((entry, at) => (at === index ? rule : entry)));

  return (
    <div className="mx-auto flex max-w-3xl flex-col gap-4 p-4">
      <Card
        title="Suggested for"
        help="When you add something from anywhere, this library is suggested for it when every condition of one of these rules holds; the library whose rule matches the most wins. A genre matches the genres below it too."
      >
        <p className="text-xs text-ink-muted">
          {rules.data.own ? "This library's own rules." : rules.data.rules.length > 0 ? "Its kind's rules; change them to make them this library's own." : "No rules: nothing is suggested here."}
        </p>
        <ul className="flex flex-col gap-2">
          {draft.map((rule, index) => (
            <li key={index} className="flex flex-col gap-1 rounded-md border border-border-subtle p-2">
              {rule.conditions.map((condition, at) => (
                <div key={at} className="flex flex-wrap items-center gap-2">
                  <span className="w-8 text-xs text-ink-muted">{at === 0 ? "If" : "and"}</span>
                  <select
                    aria-label="Fact"
                    value={condition.field}
                    onChange={(event) =>
                      setRule(index, { conditions: rule.conditions.map((entry, i) => (i === at ? { field: event.target.value as Field, value: entry.value } : entry)) })
                    }
                    className={field}
                  >
                    {FIELDS.map(([key, label]) => (
                      <option key={key} value={key}>
                        {label}
                      </option>
                    ))}
                  </select>
                  {condition.field !== "anilist" && (
                    <input
                      aria-label="Value"
                      value={condition.value ?? ""}
                      placeholder={FIELDS.find(([key]) => key === condition.field)?.[2]}
                      onChange={(event) =>
                        setRule(index, { conditions: rule.conditions.map((entry, i) => (i === at ? { ...entry, value: event.target.value } : entry)) })
                      }
                      className={field}
                    />
                  )}
                  <button
                    type="button"
                    aria-label="Remove this condition"
                    onClick={() => {
                      const conditions = rule.conditions.filter((_, i) => i !== at);
                      setDraft(conditions.length === 0 ? draft.filter((_, i) => i !== index) : draft.map((entry, i) => (i === index ? { conditions } : entry)))
                    }}
                    className="text-xs text-ink-muted hover:text-danger"
                  >
                    ×
                  </button>
                </div>
              ))}
              <button
                type="button"
                onClick={() => setRule(index, { conditions: [...rule.conditions, { field: "genre", value: "" }] })}
                className="self-start text-xs text-ink-muted hover:text-ink"
              >
                + and…
              </button>
            </li>
          ))}
        </ul>
        <div className="flex flex-wrap gap-2">
          <Button onClick={() => setDraft([...draft, { conditions: [{ field: "genre", value: "" }] }])}>Add a rule</Button>
          <Button variant="primary" busy={save.isPending} onClick={() => save.mutate(draft)}>
            Save
          </Button>
          {rules.data.own && rules.data.kind_rules.length > 0 && (
            <Button variant="ghost" onClick={() => save.mutate(null)}>
              Back to its kind's rules
            </Button>
          )}
        </div>
        {save.isError && <p className="text-xs text-danger">{(save.error as { detail?: string }).detail ?? "The rules could not be saved."}</p>}
      </Card>
    </div>
  );
}
