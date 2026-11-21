// The DMR networks of DVSwitch (docs/DVSWITCH.md "DMR networks"): a mirror of the helper's registry
// (internal/helper/dvswitch_networks.go, DVNetworks). Each one is its own panel mode with its own card,
// settings, Enable, remembered talkgroup and favourites. A new network is one entry here and one there.
import { isBMItem } from './dvbm.ts';
import type { DVListItem, DVNetworkView, DVSwitchSetup, DVTuneMode } from './types';

export type DVNetworkID = 'BM' | 'TGIF' | 'DMR';

export interface DVNetworkDef {
  id: DVNetworkID;
  label: string;
  /** 'stfu': BrandMeister's own card; 'mmdvm': MMDVM_Bridge's [DMR Network], switched on entering the mode. */
  via: 'stfu' | 'mmdvm';
  /** 'fixed': the master comes from DMR_Hosts.txt (read-only); 'user': typed in, with a picker; '' for BrandMeister. */
  master: 'fixed' | 'user' | '';
  /** The DMR_Hosts.txt rows of this network: the row named hostName, or names starting with hostPrefix (any case). */
  hostName?: string;
  hostPrefix?: string;
  /** The network of any master no other entry claims. */
  catchAll?: boolean;
}

export const DV_NETWORKS: readonly DVNetworkDef[] = [
  { id: 'BM', label: 'BrandMeister', via: 'stfu', master: '', hostPrefix: 'BM_' },
  { id: 'TGIF', label: 'TGIF', via: 'mmdvm', master: 'fixed', hostName: 'TGIF_Network', hostPrefix: 'TGIF' },
  { id: 'DMR', label: 'DMR - Other', via: 'mmdvm', master: 'user', catchAll: true },
];

export const DV_NETWORK_IDS: readonly string[] = DV_NETWORKS.map((n) => n.id);

/** A panel mode that is a DMR network (talkgroups, private calls). */
export const isDVNetwork = (mode: string): mode is DVNetworkID => DV_NETWORK_IDS.includes(mode);

export const networkDef = (id: string): DVNetworkDef | undefined => DV_NETWORKS.find((n) => n.id === id);

/** The label of a panel mode: the registry's for a DMR network, else the mode itself. */
export const networkLabel = (mode: string): string => networkDef(mode)?.label ?? mode;

/** Whether a DMR_Hosts.txt row belongs to network n (BrandMeister also by its domains). Comment rows never do. */
export function claimsRow(n: DVNetworkDef, it: DVListItem): boolean {
  const name = it.name.trim();
  if (!name || name.startsWith('#')) return false;
  if (n.id === 'BM') return isBMItem(it);
  const up = name.toUpperCase();
  if (n.hostName && up === n.hostName.toUpperCase()) return true;
  return !!n.hostPrefix && up.startsWith(n.hostPrefix.toUpperCase());
}

/** The network that claims a row (never the catch-all one), or undefined. */
export const claimedBy = (it: DVListItem): DVNetworkDef | undefined => DV_NETWORKS.find((n) => !n.catchAll && claimsRow(n, it));

/** The DMR - Other master picker: every row no other network claims. */
export const otherOptions = (items: DVListItem[]): DVListItem[] => items.filter((it) => !claimedBy(it));

/** The card of a network in the setup view. */
export const netView = (v: Pick<DVSwitchSetup, 'networks'> | null | undefined, id: string): DVNetworkView | undefined => v?.networks?.find((n) => n.id === id);

/** Where a fixed master comes from. */
export function masterSource(n: Pick<DVNetworkView, 'source' | 'name' | 'label'>): string {
  return n.source === 'hosts' ? `From DMR_Hosts.txt (${n.name || n.label}).` : `${n.label} is not in the host list, using the default.`;
}

/** The modes of the panel: the DMR networks in registry order, then YSF, P25 and NXDN. */
export const PANEL_MODES: DVTuneMode[] = [...DV_NETWORKS.map((n) => n.id), 'YSF', 'P25', 'NXDN'];
