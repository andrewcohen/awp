import { PanelRight } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Separator } from "@/components/ui/separator";

// The right panel: a column for things that are about the workspace rather than
// about the conversation.
//
// A placeholder for now, deliberately — the frame is the part worth settling
// first, because how wide this is and whether it can be dragged changes what is
// worth putting in it. The diff is the first widget, and a diff wants width, so
// the answer to "is a 480px column enough" decides its layout before it is
// written.
export function RightPanel({ onClose }: { onClose: () => void }) {
  return (
    <aside className="flex h-full min-w-0 flex-col">
      <div className="flex items-center gap-2 px-3 py-2">
        <PanelRight className="text-muted-foreground size-4" />
        <span className="text-sm font-medium">panel</span>
        <Button
          variant="ghost"
          size="sm"
          className="text-muted-foreground ml-auto"
          onClick={onClose}
        >
          hide
        </Button>
      </div>
      <Separator />
      <div className="text-muted-foreground flex flex-1 items-center justify-center p-6 text-center text-sm">
        the workspace diff goes here
      </div>
    </aside>
  );
}
