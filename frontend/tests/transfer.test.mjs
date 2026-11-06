import assert from "node:assert/strict";
import { test } from "node:test";
import { collectImportBindings, documentBindingRequirements, transferError } from "../src/business/transfer.ts";

test("uses the portable binding keys and distinguishes runtime, path, and credential references", () => {
  const document = {
    files: { app: { path: { binding: "source" } } },
    requirements: { node: { location: "windows" }, redis: { location: "docker" } },
    units: {
      app: {
        start: {
          cwd: { binding: "source" },
          executable: { requirementId: "node" },
          envBindings: { TOKEN: "token" },
        },
      },
    },
  };
  const requirements = documentBindingRequirements(document, ["source", "node", "token", "unspecified"]);
  assert.deepEqual(requirements.map(({ kind }) => kind), ["path", "runtime", "secret", undefined]);
  assert.deepEqual(collectImportBindings(requirements, {
    source: { value: " D:/Projects/example " },
    token: { value: "dpapi:fixture" },
    unspecified: { kind: "resource", value: "desktop" },
  }), [
    { key: "source", kind: "path", localLocator: "D:/Projects/example" },
    { key: "token", kind: "secret", secretRef: "dpapi:fixture" },
    { key: "unspecified", kind: "resource", localLocator: "desktop" },
  ]);
});

test("ambiguous keys need an explicit kind before they can be submitted", () => {
  const document = {
    units: { app: { start: { cwd: { binding: "ambiguous" }, envBindings: { TOKEN: "ambiguous" } } } },
  };
  const requirements = documentBindingRequirements(document, ["ambiguous"]);
  assert.deepEqual(requirements, [{ key: "ambiguous", kind: undefined }]);
  assert.deepEqual(collectImportBindings(requirements, { ambiguous: { value: "fixture" } }), []);
});

test("reports migration-specific recovery for source archives, expired previews and duplicates", () => {
  assert.match(transferError(new Error("BUNDLE_MANIFEST_MISSING")), /GitHub 源码 ZIP/);
  assert.match(transferError(new Error("IMPORT_PREVIEW_EXPIRED")), /重新预览/);
  assert.match(transferError(new Error("PROJECT_EXISTS")), /已有同一项目/);
});
