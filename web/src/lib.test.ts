import { describe, expect, it } from "vitest";
import { insertIntoSection, lineDiff, stateClass, stateLabel } from "./lib";

describe("state helpers", () => {
  it("classifies active states", () => {
    expect(stateClass({ scope: "system", name: "a.container", kind: "container", active: "active" })).toBe("running");
    expect(stateClass({ scope: "system", name: "a.container", kind: "container", active: "failed" })).toBe("failed");
    expect(stateClass({ scope: "system", name: "a.container", kind: "container", load: "unknown" })).toBe("unknown");
  });

  it("includes exit codes in labels", () => {
    expect(stateLabel({ scope: "system", name: "a.container", kind: "container", active: "failed", result: "exit-code", exitCode: 2 })).toContain("exit 2");
  });
});

describe("editor helpers", () => {
  it("inserts into existing sections", () => {
    expect(insertIntoSection("[Container]\nImage=x\n", "Container", ["Secret=db"])).toBe("[Container]\nSecret=db\nImage=x\n");
  });

  it("computes added and removed lines", () => {
    expect(lineDiff("a\nb", "a\nc").map(([op]) => op)).toEqual([" ", "-", "+"]);
  });
});
