export const SIDEBAR_WIDTH_MOBILE = "18rem";
export const SIDEBAR_WIDTH_ICON = "3rem";
export const SIDEBAR_KEYBOARD_SHORTCUT = "b";

// Bounds (in px) for the user-resizable desktop sidebar width.
export const SIDEBAR_WIDTH_DEFAULT_PX = 256;
export const SIDEBAR_WIDTH_MIN_PX = 192;
export const SIDEBAR_WIDTH_MAX_PX = 480;

export function clampSidebarWidth(width: number): number {
	if (!Number.isFinite(width)) return SIDEBAR_WIDTH_DEFAULT_PX;
	return Math.round(Math.min(SIDEBAR_WIDTH_MAX_PX, Math.max(SIDEBAR_WIDTH_MIN_PX, width)));
}
