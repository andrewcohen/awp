import * as Lucide from "lucide-react";

// shadcn's registry components are written against an icon library nobody has
// chosen yet, so they render `<IconPlaceholder lucide="CheckIcon" tabler=… />`
// and the site substitutes whichever the reader picked. That component lives in
// shadcn's own app and does not come down with the copied file, so every
// registry install lands broken on an import of `@/app/(create)/components/…`.
//
// This is the substitution, made once. Patching each copied file instead means
// doing it again for every component added later, and getting it subtly
// different each time.
//
// Only the lucide name is read — that is the library this project uses. The
// other props are accepted and ignored so the registry's markup can be pasted
// in unedited, which is the whole point.
export function IconPlaceholder({
  lucide,
  className,
  ...rest
}: {
  lucide?: string;
  tabler?: string;
  hugeicons?: string;
  phosphor?: string;
  remixicon?: string;
  className?: string;
}) {
  const icons = Lucide as unknown as Record<
    string,
    React.ComponentType<{ className?: string }>
  >;
  const Icon = lucide ? icons[lucide] : undefined;
  if (!Icon) return null;
  return <Icon className={className} {...rest} />;
}
