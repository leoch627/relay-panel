import assert from "node:assert/strict";
import test from "node:test";
import { loadTS } from "./load-ts.mjs";

const { nodeInstallCommand } = await loadTS("../src/lib/nodeInstall.ts");
const direct = { enabled: true, generated: true, fingerprint: "abc123", pin_recommended: true };

test("direct self-signed HTTPS installs with the recommended fingerprint", () => {
  assert.match(nodeInstallCommand("https://panel.example", "secret", direct, "auto"), /PANEL_TLS_FP='abc123'/);
});

test("HTTPS proxy does not pin the origin certificate", () => {
  const command = nodeInstallCommand("https://panel.example", "secret", { ...direct, pin_recommended: false }, "auto");
  assert.doesNotMatch(command, /PANEL_TLS_FP=/);
  assert.match(command, /系统 CA/);
  assert.doesNotMatch(command, /未加密/);
});

test("explicit public certificate mode overrides a guessed pin recommendation", () => {
  assert.doesNotMatch(nodeInstallCommand("https://panel.example", "secret", direct, "public"), /PANEL_TLS_FP=/);
});

test("manual pin is possible when proxy detection is ambiguous", () => {
  assert.match(nodeInstallCommand("https://panel.example", "secret", { ...direct, pin_recommended: false }, "pin"), /PANEL_TLS_FP='abc123'/);
  assert.throws(() => nodeInstallCommand("https://panel.example", "secret", null, "pin"), /指纹/);
});

test("HTTP connections are not falsely described as HTTPS or pinned", () => {
  const command = nodeInstallCommand("http://127.0.0.1:8081", "secret", direct, "auto");
  assert.doesNotMatch(command, /PANEL_TLS_FP=/);
  assert.match(command, /HTTP，节点通信未加密/);
});

test("generated shell assignments quote values", () => {
  assert.match(nodeInstallCommand("https://panel.example", "a'b", direct, "public"), /PANEL_NODE_KEY='a'\\''b'/);
});
