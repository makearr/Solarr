import { Link } from "@tanstack/react-router";

import { api } from "@/api/client";
import { ListTable, type Column } from "@/list/ListTable";
import { useLibraryList } from "@/list/useLibraryList";
import { useRouteView } from "@/list/useRouteView";
import type { components } from "@/api/schema";

type Person = components["schemas"]["Person"];

const COLUMNS: readonly Column<Person>[] = [
  {
    key: "name",
    header: "Name",
    render: (person) => (
      <Link to="/people/$id" params={{ id: String(person.id) }} className="hover:underline">
        {person.name}
      </Link>
    ),
  },
  {
    key: "disambiguation",
    header: "",
    width: "10rem",
    render: (person) => person.disambiguation ?? "",
  },
  { key: "country", header: "Country", width: "8rem", render: (person) => person.country ?? "" },
  {
    key: "monitored",
    header: "Monitored",
    width: "7rem",
    render: (person) => (person.monitored ? "Yes" : ""),
  },
];

const FILTER_KEYS: readonly string[] = ["monitored", "q"];

export function People() {
  const list = useLibraryList<Person>(
    {
      name: "people",
      idOf: (person) => person.id,
      fetchPage: async ({ cursor, query, signal }) => {
        const { data, error } = await api.GET("/people", {
          params: {
            query: {
              cursor,
              limit: 100,
              monitored: query.filters.monitored ? query.filters.monitored === "true" : undefined,
              q: query.filters.q || undefined,
            },
          },
          signal,
        });
        if (error) throw error;
        return data;
      },
    },
    useRouteView(FILTER_KEYS),
  );

  return (
    <div className="flex h-full flex-col">
      <div className="flex shrink-0 items-center gap-3 border-b border-border-subtle px-4 py-2">
        <h2 className="text-sm font-medium">People</h2>
        <input
          type="search"
          placeholder="Search by name…"
          defaultValue={list.query.filters.q ?? ""}
          onChange={(event) => list.filterBy("q", event.target.value)}
          className="ml-2 rounded-md border border-border-subtle bg-canvas px-2 py-1 text-xs
                     focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
        />
        <label className="flex items-center gap-1.5 text-xs text-ink-muted">
          <input
            type="checkbox"
            checked={list.query.filters.monitored === "true"}
            onChange={(event) => list.filterBy("monitored", event.target.checked ? "true" : "")}
          />
          Monitored only
        </label>
      </div>

      <div className="min-h-0 flex-1">
        {list.isLoading && <Message>Loading…</Message>}
        {list.error !== null && <Message>People could not be read.</Message>}
        {!list.isLoading && list.error === null && list.items.length === 0 && (
          <Message>
            Nobody here yet. People are populated from credits as the library is enriched.
          </Message>
        )}

        {list.items.length > 0 && (
          <ListTable list={list} columns={COLUMNS} idOf={(person) => person.id} />
        )}
      </div>
    </div>
  );
}

function Message({ children }: { children: React.ReactNode }) {
  return <p className="p-6 text-sm text-ink-muted">{children}</p>;
}
