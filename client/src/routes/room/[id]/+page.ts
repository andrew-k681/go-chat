import { error } from '@sveltejs/kit';
import { isValidRoomId } from '#lib/api.ts';
import type { PageLoad } from './$types';

// WebRTC and media APIs are browser-only.
export const ssr = false;

export const load: PageLoad = ({ params }) => {
	if (!isValidRoomId(params.id)) error(404, 'Meeting not found');
	return { roomId: params.id };
};
