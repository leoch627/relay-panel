import type { NodeInfo } from "@/api";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { StatusDot, nodeOptionLabel, nodeTone } from "@/components/common";
import { Server } from "lucide-react";

export function NodeSelect({ value, onChange, nodes, id }: { value: string; onChange: (v: string) => void; nodes: NodeInfo[]; id?: string }) {
  return (
    <Select value={value} onValueChange={onChange}>
      <SelectTrigger id={id}><SelectValue /></SelectTrigger>
      <SelectContent>
        <SelectItem value="0">
          <span className="flex items-center gap-2"><Server className="h-3.5 w-3.5 text-primary" />本机（主控服务器）</span>
        </SelectItem>
        {nodes.map((n) => (
          <SelectItem key={n.id} value={String(n.id)} disabled={n.deleting}>
            <span className="flex items-center gap-2">
              <StatusDot tone={nodeTone(n)} />
              {n.name}
              <span className="text-xs text-muted-foreground">{nodeOptionLabel(n)}</span>
            </span>
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  );
}
