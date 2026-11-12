import assert from "node:assert/strict";
import test from "node:test";
import { loadTS } from "./load-ts.mjs";

const { parseBatchRules } = await loadTS("../src/lib/batchRules.ts");
const { ruleDestinations } = await loadTS("../src/lib/ruleDestinations.ts");

test("UDP can omit the optional remote port without opening TCP", () => {
  const { rules, errors } = parseBatchRules("20001 1.2.3.4 udp", [], 4);
  assert.deepEqual(errors, []);
  assert.equal(rules.length, 1);
  assert.equal(rules[0].enable_tcp, false);
  assert.equal(rules[0].enable_udp, true);
  assert.equal(rules[0].remote_tcp_port, 0);
  assert.equal(rules[0].remote_udp_port, 20001);
  assert.equal(rules[0].node_id, 4);
});

test("optional ports, explicit protocols and dual protocol defaults", () => {
  const { rules, errors } = parseBatchRules("named:20002 host.example 443 TCP\n20003 host.example both\n20004 host.example\n20005 host.example 53 udp", [], 0);
  assert.deepEqual(errors, []);
  assert.deepEqual(rules.map((r) => [r.enable_tcp, r.enable_udp, r.remote_tcp_port, r.remote_udp_port]), [
    [true, false, 443, 0], [true, true, 20003, 20003], [true, true, 20004, 20004], [false, true, 0, 53],
  ]);
});

test("automatic tags skip existing and later explicit tags across batches", () => {
  const first = parseBatchRules("20001 host\nB2:20002 host\n20003 host", ["B1"], 0);
  assert.deepEqual(first.errors, []);
  assert.deepEqual(first.rules.map((r) => r.tag), ["B3", "B2", "B4"]);
  const second = parseBatchRules("20004 host", ["B1", ...first.rules.map((r) => r.tag)], 0);
  assert.equal(second.rules[0].tag, "B5");
});

test("invalid ports and protocol tokens are errors instead of silent fallbacks", () => {
  for (const line of ["20001abc host", "0 host", "65536 host", "20001 host 0", "20001 host 99999 udp", "20001 host rubbish", "20001 host 53 rubbish", "20001 host udp 53", "bad!:20001 host"]) {
    const result = parseBatchRules(line, [], 0);
    assert.equal(result.rules.length, 0, line);
    assert.equal(result.errors.length, 1, line);
  }
});

test("duplicate explicit labels are reported, comments and blank lines are ignored", () => {
  const result = parseBatchRules("# note\n\nold:20001 host\nnew:20002 host\nnew:20003 host", ["old"], 0);
  assert.equal(result.rules.length, 1);
  assert.equal(result.errors.length, 2);
  assert.match(result.errors[0], /第 3 行/);
  assert.match(result.errors[1], /第 5 行/);
});

test("topology uses the UDP port for UDP-only rules", () => {
  const result = ruleDestinations({ remote_host: "dns.example", remote_tcp_port: 443, remote_udp_port: 53, enable_tcp: false, enable_udp: true });
  assert.deepEqual(result, [{ key: "dns.example:53", host: "dns.example", port: 53, protocols: ["UDP"] }]);
});

test("topology preserves different protocol destinations and merges shared ports", () => {
  const rule = { remote_host: "remote", remote_tcp_port: 443, remote_udp_port: 53, enable_tcp: true, enable_udp: true };
  assert.deepEqual(ruleDestinations(rule).map((d) => [d.port, d.protocols]), [[443, ["TCP"]], [53, ["UDP"]]]);
  assert.deepEqual(ruleDestinations({ ...rule, remote_udp_port: 443 }).map((d) => [d.port, d.protocols]), [[443, ["TCP", "UDP"]]]);
});
