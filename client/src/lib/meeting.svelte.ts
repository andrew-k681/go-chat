import { getIceServers, wsUrl } from './api';
import { Signaling } from './signaling';
import {
	MAX_CHAT,
	MAX_NAME,
	type DataMessage,
	type PeerInfo,
	type ServerMessage,
	type SignalData
} from './types';

export type RemotePeer = {
	id: string;
	name: string;
	stream: MediaStream | null;
	audio: boolean;
	video: boolean;
	screen: boolean;
	connection: RTCPeerConnectionState;
};

export type ChatMessage = {
	key: number;
	name: string;
	text: string;
	ts: number;
	self: boolean;
};

type Status = 'lobby' | 'joining' | 'joined' | 'left' | 'error';

/** Non-reactive per-peer WebRTC state. */
type Conn = {
	pc: RTCPeerConnection;
	dc: RTCDataChannel;
	remote: MediaStream;
	polite: boolean;
	makingOffer: boolean;
	ignoreOffer: boolean;
	audio?: RTCRtpTransceiver;
	video?: RTCRtpTransceiver;
	queue: Promise<void>;
};

const VIDEO: MediaTrackConstraints = { width: { ideal: 1280 }, height: { ideal: 720 } };

/**
 * One meeting: local media, signaling, and a full-mesh of RTCPeerConnections.
 * The newcomer offers to every existing peer; existing peers only answer.
 * Perfect negotiation (newcomer impolite) covers renegotiation and ICE restarts.
 */
export class Meeting {
	status = $state<Status>('lobby');
	error = $state<string | null>(null);
	mediaError = $state<string | null>(null);
	signalingLost = $state(false);
	name = $state('');
	peers = $state<RemotePeer[]>([]);
	messages = $state<ChatMessage[]>([]);
	localStream = $state.raw<MediaStream>(new MediaStream());
	audioOn = $state(true);
	videoOn = $state(true);
	sharing = $state(false);

	readonly roomId: string;
	// Deliberately non-reactive: RTCPeerConnections must not be proxied.
	// eslint-disable-next-line svelte/prefer-svelte-reactivity
	#conns = new Map<string, Conn>();
	#signaling: Signaling | null = null;
	#iceServers: RTCIceServer[] = [];
	#audioTrack: MediaStreamTrack | null = null;
	#cameraTrack: MediaStreamTrack | null = null;
	#screenTrack: MediaStreamTrack | null = null;
	#msgKey = 0;

	constructor(roomId: string) {
		this.roomId = roomId;
	}

	// ---------- local media ----------

	/** Ask for camera + mic, falling back to whatever is available. */
	async startPreview() {
		const attempts: MediaStreamConstraints[] = [
			{ audio: true, video: VIDEO },
			{ audio: true },
			{ video: VIDEO }
		];
		let lastError: unknown;
		for (const constraints of attempts) {
			try {
				const s = await navigator.mediaDevices.getUserMedia(constraints);
				this.#audioTrack = s.getAudioTracks()[0] ?? null;
				this.#cameraTrack = s.getVideoTracks()[0] ?? null;
				this.audioOn = !!this.#audioTrack;
				this.videoOn = !!this.#cameraTrack;
				if (!this.#cameraTrack || !this.#audioTrack) {
					this.mediaError = !this.#cameraTrack
						? 'No camera available.'
						: 'No microphone available.';
				}
				this.#refreshLocalStream();
				return;
			} catch (e) {
				lastError = e;
			}
		}
		this.audioOn = false;
		this.videoOn = false;
		this.mediaError = describeMediaError(lastError);
	}

	toggleAudio() {
		if (!this.#audioTrack) return;
		this.audioOn = !this.audioOn;
		this.#audioTrack.enabled = this.audioOn;
		this.#broadcastState();
	}

	/** Turning the camera off stops the track, so the camera light goes off too. */
	async toggleVideo() {
		if (this.videoOn) {
			this.#cameraTrack?.stop();
			this.#cameraTrack = null;
			this.videoOn = false;
		} else {
			try {
				const s = await navigator.mediaDevices.getUserMedia({ video: VIDEO });
				this.#cameraTrack = s.getVideoTracks()[0];
				this.videoOn = true;
				this.mediaError = null;
			} catch (e) {
				this.mediaError = describeMediaError(e);
				return;
			}
		}
		this.#refreshLocalStream();
		await this.#replaceOutgoingVideo();
		this.#broadcastState();
	}

	async toggleScreen() {
		if (this.sharing) return this.#stopScreen();
		try {
			const s = await navigator.mediaDevices.getDisplayMedia({ video: true });
			const track = s.getVideoTracks()[0];
			track.onended = () => this.#stopScreen();
			this.#screenTrack = track;
			this.sharing = true;
		} catch {
			return; // user cancelled the picker
		}
		await this.#replaceOutgoingVideo();
		this.#broadcastState();
	}

	async #stopScreen() {
		if (!this.#screenTrack) return;
		this.#screenTrack.onended = null;
		this.#screenTrack.stop();
		this.#screenTrack = null;
		this.sharing = false;
		await this.#replaceOutgoingVideo();
		this.#broadcastState();
	}

	/** What we show on our own tile: the screen while sharing, otherwise the camera. */
	#refreshLocalStream() {
		const tracks = [this.#screenTrack ?? this.#cameraTrack].filter((t) => t !== null);
		this.localStream = new MediaStream(tracks);
	}

	#outgoingVideo() {
		return this.#screenTrack ?? this.#cameraTrack;
	}

	async #replaceOutgoingVideo() {
		this.#refreshLocalStream();
		const track = this.#outgoingVideo();
		await Promise.all(
			[...this.#conns.values()].map((c) => c.video?.sender.replaceTrack(track).catch(() => {}))
		);
	}

	// ---------- signaling ----------

	async join(name: string) {
		if (this.status === 'joining' || this.status === 'joined') return;
		this.name = name.trim().slice(0, MAX_NAME) || 'Guest';
		this.status = 'joining';
		this.error = null;
		this.#iceServers = await getIceServers();

		const signaling = new Signaling(wsUrl(this.roomId), {
			onMessage: (msg) => this.#onServerMessage(msg),
			onClose: () => {
				// Existing peer connections survive; we just can't reach new peers.
				this.signalingLost = true;
				if (this.status === 'joining') this.#fail('Lost connection to the server.');
			}
		});
		this.#signaling = signaling;
		try {
			await signaling.open();
		} catch (e) {
			this.#fail((e as Error).message);
			return;
		}
		signaling.send({ type: 'join', name: this.name });
	}

	#onServerMessage(msg: ServerMessage) {
		switch (msg.type) {
			case 'welcome':
				if (typeof msg.id !== 'string' || !Array.isArray(msg.peers)) return;
				this.status = 'joined';
				for (const p of msg.peers) {
					if (!isPeerInfo(p)) continue;
					this.#addPeerView(p);
					this.#createConn(p.id, true);
				}
				break;
			case 'peer-joined':
				if (isPeerInfo(msg.peer)) this.#addPeerView(msg.peer);
				break;
			case 'peer-left':
				if (typeof msg.id === 'string') this.#removePeer(msg.id);
				break;
			case 'signal': {
				if (typeof msg.from !== 'string' || !msg.data || typeof msg.data !== 'object') return;
				let conn = this.#conns.get(msg.from);
				if (!conn) {
					// Only peers that join after us offer to us.
					if (!msg.data.description || msg.data.description.type !== 'offer') return;
					this.#addPeerView({ id: msg.from, name: 'Guest' });
					conn = this.#createConn(msg.from, false);
				}
				const c = conn;
				const data = msg.data;
				// Serialize per peer so candidates never race ahead of their description.
				c.queue = c.queue
					.then(() => this.#handleSignal(msg.from, c, data))
					.catch((e) => console.warn('signal error', msg.from, e));
				break;
			}
			case 'error':
				this.#fail(typeof msg.message === 'string' ? msg.message : 'Server error');
				break;
		}
	}

	#send(to: string, data: SignalData) {
		this.#signaling?.send({ type: 'signal', to, data });
	}

	// ---------- peer connections ----------

	#createConn(id: string, initiator: boolean): Conn {
		const pc = new RTCPeerConnection({ iceServers: this.#iceServers });
		const dc = pc.createDataChannel('chat', { negotiated: true, id: 0 });
		const conn: Conn = {
			pc,
			dc,
			remote: new MediaStream(),
			polite: !initiator,
			makingOffer: false,
			ignoreOffer: false,
			queue: Promise.resolve()
		};
		this.#conns.set(id, conn);

		if (initiator) {
			// Fixed audio + video slots; mute/camera/screen just swap tracks, no renegotiation.
			conn.audio = pc.addTransceiver('audio', { direction: 'sendrecv' });
			conn.video = pc.addTransceiver('video', { direction: 'sendrecv' });
			this.#attachLocalTracks(conn);
		}

		pc.ontrack = ({ track }) => {
			conn.remote.addTrack(track);
			track.onunmute = () =>
				this.#updatePeer(id, { stream: new MediaStream(conn.remote.getTracks()) });
			this.#updatePeer(id, { stream: new MediaStream(conn.remote.getTracks()) });
		};

		pc.onicecandidate = ({ candidate }) => {
			if (candidate) this.#send(id, { candidate: candidate.toJSON() });
		};

		pc.onnegotiationneeded = async () => {
			try {
				conn.makingOffer = true;
				await pc.setLocalDescription();
				if (pc.localDescription) this.#send(id, { description: pc.localDescription.toJSON() });
			} catch (e) {
				console.warn('negotiation failed', id, e);
			} finally {
				conn.makingOffer = false;
			}
		};

		pc.onconnectionstatechange = () => {
			this.#updatePeer(id, { connection: pc.connectionState });
			if (pc.connectionState === 'failed') pc.restartIce();
		};

		dc.onopen = () => this.#sendData(dc, this.#stateMessage());
		dc.onmessage = (e) => this.#onData(id, e.data);

		return conn;
	}

	async #handleSignal(id: string, conn: Conn, data: SignalData) {
		const { pc } = conn;
		if (data.description) {
			const description = data.description;
			const offerCollision =
				description.type === 'offer' && (conn.makingOffer || pc.signalingState !== 'stable');
			conn.ignoreOffer = !conn.polite && offerCollision;
			if (conn.ignoreOffer) return;

			await pc.setRemoteDescription(description);
			if (description.type === 'offer') {
				if (!conn.audio || !conn.video) this.#adoptTransceivers(conn);
				await pc.setLocalDescription();
				if (pc.localDescription) this.#send(id, { description: pc.localDescription.toJSON() });
			}
		} else if (data.candidate) {
			try {
				await pc.addIceCandidate(data.candidate);
			} catch (e) {
				if (!conn.ignoreOffer) throw e;
			}
		}
	}

	/** Answerer: reuse the transceivers the offer created instead of adding new ones. */
	#adoptTransceivers(conn: Conn) {
		for (const t of conn.pc.getTransceivers()) {
			const kind = t.receiver.track.kind;
			if (kind === 'audio' && !conn.audio) conn.audio = t;
			else if (kind === 'video' && !conn.video) conn.video = t;
		}
		for (const t of [conn.audio, conn.video]) if (t) t.direction = 'sendrecv';
		this.#attachLocalTracks(conn);
	}

	#attachLocalTracks(conn: Conn) {
		conn.audio?.sender.replaceTrack(this.#audioTrack).catch(() => {});
		conn.video?.sender.replaceTrack(this.#outgoingVideo()).catch(() => {});
	}

	#addPeerView(p: PeerInfo) {
		const name = p.name.slice(0, MAX_NAME) || 'Guest';
		const existing = this.peers.find((v) => v.id === p.id);
		if (existing) {
			existing.name = name;
			return;
		}
		this.peers.push({
			id: p.id,
			name,
			stream: null,
			audio: true,
			video: true,
			screen: false,
			connection: 'new'
		});
	}

	#updatePeer(id: string, patch: Partial<RemotePeer>) {
		const view = this.peers.find((v) => v.id === id);
		if (view) Object.assign(view, patch);
	}

	#removePeer(id: string) {
		const conn = this.#conns.get(id);
		if (conn) {
			conn.dc.close();
			conn.pc.close();
			this.#conns.delete(id);
		}
		this.peers = this.peers.filter((v) => v.id !== id);
	}

	// ---------- data channel (chat + media state) ----------

	sendChat(text: string) {
		const clean = text.trim().slice(0, MAX_CHAT);
		if (!clean) return;
		const msg: DataMessage = { type: 'chat', text: clean, ts: Date.now() };
		for (const c of this.#conns.values()) this.#sendData(c.dc, msg);
		this.messages.push({
			key: this.#msgKey++,
			name: this.name,
			text: clean,
			ts: msg.ts,
			self: true
		});
	}

	#stateMessage(): DataMessage {
		return {
			type: 'state',
			audio: this.audioOn,
			video: this.videoOn || this.sharing,
			screen: this.sharing
		};
	}

	#broadcastState() {
		const msg = this.#stateMessage();
		for (const c of this.#conns.values()) this.#sendData(c.dc, msg);
	}

	#sendData(dc: RTCDataChannel, msg: DataMessage) {
		if (dc.readyState === 'open') dc.send(JSON.stringify(msg));
	}

	/** Data from peers is untrusted: parse defensively, never render as HTML. */
	#onData(id: string, raw: unknown) {
		if (typeof raw !== 'string' || raw.length > MAX_CHAT * 4) return;
		let msg: Partial<DataMessage>;
		try {
			msg = JSON.parse(raw);
		} catch {
			return;
		}
		if (msg.type === 'chat' && typeof msg.text === 'string') {
			const name = this.peers.find((v) => v.id === id)?.name ?? 'Guest';
			const text = msg.text.slice(0, MAX_CHAT);
			this.messages.push({ key: this.#msgKey++, name, text, ts: Date.now(), self: false });
		} else if (msg.type === 'state') {
			this.#updatePeer(id, {
				audio: msg.audio === true,
				video: msg.video === true,
				screen: msg.screen === true
			});
		}
	}

	// ---------- teardown ----------

	#fail(message: string) {
		this.error = message;
		this.status = 'error';
		this.#closeAll();
	}

	#closeAll() {
		this.#signaling?.close();
		this.#signaling = null;
		for (const id of [...this.#conns.keys()]) this.#removePeer(id);
	}

	leave() {
		this.#closeAll();
		for (const t of [this.#audioTrack, this.#cameraTrack, this.#screenTrack]) t?.stop();
		this.#audioTrack = this.#cameraTrack = this.#screenTrack = null;
		this.localStream = new MediaStream();
		this.status = 'left';
	}
}

function isPeerInfo(p: unknown): p is PeerInfo {
	return !!p && typeof (p as PeerInfo).id === 'string' && typeof (p as PeerInfo).name === 'string';
}

function describeMediaError(e: unknown): string {
	const name = (e as DOMException)?.name;
	if (name === 'NotAllowedError') return 'Camera/microphone permission was denied.';
	if (name === 'NotFoundError') return 'No camera or microphone found.';
	if (name === 'NotReadableError') return 'Camera or microphone is in use by another app.';
	if (!window.isSecureContext) return 'Camera access needs HTTPS (or localhost).';
	return 'Could not access camera or microphone.';
}
