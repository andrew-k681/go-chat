<script lang="ts">
	import { onMount } from 'svelte';
	import { resolve } from '$app/paths';
	import { roomExists } from '#lib/api.ts';
	import { Meeting } from '#lib/meeting.svelte.ts';
	import { MAX_NAME } from '#lib/types.ts';
	import VideoTile from '#lib/components/VideoTile.svelte';
	import Chat from '#lib/components/Chat.svelte';
	import type { PageProps } from './$types';

	let { data }: PageProps = $props();

	const NAME_KEY = 'go_chat:name';

	// The room id never changes while this page is mounted (a new id remounts the route).
	// svelte-ignore state_referenced_locally
	const meeting = new Meeting(data.roomId);
	let check = $state<'checking' | 'ok' | 'missing' | 'failed'>('checking');
	let name = $state(loadName());
	let chatOpen = $state(false);
	let seen = $state(0);
	let copied = $state(false);

	const inviteLink = $derived(`${location.origin}${resolve('/room/[id]', { id: meeting.roomId })}`);
	const unread = $derived(chatOpen ? 0 : meeting.messages.filter((m) => !m.self).length - seen);
	const tileCount = $derived(meeting.peers.length + 1);

	$effect(() => {
		if (chatOpen) seen = meeting.messages.filter((m) => !m.self).length;
	});

	onMount(() => {
		roomExists(meeting.roomId)
			.then((ok) => {
				check = ok ? 'ok' : 'missing';
				if (ok) meeting.startPreview();
			})
			.catch(() => (check = 'failed'));

		const onUnload = () => meeting.leave();
		window.addEventListener('pagehide', onUnload);
		return () => {
			window.removeEventListener('pagehide', onUnload);
			meeting.leave();
		};
	});

	function loadName(): string {
		try {
			return localStorage.getItem(NAME_KEY) ?? '';
		} catch {
			return '';
		}
	}

	function join(e: SubmitEvent) {
		e.preventDefault();
		try {
			localStorage.setItem(NAME_KEY, name.trim());
		} catch {
			// storage unavailable; fine
		}
		meeting.join(name);
	}

	async function copyLink() {
		try {
			await navigator.clipboard.writeText(inviteLink);
			copied = true;
			setTimeout(() => (copied = false), 2000);
		} catch {
			// clipboard needs a secure context; the link is visible to copy by hand
		}
	}

	async function share() {
		if (navigator.share) {
			try {
				await navigator.share({ title: 'Join my meeting', url: inviteLink });
				return;
			} catch {
				// cancelled; fall through to copy
			}
		}
		copyLink();
	}
</script>

<svelte:head><title>Meeting · go_chat</title></svelte:head>

{#if check === 'checking'}
	<p class="center">Checking meeting…</p>
{:else if check === 'missing'}
	<div class="center">
		<h1>Meeting not found</h1>
		<p>The link may be wrong or the meeting has ended.</p>
		<a href={resolve('/')}>Start a new meeting</a>
	</div>
{:else if check === 'failed'}
	<div class="center">
		<h1>Can’t reach the server</h1>
		<button type="button" onclick={() => location.reload()}>Retry</button>
	</div>
{:else if meeting.status === 'left'}
	<div class="center">
		<h1>You left the meeting</h1>
		<div class="row">
			<button type="button" class="primary" onclick={() => location.reload()}>Rejoin</button>
			<a href={resolve('/')}>Home</a>
		</div>
	</div>
{:else if meeting.status === 'lobby' || meeting.status === 'joining' || meeting.status === 'error'}
	<main class="lobby">
		<div class="preview">
			<VideoTile
				stream={meeting.localStream}
				name={name || 'You'}
				audioOn={meeting.audioOn}
				videoOn={meeting.videoOn}
				self
				mirror
			/>
			<div class="row controls">
				<button type="button" class:off={!meeting.audioOn} onclick={() => meeting.toggleAudio()}>
					{meeting.audioOn ? 'Mic on' : 'Mic off'}
				</button>
				<button type="button" class:off={!meeting.videoOn} onclick={() => meeting.toggleVideo()}>
					{meeting.videoOn ? 'Camera on' : 'Camera off'}
				</button>
			</div>
			{#if meeting.mediaError}<p class="error">{meeting.mediaError}</p>{/if}
		</div>

		<form class="join" onsubmit={join}>
			<h1>Ready to join?</h1>
			<label>
				Your name
				<input bind:value={name} maxlength={MAX_NAME} required autocomplete="nickname" />
			</label>
			<button type="submit" class="primary" disabled={!name.trim() || meeting.status === 'joining'}>
				{meeting.status === 'joining' ? 'Joining…' : 'Join now'}
			</button>
			{#if meeting.error}<p class="error" role="alert">{meeting.error}</p>{/if}

			<div class="invite">
				<span>Invite others with this link</span>
				<input
					readonly
					value={inviteLink}
					onfocus={(e) => e.currentTarget.select()}
					aria-label="Invite link"
				/>
				<button type="button" onclick={copyLink}>{copied ? 'Copied!' : 'Copy link'}</button>
			</div>
		</form>
	</main>
{:else}
	<div class="call">
		{#if meeting.signalingLost}
			<div class="banner">
				Lost connection to the server. You stay connected to current participants, but new people
				can’t join you.
			</div>
		{/if}
		<div class="stage">
			<section class="grid" style:--cols={Math.ceil(Math.sqrt(tileCount))}>
				<VideoTile
					stream={meeting.localStream}
					name={meeting.name}
					audioOn={meeting.audioOn}
					videoOn={meeting.videoOn || meeting.sharing}
					self
					mirror={!meeting.sharing}
				/>
				{#each meeting.peers as p (p.id)}
					<VideoTile
						stream={p.stream}
						name={p.name}
						audioOn={p.audio}
						videoOn={p.video && !!p.stream}
						status={p.connection}
					/>
				{/each}
				{#if meeting.peers.length === 0}
					<div class="alone">
						<p>You’re the only one here.</p>
						<button type="button" class="primary" onclick={share}
							>{copied ? 'Copied!' : 'Invite people'}</button
						>
					</div>
				{/if}
			</section>
			{#if chatOpen}
				<Chat
					messages={meeting.messages}
					onSend={(t) => meeting.sendChat(t)}
					onClose={() => (chatOpen = false)}
				/>
			{/if}
		</div>

		<nav class="bar" aria-label="Call controls">
			<button
				type="button"
				class:off={!meeting.audioOn}
				onclick={() => meeting.toggleAudio()}
				aria-pressed={!meeting.audioOn}
			>
				{meeting.audioOn ? 'Mute' : 'Unmute'}
			</button>
			<button
				type="button"
				class:off={!meeting.videoOn}
				onclick={() => meeting.toggleVideo()}
				aria-pressed={!meeting.videoOn}
			>
				{meeting.videoOn ? 'Stop video' : 'Start video'}
			</button>
			{#if 'getDisplayMedia' in (navigator.mediaDevices ?? {})}
				<button
					type="button"
					class:primary={meeting.sharing}
					onclick={() => meeting.toggleScreen()}
				>
					{meeting.sharing ? 'Stop sharing' : 'Share screen'}
				</button>
			{/if}
			<button type="button" onclick={() => (chatOpen = !chatOpen)} aria-pressed={chatOpen}>
				Chat{#if unread > 0}<span class="badge">{unread}</span>{/if}
			</button>
			<button type="button" onclick={share}>{copied ? 'Link copied' : 'Invite'}</button>
			<button type="button" class="danger" onclick={() => meeting.leave()}>Leave</button>
		</nav>
	</div>
{/if}

<style>
	.center {
		display: grid;
		place-items: center;
		align-content: center;
		gap: 12px;
		min-height: 100vh;
		padding: 16px;
		text-align: center;
	}
	.center h1 {
		margin: 0;
	}
	a {
		color: var(--accent);
	}
	.row {
		display: flex;
		gap: 12px;
		align-items: center;
		justify-content: center;
		flex-wrap: wrap;
	}

	/* lobby */
	.lobby {
		display: grid;
		grid-template-columns: minmax(0, 3fr) minmax(280px, 2fr);
		gap: 32px;
		align-items: center;
		max-width: 1100px;
		min-height: 100vh;
		margin: 0 auto;
		padding: 24px 16px;
	}
	.controls {
		margin-top: 12px;
	}
	.join {
		display: flex;
		flex-direction: column;
		gap: 14px;
	}
	.join h1 {
		margin: 0;
	}
	label {
		display: flex;
		flex-direction: column;
		gap: 6px;
		color: var(--muted);
		font-size: 0.9rem;
	}
	.invite {
		display: flex;
		flex-direction: column;
		gap: 8px;
		margin-top: 16px;
		padding-top: 16px;
		border-top: 1px solid var(--border);
		color: var(--muted);
		font-size: 0.9rem;
	}
	@media (max-width: 720px) {
		.lobby {
			grid-template-columns: 1fr;
			align-items: start;
		}
	}

	/* call */
	.call {
		display: flex;
		flex-direction: column;
		height: 100vh;
		height: 100dvh;
	}
	.banner {
		background: #5a3b00;
		padding: 8px 16px;
		font-size: 0.9rem;
		text-align: center;
	}
	.stage {
		flex: 1;
		display: flex;
		min-height: 0;
	}
	.grid {
		flex: 1;
		display: grid;
		grid-template-columns: repeat(var(--cols), minmax(0, 1fr));
		gap: 12px;
		padding: 16px;
		align-content: center;
		overflow-y: auto;
	}
	.alone {
		display: grid;
		place-items: center;
		align-content: center;
		gap: 8px;
		border: 1px dashed var(--border);
		border-radius: 12px;
		color: var(--muted);
		aspect-ratio: 16 / 9;
	}
	.bar {
		display: flex;
		gap: 8px;
		justify-content: center;
		flex-wrap: wrap;
		padding: 12px 16px;
		background: var(--panel);
		border-top: 1px solid var(--border);
	}
	.badge {
		margin-left: 6px;
		background: var(--accent);
		color: white;
		border-radius: 999px;
		padding: 0 7px;
		font-size: 0.75rem;
	}
	@media (max-width: 720px) {
		.grid {
			grid-template-columns: 1fr;
			align-content: start;
		}
	}
</style>
