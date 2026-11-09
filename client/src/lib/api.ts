const ROOM_ID = /^[A-Za-z0-9_-]{8,64}$/;

export function isValidRoomId(id: string): boolean {
	return ROOM_ID.test(id);
}

/** Accepts a bare room id or a full invite link and returns the id, or null. */
export function parseRoomInput(input: string): string | null {
	const value = input.trim();
	if (isValidRoomId(value)) return value;
	try {
		const url = new URL(value);
		const match = url.pathname.match(/\/room\/([^/]+)\/?$/);
		if (match && isValidRoomId(match[1])) return match[1];
	} catch {
		// not a URL
	}
	return null;
}

export async function createRoom(): Promise<string> {
	const res = await fetch('/api/rooms', { method: 'POST' });
	if (!res.ok) throw new Error(`Could not create meeting (${res.status})`);
	const body: unknown = await res.json();
	const id = (body as { id?: unknown })?.id;
	if (typeof id !== 'string' || !isValidRoomId(id))
		throw new Error('Server returned an invalid room id');
	return id;
}

export async function roomExists(id: string): Promise<boolean> {
	const res = await fetch(`/api/rooms/${encodeURIComponent(id)}`);
	if (res.status === 404) return false;
	if (!res.ok) throw new Error(`Could not check meeting (${res.status})`);
	return true;
}

const FALLBACK_ICE: RTCIceServer[] = [{ urls: 'stun:stun.l.google.com:19302' }];

export async function getIceServers(): Promise<RTCIceServer[]> {
	try {
		const res = await fetch('/api/ice');
		if (!res.ok) return FALLBACK_ICE;
		const body = (await res.json()) as { iceServers?: unknown };
		return Array.isArray(body.iceServers) && body.iceServers.length > 0
			? (body.iceServers as RTCIceServer[])
			: FALLBACK_ICE;
	} catch {
		return FALLBACK_ICE;
	}
}

export function wsUrl(roomId: string): string {
	const proto = location.protocol === 'https:' ? 'wss:' : 'ws:';
	return `${proto}//${location.host}/ws/${encodeURIComponent(roomId)}`;
}
