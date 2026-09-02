import { cleanup } from "@testing-library/react";
import { afterEach } from "vitest";

// Testing Library only cleans up on its own when vitest globals are enabled, and
// they are not: a test importing what it uses is worth the one line here.
afterEach(cleanup);

// The router scrolls to the top on navigation and jsdom has no scrollTo, which
// prints a page of "Not implemented" over every routed test.
window.scrollTo = () => {};
