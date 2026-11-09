// Wire types shared with the Go server. Keep in sync with ../../../PROTOCOL.md.

export type PeerInfo = { id: string; name: string };

export type SignalData = {
	description?: RTCSessionDescriptionInit;
	candidate?: RTCIceCandidateInit;
};

export type ClientMessage =
	{ type: 'join'; name: string } | { type: 'signal'; to: string; data: SignalData };

export type ServerMessage =
	| { type: 'welcome'; id: string; peers: PeerInfo[] }
	| { type: 'peer-joined'; peer: PeerInfo }
	| { type: 'peer-left'; id: string }
	| { type: 'signal'; from: string; data: SignalData }
	| { type: 'error'; message: string };

// Peer-to-peer messages sent over the RTCDataChannel.
export type DataMessage =
	| { type: 'chat'; text: string; ts: number }
	| { type: 'state'; audio: boolean; video: boolean; screen: boolean };

export const MAX_NAME = 40;
export const MAX_CHAT = 2000;
