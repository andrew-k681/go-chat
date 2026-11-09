<script lang="ts">
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { createRoom, parseRoomInput } from '#lib/api.ts';

	let link = $state('');
	let busy = $state(false);
	let error = $state<string | null>(null);

	async function start() {
		busy = true;
		error = null;
		try {
			const id = await createRoom();
			await goto(resolve('/room/[id]', { id }));
		} catch (e) {
			error = (e as Error).message;
		} finally {
			busy = false;
		}
	}

	function join(e: SubmitEvent) {
		e.preventDefault();
		const id = parseRoomInput(link);
		if (!id) {
			error = 'That doesn’t look like a meeting link or code.';
			return;
		}
		goto(resolve('/room/[id]', { id }));
	}
</script>

<main>
	<h1>Video meetings, peer to peer</h1>
	<p class="sub">
		Start a meeting and share the link. Audio, video and chat go directly between participants.
	</p>

	<div class="actions">
		<button type="button" class="primary" onclick={start} disabled={busy}>
			{busy ? 'Creating…' : 'Start a meeting'}
		</button>
		<span class="or">or</span>
		<form onsubmit={join}>
			<input
				bind:value={link}
				placeholder="Paste a link or code"
				aria-label="Meeting link or code"
			/>
			<button type="submit" disabled={!link.trim()}>Join</button>
		</form>
	</div>

	{#if error}<p class="error" role="alert">{error}</p>{/if}
</main>

<style>
	main {
		max-width: 720px;
		margin: 0 auto;
		padding: 20vh 16px 0;
	}
	h1 {
		font-size: clamp(1.8rem, 5vw, 2.6rem);
		margin: 0 0 12px;
	}
	.sub {
		color: var(--muted);
		margin: 0 0 32px;
	}
	.actions {
		display: flex;
		flex-wrap: wrap;
		gap: 12px;
		align-items: center;
	}
	.or {
		color: var(--muted);
	}
	form {
		display: flex;
		gap: 8px;
		flex: 1;
		min-width: 240px;
	}
	form input {
		flex: 1;
		min-width: 0;
	}
</style>
