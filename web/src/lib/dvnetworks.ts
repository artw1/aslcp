// The DMR networks of the digital modes (docs/DV.md): each one is its own panel mode with its own remembered
// talkgroup and favourites. Availability and login state come from the engine (the snapshot says which are ready).
// Read-only: there is nothing to configure here any more.
import type { DVTuneMode } from './types';

export type DVNetworkID = 'BM' | 'TGIF' | 'DMR';

export interface DVNetworkDef {
  id: DVNetworkID;
  label: string;
}

export const DV_NETWORKS: readonly DVNetworkDef[] = [
  { id: 'BM', label: 'BrandMeister' },
  { id: 'TGIF', label: 'TGIF' },
  { id: 'DMR', label: 'DMR - Other' },
];

export const DV_NETWORK_IDS: readonly string[] = DV_NETWORKS.map((n) => n.id);

/** A panel mode that is a DMR network (talkgroups, private calls). */
export const isDVNetwork = (mode: string): mode is DVNetworkID => DV_NETWORK_IDS.includes(mode);

export const networkDef = (id: string): DVNetworkDef | undefined => DV_NETWORKS.find((n) => n.id === id);

/** The parrot network of the engine (mode id LOOP) is shown as "Parrot (test)". */
export const PARROT_LABEL = 'Parrot (test)';

/** The label of a panel mode: "Parrot (test)" for LOOP, the registry's for a DMR network, else the mode itself. */
export const networkLabel = (mode: string): string => (mode === 'LOOP' ? PARROT_LABEL : networkDef(mode)?.label ?? mode);

/** The modes of the panel: the parrot (test), the DMR networks in registry order, then YSF, P25 and NXDN. */
export const PANEL_MODES: DVTuneMode[] = ['LOOP', ...DV_NETWORKS.map((n) => n.id), 'YSF', 'P25', 'NXDN'];
