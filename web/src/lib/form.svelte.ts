/** State shared by the connect bar and the favorites table. */
export const form = $state({ target: '', permanent: false });
let focuser: (() => void) | null = null;
export function registerFocus(fn: (() => void) | null) { focuser = fn; }
/** Put a node number into the Node# input and focus it. */
export function pickNode(node: string) { form.target = node; focuser?.(); }
