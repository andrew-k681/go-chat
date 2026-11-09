<script lang="ts">
	type Props = {
		stream: MediaStream | null;
		name: string;
		audioOn: boolean;
		videoOn: boolean;
		self?: boolean;
		mirror?: boolean;
		status?: string;
	};

	let { stream, name, audioOn, videoOn, self = false, mirror = false, status }: Props = $props();

	const initials = $derived(
		name
			.split(/\s+/)
			.filter(Boolean)
			.slice(0, 2)
			.map((w) => w[0]?.toUpperCase())
			.join('') || '?'
	);
</script>

<figure class="tile">
	<!-- Remote tiles keep playing audio even when video is off, so the element stays mounted. -->
	<video
		{@attach (el: HTMLVideoElement) => {
			el.srcObject = stream;
		}}
		autoplay
		playsinline
		muted={self}
		class:mirror
		class:hidden={!videoOn}
	></video>
	{#if !videoOn}
		<div class="avatar" aria-hidden="true">{initials}</div>
	{/if}
	<figcaption>
		{#if !audioOn}<span class="muted" title="Muted">🔇</span>{/if}
		<span>{name}{self ? ' (you)' : ''}</span>
		{#if status && status !== 'connected'}<span class="status">· {status}</span>{/if}
	</figcaption>
</figure>

<style>
	.tile {
		position: relative;
		margin: 0;
		background: var(--tile);
		border-radius: 12px;
		overflow: hidden;
		aspect-ratio: 16 / 9;
		display: grid;
		place-items: center;
	}
	video {
		width: 100%;
		height: 100%;
		object-fit: cover;
	}
	video.mirror {
		transform: scaleX(-1);
	}
	video.hidden {
		position: absolute;
		opacity: 0;
		pointer-events: none;
	}
	.avatar {
		width: 96px;
		height: 96px;
		border-radius: 50%;
		background: var(--accent);
		color: white;
		display: grid;
		place-items: center;
		font-size: 2rem;
		font-weight: 600;
	}
	figcaption {
		position: absolute;
		left: 8px;
		bottom: 8px;
		display: flex;
		gap: 6px;
		align-items: center;
		padding: 4px 10px;
		border-radius: 8px;
		background: rgb(0 0 0 / 0.55);
		color: white;
		font-size: 0.85rem;
	}
	.status {
		opacity: 0.75;
	}
</style>
