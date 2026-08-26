import {
  Eye,
  EyeOff,
  Info,
  Mail,
  TriangleAlert,
  type LucideIcon,
} from "lucide-react";

import styles from "./Icon.module.css";

const iconComponents = {
  hidePassword: EyeOff,
  information: Info,
  mail: Mail,
  showPassword: Eye,
  warning: TriangleAlert,
} satisfies Record<string, LucideIcon>;

export type IconName = keyof typeof iconComponents;
export type IconSize = "small" | "default" | "large";

export interface IconProps {
  className?: string;
  name: IconName;
  size?: IconSize;
}

export function Icon({ className, name, size = "default" }: IconProps) {
  const IconComponent = iconComponents[name];
  const classes = [styles.icon, styles[size], className]
    .filter((value): value is string => value !== undefined)
    .join(" ");

  return (
    <IconComponent
      aria-hidden="true"
      className={classes}
      data-proctor-icon={name}
      focusable="false"
      strokeWidth={2}
    />
  );
}
