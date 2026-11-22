import { Fragment, type ReactNode } from "react";

import { t, type MessageKey } from "./i18n";

/** The catalogue's text for key as nodes: each {placeholder} becomes the element given for it, text stays text. */
export function tr(key: MessageKey, parts: Record<string, ReactNode>): ReactNode {
  const pieces = t(key).split(/(\{\w+\})/);
  return pieces.map((piece, index) => {
    const name = /^\{(\w+)\}$/.exec(piece)?.[1];
    return <Fragment key={index}>{name !== undefined && name in parts ? parts[name] : piece}</Fragment>;
  });
}
