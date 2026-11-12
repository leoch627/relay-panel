export interface BatchRule {
  tag: string;
  listen_ip: string;
  listen_port: number;
  remote_host: string;
  remote_tcp_port: number;
  remote_udp_port: number;
  enable_tcp: boolean;
  enable_udp: boolean;
  node_id: number;
  advanced: Record<string, number>;
}

const validTag = /^[A-Za-z0-9_-]{1,32}$/;
const protocol = /^(tcp|udp|both)$/i;
const port = (s: string) => /^\d+$/.test(s) && Number(s) >= 1 && Number(s) <= 65535 ? Number(s) : null;

export function parseBatchRules(text: string, existingTags: Iterable<string>, nodeID: number) {
  const rules: BatchRule[] = [];
  const errors: string[] = [];
  const used = new Set(existingTags);
  // Reserve explicit tags before assigning automatic ones, including tags on
  // later lines. This makes the result independent of explicit-tag ordering.
  const reserved = new Set(used);
  const lines = text.split("\n").map((line) => line.trim());
  for (const line of lines) {
    if (!line || line.startsWith("#")) continue;
    const first = line.split(/\s+/, 1)[0];
    if (first.includes(":")) reserved.add(first.split(":", 1)[0]);
  }
  let auto = 1;
  lines.forEach((line, index) => {
    if (!line || line.startsWith("#")) return;
    const fail = (message: string) => errors.push(`第 ${index + 1} 行${message}`);
    const parts = line.split(/\s+/);
    if (parts.length < 2 || parts.length > 4) { fail("格式应为 [标签:]监听端口 远端地址 [远端端口] [tcp|udp|both]"); return; }
    const first = parts[0].split(":");
    let tag = first.length === 2 ? first[0] : "";
    const listenPort = port(first.length === 2 ? first[1] : parts[0]);
    if (first.length > 2 || (first.length === 2 && !validTag.test(tag))) { fail("标签只能包含字母、数字、下划线或横杠，长度 1-32"); return; }
    if (listenPort === null) { fail("监听端口需在 1-65535"); return; }
    if (tag && used.has(tag)) { fail(`标签 ${tag} 已存在或重复`); return; }
    let remotePort = listenPort;
    let proto = "both";
    if (parts[2]) {
      if (protocol.test(parts[2])) {
        if (parts[3]) { fail("协议后不应再有参数"); return; }
        proto = parts[2].toLowerCase();
      } else {
        const parsed = port(parts[2]);
        if (parsed === null) { fail("远端端口需在 1-65535，或填写 tcp/udp/both"); return; }
        remotePort = parsed;
        if (parts[3]) proto = parts[3].toLowerCase();
      }
    }
    if (!protocol.test(proto)) { fail("协议仅支持 tcp、udp 或 both"); return; }
    if (!tag) {
      while (reserved.has(`B${auto}`)) auto++;
      tag = `B${auto++}`;
      reserved.add(tag);
    }
    used.add(tag);
    rules.push({
      tag, listen_ip: "0.0.0.0", listen_port: listenPort, remote_host: parts[1],
      remote_tcp_port: proto === "udp" ? 0 : remotePort,
      remote_udp_port: proto === "tcp" ? 0 : remotePort,
      enable_tcp: proto !== "udp", enable_udp: proto !== "tcp", node_id: nodeID, advanced: {},
    });
  });
  return { rules, errors };
}
