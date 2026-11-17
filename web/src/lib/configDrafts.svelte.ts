// Unsaved Config-page edits live at module level so they survive navigating to another page and back.
export const drafts = $state<{ files: Record<string, { content: string; base: string }>; side: Record<string, string> }>({ files: {}, side: {} });
