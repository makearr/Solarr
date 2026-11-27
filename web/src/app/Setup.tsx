import { useEffect, useRef } from "react";
import { useQuery } from "@tanstack/react-query";

import { api, ok } from "@/api/client";
import { t, type MessageKey } from "@/lib/i18n";

import { AccountStep } from "./setup/AccountStep";
import { ConnectStep } from "./setup/ConnectStep";
import { FinishStep } from "./setup/FinishStep";
import { LanguagesStep } from "./setup/LanguagesStep";
import { LibrariesStep } from "./setup/LibrariesStep";
import { QualityStep } from "./setup/QualityStep";
import { ReviewStep } from "./setup/ReviewStep";
import { StartStep } from "./setup/StartStep";
import { ToolsStep } from "./setup/ToolsStep";
import { Centered } from "./setup/ui";
import { stepsOf, useWizardState, type StepId, type WizardPath } from "./setup/wizardState";

const STEP_NAMES: Record<StepId, MessageKey> = {
  start: "setup.step.start",
  languages: "setup.step.languages",
  connect: "setup.step.connect",
  libraries: "setup.step.libraries",
  quality: "setup.step.quality",
  tools: "setup.step.tools",
  review: "setup.step.review",
  finish: "setup.step.finish",
};

/** The setup assistant: Account first, then a path of named steps with Back and Next in the same place, a Review of everything
 *  before anything heavy runs, and a Finish that does the work. Where it stopped is kept on the server, so a reload carries on.
 *  `rerun` is the same assistant opened later from Settings: no account step, and it only adds, never overwrites. */
export function Setup({ onDone, rerun = false }: { onDone: () => void; rerun?: boolean }) {
  const status = useQuery({ queryKey: ["setup"], queryFn: () => ok(api.GET("/setup")) });
  const wizard = useWizardState();
  const reset = useRef(false);
  useEffect(() => {
    if (rerun && wizard.ready && !reset.current) {
      reset.current = true;
      wizard.reset();
    }
  }, [rerun, wizard]);

  if (status.isPending) return <Centered>{t("setup.checking")}</Centered>;
  if (status.isError) return <Centered>{t("about.unreachable")}</Centered>;
  const needsAccount = status.data.needs_administrator;
  if (!needsAccount && (!wizard.ready || (rerun && !reset.current))) return <Centered>{t("setup.checking")}</Centered>;

  const { state, update } = wizard;
  const steps = stepsOf(state.path);
  const at = steps.indexOf(state.step);
  const go = (step: StepId) => update({ ...state, step });
  const next = () => go(steps[Math.min(at + 1, steps.length - 1)]!);
  const back = () => go(steps[Math.max(at - 1, 0)]!);
  const leave = () => {
    update({ ...state, finished: true });
    onDone();
  };
  const choose = (path: WizardPath) => update({ ...state, path, step: "languages" });

  return (
    <Centered>
      <div className="w-full max-w-2xl space-y-6">
        <header className="space-y-1">
          <img src="/favicon.svg" alt="" className="mb-3 size-12" />
          <h1 className="text-lg font-semibold tracking-tight">{t("setup.setUpSolarr")}</h1>
          <p className="text-sm text-ink-muted">{rerun ? t("setup.rerunIntro") : t("setup.intro")}</p>
        </header>

        <Stepper current={needsAccount ? "account" : state.step} steps={steps} showAccount={!rerun} />

        {needsAccount && <AccountStep />}
        {!needsAccount && state.step === "start" && <StartStep rerun={rerun} onChoose={choose} onLeave={leave} />}
        {!needsAccount && state.step === "languages" && (
          <LanguagesStep state={state} onChange={(languages) => update({ ...state, languages })} onBack={back} onNext={next} />
        )}
        {!needsAccount && state.step === "connect" && <ConnectStep state={state} update={update} onBack={back} onNext={next} />}
        {!needsAccount && state.step === "libraries" && <LibrariesStep state={state} update={update} onBack={back} onNext={next} />}
        {!needsAccount && state.step === "quality" && <QualityStep state={state} update={update} onBack={back} onNext={next} />}
        {!needsAccount && state.step === "tools" && <ToolsStep state={state} update={update} onBack={back} onNext={next} />}
        {!needsAccount && state.step === "review" && <ReviewStep state={state} onBack={back} onFinish={next} />}
        {!needsAccount && state.step === "finish" && <FinishStep state={state} update={update} onDone={onDone} />}
      </div>
    </Centered>
  );
}

function Stepper({ current, steps, showAccount }: { current: StepId | "account"; steps: readonly StepId[]; showAccount: boolean }) {
  const all: (StepId | "account")[] = [...(showAccount ? (["account"] as const) : []), ...steps];
  const at = all.indexOf(current);
  return (
    <ol aria-label={t("setup.progress")} className="flex flex-wrap gap-x-2 gap-y-1 text-xs">
      {all.map((step, index) => {
        const state = index < at ? "done" : index === at ? "current" : "todo";
        return (
          <li
            key={step}
            aria-current={state === "current" ? "step" : undefined}
            className={`flex items-center gap-1 rounded-full border px-2 py-0.5 ${
              state === "current" ? "border-accent bg-accent/10 text-ink" : state === "done" ? "border-border-subtle text-ink" : "border-border-subtle text-ink-muted"
            }`}
          >
            <span aria-hidden>{state === "done" ? "✓" : index + 1}</span>
            {step === "account" ? t("setup.step.account") : t(STEP_NAMES[step])}
          </li>
        );
      })}
    </ol>
  );
}
