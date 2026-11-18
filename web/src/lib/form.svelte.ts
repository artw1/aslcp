import { app } from './store.svelte';
/** State shared by the connect bar and the favorites table. */
export const form = $state({ target: '', permanent: false });
let focuser: (() => void) | null = null;
export function registerFocus(fn: (() => void) | null) { focuser = fn; }
/** Put a node number into the Node# input and focus it. */
export function pickNode(node: string) { form.target = node; focuser?.(); }

/** Node popup (connect/disconnect for any node number clicked on the dashboard). */
export const nodeDialog = $state({ open: false, node: '' });
/** Opens the connect popup for a node; `local` (the status table or network list it was clicked in) becomes the selected local node. */
export function openNode(node: string, local?: string) { if (local && app.nodes.some((n) => n.node === local)) app.selectedNode = local; form.target = node; nodeDialog.node = node; nodeDialog.open = true; }
