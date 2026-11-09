<script lang="ts">
	import type { ChatMessage } from '#lib/meeting.svelte.ts';
	import { MAX_CHAT } from '#lib/types.ts';

	type Props = { messages: ChatMessage[]; onSend: (text: string) => void; onClose: () => void };
	let { messages, onSend, onClose }: Props = $props();

	let draft = $state('');
	let list: HTMLOListElement | undefined = $state();

	$effect(() => {
		void messages.length;
		list?.scrollTo({ top: list.scrollHeight });
	});

	function submit(e: SubmitEvent) {
		e.preventDefault();
		onSend(draft);
		draft = '';
	}

	const time = (ts: number) =>
		new Date(ts).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
</script>

<aside class="chat">
	<header>
		<h2>Chat</h2>
		<button type="button" class="icon" onclick={onClose} aria-label="Close chat">✕</button>
	</header>
	<ol bind:this={list}>
		{#each messages as m (m.key)}
			<li class:self={m.self}>
				<div class="meta">{m.self ? 'You' : m.name} · {time(m.ts)}</div>
				<div class="text">{m.text}</div>
			</li>
		{:else}
			<li class="empty">Messages go directly to everyone in the call and aren't stored.</li>
		{/each}
	</ol>
	<form onsubmit={submit}>
		<input
			bind:value={draft}
			maxlength={MAX_CHAT}
			placeholder="Send a message"
			aria-label="Message"
		/>
		<button type="submit" disabled={!draft.trim()}>Send</button>
	</form>
</aside>

<style>
	.chat {
		display: flex;
		flex-direction: column;
		background: var(--panel);
		border-left: 1px solid var(--border);
		min-height: 0;
		width: 320px;
	}
	header {
		display: flex;
		align-items: center;
		justify-content: space-between;
		padding: 12px 16px;
		border-bottom: 1px solid var(--border);
	}
	h2 {
		margin: 0;
		font-size: 1rem;
	}
	ol {
		flex: 1;
		overflow-y: auto;
		list-style: none;
		margin: 0;
		padding: 12px 16px;
		display: flex;
		flex-direction: column;
		gap: 10px;
	}
	.meta {
		font-size: 0.75rem;
		color: var(--muted);
	}
	.text {
		white-space: pre-wrap;
		overflow-wrap: anywhere;
	}
	li.self .meta {
		color: var(--accent);
	}
	.empty {
		color: var(--muted);
		font-size: 0.85rem;
	}
	form {
		display: flex;
		gap: 8px;
		padding: 12px;
		border-top: 1px solid var(--border);
	}
	input {
		flex: 1;
		min-width: 0;
	}
	@media (max-width: 720px) {
		.chat {
			position: fixed;
			inset: 0 0 72px 0;
			width: auto;
			z-index: 10;
			border-left: none;
		}
	}
</style>
