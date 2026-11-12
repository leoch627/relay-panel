import { cn } from "@/lib/utils";

export function BrandMark({ className }: { className?: string }) {
  return (
    <div className={cn("grid h-8 w-8 shrink-0 place-items-center rounded-lg bg-gradient-to-br from-indigo-500 to-violet-600 text-white shadow-md shadow-indigo-500/30", className)}>
      <svg viewBox="0 0 32 32" className="h-[70%] w-[70%]" fill="none">
        <path d="M6 21h7l6-10h7" stroke="currentColor" strokeWidth="2.8" strokeLinecap="round" strokeLinejoin="round" />
        <circle cx="6" cy="21" r="2.6" fill="currentColor" />
        <circle cx="26" cy="11" r="2.6" fill="currentColor" />
      </svg>
    </div>
  );
}
