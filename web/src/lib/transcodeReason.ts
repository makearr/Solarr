import type { components } from "@/api/schema";
import { tOr } from "@/lib/i18n";

type Reason = components["schemas"]["TranscodeReason"];

/** A reason in words: the sentence of its code, with what was wanted and what the file has when the code names them. */
export function reasonText(reason: Reason): string {
  const sentence = tOr(`transcode.reason.${reason.code}`, reason.code.replaceAll("_", " "));
  const detail = [reason.want && `wanted ${reason.want}`, reason.have && `has ${reason.have}`].filter(Boolean).join(", ");
  return detail ? `${sentence} (${detail})` : sentence;
}
