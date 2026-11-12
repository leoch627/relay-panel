interface DestinationRule {
  remote_host: string;
  remote_tcp_port: number;
  remote_udp_port: number;
  enable_tcp: boolean;
  enable_udp: boolean;
}

export function ruleDestinations(rule: DestinationRule) {
  const destinations = new Map<number, { key: string; host: string; port: number; protocols: string[] }>();
  for (const [enabled, port, protocol] of [
    [rule.enable_tcp, rule.remote_tcp_port, "TCP"],
    [rule.enable_udp, rule.remote_udp_port, "UDP"],
  ] as const) {
    if (!enabled) continue;
    const destination = destinations.get(port) || { key: `${rule.remote_host}:${port}`, host: rule.remote_host, port, protocols: [] };
    destination.protocols.push(protocol);
    destinations.set(port, destination);
  }
  return [...destinations.values()];
}
