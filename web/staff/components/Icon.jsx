import { HugeiconsIcon } from '@hugeicons/react';

export const ICON_SIZE = 20;
export const ICON_STROKE = 1.5;

/**
 * The only way this app renders an icon. Icons are decorative (aria-hidden)
 * unless given a label. Icon-only buttons must still carry their own
 * aria-label, and gate results always pair an icon with text, color and sound.
 */
export function Icon({ icon, label, size = ICON_SIZE, className }) {
  const a11y = label ? { role: 'img', 'aria-label': label } : { 'aria-hidden': 'true' };
  return (
    <HugeiconsIcon
      icon={icon}
      size={size}
      strokeWidth={ICON_STROKE}
      focusable="false"
      className={className}
      {...a11y}
    />
  );
}
