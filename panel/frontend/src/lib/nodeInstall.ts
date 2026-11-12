export interface TLSInfo {
  enabled: boolean;
  fingerprint: string;
  generated: boolean;
  pin_recommended: boolean;
}
export type TLSMode = "auto" | "public" | "pin";

const quote = (value: string) => "'" + value.replace(/'/g, "'\\''") + "'";

export function nodeInstallCommand(origin: string, key: string, tls: TLSInfo | null, mode: TLSMode) {
  const https = new URL(origin).protocol === "https:";
  const pin = https && (mode === "pin" || (mode === "auto" && tls?.pin_recommended));
  if (pin && !tls?.fingerprint) throw new Error("无法获取主控证书指纹，请刷新后重试");
  const tlsEnv = pin ? ` PANEL_TLS_FP=${quote(tls!.fingerprint)}` : "";
  const note = !https ? "当前地址使用 HTTP，节点通信未加密。"
    : pin ? "直连主控，按所选主控证书指纹校验。"
    : "按系统 CA 校验 HTTPS 证书，适用于公共证书或由反向代理终止 TLS。";
  return `# 在被控机（root）执行：\n` +
    `curl -fsSL https://raw.githubusercontent.com/leoch627/relay-panel/main/panel_install.sh -o panel_install.sh\n` +
    `PANEL_SLAVE=1 PANEL_MASTER_URL=${quote(origin)} PANEL_NODE_KEY=${quote(key)}${tlsEnv} bash panel_install.sh\n\n` +
    `# ${note}\n# 安装完成后节点上线，在「新建转发」里选择该节点。`;
}
