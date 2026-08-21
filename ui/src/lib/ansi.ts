import { escapeHtml } from "./markdown";

interface AnsiStyle {
  bold: boolean;
  italic: boolean;
  underline: boolean;
  strikethrough: boolean;
  fg: string | null;
  bg: string | null;
}

const DEFAULT_STYLE: AnsiStyle = {
  bold: false,
  italic: false,
  underline: false,
  strikethrough: false,
  fg: null,
  bg: null,
};

interface AnsiPalette {
  colors: string[];
  brightColors: string[];
}

// Classic terminal colors, darkened where needed for light backgrounds
const LIGHT_PALETTE: AnsiPalette = {
  colors: ["#000000", "#cd0000", "#008700", "#8a6d00", "#0000ee", "#cd00cd", "#008b8b", "#e5e5e5"],
  brightColors: ["#7f7f7f", "#ff0000", "#00cd00", "#cdcd00", "#5c5cff", "#ff00ff", "#00cdcd", "#ffffff"],
};

// Brighter variants for dark backgrounds
const DARK_PALETTE: AnsiPalette = {
  colors: ["#767676", "#ff5555", "#50fa7b", "#f1fa8c", "#61afef", "#ff79c1", "#8be9fd", "#e5e5e5"],
  brightColors: ["#969696", "#ff6e6e", "#69ff94", "#f5fc9e", "#7cc0ff", "#ff92d0", "#a4f5ff", "#ffffff"],
};

function color256(n: number, palette: AnsiPalette): string {
  if (n < 16) {
    return n < 8 ? palette.colors[n] : palette.brightColors[n - 8];
  }
  if (n < 232) {
    const i = n - 16;
    const to255 = (v: number) => (v === 0 ? 0 : v * 40 + 55);
    const r = to255(Math.floor(i / 36));
    const g = to255(Math.floor((i % 36) / 6));
    const b = to255(i % 6);
    return `rgb(${r}, ${g}, ${b})`;
  }
  const v = (n - 232) * 10 + 8;
  return `rgb(${v}, ${v}, ${v})`;
}

function applySgr(prev: AnsiStyle, params: number[], palette: AnsiPalette): AnsiStyle {
  let style = prev;
  const update = (patch: Partial<AnsiStyle>) => {
    if (style === prev) {
      style = { ...prev };
    }
    Object.assign(style, patch);
  };

  let i = 0;
  while (i < params.length) {
    const p = params[i];
    if (p === 0) {
      update({ ...DEFAULT_STYLE });
    } else if (p === 1) {
      update({ bold: true });
    } else if (p === 3) {
      update({ italic: true });
    } else if (p === 4) {
      update({ underline: true });
    } else if (p === 9) {
      update({ strikethrough: true });
    } else if (p === 22) {
      update({ bold: false });
    } else if (p === 23) {
      update({ italic: false });
    } else if (p === 24) {
      update({ underline: false });
    } else if (p === 29) {
      update({ strikethrough: false });
    } else if (p >= 30 && p <= 37) {
      update({ fg: palette.colors[p - 30] });
    } else if (p === 39) {
      update({ fg: null });
    } else if (p >= 40 && p <= 47) {
      update({ bg: palette.colors[p - 40] });
    } else if (p === 49) {
      update({ bg: null });
    } else if (p >= 90 && p <= 97) {
      update({ fg: palette.brightColors[p - 90] });
    } else if (p >= 100 && p <= 107) {
      update({ bg: palette.brightColors[p - 100] });
    } else if (p === 38 || p === 48) {
      const isFg = p === 38;
      if (params[i + 1] === 5) {
        const color = color256(params[i + 2] ?? 0, palette);
        update(isFg ? { fg: color } : { bg: color });
        i += 2;
      } else if (params[i + 1] === 2) {
        const r = params[i + 2] ?? 0;
        const g = params[i + 3] ?? 0;
        const b = params[i + 4] ?? 0;
        const color = `rgb(${r}, ${g}, ${b})`;
        update(isFg ? { fg: color } : { bg: color });
        i += 4;
      }
    }
    i++;
  }
  if (
    style.bold === prev.bold &&
    style.italic === prev.italic &&
    style.underline === prev.underline &&
    style.strikethrough === prev.strikethrough &&
    style.fg === prev.fg &&
    style.bg === prev.bg
  ) {
    return prev;
  }
  return style;
}

function styleIsDefault(style: AnsiStyle): boolean {
  return (
    !style.bold &&
    !style.italic &&
    !style.underline &&
    !style.strikethrough &&
    style.fg === null &&
    style.bg === null
  );
}

function styleToCss(style: AnsiStyle): string {
  const parts: string[] = [];
  if (style.bold) parts.push("font-weight:600");
  if (style.italic) parts.push("font-style:italic");
  const decoration: string[] = [];
  if (style.underline) decoration.push("underline");
  if (style.strikethrough) decoration.push("line-through");
  if (decoration.length > 0) parts.push(`text-decoration:${decoration.join(" ")}`);
  if (style.fg) parts.push(`color:${style.fg}`);
  if (style.bg) parts.push(`background-color:${style.bg}`);
  return parts.join(";");
}

const CSI_REGEX = /\x1b\[([0-9;?]*)([a-zA-Z])/g;

/**
 * Convert a string containing ANSI escape sequences into HTML. SGR
 * (select graphic rendition) sequences become inline-styled spans;
 * other CSI sequences are dropped. Text content is HTML-escaped.
 * Strings without escape codes are returned escaped as-is.
 * The `dark` flag selects a palette readable on the given background.
 */
export function ansiToHtml(text: string, dark: boolean = false): string {
  if (!text.includes("\x1b[")) {
    return escapeHtml(text);
  }

  const palette = dark ? DARK_PALETTE : LIGHT_PALETTE;
  let html = "";
  let lastIndex = 0;
  let style: AnsiStyle = DEFAULT_STYLE;
  let openStyle: AnsiStyle | null = null;

  const closeSpan = () => {
    if (openStyle !== null) {
      html += "</span>";
      openStyle = null;
    }
  };

  const flush = (end: number) => {
    if (end <= lastIndex) return;
    const chunk = escapeHtml(text.slice(lastIndex, end));
    lastIndex = end;
    if (styleIsDefault(style)) {
      closeSpan();
      html += chunk;
    } else if (openStyle === style) {
      html += chunk;
    } else {
      closeSpan();
      html += `<span style="${styleToCss(style)}">${chunk}`;
      openStyle = style;
    }
  };

  const regex = new RegExp(CSI_REGEX.source, "g");
  let match: RegExpExecArray | null;
  while ((match = regex.exec(text)) !== null) {
    flush(match.index);
    lastIndex = match.index + match[0].length;
    if (match[2] === "m") {
      const raw = match[1];
      const params =
        raw === "" ? [0] : raw.split(";").map((part) => (part === "" ? 0 : Number.parseInt(part, 10)));
      style = applySgr(style, params, palette);
    }
  }
  flush(text.length);
  closeSpan();
  return html;
}
