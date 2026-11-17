export interface Throttled {
  readonly call: () => void;
  readonly cancel: () => void;
}

/** Runs `run` at once, then at most once per `waitMs`: calls inside the window collapse into one trailing run. */
export function throttle(run: () => void, waitMs: number): Throttled {
  let timer: ReturnType<typeof setTimeout> | undefined;
  let pending = false;

  const open = () => {
    timer = setTimeout(() => {
      timer = undefined;
      if (!pending) return;
      pending = false;
      run();
      open();
    }, waitMs);
  };

  return {
    call: () => {
      if (timer !== undefined) {
        pending = true;
        return;
      }
      run();
      open();
    },
    cancel: () => {
      clearTimeout(timer);
      timer = undefined;
      pending = false;
    },
  };
}
