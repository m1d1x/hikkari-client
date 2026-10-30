import { groups, presets, type Group, type Preset } from './mock';

/** Groups and presets, shared by both pages: a group edited on one shows up in the other. */
export const db = $state<{ groups: Group[]; presets: Preset[] }>({
	groups: structuredClone(groups),
	presets: structuredClone(presets),
});
