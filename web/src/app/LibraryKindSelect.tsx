import { useMediaTypes } from "@/lib/useMediaTypes";
import { inputStyle, labelStyle } from "@/ui/form";

/** A library's kind within its media type (TV: Series, Anime, ...); nothing for a type of one kind. "" is the type's first. */
export function KindSelect({ mediaType, value, onChange }: { mediaType: string; value: string; onChange: (kind: string) => void }) {
  const kinds = useMediaTypes().find((type) => type.key === mediaType)?.kinds ?? [];
  if (kinds.length === 0) return null;
  const chosen = kinds.find((kind) => kind.key === value) ?? kinds[0]!;
  return (
    <label className={labelStyle} title={chosen.help}>
      Kind
      <select className={inputStyle} value={chosen.key} onChange={(event) => onChange(event.target.value)}>
        {kinds.map((kind) => (
          <option key={kind.key} value={kind.key}>
            {kind.label}
          </option>
        ))}
      </select>
    </label>
  );
}
