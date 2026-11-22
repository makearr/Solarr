import { useQueryClient } from "@tanstack/react-query";
import { useRouter } from "@tanstack/react-router";
import { Component, type ErrorInfo, type ReactNode } from "react";
import { t } from "@/lib/i18n";

import { Button } from "@/ui/Button";

/** What "Report" copies: the failure and the build, so a bug report needs no screenshots. */
export function reportText(error: unknown, version: string | undefined, where: string): string {
  const failure = error instanceof Error ? (error.stack ?? error.message) : String(error);
  return [`Solarr ${version ?? "(version unknown)"}`, `At ${where}`, failure].join("\n");
}

function Failure({ title, error, where, onRetry }: { title: string; error: unknown; where: string; onRetry: () => void }) {
  const client = useQueryClient();
  const version = (client.getQueryData(["about"]) as { version?: string } | undefined)?.version;
  const message = error instanceof Error ? error.message : String(error);
  return (
    <div role="alert" className="m-4 max-w-xl rounded-md border border-danger p-4 text-sm">
      <p className="font-medium">{title}</p>
      <p className="mt-1 break-words text-ink-muted">{message}</p>
      <div className="mt-3 flex gap-2">
        <Button onClick={onRetry}>Try again</Button>
        <Button onClick={() => void navigator.clipboard.writeText(reportText(error, version, where))}>Report</Button>
      </div>
    </div>
  );
}

/** Router `defaultErrorComponent`: the shell around the route stays, only the page is replaced. */
export function RouteError({ error }: { error: unknown }) {
  const router = useRouter();
  return (
    <Failure
      title={t("errorBoundary.title")}
      error={error}
      where={window.location.pathname}
      onRetry={() => void router.invalidate()}
    />
  );
}

interface Props {
  readonly name: string;
  readonly children: ReactNode;
}

/** Keeps one region's render error from unmounting the whole application. */
export class ErrorBoundary extends Component<Props, { error?: { value: unknown } }> {
  state: { error?: { value: unknown } } = {};

  static getDerivedStateFromError(error: unknown) {
    return { error: { value: error } };
  }

  componentDidCatch(error: unknown, info: ErrorInfo) {
    console.error(`${this.props.name} failed`, error, info.componentStack);
  }

  render() {
    if (this.state.error === undefined) return this.props.children;
    return (
      <Failure
        title={`${this.props.name} failed`}
        error={this.state.error.value}
        where={`${this.props.name} at ${window.location.pathname}`}
        onRetry={() => this.setState({ error: undefined })}
      />
    );
  }
}
