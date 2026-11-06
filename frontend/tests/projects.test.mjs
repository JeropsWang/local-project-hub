import assert from "node:assert/strict";
import { test } from "node:test";
import { bindExecutable, buildCommand, createProject, editCommand, emptyCommand } from "../src/business/projects.ts";

test("directory registration supplies the required default group without exporting the local path", () => {
  const source = "D:/QA/source";
  const start = { ...emptyCommand, target: "bin\\app.exe", args: "one argument\n--verbose" };
  const { document, bindings } = createProject("QA app", "", source, start);
  assert.equal(document.group, "未分组");
  assert.equal(document.units.app.start.executable.relativePath, "bin/app.exe");
  assert.deepEqual(document.units.app.start.args, ["one argument", "--verbose"]);
  assert.equal(document.units.app.stop.kind, "manual");
  assert.equal(JSON.stringify(document).includes(source), false);
  assert.equal(bindings[0].localLocator, source);
});

test("editing argv keeps bound credentials and the original timeout", () => {
  const original = {
    executable: { kind: "system", tool: "powershell" },
    args: ["-File", "${file:stop-script}"],
    cwd: { binding: "source", relativePath: "." },
    envBindings: { APP_TOKEN: "app-token" },
    timeoutSeconds: 30,
  };
  const result = buildCommand(editCommand(original), original);
  assert.deepEqual(result, original);
});

test("an executable outside the source directory is stored as a separate machine binding", () => {
  const { binding, command } = bindExecutable("C:\\node.exe", "start-executable", emptyCommand);
  assert.equal(binding.localLocator, "C:\\");
  const request = createProject("QA app", "", "D:/QA/source", command, undefined, [binding]);
  assert.equal(request.document.units.app.start.executable.binding, "start-executable");
  assert.equal(request.document.units.app.start.executable.relativePath, "node.exe");
  assert.deepEqual(request.bindings[1], binding);
});
