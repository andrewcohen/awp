import { useEffect, useState } from "react";
import { Label } from "@/components/ui/label";
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectLabel,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import { api, type ConfigOption, type SessionSummary } from "./api";

// Every setting the agent exposes, rendered from what the agent says it has.
//
// This is one component rather than a model picker plus an effort picker plus a
// mode picker, because ACP made them one thing: `configOptions` on the session,
// each with a type, a current value and its own list of values, changed through
// one `session/set_config_option`. A client that hand-builds a control per
// setting has to be taught each new one — and worse, has to guess. A first pass
// here shipped an effort picker that sent a `/effort` slash command, on the
// grounds that ACP had no notion of reasoning effort. It has one; the client
// was not asking for it.
//
// The interesting consequence of rendering the agent's list is that the list is
// not fixed, and it is more dramatic than "the values change". Measured against
// the real agent: switching the model from Opus to Haiku removed the effort and
// fast-mode options *entirely* and clamped the permission mode from "auto" to
// "default", because that model supports none of them. A client patching one
// entry locally would go on offering an effort picker the agent had withdrawn.
//
// So every change replaces the whole set with whatever comes back.

function groupsOf(
  option: ConfigOption,
): { name: string; options: ConfigOption["options"] }[] {
  const groups: { name: string; options: ConfigOption["options"] }[] = [];
  for (const value of option.options) {
    const name = value.group ?? "";
    const last = groups[groups.length - 1];
    if (last && last.name === name) last.options.push(value);
    else groups.push({ name, options: [value] });
  }
  return groups;
}

export function ConfigBar({
  session,
  onChanged,
}: {
  session: SessionSummary;
  onChanged: (session: SessionSummary) => void;
}) {
  // Mirrored locally so a picker responds to the click rather than to the round
  // trip, then replaced wholesale by whatever the agent says afterwards.
  const [config, setConfig] = useState(session.config);
  useEffect(() => setConfig(session.config), [session.config]);

  const change = async (id: string, value: string | boolean) => {
    setConfig((have) => have.map((o) => (o.id === id ? { ...o, value } : o)));
    try {
      const updated = await api.setConfig(session.sessionId, id, value);
      setConfig(updated.config);
      onChanged(updated);
    } catch (err) {
      console.error("setting failed", err);
    }
  };

  if (config.length === 0) return null;

  return (
    <>
      {config.map((option) =>
        option.type === "boolean" ? (
          <Label
            key={option.id}
            className="text-muted-foreground flex items-center gap-2 px-2 text-sm font-normal"
            title={option.description}
          >
            <Switch
              checked={option.value === true}
              onCheckedChange={(next) => void change(option.id, next)}
            />
            {option.name}
          </Label>
        ) : (
          <Select
            key={option.id}
            value={typeof option.value === "string" ? option.value : ""}
            onValueChange={(next) => {
              if (typeof next === "string") void change(option.id, next);
            }}
          >
            <SelectTrigger
              size="sm"
              className="h-8 w-auto gap-1 border-0 shadow-none"
              aria-label={option.name}
              title={option.description ?? option.name}
            >
              <SelectValue placeholder={option.name} />
            </SelectTrigger>
            <SelectContent>
              {groupsOf(option).map((group, i) => (
                <SelectGroup key={group.name || i}>
                  {group.name !== "" && <SelectLabel>{group.name}</SelectLabel>}
                  {group.options.map((value) => (
                    <SelectItem key={value.value} value={value.value}>
                      {value.name}
                    </SelectItem>
                  ))}
                </SelectGroup>
              ))}
            </SelectContent>
          </Select>
        ),
      )}
    </>
  );
}
