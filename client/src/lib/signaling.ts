import type { ClientMessage, ServerMessage } from './types';

type Handlers = {
	onMessage: (msg: ServerMessage) => void;
	onClose: (event: CloseEvent) => void;
};

/** Thin JSON wrapper around the signaling WebSocket. */
export class Signaling {
	#ws: WebSocket | null = null;
	#closedByUs = false;
	private url: string;
	private handlers: Handlers;

	constructor(url: string, handlers: Handlers) {
		this.url = url;
		this.handlers = handlers;
	}

	open(): Promise<void> {
		return new Promise((resolve, reject) => {
			const ws = new WebSocket(this.url);
			this.#ws = ws;
			ws.onopen = () => resolve();
			ws.onerror = () => reject(new Error('Could not reach the signaling server'));
			ws.onmessage = (e) => {
				if (typeof e.data !== 'string') return;
				let msg: unknown;
				try {
					msg = JSON.parse(e.data);
				} catch {
					return;
				}
				if (msg && typeof (msg as { type?: unknown }).type === 'string') {
					this.handlers.onMessage(msg as ServerMessage);
				}
			};
			ws.onclose = (e) => {
				if (!this.#closedByUs) this.handlers.onClose(e);
			};
		});
	}

	send(msg: ClientMessage) {
		if (this.#ws?.readyState === WebSocket.OPEN) this.#ws.send(JSON.stringify(msg));
	}

	close() {
		this.#closedByUs = true;
		this.#ws?.close(1000);
		this.#ws = null;
	}
}
